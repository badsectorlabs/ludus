package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var migrationIfacePattern = regexp.MustCompile(`^\s*iface\s+(\S+)(?:\s|$)`)
var migrationLegacyAddress = regexp.MustCompile(`^\s*address\s+192\.0\.2\.254(?:/24)?\s*$`)
var migrationLegacyNetmask = regexp.MustCompile(`^\s*netmask\s+255\.255\.255\.0\s*$`)
var migrationIncludePattern = regexp.MustCompile(`(?m)^\s*(?:source\s+/etc/network/interfaces\.d/\*|source-directory\s+/etc/network/interfaces\.d/?)\s*$`)

func migrationRemoveLegacyHooks(text, natBridge string, ranges []migrationRange) string {
	var result strings.Builder
	iface := ""
	var routes []*regexp.Regexp
	for _, r := range ranges {
		routes = append(routes, regexp.MustCompile(fmt.Sprintf(`\bip route (?:add|del|replace) 10\.%d\.0\.0/16\b`, r.Number)))
	}
	for _, line := range strings.SplitAfter(text, "\n") {
		match := migrationIfacePattern.FindStringSubmatch(line)
		if len(match) > 0 {
			iface = match[1]
		}
		if iface == natBridge {
			if migrationLegacyAddress.MatchString(line) || migrationLegacyNetmask.MatchString(line) {
				continue
			}
			if len(match) > 0 {
				line = strings.Replace(line, "inet static", "inet manual", 1)
			}
		}
		drop := false
		for _, route := range routes {
			if route.MatchString(line) {
				drop = true
				break
			}
		}
		if !drop {
			result.WriteString(line)
		}
	}
	return result.String()
}
func migrationRemoveRouteBlocks(text string, ranges []migrationRange) string {
	for _, r := range ranges {
		marker := regexp.QuoteMeta(fmt.Sprintf("# LUDUS MANAGED BLOCK FOR RANGE %d", r.Number))
		pattern := regexp.MustCompile(`(?ms)^` + marker + ` BEGIN\r?\n.*?^` + marker + ` END(?:\r?\n|$)`)
		text = pattern.ReplaceAllString(text, "")
	}
	return text
}
func (m *Migration) ensureNodeInclude(ctx context.Context, n migrationNodeIO) error {
	path := "/etc/network/interfaces"
	data, err := n.read(ctx, path)
	if err != nil {
		return err
	}
	if migrationIncludePattern.Match(data) {
		return nil
	}
	mode, err := n.command(ctx, nil, "stat", "-c", "%a", path)
	if err != nil {
		return err
	}
	permissions, err := strconv.ParseUint(strings.TrimSpace(string(mode)), 8, 32)
	if err != nil {
		return err
	}
	return m.writeNodeFile(ctx, n, path, append(data, []byte("\nsource /etc/network/interfaces.d/*\n")...), uint32(permissions))
}
func (m *Migration) nodeGateway(ctx context.Context, n migrationNodeIO, gateway string) error {
	if err := m.writeNodeFile(ctx, n, filepath.Join(migrationNetworkRoot, migrationGatewayFragment), []byte("iface ludusnat inet static\n\taddress "+gateway+"/24\n"), 0644); err != nil {
		return err
	}
	if err := m.ensureNodeInclude(ctx, n); err != nil {
		return err
	}
	return n.run(ctx, "ip", "address", "replace", gateway+"/24", "dev", "ludusnat")
}
func migrationVerifyGateway(ctx context.Context, n migrationNodeIO, gateway string, owner bool, bridges []string) error {
	data, err := n.command(ctx, nil, "ip", "-j", "-4", "address")
	if err != nil {
		return err
	}
	var links []migrationLink
	if err = json.Unmarshal(data, &links); err != nil {
		return err
	}
	count := 0
	for _, link := range links {
		for _, a := range link.Addresses {
			if a.Local == gateway {
				count++
				if !owner || link.Name != "ludusnat" || a.PrefixLen != 24 {
					return errors.New("NAT gateway must exist only on the owning host")
				}
			}
		}
	}
	if owner && count != 1 {
		return errors.New("NAT gateway must exist only on the owning host")
	}
	for _, bridge := range bridges {
		if !n.exists(ctx, filepath.Join("/sys/class/net", bridge, "bridge")) {
			return fmt.Errorf("SDN bridge not active: %s", bridge)
		}
	}
	return nil
}
func (m *Migration) nodeCutover(ctx context.Context, n migrationNodeIO, c migrationNetworkPlan) error {
	saved := c.Snapshots[n.Node.Name]
	for name, item := range c.StagedFiles[n.Node.Name] {
		if name == "interfaces" || (strings.HasPrefix(name, "interfaces.d/") && name != "interfaces.d/sdn" && name != migrationGatewayFragment) {
			text := migrationRemoveLegacyHooks(string(item.Data), c.NATBridge, c.Ranges)
			if text != string(item.Data) {
				if err := m.writeNodeFile(ctx, n, filepath.Join(migrationNetworkRoot, name), []byte(text), item.Mode); err != nil {
					return err
				}
			}
		}
		for _, hook := range migrationRouteHooks {
			if name == hook {
				text := migrationRemoveRouteBlocks(string(item.Data), c.Ranges)
				if text != string(item.Data) {
					if err := m.writeNodeFile(ctx, n, filepath.Join(migrationNetworkRoot, name), []byte(text), item.Mode); err != nil {
						return err
					}
				}
			}
		}
	}
	data, err := n.command(ctx, nil, "ip", "-j", "-4", "address")
	if err != nil {
		return err
	}
	var links []migrationLink
	if err = json.Unmarshal(data, &links); err != nil {
		return err
	}
	for _, link := range links {
		for _, a := range link.Addresses {
			if a.Local == "192.0.2.254" {
				if err = n.run(ctx, "ip", "address", "del", fmt.Sprintf("%s/%d", a.Local, a.PrefixLen), "dev", link.Name); err != nil {
					return err
				}
			}
		}
	}
	if n.Node.Local {
		if err = n.run(ctx, "ip", "address", "replace", c.NATGateway+"/24", "dev", "ludusnat"); err != nil {
			return err
		}
	}
	fragment := filepath.Join(migrationNetworkRoot, migrationGatewayFragment)
	text := "iface ludusnat inet manual\n"
	if n.exists(ctx, fragment) {
		data, err = n.read(ctx, fragment)
		if err != nil {
			return err
		}
		text = string(data)
	}
	for _, r := range c.Ranges {
		destination := fmt.Sprintf("10.%d.0.0/16", r.Number)
		routed := false
		for _, route := range saved.Routes {
			if route.Destination == destination && (route.Device == c.NATBridge || route.Device == "ludusnat") {
				routed = true
				break
			}
		}
		if routed {
			if err = n.run(ctx, "ip", "route", "replace", destination, "via", "192.0.2.254", "dev", "ludusnat", "onlink"); err != nil {
				return err
			}
			text += "\tpost-up ip route replace " + destination + " via 192.0.2.254 dev ludusnat onlink\n"
		}
	}
	if n.Node.Local {
		if err = n.run(ctx, "ip", "route", "replace", "198.51.100.0/24", "via", "192.0.2.254", "dev", "ludusnat"); err != nil {
			return err
		}
		text += "\tpost-up ip route replace 198.51.100.0/24 via 192.0.2.254 dev ludusnat\n"
	}
	if err = m.writeNodeFile(ctx, n, fragment, []byte(text), 0644); err != nil {
		return err
	}
	return m.ensureNodeInclude(ctx, n)
}
func (m *Migration) StageNetworks(ctx context.Context) error {
	if !m.Prepared {
		return errors.New("migration is not prepared")
	}
	m.SDNChanged = true
	if err := m.save(); err != nil {
		return err
	}
	c := m.Network
	if !c.ZoneExists {
		err := commandRun(ctx, "pvesh", "create", "/cluster/sdn/zones", "--zone", c.Zone, "--type", "simple", "--ipam", "pve")
		captureErr := m.captureSDN()
		if err != nil {
			return err
		}
		if captureErr != nil {
			return captureErr
		}
	}
	// Capture each completed change, so rollback never overwrites a concurrent administrator edit.
	if err := m.captureSDN(); err != nil {
		return err
	}
	for _, name := range migrationTargetBridges(c.Ranges) {
		var err error
		if _, ok := c.VNets[name]; !ok {
			aware := "1"
			if name == "ludusnat" {
				aware = "0"
			}
			err = commandRun(ctx, "pvesh", "create", "/cluster/sdn/vnets", "--vnet", name, "--zone", c.Zone, "--vlanaware", aware)
		} else if name != "ludusnat" && c.VNets[name].VLANAware == 0 {
			err = commandRun(ctx, "pvesh", "set", "/cluster/sdn/vnets/"+name, "--vlanaware", "1")
		}
		captureErr := m.captureSDN()
		if err != nil {
			return err
		}
		if captureErr != nil {
			return captureErr
		}
	}
	for _, subnet := range c.Subnets {
		args := []string{"set", "/cluster/sdn/vnets/ludusnat/subnets/" + subnet.Name, "--snat", "0"}
		if subnet.Gateway != "" {
			args = append(args, "--delete", "gateway")
		}
		err := commandRun(ctx, "pvesh", args...)
		captureErr := m.captureSDN()
		if err != nil {
			return err
		}
		if captureErr != nil {
			return captureErr
		}
	}
	if err := commandRun(ctx, "pvesh", "set", "/cluster/sdn"); err != nil {
		return err
	}
	// Removing an SDN-generated gateway must not retire the live host address yet.
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	for {
		var verifyErr error
		for _, node := range c.Nodes {
			if err := migrationVerifyGateway(waitCtx, migrationNodeIO{node}, c.NATGateway, false, migrationTargetBridges(c.Ranges)); err != nil {
				verifyErr = err
				break
			}
		}
		if verifyErr == nil {
			break
		}
		if err := migrationPause(waitCtx, time.Second); err != nil {
			return errors.Join(verifyErr, err)
		}
	}
	for _, link := range c.Snapshots[c.Owner].Addresses {
		for _, address := range link.Addresses {
			if address.Local == "192.0.2.254" {
				if err := commandRun(ctx, "ip", "address", "replace", fmt.Sprintf("%s/%d", address.Local, address.PrefixLen), "dev", link.Name); err != nil {
					return err
				}
			}
		}
	}
	for _, node := range c.Nodes {
		if node.Local {
			if err := m.nodeGateway(ctx, migrationNodeIO{node}, c.NATGateway); err != nil {
				return err
			}
		}
	}
	for _, node := range c.Nodes {
		if err := migrationVerifyGateway(ctx, migrationNodeIO{node}, c.NATGateway, node.Local, []string{"ludusnat"}); err != nil {
			return err
		}
	}
	m.Network.StagedFiles = map[string]map[string]migrationSavedFile{}
	for _, node := range c.Nodes {
		s, err := migrationSnapshot(ctx, migrationNodeIO{node}, true)
		if err != nil {
			return err
		}
		m.Network.StagedFiles[node.Name] = s.Files
	}
	if err := m.save(); err != nil {
		return err
	}
	return m.forward(ctx, "stage")
}
func migrationPause(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (m *Migration) captureSDN() error {
	files, err := migrationReadDirectory("/etc/pve/sdn", true)
	if err != nil {
		return err
	}
	m.StagedSDN = files
	return m.save()
}
func (m *Migration) moveGuestNICs(ctx context.Context, forward bool) error {
	var resources []migrationResource
	if err := migrationAPI(ctx, "/cluster/resources", &resources, "--type", "vm"); err != nil {
		return err
	}
	locations := map[int]string{}
	for _, vm := range resources {
		locations[vm.VMID] = vm.Node
	}
	var failures []error
	for _, guest := range m.Network.Guests {
		if !forward && !m.ChangedNICs[guest.VMID] {
			continue
		}
		err := func() error {
			if locations[guest.VMID] != guest.Node {
				return fmt.Errorf("VM %d moved nodes during migration", guest.VMID)
			}
			path := fmt.Sprintf("/nodes/%s/qemu/%d/config", guest.Node, guest.VMID)
			var actual map[string]any
			if err := migrationAPI(ctx, path, &actual, "--current", "1"); err != nil {
				return err
			}
			desired, previous := guest.New, guest.Old
			if !forward {
				desired, previous = guest.Old, guest.New
			}
			keys := make([]string, 0, len(desired))
			for key := range desired {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			args := []string{"set", path}
			for _, key := range keys {
				value, _ := actual[key].(string)
				if migrationNICEqual(value, desired[key]) {
					continue
				}
				if !migrationNICEqual(value, previous[key]) {
					return fmt.Errorf("VM %d %s changed independently; refusing to overwrite", guest.VMID, key)
				}
				args = append(args, "--"+key, desired[key])
			}
			if len(args) > 2 {
				if forward {
					m.ChangedNICs[guest.VMID] = true
					if err := m.save(); err != nil {
						return err
					}
				}
				if err := commandRun(ctx, "pvesh", args...); err != nil {
					return err
				}
			}
			if err := migrationAPI(ctx, path, &actual, "--current", "1"); err != nil {
				return err
			}
			for _, key := range keys {
				value, _ := actual[key].(string)
				if !migrationNICEqual(value, desired[key]) {
					return fmt.Errorf("VM %d %s configuration did not apply", guest.VMID, key)
				}
			}
			for _, node := range m.Network.Nodes {
				if node.Name == guest.Node {
					for _, key := range keys {
						if err := migrationVerifyTap(ctx, migrationNodeIO{node}, "/sys/class/net", guest.VMID, key, desired[key]); err != nil {
							return err
						}
					}
				}
			}
			return nil
		}()
		if err != nil {
			if forward {
				return err
			}
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (m *Migration) nodeRestore(ctx context.Context, n migrationNodeIO, c migrationNetworkPlan) error {
	saved := c.Snapshots[n.Node.Name]
	var failures []error
	attempt := func(err error) {
		if err != nil {
			failures = append(failures, err)
		}
	}
	attempt(m.restoreNodeFiles(ctx, n))
	data, err := n.command(ctx, nil, "ip", "-j", "-4", "address")
	attempt(err)
	var links []migrationLink
	if err == nil {
		attempt(json.Unmarshal(data, &links))
	}
	if n.Node.Local {
		for _, link := range links {
			for _, a := range link.Addresses {
				if a.Local == c.NATGateway && link.Name == "ludusnat" {
					attempt(n.run(ctx, "ip", "address", "del", c.NATGateway+"/24", "dev", link.Name))
				}
			}
		}
	}
	destinations := map[string]bool{"198.51.100.0/24": true}
	for _, r := range c.Ranges {
		destinations[fmt.Sprintf("10.%d.0.0/16", r.Number)] = true
	}
	if m.CutoverStarted {
		for destination := range destinations {
			data, err = n.command(ctx, nil, "ip", "-j", "-4", "route", "show", destination)
			attempt(err)
			if err != nil {
				continue
			}
			var routes []migrationRoute
			err = json.Unmarshal(data, &routes)
			attempt(err)
			if err != nil {
				continue
			}
			for _, route := range routes {
				if route.Gateway == "192.0.2.254" && route.Device == "ludusnat" {
					attempt(n.run(ctx, "ip", "route", "del", destination, "via", "192.0.2.254", "dev", "ludusnat"))
				}
			}
		}
	}
	attempt(n.run(ctx, "ifreload", "-a"))
	if n.Node.Local && c.AutoManagement && n.exists(ctx, filepath.Join("/sys/class/net", c.Bridge)) {
		attempt(n.run(ctx, "ip", "link", "delete", c.Bridge))
	}
	if m.CutoverStarted {
		for _, route := range saved.Routes {
			if destinations[route.Destination] && route.Gateway != "" && (route.Device == c.NATBridge || route.Device == "ludusnat") {
				args := []string{"route", "replace", route.Destination, "via", route.Gateway, "dev", route.Device}
				if route.Table != nil && fmt.Sprint(route.Table) != "main" && fmt.Sprint(route.Table) != "254" {
					args = append(args, "table", fmt.Sprint(route.Table))
				}
				attempt(n.run(ctx, "ip", args...))
			}
		}
	}
	// Restore the legacy primary even when it was assigned outside ifupdown.
	for _, link := range saved.Addresses {
		for _, a := range link.Addresses {
			if a.Local == "192.0.2.254" {
				attempt(n.run(ctx, "ip", "address", "replace", fmt.Sprintf("%s/%d", a.Local, a.PrefixLen), "dev", link.Name))
			}
		}
	}
	// Forwarding sysctls are shared host state, not owned firewall rules.
	// Leave them enabled: restoring a snapshot could disable another service
	// installed since the migration began.
	// Firewall rollback is performed separately using only rules owned by this migration.
	if n.Node.Local && m.ServicesChanged {
		attempt(migrationRestoreServices(ctx, saved.Services, n.run))
	}
	for _, link := range saved.Addresses {
		for _, a := range link.Addresses {
			if a.Local == "192.0.2.254" {
				attempt(migrationAnnounceAddress(ctx, link.Name, a.Local))
			}
		}
	}
	return errors.Join(failures...)
}
func migrationRestoreServices(ctx context.Context, services map[string]migrationServiceState, run func(context.Context, string, ...string) error) error {
	var failures []error
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, service := range names {
		state := services[service]
		switch state.Enablement {
		case "enabled", "disabled", "masked":
			action := map[string]string{"enabled": "enable", "disabled": "disable", "masked": "mask"}[state.Enablement]
			if err := run(ctx, "systemctl", action, service); err != nil {
				failures = append(failures, err)
			}
		}
		action := "stop"
		if state.Active {
			action = "start"
		}
		if err := run(ctx, "systemctl", action, service); err != nil {
			failures = append(failures, fmt.Errorf("restore service %s: %w", service, err))
		}
	}
	return errors.Join(failures...)
}
func (m *Migration) restoreSDN(ctx context.Context) error {
	current, err := migrationReadDirectory("/etc/pve/sdn", true)
	if err != nil {
		return err
	}
	for _, name := range []string{"zones.cfg", "vnets.cfg", "subnets.cfg"} {
		original, was := m.OriginalSDN[name]
		actual, exists := current[name]
		staged, stagedExists := m.StagedSDN[name]
		if was == exists && reflect.DeepEqual(original, actual) {
			continue
		}
		if exists != stagedExists || !reflect.DeepEqual(actual, staged) {
			return fmt.Errorf("SDN %s changed independently; refusing to overwrite; restore from %s", name, m.Directory)
		}
		path := filepath.Join("/etc/pve/sdn", name)
		if was {
			if err = os.WriteFile(path, original.Data, os.FileMode(original.Mode)); err != nil {
				return err
			}
		} else if exists {
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	// Only the NAT subnet's IPAM identity belongs to this operation. Other subnets remain untouched.
	ipamPath := "/etc/pve/sdn/pve-ipam-state.json"
	if fileExists(ipamPath) {
		data, err := os.ReadFile(ipamPath)
		if err != nil {
			return err
		}
		var current map[string]any
		if len(data) > 0 {
			if err = json.Unmarshal(data, &current); err != nil {
				return err
			}
		}
		var saved map[string]any
		if original, ok := m.OriginalSDN["pve-ipam-state.json"]; ok && len(original.Data) > 0 {
			if err = json.Unmarshal(original.Data, &saved); err != nil {
				return err
			}
		}
		current = migrationRestoreIPAM(current, saved, m.Network.Zone)
		data, err = json.Marshal(current)
		if err != nil {
			return err
		}
		if err = os.WriteFile(ipamPath, data, 0600); err != nil {
			return err
		}
	}
	return commandRun(ctx, "pvesh", "set", "/cluster/sdn")
}
func migrationMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return nil
	}
	child, _ := parent[key].(map[string]any)
	return child
}
func migrationRestoreIPAM(current, saved map[string]any, zone string) map[string]any {
	if current == nil {
		current = map[string]any{}
	}
	savedZone := migrationMap(migrationMap(saved, "zones"), zone)
	old, existed := migrationMap(savedZone, "subnets")["192.0.2.0/24"]
	zones := migrationMap(current, "zones")
	if zones == nil {
		zones = map[string]any{}
		current["zones"] = zones
	}
	currentZone := migrationMap(zones, zone)
	if existed {
		if currentZone == nil {
			currentZone = map[string]any{}
			zones[zone] = currentZone
		}
		subnets := migrationMap(currentZone, "subnets")
		if subnets == nil {
			subnets = map[string]any{}
			currentZone["subnets"] = subnets
		}
		subnets["192.0.2.0/24"] = old
	} else if currentZone != nil {
		subnets := migrationMap(currentZone, "subnets")
		delete(subnets, "192.0.2.0/24")
		if len(subnets) == 0 && len(savedZone) == 0 {
			delete(zones, zone)
		}
	}
	return current
}
