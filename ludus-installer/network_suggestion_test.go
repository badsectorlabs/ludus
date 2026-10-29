package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type managementProbeExit int

func (e managementProbeExit) Error() string { return fmt.Sprintf("exit %d", int(e)) }
func (e managementProbeExit) ExitCode() int { return int(e) }

type managementSuggestionFixture struct {
	networks  string
	addresses string
	links     string
	routes    string
	neighbors []string
	probes    []string
	ping      func(context.Context, string) error
}

func newManagementSuggestionFixture() *managementSuggestionFixture {
	return &managementSuggestionFixture{
		networks:  `[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.254/24","gateway":"192.0.2.1"}]`,
		addresses: `[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":"192.0.2.254","prefixlen":24}]}]`,
		links:     `[]`,
		routes:    `[{"dst":"default","dev":"vmbr0","gateway":"192.0.2.1"}]`,
		neighbors: []string{`[]`},
	}
}

func (f *managementSuggestionFixture) commands(t *testing.T) managementSuggestionCommands {
	t.Helper()
	return managementSuggestionCommands{
		pve: func(ctx context.Context, path string, result any, args ...string) error {
			if path != "/nodes/pve/network" || len(args) != 0 {
				t.Fatalf("unexpected API read: %s %v", path, args)
			}
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 15*time.Second {
				t.Fatal("discovery lacks bounded deadline")
			}
			return json.Unmarshal([]byte(f.networks), result)
		},
		output: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "ping" {
				if len(args) != 8 || !reflect.DeepEqual(args[:6], []string{"-n", "-c", "1", "-W", "1", "-I"}) {
					t.Fatalf("unbounded or unscoped ping: %v", args)
				}
				f.probes = append(f.probes, args[6]+" "+args[7])
				if f.ping != nil {
					return nil, f.ping(ctx, args[7])
				}
				return nil, fmt.Errorf("ping: %w", managementProbeExit(1))
			}
			if name != "ip" {
				t.Fatalf("unexpected command: %s %v", name, args)
			}
			switch strings.Join(args, " ") {
			case "-j -4 addr show":
				return []byte(f.addresses), nil
			case "-j -d link show":
				return []byte(f.links), nil
			case "-j -4 route show":
				return []byte(f.routes), nil
			default:
				if len(args) == 6 && reflect.DeepEqual(args[:5], []string{"-j", "-4", "neigh", "show", "dev"}) {
					value := f.neighbors[0]
					if len(f.neighbors) > 1 {
						f.neighbors = f.neighbors[1:]
					}
					return []byte(value), nil
				}
				t.Fatalf("unexpected ip command: %v", args)
				return nil, nil
			}
		},
	}
}

func (f *managementSuggestionFixture) suggest(t *testing.T, vlan string) (managementAddressSuggestion, error) {
	t.Helper()
	return suggestManagementAddressWithCommands(context.Background(), &Inventory{Node: "pve"}, "vmbr0", vlan, "", f.commands(t))
}

func TestManagementSuggestionCandidateOrderingAndExclusions(t *testing.T) {
	f := newManagementSuggestionFixture()
	f.networks = `[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.2/24","gateway":"192.0.2.4"}]`
	f.addresses = `[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":"192.0.2.2","prefixlen":24}]},{"ifname":"other","addr_info":[{"local":"192.0.2.5","prefixlen":24}]}]`
	f.routes = `[]`
	f.neighbors = []string{`[{"dst":"192.0.2.6","lladdr":"00:11:22:33:44:55","state":["STALE"]}]`}
	inv := &Inventory{Node: "pve", LocalIP: "192.0.2.8", Nodes: []ClusterNode{{IP: "192.0.2.3"}}}
	got, err := suggestManagementAddressWithCommands(context.Background(), inv, "vmbr0", "none", "192.0.2.7", f.commands(t))
	if err != nil || got.IP != "192.0.2.9/24" || got.Gateway != "192.0.2.4" {
		t.Fatalf("suggestion = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(f.probes, []string{"vmbr0 192.0.2.9"}) {
		t.Fatalf("reserved addresses probed: %v", f.probes)
	}
}

func TestManagementSuggestionCIDRBoundaries(t *testing.T) {
	for _, tc := range []struct {
		cidr, local, want string
		bits              int
	}{
		{"192.0.2.129/25", "192.0.2.129", "192.0.2.130/25", 25},
		{"192.0.3.254/23", "192.0.3.254", "192.0.2.2/23", 23},
		{"192.0.2.5/30", "192.0.2.5", "192.0.2.6/30", 30},
		{"192.0.2.6/30", "192.0.2.6", "", 30},
		{"192.0.2.4/31", "192.0.2.4", "", 31},
		{"192.0.2.4/32", "192.0.2.4", "", 32},
	} {
		t.Run(tc.cidr, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			f.networks = fmt.Sprintf(`[{"iface":"vmbr0","type":"bridge","active":1,"cidr":%q}]`, tc.cidr)
			f.addresses = fmt.Sprintf(`[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":%q,"prefixlen":%d}]}]`, tc.local, tc.bits)
			f.routes = `[]`
			got, err := f.suggest(t, "")
			if got.IP != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("suggestion = %+v, %v", got, err)
			}
			if tc.want == "" && len(f.probes) != 0 {
				t.Fatalf("probed host/broadcast on exhausted network: %v", f.probes)
			}
		})
	}
}

