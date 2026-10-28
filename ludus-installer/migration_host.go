package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const migrationInstalledBinary = "/usr/local/libexec/ludus-installer"
const migrationForwardUnit = "/etc/systemd/system/ludus-lxc-forwarding.service"
const migrationGuestBinary = "/opt/ludus/install/ludus-installer"

type Migration struct {
	Options         *Options                      `json:"-"`
	Directory       string                        `json:"directory"`
	Info            migrationManifest             `json:"info"`
	Network         migrationNetworkPlan          `json:"network"`
	OriginalSDN     map[string]migrationSavedFile `json:"original_sdn"`
	StagedSDN       map[string]migrationSavedFile `json:"staged_sdn"`
	Prepared        bool                          `json:"prepared"`
	Mutated         bool                          `json:"mutated"`
	SDNChanged      bool                          `json:"sdn_changed"`
	CutoverStarted  bool                          `json:"cutover_started"`
	ServicesChanged bool                          `json:"services_changed"`
	CandidateVMID   int                           `json:"candidate_vmid"`
	ChangedNICs     map[int]bool                  `json:"changed_nics"`
	OwnedRules      []migrationFirewallRule       `json:"owned_rules"`
	NetworkWrites   migrationNetworkWriteJournal  `json:"network_writes"`
	UnitOwned       bool                          `json:"unit_owned"`
	Committed       bool                          `json:"committed"`
	Fingerprint     string                        `json:"fingerprint"`
	Helpers         map[string]string             `json:"helpers"`
	BridgePID       int                           `json:"bridge_pid"`
	BridgeRules     []migrationFirewallRule       `json:"bridge_rules"`
	lock            *os.File
	bridgeProcess   *os.Process
}

