package pveclient

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHostManagedVNetsPreserveTopology(t *testing.T) {
	vnets := []map[string]any{
		{"vnet": "ludusnat", "zone": "custom", "tag": 16777215, "vlanaware": 1},
		{"vnet": "r9", "zone": "custom", "tag": 9009, "vlanaware": 1},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			vnets = append(vnets, map[string]any{"vnet": r.Form.Get("vnet"), "zone": r.Form.Get("zone"), "tag": r.Form.Get("tag"), "vlanaware": 1})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": vnets})
	})
	c := fakeAPI(t, mux)
	if err := c.RequireVNet(t.Context(), "custom", "ludusnat", false); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureVNetPreservingTag(t.Context(), "custom", "r9", 9, true); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsureVNetPreservingTag(t.Context(), "custom", "r10", 10, true); err != nil {
		t.Fatal(err)
	}
	if vnets[0]["tag"] != 16777215 || vnets[1]["tag"] != 9009 || len(vnets) != 3 || vnets[2]["vnet"] != "r10" || vnets[2]["tag"] != "10" {
		t.Fatalf("existing topology changed or new range not provisioned: %v", vnets)
	}
	if err := c.RequireVNet(t.Context(), "custom", "missing", false); err == nil {
		t.Fatal("missing host-owned NAT network accepted")
	}
	if err := c.EnsureVNetPreservingTag(t.Context(), "wrong-zone", "r9", 9, true); err == nil {
		t.Fatal("range in another zone accepted")
	}
	vnets[1]["vlanaware"] = 0
	if err := c.EnsureVNetPreservingTag(t.Context(), "custom", "r9", 9, true); err == nil {
		t.Fatal("range without VLAN isolation accepted")
	}
}

func TestHostManagedNetworkReadFailureDoesNotCreate(t *testing.T) {
	mux := http.NewServeMux()
	mutated := false
	mux.HandleFunc("/api2/json/cluster/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutated = true
		}
		http.Error(w, "denied", http.StatusForbidden)
	})
	c := fakeAPI(t, mux)
	if err := c.EnsureVNetPreservingTag(t.Context(), "ludus", "r1", 1, true); err == nil || mutated {
		t.Fatalf("network read failure must not provision: err=%v mutated=%v", err, mutated)
	}
}