func TestManagementSuggestionBusyAndFilteredHosts(t *testing.T) {
	f := newManagementSuggestionFixture()
	f.ping = func(_ context.Context, ip string) error {
		if ip == "192.0.2.2" {
			return nil
		}
		return managementProbeExit(1)
	}
	f.neighbors = []string{
		`[]`,
		`[{"dst":"192.0.2.3","lladdr":"00:11:22:33:44:55","state":["REACHABLE"]}]`,
		`[{"dst":"192.0.2.4","state":["FAILED"]},{"dst":"192.0.2.4","state":["INCOMPLETE"]}]`,
	}
	got, err := f.suggest(t, "")
	if err != nil || got.IP != "192.0.2.4/24" || !reflect.DeepEqual(f.probes, []string{"vmbr0 192.0.2.2", "vmbr0 192.0.2.3", "vmbr0 192.0.2.4"}) {
		t.Fatalf("suggestion = %+v, %v; probes %v", got, err, f.probes)
	}
}

func TestManagementSuggestionProbeFailures(t *testing.T) {
	for _, probeErr := range []error{managementProbeExit(2), errors.New("ping executable missing"), context.DeadlineExceeded} {
		t.Run(probeErr.Error(), func(t *testing.T) {
			f := newManagementSuggestionFixture()
			f.ping = func(context.Context, string) error { return probeErr }
			got, err := f.suggest(t, "")
			if err == nil || got.IP != "" || len(f.probes) != 1 {
				t.Fatalf("probe failure treated as silence: %+v, %v", got, err)
			}
		})
	}
}

func TestManagementSuggestionRejectsUntrustworthySubnets(t *testing.T) {
	for _, tc := range []struct {
		name, networks, addresses string
	}{
		{"missing", `[]`, ""},
		{"inactive", `[{"iface":"vmbr0","type":"bridge","active":0,"cidr":"192.0.2.254/24"}]`, ""},
		{"prefix missing", `[{"iface":"vmbr0","type":"bridge","active":1}]`, ""},
		{"pending only", `[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"198.51.100.254/24"}]`, ""},
		{"ambiguous interface", `[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.254/24"},{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.253/24"}]`, ""},
		{"ambiguous subnet", "", `[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":"192.0.2.254","prefixlen":24},{"local":"198.51.100.1","prefixlen":24}]}]`},
		{"down", "", `[{"ifname":"vmbr0","flags":[],"addr_info":[{"local":"192.0.2.254","prefixlen":24}]}]`},
		{"tentative", "", `[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":"192.0.2.254","prefixlen":24,"tentative":true}]}]`},
		{"no live address", "", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			if tc.networks != "" {
				f.networks = tc.networks
			}
			if tc.addresses != "" {
				f.addresses = tc.addresses
			}
			got, err := f.suggest(t, "")
			if err == nil || got.IP != "" || len(f.probes) != 0 {
				t.Fatalf("untrustworthy subnet accepted: %+v, %v; probes %v", got, err, f.probes)
			}
		})
	}
}

func TestManagementSuggestionVLANIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, links string
		want        string
	}{
		{"exact topology", `[{"ifname":"vmbr0","ifindex":4},{"ifname":"host100","ifindex":8,"link_index":4,"linkinfo":{"info_kind":"vlan","info_data":{"id":100}}}]`, "198.51.100.2/24"},
		{"wrong parent", `[{"ifname":"vmbr0","ifindex":4},{"ifname":"host100","link_index":5,"linkinfo":{"info_kind":"vlan","info_data":{"id":100}}}]`, ""},
		{"wrong tag", `[{"ifname":"vmbr0","ifindex":4},{"ifname":"host100","link_index":4,"linkinfo":{"info_kind":"vlan","info_data":{"id":101}}}]`, ""},
		{"missing topology", `[]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			f.networks = `[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.254/24"},{"iface":"host100","type":"vlan","active":1,"cidr":"198.51.100.254/24"}]`
			f.addresses = `[{"ifname":"vmbr0","flags":["UP"],"addr_info":[{"local":"192.0.2.254","prefixlen":24}]},{"ifname":"host100","flags":["UP"],"addr_info":[{"local":"198.51.100.254","prefixlen":24}]}]`
			f.links = tc.links
			got, err := f.suggest(t, "100")
			if got.IP != tc.want || (err != nil) != (tc.want == "") || got.Gateway != "" {
				t.Fatalf("VLAN suggestion = %+v, %v", got, err)
			}
			if tc.want != "" && !reflect.DeepEqual(f.probes, []string{"host100 198.51.100.2"}) {
				t.Fatalf("probe escaped VLAN: %v", f.probes)
			}
		})
	}
}

