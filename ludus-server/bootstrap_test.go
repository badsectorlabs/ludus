package main

import (
	"context"
	"testing"

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
func (m *mockPVE) EnsureVNet(_ context.Context, _, n string, _ int, _ bool) error {
	m.calls = append(m.calls, "EnsureVNet:"+n)
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
		ProxmoxEndpoints:  []string{"https://10.0.0.1:8006"},
		ProxmoxNode:       "pve",
		SDNZone:           "ludus",
		LudusNATInterface: "ludusnat",
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
		"EnsureVNet:ludusnat",
		"EnsureSubnet:ludusnat:192.0.2.0/24",
		"ApplySDN",
		"EnsureACL:/nodes:LudusPacker",
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

func TestBootstrap_VXLANOnCluster(t *testing.T) {
	m := &mockPVE{nodes: 3, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{ProxmoxEndpoints: []string{"https://10.0.0.1:8006"}, SDNZone: "ludus", LudusNATInterface: "ludusnat"}
	_ = bootstrapProxmoxObjects(t.Context(), m, cfg, t.TempDir())
	found := false
	for _, c := range m.calls {
		if c == "EnsureSDNZone:ludus:vxlan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected vxlan zone on 3-node cluster: %v", m.calls)
	}
}

func TestBootstrap_RejectsOldPVE(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "7.4.1"}}
	err := bootstrapProxmoxObjects(t.Context(), m, ludusapi.Configuration{SDNZone: "ludus"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for PVE < 8.0")
	}
}
