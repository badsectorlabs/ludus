package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"ludus-server/localgen"
	"ludusapi"
	"ludusapi/pveclient"
)

type mockPVE struct {
	calls   []string
	nodes   int
	version pveclient.Version
}

func (m *mockPVE) Version(context.Context) (pveclient.Version, error) {
	m.calls = append(m.calls, "Version")
	return m.version, nil
}
func (m *mockPVE) ClusterNodeCount(context.Context) (int, error) {
	m.calls = append(m.calls, "ClusterNodeCount")
	return m.nodes, nil
}
func (m *mockPVE) ClusterNodeIPs(context.Context) ([]string, error) {
	return []string{"10.0.0.1"}, nil
}
func (m *mockPVE) VerifyTokenOnAll(context.Context) error {
	m.calls = append(m.calls, "VerifyTokenOnAll")
	return nil
}
func (m *mockPVE) EnsureRole(_ context.Context, n string, _ []string) error {
	m.calls = append(m.calls, "EnsureRole:"+n)
	return nil
}
func (m *mockPVE) EnsureGroup(_ context.Context, n string) error {
	m.calls = append(m.calls, "EnsureGroup:"+n)
	return nil
}
func (m *mockPVE) EnsurePool(_ context.Context, n string) error {
	m.calls = append(m.calls, "EnsurePool:"+n)
	return nil
}
func (m *mockPVE) EnsureACL(_ context.Context, p, r string, _, _ []string) error {
	m.calls = append(m.calls, "EnsureACL:"+p+":"+r)
	return nil
}
func (m *mockPVE) EnsureSDNZone(_ context.Context, n, k string, _ []string) error {
	m.calls = append(m.calls, "EnsureSDNZone:"+n+":"+k)
	return nil
}
func (m *mockPVE) EnsureVNet(_ context.Context, _, n string, tag int, vlanaware bool) error {
	m.calls = append(m.calls, fmt.Sprintf("EnsureVNet:%s:%d:%t", n, tag, vlanaware))
	return nil
}
func (m *mockPVE) EnsureSubnet(_ context.Context, v, c, _ string, _ bool) error {
	m.calls = append(m.calls, "EnsureSubnet:"+v+":"+c)
	return nil
}
func (m *mockPVE) ApplySDN(context.Context) error {
	m.calls = append(m.calls, "ApplySDN")
	return nil
}

func TestBootstrap_EnsureSequence_SingleNode(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{
		ProxmoxEndpoints:      []string{"https://10.0.0.1:8006"},
		ProxmoxNode:           "pve",
		ProxmoxVMStoragePool:  "vmstore",
		ProxmoxISOStoragePool: "isostore",
		SDNZone:               "ludus",
		LudusNATInterface:     "ludusnat",
	}
	dir := t.TempDir()
	if err := bootstrapProxmoxObjects(t.Context(), m, cfg, dir); err != nil {
		t.Fatal(err)
	}
	wantContains := []string{
		"Version", "VerifyTokenOnAll", "ClusterNodeCount",
		"EnsureRole:LudusPacker", "EnsureRole:LudusUser", "EnsureRole:LudusAdmin",
		"EnsureGroup:ludus_users", "EnsureGroup:ludus_admins",
		"EnsurePool:SHARED", "EnsurePool:ADMIN",
		"EnsureSDNZone:ludus:simple",
		"EnsureVNet:ludusnat:0:false",
		"EnsureSubnet:ludusnat:192.0.2.0/24",
		"ApplySDN",
		"EnsureACL:/nodes:LudusPacker",
		"EnsureACL:/vms:LudusPacker",
		"EnsureACL:/storage/vmstore:LudusPacker",
		"EnsureACL:/storage/isostore:LudusPacker",
	}
	got := map[string]bool{}
	for _, c := range m.calls {
		got[c] = true
	}
	for _, w := range wantContains {
		if !got[w] {
			t.Fatalf("missing call %q; got %v", w, m.calls)
		}
	}
}