func planMigration(ctx context.Context, opts *Options) (*Migration, error) {
	if !fileExists("/opt/ludus/config.yml") || fileExists("/opt/ludus/install/.bootstrap-complete") {
		return nil, errors.New("--migrate-host requires an existing host-installed Ludus instance")
	}
	if opts.ImportDB != "" {
		return nil, errors.New("do not combine --migrate-host and --import-db")
	}
	for _, name := range []string{"pvesh", "pvesm", "qm", "pct", "ip", "perl", "iptables", "iptables-save", "iptables-restore", "systemctl", "ssh", "pgrep"} {
		if _, err := exec.LookPath(name); err != nil {
			return nil, fmt.Errorf("migration requires %s", name)
		}
	}
	if err := migrationCheckSDNPending(ctx); err != nil {
		return nil, err
	}
	info, err := migrationCollect("", false)
	if err != nil {
		return nil, err
	}
	if info.RequiresPlugin && opts.EnterprisePlugin == "" {
		return nil, errors.New("this install requires the Enterprise plugin package (--enterprise-plugin ZIP); the old Go plugin cannot be reused")
	}
	network, err := inspectMigrationNetwork(ctx, opts, info)
	if err != nil {
		return nil, err
	}
	sdn, err := migrationReadDirectory("/etc/pve/sdn", false)
	if err != nil {
		return nil, err
	}
	m := &Migration{Options: opts, Info: info, Network: network, OriginalSDN: sdn, ChangedNICs: map[int]bool{}, Helpers: map[string]string{}}
	if opts.WireGuardEndpoint == "" {
		opts.WireGuardEndpoint = info.WireguardEndpoint
	}
	opts.WireGuardPort = info.WireguardPort
	opts.APIPort = info.Port
	opts.AdminPort = info.AdminPort
	if opts.VMStorage == "" {
		opts.VMStorage = info.ProxmoxVMStoragePool
	}
	if opts.VMStorageFormat == "" {
		opts.VMStorageFormat = info.ProxmoxVMStorageFormat
	}
	if opts.ISOStorage == "" {
		opts.ISOStorage = info.ProxmoxISOStoragePool
	}
	opts.NATIP = "192.0.2.254"
	opts.NATGateway = network.NATGateway
	opts.SDNZone = network.Zone
	opts.HostManagedNetwork = true
	opts.Bridge = network.Bridge
	opts.IP = network.IP
	opts.Gateway = network.Gateway
	return m, nil
}
func migrationCheckSDNPending(ctx context.Context) error {
	// Vendor introspection is necessary: pending SDN is parsed state, not *.new files.
	const program = `use strict; use warnings;
 die "SDN configuration is locked by another operation\n" if -e '/etc/pve/sdn/.lock';
 PVE::Cluster::cfs_update(1);
 my $running=PVE::Network::SDN::running_config();
 my $configs={zones=>PVE::Network::SDN::Zones::config(),vnets=>PVE::Network::SDN::Vnets::config(),subnets=>PVE::Network::SDN::Subnets::config(),controllers=>PVE::Network::SDN::Controllers::config()};
 for my $extra (['fabrics','Fabrics'],['route-maps','RouteMaps'],['prefix-lists','PrefixLists']) {my ($type,$module)=@$extra;my $config=('PVE::Network::SDN::'.$module)->can('config');$configs->{$type}={ids=>$config->()->to_sections()} if $config;}
 for my $type (keys %$configs) {my $pending=PVE::Network::SDN::pending_config($running,$configs->{$type},$type);for my $object (values %{$pending->{ids}}) {die "SDN has pending changes; apply or revert them before migrating\n" if exists($object->{pending}) || exists($object->{state});}}`
	return commandInput(ctx, []byte(program), "perl", "-MPVE::Network::SDN", "-")
}
func migrationReadFile(path string) (migrationSavedFile, error) {
	s := migrationSavedFile{}
	info, err := os.Lstat(path)
	if err != nil {
		return s, err
	}
	if !info.Mode().IsRegular() {
		return s, fmt.Errorf("cannot snapshot non-regular file %s", path)
	}
	s.Mode = uint32(info.Mode().Perm())
	s.Data, err = os.ReadFile(path)
	return s, err
}
func migrationReadDirectory(path string, cfgOnly bool) (map[string]migrationSavedFile, error) {
	files := map[string]migrationSavedFile{}
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == ".lock" || (cfgOnly && !strings.HasSuffix(entry.Name(), ".cfg")) {
			continue
		}
		file, err := migrationReadFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		files[entry.Name()] = file
	}
	return files, nil
}
func (m *Migration) save() error {
	if m.Directory == "" {
		return errors.New("migration has no rollback directory")
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filepath.Join(m.Directory, "migration.json"), data, 0600)
}
func migrationStableInfo(a, b migrationManifest) bool {
	return a.DataDirectory == b.DataDirectory && a.Port == b.Port && a.AdminPort == b.AdminPort && a.ExposeAdminPort == b.ExposeAdminPort && a.WireguardPort == b.WireguardPort && a.WireguardEndpoint == b.WireguardEndpoint && reflect.DeepEqual(a.Ranges, b.Ranges) && reflect.DeepEqual(a.Inventory, b.Inventory)
}
func migrationSDNConfigOnly(files map[string]migrationSavedFile) map[string]migrationSavedFile {
	result := map[string]migrationSavedFile{}
	for name, item := range files {
		if strings.HasSuffix(name, ".cfg") {
			result[name] = item
		}
	}
	return result
}
func (m *Migration) Prepare(ctx context.Context) error {
	if m.Prepared {
		return errors.New("migration already prepared")
	}
	if _, err := exec.LookPath("conntrack"); err != nil && !m.Options.NoWireGuardForward {
		return errors.New("migration WireGuard forwarding requires conntrack")
	}
	lock, err := migrationAcquireLock()
	if err != nil {
		return err
	}
	m.lock = lock
	if err = migrationCheckSDNPending(ctx); err != nil {
		return err
	}
	current, err := migrationCollect("", false)
	if err != nil {
		return err
	}
	if !migrationStableInfo(m.Info, current) {
		return errors.New("installation changed during preflight; retry migration in a maintenance window")
	}
	// Re-plan against the form's current network selection, but retain an automatic bridge's ownership.
	selected := *m.Options
	if m.Network.AutoManagement && selected.Bridge == m.Network.Bridge && selected.IP == m.Network.IP && selected.Gateway == m.Network.Gateway {
		selected.Bridge = ""
		selected.IP = ""
		selected.Gateway = ""
	}
	network, err := inspectMigrationNetwork(ctx, &selected, current)
	if err != nil {
		return err
	}
	if network.Bridge != m.Options.Bridge || network.IP != m.Options.IP || network.Gateway != m.Options.Gateway {
		return errors.New("management network allocation changed after confirmation; retry planning")
	}
	if !reflect.DeepEqual(network.Nodes, m.Network.Nodes) {
		return errors.New("cluster membership changed during preflight")
	}
	for name, saved := range m.Network.Snapshots {
		if !reflect.DeepEqual(saved.Files, network.Snapshots[name].Files) {
			return fmt.Errorf("node network configuration changed during preflight: %s", name)
		}
	}
	sdn, err := migrationReadDirectory("/etc/pve/sdn", false)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(migrationSDNConfigOnly(sdn), migrationSDNConfigOnly(m.OriginalSDN)) {
		return errors.New("SDN configuration changed during preflight")
	}
	m.Network = network
	m.OriginalSDN = sdn
	m.StagedSDN = migrationSDNConfigOnly(sdn)
	if err = os.MkdirAll("/var/lib/ludus-migration", 0700); err != nil {
		return err
	}
	m.Directory, err = os.MkdirTemp("/var/lib/ludus-migration", "upgrade.")
	if err != nil {
		return err
	}
	m.Prepared = true
	if err = m.save(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	if err = copyFile(executable, filepath.Join(m.Directory, "ludus-installer"), 0700); err != nil {
		return err
	}
	// Remote executable staging is deliberately after confirmation and lock acquisition.
	for _, node := range network.Nodes {
		n := migrationNodeIO{node}
		helper := filepath.Join(m.Directory, "ludus-installer")
		if !node.Local {
			// Proxmox mounts /run noexec; stage the helper beside the local
			// copy's filesystem, in a root-only directory removed afterwards.
			if err := n.run(ctx, "install", "-d", "-m", "0700", "/var/lib/ludus-migration"); err != nil {
				return err
			}
			out, err := n.command(ctx, nil, "mktemp", "-d", "/var/lib/ludus-migration/peer.XXXXXXXX")
			if err != nil {
				return err
			}
			helper = filepath.Join(strings.TrimSpace(string(out)), "ludus-installer")
			m.Helpers[node.Name] = helper
			if err = m.save(); err != nil {
				return err
			}
			if err = n.copyExecutable(ctx, executable, helper); err != nil {
				return err
			}
		} else {
			m.Helpers[node.Name] = helper
		}
		if n.exists(ctx, filepath.Join("/sys/class/net", network.NATBridge)) {
			if node.Local {
				err = migrationProbeAddress(ctx, network.NATBridge, network.NATGateway)
			} else {
				err = n.run(ctx, helper, "--migration-probe", network.NATBridge, network.NATGateway)
			}
			if err != nil {
				return err
			}
		}
	}
	m.Mutated = true
	if err = m.save(); err != nil {
		return err
	}
	if network.AutoManagement {
		n := migrationNodeIO{ClusterNode{Name: network.Owner, Local: true}}
		path := "/etc/network/interfaces.d/ludus-migration"
		text := fmt.Sprintf("auto %s\niface %s inet static\n\taddress %s/30\n\tbridge-ports none\n\tbridge-stp off\n\tbridge-fd 0\n", network.Bridge, network.Bridge, network.Gateway)
		if err = m.writeNodeFile(ctx, n, path, []byte(text), 0644); err != nil {
			return err
		}
		if err = m.ensureNodeInclude(ctx, n); err != nil {
			return err
		}
		if err = n.run(ctx, "ifup", network.Bridge); err != nil {
			return err
		}
	}
	return m.save()
}
func (m *Migration) OwnCandidate(vmid int) error {
	if !m.Prepared || vmid < 100 || m.CandidateVMID != 0 {
		return errors.New("invalid migration candidate ownership transition")
	}
	m.CandidateVMID = vmid
	return m.save()
}
func (m *Migration) CandidateDescription() string { return "Ludus migration candidate " + m.Directory }
func (m *Migration) guest(ctx context.Context, args ...string) error {
	if m.CandidateVMID == 0 {
		return errors.New("migration does not own a candidate")
	}
	argv := append([]string{"exec", strconv.Itoa(m.CandidateVMID), "--"}, args...)
	return commandRun(ctx, "pct", argv...)
}
func (m *Migration) push(ctx context.Context, source, destination string, mode os.FileMode) error {
	return commandRun(ctx, "pct", "push", strconv.Itoa(m.CandidateVMID), source, destination, "--perms", fmt.Sprintf("%04o", mode.Perm()), "--user", "0", "--group", "0")
}
func migrationStaticFingerprint() (string, error) {
	var roots []string
	for _, name := range []string{"ranges", "users", "templates", "resources", "blueprints", "sources", "tls", "license.lic", "cert.pem", "key.pem", "config.yml", "install/root-api-key", "ansible/server-config.yml"} {
		roots = append(roots, filepath.Join("/opt/ludus", name))
	}
	for _, name := range []string{".ssh", ".gitconfig", ".git-credentials"} {
		roots = append(roots, filepath.Join("/home/ludus", name))
	}
	return migrationFingerprintPaths(roots)
}
func migrationFingerprintPaths(roots []string) (string, error) {
	h := sha256.New()
	for _, root := range roots {
		_, err := os.Lstat(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		resolvedRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return "", err
		}
		// Match migrationCopyTree: fingerprint exported referents under their
		// logical names, including symlinked roots, without escaping the tree.
		ancestors := map[string]bool{}
		var visit func(string, string) error
		visit = func(from, logical string) error {
			resolved, err := filepath.EvalSymlinks(from)
			if err != nil {
				return err
			}
			if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(os.PathSeparator)) {
				return fmt.Errorf("migration symlink escapes source tree: %s", from)
			}
			st, err := os.Stat(resolved)
			if err != nil {
				return err
			}
			if st.IsDir() {
				if ancestors[resolved] {
					return fmt.Errorf("migration symlink cycle: %s", from)
				}
				ancestors[resolved] = true
				defer delete(ancestors, resolved)
				fmt.Fprintf(h, "%s\x00directory\x00%d\x00", logical, st.Mode().Perm())
				entries, err := os.ReadDir(resolved)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if err := visit(filepath.Join(resolved, entry.Name()), filepath.Join(logical, entry.Name())); err != nil {
						return err
					}
				}
				return nil
			}
			if st.Mode()&os.ModeSocket != 0 && strings.Contains(from, "/.ansible/cp/") {
				return nil
			}
			if !st.Mode().IsRegular() {
				return fmt.Errorf("unsupported migration file type: %s", from)
			}
			fmt.Fprintf(h, "%s\x00file\x00%d\x00%d\x00", logical, st.Mode().Perm(), st.Size())
			file, err := os.Open(resolved)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(h, file)
			return errors.Join(copyErr, file.Close())
		}
		if err := visit(root, root); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (m *Migration) StageCandidateState(ctx context.Context) error {
	before, err := migrationStaticFingerprint()
	if err != nil {
		return err
	}
	archive := filepath.Join(m.Directory, "state.tar.gz")
	if err = exportMigrationState(archive, false, true); err != nil {
		return err
	}
	after, err := migrationStaticFingerprint()
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("Ludus files changed while candidate state was staged; retry after the active command finishes")
	}
	m.Fingerprint = after
	if err = m.save(); err != nil {
		return err
	}
	if err = m.guest(ctx, "systemctl", "stop", "ludus-admin", "ludus"); err != nil {
		return err
	}
	if err = m.push(ctx, filepath.Join(m.Directory, "ludus-installer"), migrationGuestBinary, 0700); err != nil {
		return err
	}
	if err = m.guest(ctx, "mv", "/opt/ludus/install/.bootstrap-complete", "/run/ludus-staged-bootstrap-complete"); err != nil {
		return err
	}
	if err = m.push(ctx, archive, "/tmp/ludus-import.tar.gz", 0600); err != nil {
		return err
	}
	if err = m.guest(ctx, migrationGuestBinary, "--import-state", "/tmp/ludus-import.tar.gz"); err != nil {
		return err
	}
	if err = m.guest(ctx, "rm", "/tmp/ludus-import.tar.gz"); err != nil {
		return err
	}
	if err = m.guest(ctx, "mv", "/run/ludus-staged-bootstrap-complete", "/opt/ludus/install/.bootstrap-complete"); err != nil {
		return err
	}
	var routes strings.Builder
	routes.WriteString("#!/bin/sh\n")
	for _, r := range m.Network.Ranges {
		fmt.Fprintf(&routes, "# LUDUS MANAGED BLOCK FOR RANGE %d BEGIN\nif [ \"$IFACE\" = \"eth1\" ]; then\n\tip route replace 10.%d.0.0/16 via 192.0.2.%d\nfi\n# LUDUS MANAGED BLOCK FOR RANGE %d END\n", r.Number, r.Number, 100+r.Number, r.Number)
	}
	path := filepath.Join(m.Directory, "ludus-routes")
	if err = atomicWriteFile(path, []byte(routes.String()), 0755); err != nil {
		return err
	}
	if err = m.push(ctx, path, "/etc/network/if-up.d/ludus-routes", 0755); err != nil {
		return err
	}
	return m.guest(ctx, "env", "IFACE=eth1", "/etc/network/if-up.d/ludus-routes")
}
func (m *Migration) Cutover(ctx context.Context) error {
	if err := migrationCheckSDNPending(ctx); err != nil {
		return err
	}
	currentSDN, err := migrationReadDirectory("/etc/pve/sdn", true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(currentSDN, m.StagedSDN) {
		return errors.New("SDN configuration changed during staging")
	}
	nodes, err := migrationClusterNodes(ctx)
	if err != nil {
		return err
	}
	for i := range nodes {
		nodes[i].Local = nodes[i].Name == m.Network.Owner
	}
	if !reflect.DeepEqual(nodes, m.Network.Nodes) {
		return errors.New("cluster membership changed during migration")
	}
	for _, node := range m.Network.Nodes {
		current, err := migrationSnapshot(ctx, migrationNodeIO{node}, true)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current.Files, m.Network.StagedFiles[node.Name]) {
			return fmt.Errorf("node network configuration changed during staging: %s", node.Name)
		}
	}
	if err = m.guest(ctx, "systemctl", "stop", "ludus-admin", "ludus"); err != nil {
		return err
	}
	if err = m.startAPIBridge(ctx); err != nil {
		return err
	}
	m.ServicesChanged = true
	if err = m.save(); err != nil {
		return err
	}
	if err = commandRun(ctx, "systemctl", "stop", "ludus", "ludus-admin"); err != nil {
		return err
	}
	current, err := migrationCollect("", false)
	if err != nil {
		return err
	}
	if !migrationStableInfo(m.Info, current) {
		return errors.New("installation changed during preflight; retry migration in a maintenance window")
	}
	fingerprint, err := migrationStaticFingerprint()
	if err != nil {
		return err
	}
	if m.Fingerprint == "" || fingerprint != m.Fingerprint {
		return errors.New("Ludus files changed after the LXC state was staged; retry migration")
	}
	archive := filepath.Join(m.Directory, "final-state.tar.gz")
	if err = migrationWriteFinalState(archive, m.Info.DataDirectory, current.DataDirectory, "/etc/wireguard", "/var/lib/misc/dnsmasq.leases"); err != nil {
		return err
	}
	if err = m.push(ctx, archive, "/tmp/ludus-final-state.tar.gz", 0600); err != nil {
		return err
	}
	if err = m.guest(ctx, "systemctl", "stop", "wg-quick@wg0"); err != nil {
		return err
	}
	if err = m.guest(ctx, migrationGuestBinary, "--migration-final-import", "/tmp/ludus-final-state.tar.gz"); err != nil {
		return err
	}
	if err = m.guest(ctx, "systemctl", "start", "wg-quick@wg0"); err != nil {
		return err
	}
	if err = m.forward(ctx, "wireguard"); err != nil {
		return err
	}
	if err = m.resetWireGuardSessions(ctx); err != nil {
		return err
	}
	if err = commandRun(ctx, "systemctl", "stop", "wg-quick@wg0", "dnsmasq"); err != nil {
		return err
	}
	m.CutoverStarted = true
	if err = m.save(); err != nil {
		return err
	}
	for _, node := range m.Network.Nodes {
		if err = m.nodeCutover(ctx, migrationNodeIO{node}, m.Network); err != nil {
			return err
		}
	}
	path := fmt.Sprintf("/nodes/%s/lxc/%d/config", m.Network.Owner, m.CandidateVMID)
	var config map[string]any
	if err = migrationAPI(ctx, path, &config); err != nil {
		return err
	}
	nic, ok := config["net1"].(string)
	if !ok {
		return errors.New("candidate is missing net1")
	}
	var fields []string
	for _, field := range strings.Split(nic, ",") {
		if !strings.HasPrefix(field, "link_down=") {
			fields = append(fields, field)
		}
	}
	if err = commandRun(ctx, "pvesh", "set", path, "--net1", strings.Join(fields, ",")); err != nil {
		return err
	}
	if err = m.guest(ctx, "ip", "link", "set", "eth1", "up"); err != nil {
		return err
	}
	return m.guest(ctx, migrationGuestBinary, "--migration-announce", "eth1", "192.0.2.254")
}
func (m *Migration) MoveNICs(ctx context.Context) error {
	if err := m.moveGuestNICs(ctx, true); err != nil {
		return err
	}
	return m.guest(ctx, migrationGuestBinary, "--migration-announce", "eth1", "192.0.2.254")
}
func (m *Migration) StartForwarding(ctx context.Context) error { return m.forward(ctx, "start") }
func (m *Migration) resetWireGuardSessions(ctx context.Context) error {
	if m.Network.NoWireGuardForward {
		return nil
	}
	seen := map[string]bool{}
	for _, address := range m.Network.HostAddresses {
		if seen[address] {
			continue
		}
		seen[address] = true
		cmd := exec.CommandContext(ctx, "conntrack", "-D", "-p", "udp", "--orig-dst", address, "--dport", strconv.Itoa(m.Network.WireGuardPort))
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		out, err := cmd.CombinedOutput()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 && strings.Contains(string(out), "0 flow entries have been deleted") {
				continue
			}
			return fmt.Errorf("cannot reset previous WireGuard connection: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}
func (m *Migration) Commit(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(migrationInstalledBinary), 0755); err != nil {
		return err
	}
	// This is also the host's native status executable, owned by the installer cutover.
	if err := copyFile(filepath.Join(m.Directory, "ludus-installer"), migrationInstalledBinary, 0755); err != nil {
		return err
	}
	unitFile, err := os.OpenFile(migrationForwardUnit, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("cannot exclusively create migration forwarding service: %w", err)
	}
	m.UnitOwned = true
	if err = m.save(); err != nil {
		unitFile.Close()
		return err
	}
	unit := fmt.Sprintf("[Unit]\nDescription=Preserve the host-installed Ludus API and WireGuard endpoints\nWants=network-online.target\nAfter=network-online.target\nBefore=pve-guests.service\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=%s --migration-forward-start %s\nExecStop=%s --migration-forward-stop %s\n[Install]\nWantedBy=multi-user.target\n", migrationInstalledBinary, m.Directory, migrationInstalledBinary, m.Directory)
	_, writeErr := io.WriteString(unitFile, unit)
	if err = errors.Join(writeErr, unitFile.Close()); err != nil {
		return err
	}
	if err := commandRun(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := commandRun(ctx, "systemctl", "enable", "--now", "ludus-lxc-forwarding.service"); err != nil {
		return err
	}
	if err := commandRun(ctx, "systemctl", "disable", "ludus", "ludus-admin", "wg-quick@wg0", "dnsmasq"); err != nil {
		return err
	}
	if err := commandRun(ctx, "pct", "set", strconv.Itoa(m.CandidateVMID), "--onboot", "1"); err != nil {
		return err
	}
	if err := m.removeHelpers(ctx); err != nil {
		return err
	}
	m.Committed = true
	if err := m.save(); err != nil {
		m.Committed = false
		return err
	}
	m.releaseLock()
	logStep("Migration committed. Original data and rollback state: %s", m.Directory)
	return nil
}
func migrationStopCandidate(ctx context.Context, id int, run func(context.Context, string, ...string) ([]byte, error)) error {
	if id == 0 {
		return nil
	}
	vmid := strconv.Itoa(id)
	status, err := run(ctx, "pct", "status", vmid)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "status: stopped" {
		if _, err = run(ctx, "pct", "stop", vmid); err != nil {
			return fmt.Errorf("cannot stop candidate %d; refusing to reactivate duplicate host services: %w", id, err)
		}
	}
	if _, err = run(ctx, "pct", "set", vmid, "--onboot", "0"); err != nil {
		return fmt.Errorf("cannot disable candidate %d autostart; rollback incomplete: %w", id, err)
	}
	status, err = run(ctx, "pct", "status", vmid)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(status)) != "status: stopped" {
		return errors.New("candidate remains active; rollback incomplete")
	}
	return nil
}
func (m *Migration) Rollback(ctx context.Context) error {
	if m.Committed {
		m.releaseLock()
		return nil
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
	defer cancel()
	ctx = rollbackCtx
	defer m.releaseLock()
	var failures []error
	attempt := func(err error) {
		if err != nil {
			failures = append(failures, err)
		}
	}
	attempt(m.stopAPIBridge(ctx, false))
	// Never revive the original endpoints while an owned candidate can still serve them.
	if m.CandidateVMID != 0 {
		var config map[string]any
		path := fmt.Sprintf("/nodes/%s/lxc/%d/config", m.Network.Owner, m.CandidateVMID)
		if err := migrationAPI(ctx, path, &config); err != nil {
			return errors.Join(append(failures, err)...)
		}
		description, ok := config["description"].(string)
		// PVE's config parser appends one newline to the stored comment.
		// Normalize only that terminator, not independently edited whitespace.
		if !ok || strings.TrimSuffix(description, "\n") != m.CandidateDescription() {
			return errors.Join(append(failures, errors.New("candidate ownership marker changed; refusing to stop an unowned guest"))...)
		}
	}
	if err := migrationStopCandidate(ctx, m.CandidateVMID, commandOutput); err != nil {
		return errors.Join(append(failures, err)...)
	}
	if m.UnitOwned {
		attempt(commandRun(ctx, "systemctl", "disable", "--now", "ludus-lxc-forwarding.service"))
		if err := os.Remove(migrationForwardUnit); err != nil && !errors.Is(err, os.ErrNotExist) {
			attempt(err)
		}
		attempt(commandRun(ctx, "systemctl", "daemon-reload"))
	}
	if m.Mutated {
		if len(m.ChangedNICs) > 0 {
			attempt(m.moveGuestNICs(ctx, false))
		}
		if m.SDNChanged {
			attempt(m.restoreSDN(ctx))
		}
		attempt(m.removeOwnedRules(ctx))
		for _, node := range m.Network.Nodes {
			if m.SDNChanged || m.CutoverStarted || len(m.NetworkWrites[node.Name]) > 0 {
				attempt(m.nodeRestore(ctx, migrationNodeIO{node}, m.Network))
			}
		}
		attempt(m.resetWireGuardSessions(ctx))
	}
	attempt(m.removeHelpers(ctx))
	return errors.Join(failures...)
}
func (m *Migration) releaseLock() {
	if m.lock != nil {
		m.lock.Close()
		m.lock = nil
	}
}
func (m *Migration) removeHelpers(ctx context.Context) error {
	var failures []error
	for _, node := range m.Network.Nodes {
		path := m.Helpers[node.Name]
		if !node.Local && path != "" {
			if err := (migrationNodeIO{node}).run(ctx, "rm", "-rf", "--", filepath.Dir(path)); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}