func TestManagementSuggestionGatewaySelection(t *testing.T) {
	for _, tc := range []struct {
		name, api, routes, want string
		fail                    bool
	}{
		{"API gateway", "192.0.2.1", `[]`, "192.0.2.1", false},
		{"live route", "", `[{"dst":"default","dev":"vmbr0","gateway":"192.0.2.10"}]`, "192.0.2.10", false},
		{"no invented gateway", "", `[]`, "", false},
		{"other device", "", `[{"dst":"default","dev":"vmbr1","gateway":"192.0.2.1"}]`, "", false},
		{"outside subnet", "198.51.100.1", `[]`, "", true},
		{"conflicting route", "192.0.2.1", `[{"dst":"default","dev":"vmbr0","gateway":"192.0.2.10"}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			f.networks = fmt.Sprintf(`[{"iface":"vmbr0","type":"bridge","active":1,"cidr":"192.0.2.254/24","gateway":%q}]`, tc.api)
			f.routes = tc.routes
			got, err := f.suggest(t, "")
			if (err != nil) != tc.fail || got.Gateway != tc.want {
				t.Fatalf("gateway selection = %+v, %v", got, err)
			}
		})
	}
}

func TestManagementSuggestionSearchBound(t *testing.T) {
	f := newManagementSuggestionFixture()
	f.ping = func(context.Context, string) error { return nil }
	got, err := f.suggest(t, "")
	if err == nil || got.IP != "" || len(f.probes) != 32 || f.probes[31] != "vmbr0 192.0.2.33" {
		t.Fatalf("unbounded search: %+v, %v; probes %v", got, err, f.probes)
	}
}

func TestManagementSuggestionCancellation(t *testing.T) {
	for _, when := range []string{"before discovery", "during discovery", "during probe", "deadline"} {
		t.Run(when, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := context.Canceled
			commands := f.commands(t)
			switch when {
			case "before discovery":
				cancel()
				commands.pve = func(context.Context, string, any, ...string) error {
					t.Fatal("discovery called after cancellation")
					return nil
				}
			case "during discovery":
				commands.pve = func(ctx context.Context, _ string, _ any, _ ...string) error {
					cancel()
					return ctx.Err()
				}
			case "during probe":
				f.ping = func(ctx context.Context, _ string) error {
					cancel()
					return ctx.Err()
				}
			case "deadline":
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				want = context.DeadlineExceeded
			}
			got, err := suggestManagementAddressWithCommands(ctx, &Inventory{Node: "pve"}, "vmbr0", "", "", commands)
			if !errors.Is(err, want) || got.IP != "" {
				t.Fatalf("cancellation ignored: %+v, %v", got, err)
			}
		})
	}
}

func TestManagementNetworkPrefixNetmask(t *testing.T) {
	for _, mask := range []string{"24", "255.255.255.0"} {
		prefix, err := managementNetworkPrefix(managementNetwork{Address: "192.0.2.254", Netmask: mask})
		if err != nil || prefix.String() != "192.0.2.254/24" {
			t.Fatalf("netmask %s: %v, %v", mask, prefix, err)
		}
	}
	for _, network := range []managementNetwork{
		{Address: "192.0.2.254", Netmask: "255.0.255.0"},
		{Address: "192.0.2.254", Netmask: "33"},
		{CIDR: "2001:db8::1/64"},
		{CIDR: "127.0.0.1/8"},
	} {
		if _, err := managementNetworkPrefix(network); err == nil {
			t.Fatalf("invalid prefix accepted: %+v", network)
		}
	}
}

func TestManagementSuggestionOVSTopology(t *testing.T) {
	for _, tc := range []struct {
		name, bridge, tag string
		commandErr        error
		want              string
	}{
		{"matching live topology", "vmbr0\n", "100\n", nil, "198.51.100.2/24"},
		{"pending bridge", "vmbr1\n", "100\n", nil, ""},
		{"pending tag", "vmbr0\n", "101\n", nil, ""},
		{"unavailable topology", "", "", errors.New("OVS unavailable"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newManagementSuggestionFixture()
			f.networks = `[{"iface":"internal100","type":"OVSIntPort","active":1,"cidr":"198.51.100.254/24","ovs_bridge":"vmbr0","ovs_tag":100}]`
			f.addresses = `[{"ifname":"internal100","flags":["UP"],"addr_info":[{"local":"198.51.100.254","prefixlen":24}]}]`
			commands := f.commands(t)
			output := commands.output
			commands.output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
				if name != "ovs-vsctl" {
					return output(ctx, name, args...)
				}
				if tc.commandErr != nil {
					return nil, tc.commandErr
				}
				switch strings.Join(args, " ") {
				case "--timeout=2 iface-to-br internal100":
					return []byte(tc.bridge), nil
				case "--timeout=2 get port internal100 tag":
					return []byte(tc.tag), nil
				default:
					t.Fatalf("unexpected OVS command: %v", args)
					return nil, nil
				}
			}
			got, err := suggestManagementAddressWithCommands(context.Background(), &Inventory{Node: "pve"}, "vmbr0", "100", "", commands)
			if got.IP != tc.want || (err != nil) != (tc.want == "") {
				t.Fatalf("OVS suggestion = %+v, %v", got, err)
			}
			if tc.want != "" && !reflect.DeepEqual(f.probes, []string{"internal100 198.51.100.2"}) {
				t.Fatalf("OVS probe escaped VLAN: %v", f.probes)
			}
		})
	}
}

func TestManagementSuggestionDiscoveryErrors(t *testing.T) {
	for _, command := range []string{"-j -4 addr show", "-j -4 route show", "-j -4 neigh show dev vmbr0"} {
		for _, malformed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/malformed=%v", command, malformed), func(t *testing.T) {
				f := newManagementSuggestionFixture()
				commands := f.commands(t)
				output := commands.output
				commands.output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
					if name == "ip" && strings.Join(args, " ") == command {
						if malformed {
							return []byte("{"), nil
						}
						return nil, errors.New("discovery failed")
					}
					return output(ctx, name, args...)
				}
				got, err := suggestManagementAddressWithCommands(context.Background(), &Inventory{Node: "pve"}, "vmbr0", "", "", commands)
				if err == nil || got.IP != "" {
					t.Fatalf("discovery failure became a suggestion: %+v, %v", got, err)
				}
			})
		}
	}
}

func TestManagementSuggestionNeighborReadAfterProbeFails(t *testing.T) {
	f := newManagementSuggestionFixture()
	commands := f.commands(t)
	output := commands.output
	neighborReads := 0
	commands.output = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "ip" && strings.Join(args, " ") == "-j -4 neigh show dev vmbr0" {
			neighborReads++
			if neighborReads == 2 {
				return nil, errors.New("neighbor inspection failed")
			}
		}
		return output(ctx, name, args...)
	}
	got, err := suggestManagementAddressWithCommands(context.Background(), &Inventory{Node: "pve"}, "vmbr0", "", "", commands)
	if err == nil || got.IP != "" || len(f.probes) != 1 || neighborReads != 2 {
		t.Fatalf("post-probe neighbor failure ignored: %+v, %v", got, err)
	}
}

func TestManagementSuggestionSkipsNondefaultRouteGateway(t *testing.T) {
	f := newManagementSuggestionFixture()
	f.routes = `[{"dst":"198.51.100.0/24","dev":"vmbr0","gateway":"192.0.2.2"}]`
	got, err := f.suggest(t, "")
	if err != nil || got.IP != "192.0.2.3/24" || got.Gateway != "192.0.2.1" {
		t.Fatalf("route gateway reused or selected as default: %+v, %v", got, err)
	}
}
