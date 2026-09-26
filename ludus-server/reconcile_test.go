package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"ludusapi"
)

type recMock struct {
	mockPVE
	zoneType string
	vnets    []recVNetCall
}

type recVNetCall struct {
	name      string
	tag       int
	vlanaware bool
}

func (m *recMock) SDNZoneType(_ context.Context, _ string) (string, error) {
	if m.zoneType == "" {
		return ludusapi.SDNZoneTypeSimple, nil
	}
	return m.zoneType, nil
}

func (m *recMock) EnsureVNet(_ context.Context, _, n string, tag int, vlanaware bool) error {
	m.vnets = append(m.vnets, recVNetCall{name: n, tag: tag, vlanaware: vlanaware})
	return nil
}
func (m *recMock) UserExists(_ context.Context, _ string) (bool, error) { return false, nil }

func TestReconcile_CreatesVNetsAndRoutes(t *testing.T) {
	m := &recMock{}
	m.nodes = 1
	m.version.Version = "8.2.4"
	dir := t.TempDir()
	routesPath := dir + "/ludus-routes"
	cfg := ludusapi.Configuration{SDNZone: "ludus", VXLANTagBase: 0}

	ranges := []importedRange{{Number: 2}, {Number: 7}}
	users := []importedUser{{ProxmoxUsername: "alice@pam"}}

	warns, err := reconcileImportedState(context.Background(), m, cfg, ranges, users, routesPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.vnets) != 2 || m.vnets[0].name != "r2" || m.vnets[1].name != "r7" {
		t.Fatalf("vnets: %v", m.vnets)
	}
	for _, vnet := range m.vnets {
		if vnet.tag != 0 || !vnet.vlanaware {
			t.Fatalf("simple zone must not tag but must make range VNets VLAN-aware: %+v", vnet)
		}
	}
	routes, _ := os.ReadFile(routesPath)
	if !strings.Contains(string(routes), "10.2.0.0/16 via 192.0.2.102") {
		t.Fatalf("routes:\n%s", routes)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "alice@pam") {
		t.Fatalf("expected missing-user warning: %v", warns)
	}
}

func TestReconcile_TagsRangeVNetsForVXLAN(t *testing.T) {
	m := &recMock{zoneType: ludusapi.SDNZoneTypeVXLAN}
	m.nodes = 3
	m.version.Version = "8.2.4"
	cfg := ludusapi.Configuration{SDNZone: "ludus", VXLANTagBase: 4000}

	_, err := reconcileImportedState(context.Background(), m, cfg, []importedRange{{Number: 7}}, nil, t.TempDir()+"/ludus-routes")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.vnets) != 1 || m.vnets[0].tag != 4007 || !m.vnets[0].vlanaware {
		t.Fatalf("vxlan range vnet options: %+v", m.vnets)
	}
}
