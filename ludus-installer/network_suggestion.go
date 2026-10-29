package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

type managementAddressSuggestion struct {
	IP, Gateway string
}

type managementSuggestionCommands struct {
	pve    func(context.Context, string, any, ...string) error
	output func(context.Context, string, ...string) ([]byte, error)
}

type managementNetwork struct {
	Name      string          `json:"iface"`
	Type      string          `json:"type"`
	Active    json.RawMessage `json:"active"`
	Address   string          `json:"address"`
	CIDR      string          `json:"cidr"`
	Netmask   string          `json:"netmask"`
	Gateway   string          `json:"gateway"`
	OVSBridge string          `json:"ovs_bridge"`
	OVSTag    json.RawMessage `json:"ovs_tag"`
}

type managementInterface struct {
	Name      string   `json:"ifname"`
	Index     int      `json:"ifindex"`
	LinkIndex int      `json:"link_index"`
	Flags     []string `json:"flags"`
	Addresses []struct {
		Local     string   `json:"local"`
		PrefixLen int      `json:"prefixlen"`
		Flags     []string `json:"flags"`
		Tentative bool     `json:"tentative"`
		DADFailed bool     `json:"dadfailed"`
	} `json:"addr_info"`
	LinkInfo struct {
		Kind string `json:"info_kind"`
		Data struct {
			ID int `json:"id"`
		} `json:"info_data"`
	} `json:"linkinfo"`
}

type managementNeighbor struct {
	Address string `json:"dst"`
	LLAddr  string `json:"lladdr"`
}

// A suggestion is only a bounded conflict check, not a reservation or proof that
// an address is unused. It is intentionally separate from unattended defaults.
func suggestManagementAddress(ctx context.Context, inv *Inventory, bridge, vlan, gateway string) (managementAddressSuggestion, error) {
	return suggestManagementAddressWithCommands(ctx, inv, bridge, vlan, gateway, managementSuggestionCommands{
		pve: readPVEJSON, output: commandOutput,
	})
}

