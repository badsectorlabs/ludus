package main

import (
	"context"
	"net/http"
	"os"
	"sort"
	"testing"

	"github.com/h2non/gock"
	"github.com/luthermonson/go-proxmox"
)

// All mocks live under this fake host so gock can intercept by URL.
const mockBase = "http://pve.test"

func newMockClient() *proxmox.Client {
	// Force the proxmox client onto a real net/http client so gock can hook the
	// transport. Token auth keeps us out of the /access/ticket login path.
	c := &http.Client{Transport: http.DefaultTransport}
	gock.InterceptClient(c)
	return proxmox.NewClient(
		mockBase,
		proxmox.WithHTTPClient(c),
		proxmox.WithAPIToken("user@pam!test", "secret"),
	)
}

// resetEnv clears the LUDUS_* env vars so a test that doesn't care about them
// doesn't inherit values from the developer's shell.
func resetLudusEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"LUDUS_RANGE_ID",
		"LUDUS_RANGE_NUMBER",
		"LUDUS_RANGE_CONFIG",
		"LUDUS_USER_IS_ADMIN",
		"LUDUS_RETURN_ALL_RANGES",
	} {
		t.Setenv(k, "")
	}
}

// stockMocks registers a small but realistic Proxmox mock set:
//   - one pool "TEST7" containing one running qemu VM (vmid 100, "TEST7-web")
//     and one running lxc (vmid 200, "TEST7-ct")
//   - one template qemu VM (vmid 999, "ubuntu-tmpl") that lives outside the pool
//   - the qemu VM has guest agent running and reports linux + an IP
//   - the lxc has its IP encoded in net0
func stockMocks() {
	// /pools list
	gock.New(mockBase).
		Get("^/pools$").
		Reply(200).
		JSON(`{"data":[{"poolid":"TEST7","comment":""}]}`)

	// /pools/?poolid=TEST7 (new style, returns array)
	gock.New(mockBase).
		Get("^/pools/$").
		MatchParam("poolid", "TEST7").
		Reply(200).
		JSON(`{"data":[{
			"poolid":"TEST7",
			"members":[
				{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","template":0},
				{"type":"lxc","vmid":200,"name":"TEST7-ct","node":"node1","template":0},
				{"type":"storage","id":"storage/node1/local"}
			]}]}`)

	// /cluster/resources — the qemu, lxc, and an out-of-pool template
	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","status":"running","template":0,"maxcpu":2,"mem":1073741824},
			{"type":"lxc","vmid":200,"name":"TEST7-ct","node":"node1","status":"running","template":0,"maxcpu":1,"mem":536870912},
			{"type":"qemu","vmid":999,"name":"ubuntu-tmpl","node":"node1","status":"stopped","template":1,"maxcpu":1,"mem":0}
		]}`)

	// VM 100 config — has a description with embedded JSON groups list
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/config$").
		Reply(200).
		JSON(`{"data":{"description":"{\"groups\":[\"webservers\"],\"role\":\"frontend\"}","name":"TEST7-web"}}`)

	// LXC 200 config — net0 carries the IP
	gock.New(mockBase).
		Get("^/nodes/node1/lxc/200/config$").
		Reply(200).
		JSON(`{"data":{"net0":"name=eth0,bridge=vmbr0,ip=10.7.10.42/24,gw=10.7.10.1"}}`)

	// Template config — we still ask for it (the current code does); empty desc.
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/999/config$").
		Reply(200).
		JSON(`{"data":{}}`)

	// QEMU agent: network interfaces (lo is filtered by the lib but we include it for realism)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/agent/network-get-interfaces$").
		Reply(200).
		JSON(`{"data":{"result":[
			{"name":"lo","ip-addresses":[{"ip-address-type":"ipv4","ip-address":"127.0.0.1"}]},
			{"name":"eth0","ip-addresses":[
				{"ip-address-type":"ipv4","ip-address":"10.7.10.5"},
				{"ip-address-type":"ipv4","ip-address":"172.20.0.1"}
			]}
		]}}`)

	// QEMU agent: os info
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/agent/get-osinfo$").
		Reply(200).
		JSON(`{"data":{"result":{
			"id":"ubuntu","name":"Ubuntu","machine":"x86_64",
			"kernel-release":"6.5.0","version-id":"22.04"
		}}}`)
}

func TestMainList_BuildsInventory(t *testing.T) {
	resetLudusEnv(t)
	defer gock.Off()
	stockMocks()

	client := newMockClient()
	got := mainList(context.Background(), client)

	// "all" should hold both running, non-template hosts. Template "ubuntu-tmpl"
	// must not appear in any group, but per current behavior it IS in hostvars.
	all, ok := got["all"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected 'all' group, got %#v", got["all"])
	}
	hosts, _ := all["hosts"].([]string)
	sort.Strings(hosts)
	wantHosts := []string{"TEST7-ct", "TEST7-web"}
	if !equalStrings(hosts, wantHosts) {
		t.Errorf("all.hosts = %v, want %v", hosts, wantHosts)
	}

	// _meta.hostvars is keyed by VM name
	meta, ok := got["_meta"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected _meta block")
	}
	hvars, ok := meta["hostvars"].(map[string]map[string]interface{})
	if !ok {
		t.Fatalf("hostvars wrong type %T", meta["hostvars"])
	}

	// Web VM: agent OS detected, IP picked from non-172 candidate
	web := hvars["TEST7-web"]
	if web == nil {
		t.Fatal("TEST7-web missing from hostvars")
	}
	if got := web["ansible_host"]; got != "10.7.10.5" {
		t.Errorf("web ansible_host = %v, want 10.7.10.5", got)
	}
	if got := web["proxmox_os_id"]; got != "ubuntu" {
		t.Errorf("web os_id = %v, want ubuntu", got)
	}
	if got := web["role"]; got != "frontend" {
		t.Errorf("metadata role = %v, want frontend", got)
	}

	// LXC: ansible_host pulled from net0 line
	ct := hvars["TEST7-ct"]
	if ct == nil {
		t.Fatal("TEST7-ct missing from hostvars")
	}
	if got := ct["ansible_host"]; got != "10.7.10.42" {
		t.Errorf("ct ansible_host = %v, want 10.7.10.42", got)
	}

	// Pool group should appear and contain only non-template members
	pool, ok := got["TEST7"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected pool group TEST7")
	}
	poolHosts, _ := pool["hosts"].([]string)
	sort.Strings(poolHosts)
	if !equalStrings(poolHosts, wantHosts) {
		t.Errorf("TEST7.hosts = %v, want %v", poolHosts, wantHosts)
	}

	// Description-derived group exists and contains the web VM
	if grp, ok := got["webservers"].(map[string]interface{}); ok {
		if hh, _ := grp["hosts"].([]string); !contains(hh, "TEST7-web") {
			t.Errorf("webservers group missing TEST7-web: %v", hh)
		}
	} else {
		t.Errorf("expected 'webservers' group from description metadata")
	}

	// OS-derived group from agent osinfo
	if grp, ok := got["ubuntu"].(map[string]interface{}); ok {
		if hh, _ := grp["hosts"].([]string); !contains(hh, "TEST7-web") {
			t.Errorf("ubuntu group missing TEST7-web: %v", hh)
		}
	} else {
		t.Errorf("expected 'ubuntu' OS group")
	}

	// 'running' group includes both
	if grp, ok := got["running"].(map[string]interface{}); ok {
		hh, _ := grp["hosts"].([]string)
		sort.Strings(hh)
		if !equalStrings(hh, wantHosts) {
			t.Errorf("running group = %v, want %v", hh, wantHosts)
		}
	} else {
		t.Errorf("expected 'running' group")
	}

	// Template should NOT appear in any group's host list
	for name, val := range got {
		if name == "_meta" {
			continue
		}
		grp, ok := val.(map[string]interface{})
		if !ok {
			continue
		}
		hh, _ := grp["hosts"].([]string)
		if contains(hh, "ubuntu-tmpl") {
			t.Errorf("template ubuntu-tmpl leaked into group %q", name)
		}
	}
}

func TestMainList_RangeFilter_OnlyMatchingPool(t *testing.T) {
	resetLudusEnv(t)
	t.Setenv("LUDUS_RANGE_ID", "TEST7")
	defer gock.Off()

	gock.New(mockBase).
		Get("^/pools/$").
		MatchParam("poolid", "TEST7").
		Reply(200).
		JSON(`{"data":[{"poolid":"TEST7","members":[
			{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","template":0}
		]}]}`)

	// Cluster reports both VMs; mainList must not even fetch config for the OTHER one.
	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","status":"stopped","template":0},
			{"type":"qemu","vmid":500,"name":"OTHER-leaky","node":"node1","status":"stopped","template":0}
		]}`)

	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/config$").
		Reply(200).
		JSON(`{"data":{}}`)

	client := newMockClient()
	got := mainList(context.Background(), client)

	all, _ := got["all"].(map[string]interface{})
	hosts, _ := all["hosts"].([]string)
	if !equalStrings(hosts, []string{"TEST7-web"}) {
		t.Errorf("range filter leaked: all.hosts = %v", hosts)
	}

	meta, _ := got["_meta"].(map[string]interface{})
	hvars, _ := meta["hostvars"].(map[string]map[string]interface{})
	if _, present := hvars["OTHER-leaky"]; present {
		t.Errorf("hostvars leaked OTHER-leaky: %v", hvars)
	}

	// The TEST7 group must always be emitted (even if it would otherwise be empty)
	if _, ok := got["TEST7"]; !ok {
		t.Errorf("rangeID group TEST7 should always exist in output")
	}
	if _, ok := got["OTHER"]; ok {
		t.Errorf("out-of-range pool group OTHER should not be emitted: %#v", got["OTHER"])
	}
}

