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
	vnets []string
}

func (m *recMock) EnsureVNet(_ context.Context, _, n string, _ int, _ bool) error {
	m.vnets = append(m.vnets, n)
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
	if len(m.vnets) != 2 || m.vnets[0] != "r2" || m.vnets[1] != "r7" {
		t.Fatalf("vnets: %v", m.vnets)
	}
	routes, _ := os.ReadFile(routesPath)
	if !strings.Contains(string(routes), "10.2.0.0/16 via 192.0.2.102") {
		t.Fatalf("routes:\n%s", routes)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "alice@pam") {
		t.Fatalf("expected missing-user warning: %v", warns)
	}
}
