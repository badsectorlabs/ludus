package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const migrationNetworkRoot = "/etc/network"
const migrationGatewayFragment = "interfaces.d/ludus-lxc-gateway"

var migrationHostServices = []string{"ludus", "ludus-admin", "wg-quick@wg0", "dnsmasq"}
var migrationRouteHooks = []string{"if-up.d/sdn-routes", "if-up.d/ludus-routes"}

type migrationSavedFile struct {
	Data []byte `json:"data"`
	Mode uint32 `json:"mode"`
}
type migrationServiceState struct {
	Active     bool   `json:"active"`
	Enablement string `json:"enablement"`
}
type migrationAddress struct {
	Local     string `json:"local"`
	PrefixLen int    `json:"prefixlen"`
}
type migrationLink struct {
	Name      string             `json:"ifname"`
	Addresses []migrationAddress `json:"addr_info"`
}
type migrationRoute struct {
	Destination string `json:"dst"`
	Gateway     string `json:"gateway"`
	Device      string `json:"dev"`
	Table       any    `json:"table"`
}
type migrationNodeSnapshot struct {
	Name      string                           `json:"name"`
	Files     map[string]migrationSavedFile    `json:"files"`
	Services  map[string]migrationServiceState `json:"services"`
	Addresses []migrationLink                  `json:"addresses"`
	Routes    []migrationRoute                 `json:"routes"`
	IPTables  string                           `json:"iptables"`
	IPForward string                           `json:"ip_forward"`
}
type migrationGuest struct {
	Node string            `json:"node"`
	VMID int               `json:"vmid"`
	Old  map[string]string `json:"old"`
	New  map[string]string `json:"new"`
}
type migrationVNet struct {
	Name      string `json:"vnet"`
	Zone      string `json:"zone"`
	VLANAware int    `json:"vlanaware"`
}
type migrationZone struct {
	Name       string `json:"zone"`
	Type       string `json:"type"`
	IPAM       string `json:"ipam"`
	DNS        string `json:"dns"`
	ReverseDNS string `json:"reversedns"`
	Nodes      string `json:"nodes"`
	Peers      string `json:"peers"`
}
type migrationSubnet struct {
	Name      string `json:"subnet"`
	Gateway   string `json:"gateway"`
	DHCPRange any    `json:"dhcp-range"`
	DHCPDNS   string `json:"dhcp-dns-server"`
}
type migrationClusterStatus struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Online  int    `json:"online"`
	Quorate int    `json:"quorate"`
}
type migrationResource struct {
	Node string `json:"node"`
	Type string `json:"type"`
	VMID int    `json:"vmid"`
}
type migrationNetworkPlan struct {
	Owner              string                                   `json:"owner"`
	Nodes              []ClusterNode                            `json:"nodes"`
	Snapshots          map[string]migrationNodeSnapshot         `json:"snapshots"`
	StagedFiles        map[string]map[string]migrationSavedFile `json:"staged_files,omitempty"`
	NATBridge          string                                   `json:"nat_bridge"`
	NATGateway         string                                   `json:"nat_gateway"`
	Zone               string                                   `json:"zone"`
	ZoneType           string                                   `json:"zone_type"`
	ZoneExists         bool                                     `json:"zone_exists"`
	VNets              map[string]migrationVNet                 `json:"vnets"`
	Subnets            []migrationSubnet                        `json:"subnets"`
	Bridge             string                                   `json:"bridge"`
	IP                 string                                   `json:"ip"`
	Gateway            string                                   `json:"gateway"`
	AutoManagement     bool                                     `json:"auto_management"`
	Subnet             string                                   `json:"subnet"`
	Uplink             string                                   `json:"uplink"`
	Guests             []migrationGuest                         `json:"guests"`
	Ranges             []migrationRange                         `json:"ranges"`
	HostAddresses      []string                                 `json:"host_addresses"`
	Port               int                                      `json:"port"`
	AdminPort          int                                      `json:"admin_port"`
	ExposeAdminPort    bool                                     `json:"expose_admin_port"`
	WireGuardPort      int                                      `json:"wireguard_port"`
	NoWireGuardForward bool                                     `json:"no_wireguard_forward,omitempty"`
	RouteLocalnet      string                                   `json:"route_localnet"`
}