func TestMainList_AgentDownFallsBackToConfigOS(t *testing.T) {
	resetLudusEnv(t)
	t.Setenv("LUDUS_RANGE_ID", "TEST7")
	t.Setenv("LUDUS_RANGE_NUMBER", "7")

	// Range config uses the documented Windows mapping form and force_ip.
	dir := t.TempDir()
	ludusYAML := []byte(`ludus:
  - vm_name: "{{ range_id }}-dc"
    vlan: 10
    ip_last_octet: 9
    windows:
      sysprep: false
    force_ip: true
`)
	cfgPath := dir + "/range.yml"
	if err := os.WriteFile(cfgPath, ludusYAML, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUDUS_RANGE_CONFIG", cfgPath)

	defer gock.Off()

	gock.New(mockBase).
		Get("^/pools/$").
		MatchParam("poolid", "TEST7").
		Reply(200).
		JSON(`{"data":[{"poolid":"TEST7","members":[
			{"type":"qemu","vmid":100,"name":"TEST7-dc","node":"node1","template":0}
		]}]}`)
	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":100,"name":"TEST7-dc","node":"node1","status":"running","template":0}
		]}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/config$").
		Reply(200).
		JSON(`{"data":{}}`)

	// Agent is "down" — return 500 twice (the retry will hit it once more).
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/100/agent/network-get-interfaces$").
		Times(2).
		Reply(500).
		JSON(`{"errors":"agent not running"}`)

	client := newMockClient()
	got := mainList(context.Background(), client)

	meta, _ := got["_meta"].(map[string]interface{})
	hvars, _ := meta["hostvars"].(map[string]map[string]interface{})
	dc := hvars["TEST7-dc"]
	if dc == nil {
		t.Fatal("expected TEST7-dc in hostvars")
	}
	if dc["proxmox_os_id"] != "windows" {
		t.Errorf("agent-down: expected os_id from config (windows), got %v", dc["proxmox_os_id"])
	}
	// forceIP should yield the configured 10.7.10.9
	if dc["ansible_host"] != "10.7.10.9" {
		t.Errorf("agent-down: expected forceIP ansible_host 10.7.10.9, got %v", dc["ansible_host"])
	}
}