func TestBootstrap_StorageACLsAreDeduplicated(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{
		ProxmoxVMStoragePool:  "nfs",
		ProxmoxISOStoragePool: "nfs",
		SDNZone:               "ludus",
		LudusNATInterface:     "ludusnat",
	}
	if err := bootstrapProxmoxObjects(t.Context(), m, cfg, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	got := 0
	for _, c := range m.calls {
		if c == "EnsureACL:/storage/nfs:LudusPacker" {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("expected one storage ACL for shared VM/ISO storage, got %d calls: %v", got, m.calls)
	}
}

func TestBootstrap_PrivilegesAreAcceptedOnPVENine(t *testing.T) {
	for role, privs := range map[string][]string{
		"LudusPacker": privsPacker,
		"LudusUser":   privsUser,
		"LudusAdmin":  privsAdmin,
	} {
		for _, priv := range privs {
			if priv == "VM.Monitor" {
				t.Fatalf("%s includes removed PVE 9 privilege %q", role, priv)
			}
		}
	}
}

func TestBootstrap_VXLANOnCluster(t *testing.T) {
	m := &mockPVE{nodes: 3, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{ProxmoxEndpoints: []string{"https://10.0.0.1:8006"}, SDNZone: "ludus", LudusNATInterface: "ludusnat"}
	_ = bootstrapProxmoxObjects(t.Context(), m, cfg, t.TempDir())
	found := false
	natTagged := false
	for _, c := range m.calls {
		if c == "EnsureSDNZone:ludus:vxlan" {
			found = true
		}
		if c == "EnsureVNet:ludusnat:100000:false" {
			natTagged = true
		}
	}
	if !found {
		t.Fatalf("expected vxlan zone on 3-node cluster: %v", m.calls)
	}
	if !natTagged {
		t.Fatalf("expected tagged NAT VNet on vxlan cluster: %v", m.calls)
	}
}

func TestBootstrap_RejectsOldPVE(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "7.4.1"}}
	err := bootstrapProxmoxObjects(t.Context(), m, ludusapi.Configuration{SDNZone: "ludus"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for PVE < 8.0")
	}
}

func TestBootstrapDNSUsesConfiguredLXCServer(t *testing.T) {
	resolvConf := t.TempDir() + "/resolv.conf"
	if err := os.WriteFile(resolvConf, []byte("nameserver 1.1.1.1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	upstreams, err := dnsUpstreams("10.20.30.40", resolvConf)
	if err != nil {
		t.Fatal(err)
	}
	got := localgen.RenderDnsmasq(localgen.DnsmasqConfig{
		BindIP:    "192.0.2.253",
		Gateway:   "192.0.2.253",
		PoolLow:   "192.0.2.50",
		PoolHigh:  "192.0.2.100",
		IfName:    "eth1",
		Upstreams: upstreams,
	})
	for _, want := range []string{"no-resolv", "server=10.20.30.40"} {
		if !strings.Contains(got, want) {
			t.Fatalf("LXC resolver configuration is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "server=1.1.1.1") || strings.Contains(got, "server=8.8.8.8") {
		t.Fatalf("resolv.conf or public defaults overrode LXC_DNS_SERVER:\n%s", got)
	}
}

func TestDNSUpstreamsFallsBackToResolvConf(t *testing.T) {
	resolvConf := t.TempDir() + "/resolv.conf"
	if err := os.WriteFile(resolvConf, []byte(`# Managed by Proxmox
nameserver 10.20.30.40
nameserver 2001:0db8::53
nameserver 10.20.30.40
`), 0644); err != nil {
		t.Fatal(err)
	}
	upstreams, err := dnsUpstreams("", resolvConf)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(upstreams, ",")
	if got != "10.20.30.40,2001:db8::53" {
		t.Fatalf("unexpected resolv.conf fallback: %s", got)
	}
}

func TestReadDNSUpstreamsRequiresNameserver(t *testing.T) {
	resolvConf := t.TempDir() + "/resolv.conf"
	if err := os.WriteFile(resolvConf, []byte("search ludus.internal\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readDNSUpstreams(resolvConf); err == nil {
		t.Fatal("expected an error when resolv.conf has no nameserver")
	}
}
