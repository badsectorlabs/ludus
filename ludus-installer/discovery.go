package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

func readPVEJSON(ctx context.Context, path string, result any, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	command := append([]string{"get", path}, args...)
	command = append(command, "--output-format", "json")
	data, err := commandOutput(ctx, "pvesh", command...)
	if err != nil {
		return fmt.Errorf("discover %s: %w", path, err)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("Proxmox returned invalid JSON for %s: %w", path, err)
	}
	return nil
}

func apiEnabled(raw json.RawMessage, missing bool) bool {
	if len(raw) == 0 {
		return missing
	}
	return string(raw) == "true" || string(raw) == "1" || string(raw) == `"1"`
}

func apiInteger(raw json.RawMessage) (int64, error) {
	value := string(raw)
	if strings.HasPrefix(value, `"`) {
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, err
		}
	}
	return strconv.ParseInt(value, 10, 64)
}

func discoverInventory(ctx context.Context) (*Inventory, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	inv := &Inventory{Node: hostname, VMIDs: make(map[int]bool)}
	var nodes []struct {
		Name  string          `json:"name"`
		Type  string          `json:"type"`
		IP    string          `json:"ip"`
		Local json.RawMessage `json:"local"`
	}
	if err := readPVEJSON(ctx, "/cluster/status", &nodes); err != nil {
		return nil, err
	}
	for _, node := range nodes {
		if node.Type != "node" {
			continue
		}
		local := apiEnabled(node.Local, false) || node.Name == hostname
		inv.Nodes = append(inv.Nodes, ClusterNode{Name: node.Name, IP: node.IP, Local: local})
		if node.IP != "" {
			inv.Endpoints = append(inv.Endpoints, "https://"+net.JoinHostPort(node.IP, "8006"))
		}
		if local {
			inv.LocalIP = node.IP
			if node.Name != "" {
				inv.Node = node.Name
			}
		}
	}
	var resources []struct {
		VMID json.RawMessage `json:"vmid"`
	}
	if err := readPVEJSON(ctx, "/cluster/resources", &resources, "--type", "vm"); err != nil {
		return nil, err
	}
	for _, resource := range resources {
		id, err := apiInteger(resource.VMID)
		if err != nil || id <= 0 || id > 999999999 {
			return nil, fmt.Errorf("cannot determine occupied VMIDs from Proxmox resources")
		}
		inv.VMIDs[int(id)] = true
	}
	var next json.RawMessage
	if err := readPVEJSON(ctx, "/cluster/nextid", &next); err != nil {
		return nil, err
	}
	nextID, err := apiInteger(next)
	if err != nil || nextID < 100 || nextID > 999999999 {
		return nil, fmt.Errorf("Proxmox returned an invalid next VMID")
	}
	inv.NextVMID = int(nextID)
	var stores []struct {
		Name    string          `json:"storage"`
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Active  json.RawMessage `json:"active"`
		Enabled json.RawMessage `json:"enabled"`
		Shared  json.RawMessage `json:"shared"`
		Avail   json.RawMessage `json:"avail"`
	}
	if err := readPVEJSON(ctx, "/nodes/"+inv.Node+"/storage", &stores); err != nil {
		return nil, err
	}
	for _, store := range stores {
		if store.Name == "" {
			continue
		}
		var content string
		if err := json.Unmarshal(store.Content, &content); err != nil {
			var contents []string
			if err := json.Unmarshal(store.Content, &contents); err != nil {
				return nil, fmt.Errorf("storage %s has an invalid content list", store.Name)
			}
			content = strings.Join(contents, ",")
		}
		available := int64(0)
		if len(store.Avail) > 0 {
			available, err = apiInteger(store.Avail)
			if err != nil || available < 0 {
				return nil, fmt.Errorf("storage %s has an invalid available size", store.Name)
			}
		}
		inv.Storages = append(inv.Storages, StorageInfo{
			Name: store.Name, Type: store.Type, Content: content,
			Active: apiEnabled(store.Active, false) && apiEnabled(store.Enabled, true),
			Shared: apiEnabled(store.Shared, false), Available: available,
		})
	}
	var networks []struct {
		Name string `json:"iface"`
		Type string `json:"type"`
	}
	if err := readPVEJSON(ctx, "/nodes/"+inv.Node+"/network", &networks); err != nil {
		return nil, err
	}
	for _, network := range networks {
		if network.Name != "" && (network.Type == "bridge" || network.Type == "OVSBridge") && !slices.Contains(inv.Bridges, network.Name) {
			inv.Bridges = append(inv.Bridges, network.Name)
		}
	}
	// Proxmox only reports bridges defined in /etc/network/interfaces itself, but
	// the container is created on this node and can use any bridge that exists
	// here, including ones from sourced files such as /etc/network/interfaces.d/.
	var vnets []struct {
		Name string `json:"vnet"`
	}
	sdnVNets := map[string]bool{"ludusnat": true}
	if readPVEJSON(ctx, "/cluster/sdn/vnets", &vnets) == nil {
		for _, vnet := range vnets {
			sdnVNets[vnet.Name] = true
		}
	}
	for _, bridge := range localKernelBridges("/sys/class/net", sdnVNets) {
		if !slices.Contains(inv.Bridges, bridge) {
			inv.Bridges = append(inv.Bridges, bridge)
		}
	}
	sort.Strings(inv.Bridges)
	var users []struct {
		UserID string `json:"userid"`
	}
	if err := readPVEJSON(ctx, "/access/users", &users); err != nil {
		return nil, err
	}
	inv.ProxmoxUsers = make(map[string]bool, len(users))
	for _, user := range users {
		inv.ProxmoxUsers[user.UserID] = true
	}
	var pools []struct {
		PoolID string `json:"poolid"`
	}
	if err := readPVEJSON(ctx, "/pools", &pools); err != nil {
		return nil, err
	}
	inv.Pools = make(map[string]bool, len(pools))
	for _, pool := range pools {
		inv.Pools[pool.PoolID] = true
	}
	return inv, nil
}