func TestMainList_MswindowsRenamedToWindows(t *testing.T) {
	resetLudusEnv(t)
	defer gock.Off()

	gock.New(mockBase).Get("^/pools$").Reply(200).JSON(`{"data":[]}`)
	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":300,"name":"win-host","node":"node1","status":"running","template":0}
		]}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/300/config$").
		Reply(200).
		JSON(`{"data":{}}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/300/agent/network-get-interfaces$").
		Reply(200).
		JSON(`{"data":{"result":[]}}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/300/agent/get-osinfo$").
		Reply(200).
		JSON(`{"data":{"result":{"id":"mswindows","name":"Windows Server 2022"}}}`)

	client := newMockClient()
	got := mainList(context.Background(), client)
	meta, _ := got["_meta"].(map[string]interface{})
	hvars, _ := meta["hostvars"].(map[string]map[string]interface{})
	if got := hvars["win-host"]["proxmox_os_id"]; got != "windows" {
		t.Errorf("mswindows should be normalized to windows, got %v", got)
	}
}

func TestMainList_MacOSNameFallback(t *testing.T) {
	// This is the bug we fixed: when agent reports osinfo with an empty/absent id,
	// a VM whose name contains "macos" should be tagged as macos.
	resetLudusEnv(t)
	defer gock.Off()

	gock.New(mockBase).Get("^/pools$").Reply(200).JSON(`{"data":[]}`)
	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":400,"name":"my-macos-runner","node":"node1","status":"running","template":0}
		]}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/400/config$").
		Reply(200).
		JSON(`{"data":{}}`)
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/400/agent/network-get-interfaces$").
		Reply(200).
		JSON(`{"data":{"result":[]}}`)
	// No osinfo "result" key -> AgentOsInfo returns nil
	gock.New(mockBase).
		Get("^/nodes/node1/qemu/400/agent/get-osinfo$").
		Reply(200).
		JSON(`{"data":{}}`)

	client := newMockClient()
	got := mainList(context.Background(), client)
	meta, _ := got["_meta"].(map[string]interface{})
	hvars, _ := meta["hostvars"].(map[string]map[string]interface{})
	if got := hvars["my-macos-runner"]["proxmox_os_id"]; got != "macos" {
		t.Errorf("macos name fallback: expected proxmox_os_id=macos, got %v", got)
	}
}