func suggestManagementAddressWithCommands(ctx context.Context, inv *Inventory, bridge, vlan, gateway string, commands managementSuggestionCommands) (managementAddressSuggestion, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	unavailable := func(err error) (managementAddressSuggestion, error) {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return managementAddressSuggestion{}, fmt.Errorf("address suggestion unavailable: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return unavailable(err)
	}
	if inv == nil || inv.Node == "" || bridge == "" {
		return unavailable(fmt.Errorf("select a known Proxmox bridge first"))
	}
	tag := 0
	if vlan != "" && vlan != "none" {
		var err error
		tag, err = strconv.Atoi(vlan)
		if err != nil || tag < 1 || tag > 4094 {
			return unavailable(fmt.Errorf("invalid VLAN tag"))
		}
	}
	var networks []managementNetwork
	if err := commands.pve(ctx, "/nodes/"+inv.Node+"/network", &networks); err != nil {
		return unavailable(err)
	}
	var addresses []managementInterface
	if err := managementReadIPJSON(ctx, commands, &addresses, "-j", "-4", "addr", "show"); err != nil {
		return unavailable(err)
	}
	var links []managementInterface
	if tag != 0 {
		if err := managementReadIPJSON(ctx, commands, &links, "-j", "-d", "link", "show"); err != nil {
			return unavailable(err)
		}
	}
	bridgeIndex := 0
	for _, link := range links {
		if link.Name == bridge {
			bridgeIndex = link.Index
		}
	}
	var selected *managementNetwork
	for i := range networks {
		network := &networks[i]
		matches := tag == 0 && network.Name == bridge && (network.Type == "bridge" || network.Type == "OVSBridge")
		if tag != 0 && network.Type == "vlan" {
			for _, link := range links {
				if bridgeIndex != 0 && link.Name == network.Name && link.LinkIndex == bridgeIndex && link.LinkInfo.Kind == "vlan" && link.LinkInfo.Data.ID == tag {
					matches = true
				}
			}
		}
		if tag != 0 && network.Type == "OVSIntPort" && network.OVSBridge == bridge {
			ovsTag, err := apiInteger(network.OVSTag)
			if err == nil && ovsTag == int64(tag) {
				// The API can contain unapplied edits. Confirm the live OVS topology.
				liveBridge, err := commands.output(ctx, "ovs-vsctl", "--timeout=2", "iface-to-br", network.Name)
				if err != nil {
					return unavailable(fmt.Errorf("cannot confirm live OVS bridge: %w", err))
				}
				liveTag, err := commands.output(ctx, "ovs-vsctl", "--timeout=2", "get", "port", network.Name, "tag")
				if err != nil {
					return unavailable(fmt.Errorf("cannot confirm live OVS tag: %w", err))
				}
				matches = strings.TrimSpace(string(liveBridge)) == bridge && strings.TrimSpace(string(liveTag)) == strconv.Itoa(tag)
			}
		}
		if !matches || !apiEnabled(network.Active, false) {
			continue
		}
		if selected != nil {
			return unavailable(fmt.Errorf("multiple active interfaces match this bridge/VLAN"))
		}
		selected = network
	}
	if selected == nil {
		return unavailable(fmt.Errorf("no active host interface proves this bridge/VLAN's subnet"))
	}
	configured, err := managementNetworkPrefix(*selected)
	if err != nil {
		return unavailable(err)
	}
	prefix := configured.Masked()
	if prefix.Bits() > 30 {
		return unavailable(fmt.Errorf("IPv4 /31 and /32 networks cannot supply a suggested guest address"))
	}
	excluded := make(map[netip.Addr]bool)
	exclude := func(value string) {
		if ip, err := netip.ParseAddr(value); err == nil && ip.Is4() {
			excluded[ip] = true
		}
	}
	exclude(inv.LocalIP)
	exclude(gateway)
	for _, node := range inv.Nodes {
		exclude(node.IP)
	}
	confirmed := false
	for _, iface := range addresses {
		for _, address := range iface.Addresses {
			exclude(address.Local)
			if iface.Name != selected.Name {
				continue
			}
			ip, err := netip.ParseAddr(address.Local)
			if err != nil || !ip.Is4() {
				continue
			}
			live := netip.PrefixFrom(ip, address.PrefixLen)
			if !live.IsValid() || live.Masked() != prefix {
				return unavailable(fmt.Errorf("active interface has ambiguous IPv4 subnets"))
			}
			if live == configured && slices.Contains(iface.Flags, "UP") && !address.Tentative && !address.DADFailed && !slices.Contains(address.Flags, "tentative") && !slices.Contains(address.Flags, "dadfailed") {
				confirmed = true
			}
		}
	}
	if !confirmed {
		return unavailable(fmt.Errorf("configured IPv4 prefix is not active on the matched interface"))
	}
	var routes []struct {
		Destination string `json:"dst"`
		Device      string `json:"dev"`
		Gateway     string `json:"gateway"`
	}
	if err := managementReadIPJSON(ctx, commands, &routes, "-j", "-4", "route", "show"); err != nil {
		return unavailable(err)
	}
	network := managementIPv4Number(prefix.Addr())
	broadcast := network | (^uint32(0) >> prefix.Bits())
	gateways := make(map[netip.Addr]bool)
	addGateway := func(value string) error {
		if value == "" {
			return nil
		}
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.Is4() || !prefix.Contains(ip) || managementIPv4Number(ip) == network || managementIPv4Number(ip) == broadcast {
			return fmt.Errorf("configured gateway does not match the active IPv4 subnet")
		}
		gateways[ip] = true
		excluded[ip] = true
		return nil
	}
	if err := addGateway(selected.Gateway); err != nil {
		return unavailable(err)
	}
	for _, route := range routes {
		if route.Device == selected.Name {
			exclude(route.Gateway)
		}
		if route.Device == selected.Name && (route.Destination == "default" || route.Destination == "0.0.0.0/0") {
			if err := addGateway(route.Gateway); err != nil {
				return unavailable(err)
			}
		}
	}
	if len(gateways) > 1 {
		return unavailable(fmt.Errorf("multiple gateways match the selected subnet"))
	}
	result := managementAddressSuggestion{}
	for ip := range gateways {
		result.Gateway = ip.String()
	}
	readNeighbors := func() error {
		var neighbors []managementNeighbor
		if err := managementReadIPJSON(ctx, commands, &neighbors, "-j", "-4", "neigh", "show", "dev", selected.Name); err != nil {
			return err
		}
		for _, neighbor := range neighbors {
			if neighbor.LLAddr != "" {
				exclude(neighbor.Address)
			}
		}
		return nil
	}
	if err := readNeighbors(); err != nil {
		return unavailable(fmt.Errorf("cannot inspect resolved neighbors: %w", err))
	}
	for offset := uint32(2); offset < 34 && offset < broadcast-network; offset++ {
		if err := ctx.Err(); err != nil {
			return unavailable(err)
		}
		candidate := managementIPv4Address(network + offset)
		if excluded[candidate] || !candidate.IsGlobalUnicast() {
			continue
		}
		_, err := commands.output(ctx, "ping", "-n", "-c", "1", "-W", "1", "-I", selected.Name, candidate.String())
		if ctx.Err() != nil {
			return unavailable(ctx.Err())
		}
		if err == nil {
			continue
		}
		var exit interface{ ExitCode() int }
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return unavailable(fmt.Errorf("cannot probe an address on %s: %w", selected.Name, err))
		}
		// ICMP-filtered hosts may still answer ARP. FAILED/INCOMPLETE entries
		// without a link-layer address are not evidence of an occupied address.
		if err := readNeighbors(); err != nil {
			return unavailable(fmt.Errorf("cannot inspect probe neighbors: %w", err))
		}
		if err := ctx.Err(); err != nil {
			return unavailable(err)
		}
		if !excluded[candidate] {
			result.IP = netip.PrefixFrom(candidate, prefix.Bits()).String()
			return result, nil
		}
	}
	return unavailable(fmt.Errorf("no candidate found within the bounded subnet conflict check"))
}

func managementReadIPJSON(ctx context.Context, commands managementSuggestionCommands, result any, args ...string) error {
	data, err := commands.output(ctx, "ip", args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("invalid interface data: %w", err)
	}
	return nil
}

func managementNetworkPrefix(network managementNetwork) (netip.Prefix, error) {
	value := network.CIDR
	if value == "" {
		value = network.Address
		if value != "" && !strings.Contains(value, "/") {
			bits, err := strconv.Atoi(network.Netmask)
			if err != nil {
				mask := net.ParseIP(network.Netmask).To4()
				if mask == nil {
					return netip.Prefix{}, fmt.Errorf("configured interface has no usable IPv4 prefix")
				}
				var size int
				bits, size = net.IPMask(mask).Size()
				if size != 32 {
					return netip.Prefix{}, fmt.Errorf("configured interface has an invalid IPv4 netmask")
				}
			}
			value += "/" + strconv.Itoa(bits)
		}
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() {
		return netip.Prefix{}, fmt.Errorf("configured interface has no usable IPv4 prefix")
	}
	return prefix, nil
}

func managementIPv4Number(ip netip.Addr) uint32 {
	bytes := ip.As4()
	return binary.BigEndian.Uint32(bytes[:])
}

func managementIPv4Address(value uint32) netip.Addr {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], value)
	return netip.AddrFrom4(bytes)
}