// localKernelBridges lists Linux bridges under the sysfs root, excluding SDN
// VNets and the per-guest firewall bridges Proxmox creates (fwbr<vmid>i<n>).
func localKernelBridges(root string, sdnVNets map[string]bool) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var bridges []string
	for _, entry := range entries {
		name := entry.Name()
		if sdnVNets[name] || strings.HasPrefix(name, "fwbr") {
			continue
		}
		if info, err := os.Stat(filepath.Join(root, name, "bridge")); err == nil && info.IsDir() {
			bridges = append(bridges, name)
		}
	}
	return bridges
}

func hasStorageContent(content, wanted string) bool {
	for entry := range strings.SplitSeq(content, ",") {
		if entry == wanted {
			return true
		}
	}
	return false
}

func storageChoices(inv *Inventory, content string, shared bool) []StorageInfo {
	var choices []StorageInfo
	for _, store := range inv.Storages {
		if store.Active && (!shared || store.Shared) && hasStorageContent(store.Content, content) {
			choices = append(choices, store)
		}
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].Name < choices[j].Name })
	return choices
}

func storageFormat(backend string) string {
	switch backend {
	case "dir", "nfs", "cifs", "cephfs", "glusterfs", "btrfs":
		return "qcow2"
	default:
		return "raw"
	}
}

func applyDefaults(opts *Options, inv *Inventory) error {
	opts.Node = inv.Node
	if opts.VMID == 0 && !opts.Provided["vmid"] {
		opts.VMID = inv.NextVMID
	}
	if opts.Endpoints == "" {
		opts.Endpoints = strings.Join(inv.Endpoints, " ")
	}
	if opts.Hostname == "" {
		opts.Hostname = "ludus"
	}
	if opts.RootFSSize == 0 && !opts.Provided["rootfs-size"] {
		opts.RootFSSize = 250
	}
	if opts.WireGuardPort == 0 && !opts.Provided["wg-port"] && !opts.MigrateHost {
		opts.WireGuardPort = 51820
	}
	if opts.WireGuardEndpoint == "" && !opts.MigrateHost {
		opts.WireGuardEndpoint = inv.LocalIP
		if opts.WireGuardEndpoint == "" {
			return fmt.Errorf("cannot determine the local Proxmox host IP; supply --wg-endpoint")
		}
	}
	if opts.IP == "" && opts.MigrateHost {
		return fmt.Errorf("migration plan did not provide a management IP")
	}
	if opts.VLANTag == "none" {
		opts.VLANTag = ""
	}
	if opts.License == "" {
		if opts.LicenseFile != "" {
			return fmt.Errorf("--license is required with --license-file")
		}
		opts.License = "community"
	}
	if opts.Bridge == "" {
		if opts.MigrateHost {
			return fmt.Errorf("migration plan did not provide a management bridge")
		}
		if len(inv.Bridges) == 0 {
			return fmt.Errorf("no management bridges are available")
		}
		opts.Bridge = inv.Bridges[0]
		if slices.Contains(inv.Bridges, "vmbr0") {
			opts.Bridge = "vmbr0"
		}
	}
	for _, selection := range []struct {
		value                     *string
		content, preferred, label string
		shared                    bool
	}{
		{&opts.Storage, "rootdir", "local-lvm", "container root filesystems", false},
		{&opts.VMStorage, "images", "local", "VM images", false},
		{&opts.ISOStorage, "iso", "local", "ISOs", opts.Offline},
	} {
		choices := storageChoices(inv, selection.content, selection.shared)
		if len(choices) == 0 {
			return fmt.Errorf("no active storage supports %s (shared storage required: %t)", selection.label, selection.shared)
		}
		if *selection.value != "" {
			continue
		}
		*selection.value = choices[0].Name
		for _, store := range choices {
			if store.Name == selection.preferred {
				*selection.value = store.Name
				break
			}
		}
	}
	if opts.VMStorageFormat == "" {
		for _, store := range inv.Storages {
			if store.Name == opts.VMStorage {
				opts.VMStorageFormat = storageFormat(store.Type)
				break
			}
		}
	}
	return nil
}