func TestMainHost_ReturnsSingleHostvars(t *testing.T) {
	resetLudusEnv(t)
	defer gock.Off()
	stockMocks()

	client := newMockClient()
	hv := mainHost(context.Background(), client, "TEST7-web")
	if hv["ansible_host"] != "10.7.10.5" {
		t.Errorf("mainHost ansible_host = %v, want 10.7.10.5", hv["ansible_host"])
	}
	if hv["proxmox_os_id"] != "ubuntu" {
		t.Errorf("mainHost proxmox_os_id = %v, want ubuntu", hv["proxmox_os_id"])
	}
}

func TestMainHost_RangeFilterRejectsOutOfRangeHost(t *testing.T) {
	resetLudusEnv(t)
	t.Setenv("LUDUS_RANGE_ID", "TEST7")
	defer gock.Off()

	gock.New(mockBase).
		Get("^/pools/$").
		MatchParam("poolid", "TEST7").
		Reply(200).
		JSON(`{"data":[{"poolid":"TEST7","members":[
			{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","template":0}
		]}]}`)

	gock.New(mockBase).
		Get("^/cluster/resources$").
		Reply(200).
		JSON(`{"data":[
			{"type":"qemu","vmid":100,"name":"TEST7-web","node":"node1","status":"stopped","template":0},
			{"type":"qemu","vmid":500,"name":"OTHER-leaky","node":"node1","status":"stopped","template":0}
		]}`)

	client := newMockClient()
	hv := mainHost(context.Background(), client, "OTHER-leaky")
	if len(hv) != 0 {
		t.Fatalf("out-of-range --host returned hostvars: %#v", hv)
	}
}

func TestMainHost_UnknownHostReturnsEmpty(t *testing.T) {
	resetLudusEnv(t)
	defer gock.Off()
	stockMocks()

	client := newMockClient()
	hv := mainHost(context.Background(), client, "no-such-host")
	if len(hv) != 0 {
		t.Errorf("expected empty map for unknown host, got %v", hv)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