// Cluster commands retain Proxmox's host-key checking and never prompt for a password.
// Each remote invocation is one native command, not a remotely interpreted engine.
type migrationNodeIO struct{ Node ClusterNode }

func (n migrationNodeIO) sshArgs() []string {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=yes", "-o", "HostKeyAlias=" + n.Node.Name}
	known := filepath.Join("/etc/pve/nodes", n.Node.Name, "ssh_known_hosts")
	if fileExists(known) {
		args = append(args, "-o", "UserKnownHostsFile="+known, "-o", "GlobalKnownHostsFile=/dev/null")
	}
	return append(args, "root@"+n.Node.IP)
}
func (n migrationNodeIO) command(ctx context.Context, input []byte, name string, args ...string) ([]byte, error) {
	return n.commandReader(ctx, bytes.NewReader(input), name, args...)
}
func (n migrationNodeIO) commandReader(ctx context.Context, input io.Reader, name string, args ...string) ([]byte, error) {
	discard := name == "tee"
	if !n.Node.Local {
		words := []string{shellQuote(name)}
		for _, arg := range args {
			words = append(words, shellQuote(arg))
		}
		args = append(n.sshArgs(), strings.Join(words, " "))
		name = "ssh"
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var out []byte
	var err error
	if discard {
		cmd.Stdout = io.Discard
		err = cmd.Run()
	} else {
		out, err = cmd.Output()
	}
	if err != nil {
		return out, fmt.Errorf("node %s: %s: %w: %s", n.Node.Name, name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
func (n migrationNodeIO) run(ctx context.Context, name string, args ...string) error {
	_, err := n.command(ctx, nil, name, args...)
	return err
}
func (n migrationNodeIO) read(ctx context.Context, path string) ([]byte, error) {
	return n.command(ctx, nil, "cat", "--", path)
}
func (n migrationNodeIO) exists(ctx context.Context, path string) bool {
	return n.run(ctx, "test", "-e", path) == nil
}
func (n migrationNodeIO) write(ctx context.Context, path string, data []byte, mode uint32) error {
	if n.Node.Local {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return atomicWriteFile(path, data, os.FileMode(mode))
	}
	if err := n.run(ctx, "mkdir", "-p", "--", filepath.Dir(path)); err != nil {
		return err
	}
	// Write through a uniquely owned temporary file so a failed SSH transfer cannot truncate network configuration.
	out, err := n.command(ctx, nil, "mktemp", filepath.Join(filepath.Dir(path), ".ludus-migration.XXXXXXXX"))
	if err != nil {
		return err
	}
	temporary := strings.TrimSpace(string(out))
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = n.run(cleanup, "rm", "-f", "--", temporary)
	}()
	if _, err = n.command(ctx, data, "tee", "--", temporary); err != nil {
		return err
	}
	if err = n.run(ctx, "chmod", fmt.Sprintf("%o", mode), "--", temporary); err != nil {
		return err
	}
	return n.run(ctx, "mv", "-f", "--", temporary, path)
}
func (n migrationNodeIO) copyExecutable(ctx context.Context, source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if _, err = n.commandReader(ctx, input, "tee", "--", destination); err != nil {
		return err
	}
	return n.run(ctx, "chmod", "0700", "--", destination)
}
func migrationAPI(ctx context.Context, path string, destination any, args ...string) error {
	argv := append([]string{"get", path}, args...)
	argv = append(argv, "--output-format", "json")
	data, err := commandOutput(ctx, "pvesh", argv...)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}
func validateMigrationCluster(status []migrationClusterStatus) ([]ClusterNode, error) {
	var nodes []ClusterNode
	quorate := false
	for _, entry := range status {
		if entry.Type == "cluster" {
			quorate = entry.Quorate != 0
		}
		if entry.Type != "node" {
			continue
		}
		if entry.Online == 0 {
			return nil, errors.New("all cluster nodes must be online before migration")
		}
		if entry.Name == "" || strings.ContainsAny(entry.Name, "/\\\x00") {
			return nil, errors.New("invalid cluster node identity")
		}
		if _, err := netip.ParseAddr(entry.IP); err != nil {
			return nil, fmt.Errorf("invalid address for cluster node %s", entry.Name)
		}
		nodes = append(nodes, ClusterNode{Name: entry.Name, IP: entry.IP})
	}
	if len(nodes) == 0 {
		return nil, errors.New("all cluster nodes must be online before migration")
	}
	if len(nodes) > 1 && !quorate {
		return nil, errors.New("cluster must have quorum before migration")
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return nodes, nil
}
func migrationClusterNodes(ctx context.Context) ([]ClusterNode, error) {
	var status []migrationClusterStatus
	if err := migrationAPI(ctx, "/cluster/status", &status); err != nil {
		return nil, err
	}
	return validateMigrationCluster(status)
}
func migrationCheckBusy(ctx context.Context, n migrationNodeIO) error {
	_, err := n.command(ctx, nil, "pgrep", "-f", "(^|/)(ansible-playbook|packer)( |$)")
	if err == nil {
		return errors.New("wait for all range deployments and template builds to finish before migrating")
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return err
	}
	return nil
}
func migrationSnapshot(ctx context.Context, n migrationNodeIO, allow bool) (migrationNodeSnapshot, error) {
	s := migrationNodeSnapshot{Files: map[string]migrationSavedFile{}, Services: map[string]migrationServiceState{}}
	if err := migrationCheckBusy(ctx, n); err != nil {
		return s, err
	}
	for _, command := range []string{"ip", "ifreload", "iptables-save", "iptables-restore", "systemctl", "pvesh", "pgrep"} {
		// which is read-only and works over SSH without an embedded shell program.
		if err := n.run(ctx, "which", command); err != nil {
			return s, fmt.Errorf("missing prerequisite %s on %s: %w", command, n.Node.Name, err)
		}
	}
	hostname, err := n.command(ctx, nil, "hostname", "-s")
	if err != nil {
		return s, err
	}
	s.Name = strings.TrimSpace(string(hostname))
	if s.Name != n.Node.Name {
		return s, errors.New("cluster SSH connected to the wrong node")
	}
	paths := []string{"/etc/network/interfaces"}
	if n.exists(ctx, "/etc/network/interfaces.d") {
		entries, err := n.command(ctx, nil, "find", "-P", "/etc/network/interfaces.d", "-mindepth", "1", "-maxdepth", "1", "-print0")
		if err != nil {
			return s, err
		}
		for _, path := range strings.Split(string(entries), "\x00") {
			if path != "" {
				paths = append(paths, path)
			}
		}
	}
	for _, hook := range migrationRouteHooks {
		paths = append(paths, filepath.Join(migrationNetworkRoot, hook))
	}
	for _, path := range paths {
		if n.run(ctx, "test", "-L", path) == nil {
			return s, fmt.Errorf("cannot safely snapshot symlinked network configuration: %s", path)
		}
		if !n.exists(ctx, path) {
			continue
		}
		if n.run(ctx, "test", "-f", path) != nil {
			continue
		}
		data, err := n.read(ctx, path)
		if err != nil {
			return s, err
		}
		mode, err := n.command(ctx, nil, "stat", "-c", "%a", "--", path)
		if err != nil {
			return s, err
		}
		permissions, err := strconv.ParseUint(strings.TrimSpace(string(mode)), 8, 32)
		if err != nil {
			return s, err
		}
		name, err := filepath.Rel(migrationNetworkRoot, path)
		if err != nil {
			return s, err
		}
		s.Files[name] = migrationSavedFile{Data: data, Mode: uint32(permissions)}
	}
	if !allow {
		for _, name := range []string{migrationGatewayFragment, "interfaces.d/ludus-migration"} {
			if _, ok := s.Files[name]; ok {
				return s, errors.New("a previous migration network fragment exists")
			}
		}
	}
	for _, service := range migrationHostServices {
		data, err := n.command(ctx, nil, "systemctl", "show", service, "--property=LoadState", "--value")
		if err != nil {
			return s, err
		}
		if strings.TrimSpace(string(data)) == "not-found" {
			continue
		}
		enablement, err := n.command(ctx, nil, "systemctl", "is-enabled", service)
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() > 4 {
				return s, err
			}
		}
		active := n.run(ctx, "systemctl", "is-active", "--quiet", service) == nil
		s.Services[service] = migrationServiceState{Active: active, Enablement: strings.TrimSpace(string(enablement))}
	}
	data, err := n.command(ctx, nil, "ip", "-j", "-4", "address")
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s.Addresses); err != nil {
		return s, err
	}
	data, err = n.command(ctx, nil, "ip", "-j", "-4", "route", "show", "table", "all")
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(data, &s.Routes); err != nil {
		return s, err
	}
	data, err = n.command(ctx, nil, "iptables-save")
	if err != nil {
		return s, err
	}
	s.IPTables = string(data)
	data, err = n.read(ctx, "/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return s, err
	}
	s.IPForward = strings.TrimSpace(string(data))
	return s, nil
}
func migrationNICBridge(value string) string {
	for _, field := range strings.Split(value, ",") {
		if strings.HasPrefix(field, "bridge=") {
			return strings.TrimPrefix(field, "bridge=")
		}
	}
	return ""
}
func migrationNICEqual(a, b string) bool {
	left, right := strings.Split(a, ","), strings.Split(b, ",")
	sort.Strings(left)
	sort.Strings(right)
	return reflect.DeepEqual(left, right)
}
func migrationRewriteNIC(value, natBridge string, ranges []migrationRange) (string, bool, error) {
	mapping := map[string]string{"vmbr1000": "ludusnat", "ludusnat": "ludusnat"}
	for _, r := range ranges {
		mapping[fmt.Sprintf("vmbr%d", 1000+r.Number)] = fmt.Sprintf("r%d", r.Number)
		mapping[fmt.Sprintf("r%d", r.Number)] = fmt.Sprintf("r%d", r.Number)
	}
	bridge := migrationNICBridge(value)
	target, ok := mapping[bridge]
	if !ok {
		return value, false, nil
	}
	fields := strings.Split(value, ",")
	replaced := make([]string, 0, len(fields))
	for _, field := range fields {
		if bridge == natBridge || bridge == "vmbr1000" || bridge == "ludusnat" {
			if strings.HasPrefix(field, "trunks=") || (strings.HasPrefix(field, "tag=") && field != "tag=1") {
				return "", false, errors.New("guest uses non-native NAT VLANs")
			}
			if field == "tag=1" {
				continue
			}
		}
		if strings.HasPrefix(field, "bridge=") {
			field = "bridge=" + target
		}
		replaced = append(replaced, field)
	}
	return strings.Join(replaced, ","), true, nil
}
func migrationVerifyTap(ctx context.Context, n migrationNodeIO, root string, id int, key, value string) error {
	status, err := n.command(ctx, nil, "qm", "status", strconv.Itoa(id))
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "status: running" {
		return nil
	}
	return migrationCheckTapAttachment(root, id, key, value, func(path string) (string, error) {
		data, err := n.command(ctx, nil, "readlink", "-e", "--", path)
		return strings.TrimSpace(string(data)), err
	})
}
func migrationCheckTapAttachment(root string, id int, key, value string, resolve func(string) (string, error)) error {
	index := strings.TrimPrefix(key, "net")
	master, err := resolve(filepath.Join(root, fmt.Sprintf("tap%di%s", id, index), "master"))
	if err != nil {
		return fmt.Errorf("VM %d %s tap is detached: %w", id, key, err)
	}
	attached := filepath.Base(master)
	if strings.HasPrefix(attached, "fwbr") {
		master, err = resolve(filepath.Join(root, fmt.Sprintf("fwpr%dp%s", id, index), "master"))
		if err != nil {
			return err
		}
		attached = filepath.Base(master)
	}
	if attached != migrationNICBridge(value) {
		return fmt.Errorf("VM %d %s tap belongs to %s, expected %s", id, key, attached, migrationNICBridge(value))
	}
	return nil
}
func migrationManagementNetwork(bridge, address, gateway, natBridge string, local migrationNodeSnapshot) (string, string, string, bool, error) {
	auto := bridge == "" && address == "" && gateway == ""
	if auto {
		for _, link := range local.Addresses {
			if link.Name == "ludusmg" {
				return "", "", "", false, errors.New("ludusmg already exists")
			}
		}
		var occupied []netip.Prefix
		for _, route := range local.Routes {
			if route.Destination == "" || route.Destination == "default" {
				continue
			}
			p, err := netip.ParsePrefix(route.Destination)
			if err != nil {
				a, e := netip.ParseAddr(route.Destination)
				if e != nil {
					return "", "", "", false, err
				}
				p = netip.PrefixFrom(a, a.BitLen())
			}
			occupied = append(occupied, p)
		}
		for _, link := range local.Addresses {
			for _, addr := range link.Addresses {
				a, err := netip.ParseAddr(addr.Local)
				if err != nil {
					return "", "", "", false, err
				}
				occupied = append(occupied, netip.PrefixFrom(a, addr.PrefixLen))
			}
		}
		for third := 255; third >= 240; third-- {
			candidate := netip.MustParsePrefix(fmt.Sprintf("172.31.%d.0/30", third))
			free := true
			for _, p := range occupied {
				if candidate.Overlaps(p) {
					free = false
					break
				}
			}
			if free {
				return "ludusmg", fmt.Sprintf("172.31.%d.2/30", third), fmt.Sprintf("172.31.%d.1", third), true, nil
			}
		}
		return "", "", "", false, errors.New("no unused management subnet; supply --bridge, --ip and --gw")
	}
	if bridge == "" || address == "" || gateway == "" || address == "dhcp" {
		return "", "", "", false, errors.New("supply --bridge, static --ip and --gw together, or none")
	}
	prefix, err := netip.ParsePrefix(address)
	if err != nil || !prefix.Addr().Is4() {
		return "", "", "", false, errors.New("management IP must be an IPv4 CIDR")
	}
	gw, err := netip.ParseAddr(gateway)
	if err != nil || !prefix.Contains(gw) || gw == prefix.Addr() {
		return "", "", "", false, errors.New("management gateway must be another address in the management subnet")
	}
	if bridge == natBridge || prefix.Overlaps(netip.MustParsePrefix("192.0.2.0/24")) || prefix.Overlaps(netip.MustParsePrefix("198.51.100.0/24")) {
		return "", "", "", false, errors.New("management network must not overlap the Ludus networks")
	}
	return bridge, address, gateway, false, nil
}

var migrationMemberSeparator = regexp.MustCompile(`[,;\s]+`)

func migrationContainsMembers(value string, required []string) bool {
	if value == "" {
		return true
	}
	members := map[string]bool{}
	for _, v := range migrationMemberSeparator.Split(value, -1) {
		members[v] = true
	}
	for _, v := range required {
		if !members[v] {
			return false
		}
	}
	return true
}

func inspectMigrationNetwork(ctx context.Context, opts *Options, info migrationManifest) (migrationNetworkPlan, error) {
	c := migrationNetworkPlan{Snapshots: map[string]migrationNodeSnapshot{}, VNets: map[string]migrationVNet{}, NATGateway: "192.0.2.49", Ranges: info.Ranges, Port: info.Port, AdminPort: info.AdminPort, ExposeAdminPort: info.ExposeAdminPort, WireGuardPort: info.WireguardPort}
	c.NoWireGuardForward = opts.NoWireGuardForward
	nodes, err := migrationClusterNodes(ctx)
	if err != nil {
		return c, err
	}
	c.Nodes = nodes
	hostname, err := commandOutput(ctx, "hostname", "-s")
	if err != nil {
		return c, err
	}
	c.Owner = strings.TrimSpace(string(hostname))
	found := false
	for i, node := range c.Nodes {
		node.Local = node.Name == c.Owner
		c.Nodes[i] = node
		found = found || node.Local
		s, err := migrationSnapshot(ctx, migrationNodeIO{node}, false)
		if err != nil {
			return c, err
		}
		if !node.Local {
			for _, service := range migrationHostServices[:3] {
				if s.Services[service].Active {
					return c, errors.New("Ludus/WireGuard is active on another node; resolve split ownership before migrating")
				}
			}
		}
		c.Snapshots[node.Name] = s
	}
	if !found {
		return c, errors.New("cannot identify the owning Proxmox node")
	}
	natCount := 0
	for node, snapshot := range c.Snapshots {
		for _, link := range snapshot.Addresses {
			for _, address := range link.Addresses {
				if address.Local == c.NATGateway {
					return c, errors.New("reserved NAT gateway is assigned on a cluster node")
				}
				if address.Local == "192.0.2.254" {
					natCount++
					if node != c.Owner || (link.Name != "vmbr1000" && link.Name != "ludusnat") {
						return c, errors.New("legacy NAT address must belong only to the Ludus owning host on vmbr1000 or ludusnat")
					}
					c.NATBridge = link.Name
				}
			}
		}
	}
	if natCount != 1 {
		return c, errors.New("legacy NAT address must belong only to the Ludus owning host on vmbr1000 or ludusnat")
	}
	var vnets []migrationVNet
	if err = migrationAPI(ctx, "/cluster/sdn/vnets", &vnets); err != nil {
		return c, err
	}
	for _, v := range vnets {
		c.VNets[v.Name] = v
	}
	c.Zone = "ludus"
	if v, ok := c.VNets["ludusnat"]; ok {
		c.Zone = v.Zone
	}
	var zones []migrationZone
	if err = migrationAPI(ctx, "/cluster/sdn/zones", &zones); err != nil {
		return c, err
	}
	c.ZoneType = "simple"
	var names, ips []string
	for _, node := range c.Nodes {
		names = append(names, node.Name)
		ips = append(ips, node.IP)
	}
	for _, zone := range zones {
		if zone.Name != c.Zone {
			continue
		}
		c.ZoneExists = true
		c.ZoneType = zone.Type
		if (zone.Type != "simple" && zone.Type != "vxlan") || (zone.IPAM != "" && zone.IPAM != "pve") || zone.DNS != "" || zone.ReverseDNS != "" {
			return c, errors.New("Ludus SDN zone uses unsupported routing or external IPAM/DNS")
		}
		if !migrationContainsMembers(zone.Nodes, names) {
			return c, errors.New("Ludus zone must be enabled on every cluster node")
		}
		if zone.Type == "vxlan" && (zone.Peers == "" || !migrationContainsMembers(zone.Peers, ips)) {
			return c, errors.New("VXLAN peers must include every cluster node address")
		}
	}
	if len(nodes) > 1 && (!c.ZoneExists || c.ZoneType != "vxlan") {
		return c, errors.New("cluster migration requires an existing working VXLAN zone")
	}
	for _, name := range migrationTargetBridges(c.Ranges) {
		if v, ok := c.VNets[name]; ok {
			if v.Zone != c.Zone {
				return c, fmt.Errorf("SDN target belongs to another zone: %s", name)
			}
		} else if len(nodes) > 1 {
			return c, fmt.Errorf("cluster migration requires existing VXLAN VNet: %s", name)
		}
	}
	if _, ok := c.VNets["ludusnat"]; ok {
		if err = migrationAPI(ctx, "/cluster/sdn/vnets/ludusnat/subnets", &c.Subnets); err != nil {
			return c, err
		}
		for _, s := range c.Subnets {
			if !strings.HasSuffix(s.Name, "-192.0.2.0-24") || s.DHCPRange != nil || s.DHCPDNS != "" {
				return c, errors.New("unsupported NAT subnet or SDN DHCP; resolve before migration")
			}
		}
	}
	local := c.Snapshots[c.Owner]
	c.Bridge, c.IP, c.Gateway, c.AutoManagement, err = migrationManagementNetwork(opts.Bridge, opts.IP, opts.Gateway, c.NATBridge, local)
	if err != nil {
		return c, err
	}
	prefix, _ := netip.ParsePrefix(c.IP)
	c.Subnet = prefix.Masked().String()
	for _, r := range local.Routes {
		if r.Destination == "default" && r.Device != "" {
			c.Uplink = r.Device
			break
		}
	}
	if c.Uplink == "" {
		return c, errors.New("owning host requires an IPv4 default route")
	}
	for _, link := range local.Addresses {
		for _, a := range link.Addresses {
			c.HostAddresses = append(c.HostAddresses, a.Local)
		}
	}
	if !c.AutoManagement {
		b, err := os.ReadFile(filepath.Join("/proc/sys/net/ipv4/conf", c.Bridge, "route_localnet"))
		if err != nil {
			return c, err
		}
		c.RouteLocalnet = strings.TrimSpace(string(b))
	}
	for _, path := range []string{"/etc/systemd/system/ludus-lxc-forwarding.service", "/usr/local/lib/ludus/migrate-host.sh"} {
		if fileExists(path) {
			return c, errors.New("a previous migration forwarding installation exists")
		}
	}
	var resources []migrationResource
	if err = migrationAPI(ctx, "/cluster/resources", &resources, "--type", "vm"); err != nil {
		return c, err
	}
	for _, vm := range resources {
		var config map[string]any
		path := fmt.Sprintf("/nodes/%s/%s/%d/config", vm.Node, vm.Type, vm.VMID)
		if err = migrationAPI(ctx, path, &config); err != nil {
			return c, err
		}
		guest := migrationGuest{Node: vm.Node, VMID: vm.VMID, Old: map[string]string{}, New: map[string]string{}}
		for key, v := range config {
			if !migrationNICName.MatchString(key) {
				continue
			}
			value, ok := v.(string)
			if !ok {
				return c, errors.New("invalid guest NIC configuration")
			}
			rewritten, relevant, err := migrationRewriteNIC(value, c.NATBridge, c.Ranges)
			if err != nil {
				return c, fmt.Errorf("VM %d: %w", vm.VMID, err)
			}
			if !relevant {
				continue
			}
			if vm.Type != "qemu" {
				return c, errors.New("existing containers on Ludus bridges require a separate migration plan")
			}
			guest.Old[key] = value
			guest.New[key] = rewritten
		}
		if len(guest.Old) > 0 {
			if lock, ok := config["lock"]; ok && lock != "" {
				return c, fmt.Errorf("VM %d is locked", vm.VMID)
			}
			c.Guests = append(c.Guests, guest)
		}
	}
	sort.Slice(c.Guests, func(i, j int) bool { return c.Guests[i].VMID < c.Guests[j].VMID })
	for _, guest := range c.Guests {
		for _, node := range c.Nodes {
			if node.Name == guest.Node {
				for key, value := range guest.Old {
					if err = migrationVerifyTap(ctx, migrationNodeIO{node}, "/sys/class/net", guest.VMID, key, value); err != nil {
						return c, err
					}
				}
			}
		}
	}
	return c, nil
}
func migrationTargetBridges(ranges []migrationRange) []string {
	names := []string{"ludusnat"}
	for _, r := range ranges {
		names = append(names, fmt.Sprintf("r%d", r.Number))
	}
	return names
}
