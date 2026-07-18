package pveclient

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestEnsurePool_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	exists := false
	mux.HandleFunc("/api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			data := []map[string]any{}
			if exists {
				data = append(data, map[string]any{"poolid": "SHARED"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "POST":
			atomic.AddInt32(&posts, 1)
			exists = true
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.EnsurePool(t.Context(), "SHARED"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsurePool(t.Context(), "SHARED"); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestEnsureSDNZone_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/zones/ludus", func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&posts) == 0 {
			w.WriteHeader(500) // not found yet
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"zone": "ludus", "type": "simple"}})
	})
	mux.HandleFunc("/api2/json/cluster/sdn/zones", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureSDNZone(t.Context(), "ludus", "simple", nil)
	_ = c.EnsureSDNZone(t.Context(), "ludus", "simple", nil)
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestSDNZoneType(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/zones/ludus", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"zone": "ludus", "type": "simple"}})
	})
	c := fakeAPI(t, mux)
	zoneType, err := c.SDNZoneType(t.Context(), "ludus")
	if err != nil {
		t.Fatal(err)
	}
	if zoneType != "simple" {
		t.Fatalf("zoneType=%q", zoneType)
	}
}

func TestEnsureGroup_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/groups/ludus_users", func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&posts) == 0 {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"groupid": "ludus_users"}})
	})
	mux.HandleFunc("/api2/json/access/groups", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureGroup(t.Context(), "ludus_users")
	_ = c.EnsureGroup(t.Context(), "ludus_users")
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestEnsureRole_CreatesAndUpdatesPrivs(t *testing.T) {
	var posts, puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/roles/LudusUser", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			if atomic.LoadInt32(&posts) == 0 {
				w.WriteHeader(500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
		case "PUT":
			atomic.AddInt32(&puts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	mux.HandleFunc("/api2/json/access/roles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureRole(t.Context(), "LudusUser", []string{"VM.Audit"})
	_ = c.EnsureRole(t.Context(), "LudusUser", []string{"VM.Audit", "VM.Clone"})
	if posts != 1 || puts != 1 {
		t.Fatalf("expected 1 POST + 1 PUT, got posts=%d puts=%d", posts, puts)
	}
}

func TestEnsureVNet_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/ludusnat", func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&posts) == 0 {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"vnet": "ludusnat"}})
	})
	mux.HandleFunc("/api2/json/cluster/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureVNet(t.Context(), "ludus", "ludusnat", 0, true)
	_ = c.EnsureVNet(t.Context(), "ludus", "ludusnat", 0, true)
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestEnsureVNet_SimpleZoneOmitsTagAndVlanAware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/r42", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	mux.HandleFunc("/api2/json/cluster/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("tag"); got != "" {
			t.Fatalf("simple zone must omit tag, got %q", got)
		}
		if got := r.Form.Get("vlanaware"); got != "" {
			t.Fatalf("simple zone must omit vlanaware, got %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	if err := c.EnsureVNet(t.Context(), "ludus", "r42", 0, false); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureVNet_VXLANIncludesTagAndVlanAware(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/r7", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	})
	mux.HandleFunc("/api2/json/cluster/sdn/vnets", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if got := r.Form.Get("tag"); got != "4007" {
			t.Fatalf("expected tag=4007, got %q", got)
		}
		if got := r.Form.Get("vlanaware"); got != "1" {
			t.Fatalf("expected vlanaware=1, got %q", got)
		}
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	if err := c.EnsureVNet(t.Context(), "ludus", "r7", 4007, true); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureVNet_UpdatesVlanAwareMismatch(t *testing.T) {
	var puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/ludusnat", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"vnet": "ludusnat", "vlanaware": 1}})
		case "PUT":
			atomic.AddInt32(&puts, 1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("vlanaware"); got != "0" {
				t.Fatalf("expected vlanaware=0, got %q", got)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.EnsureVNet(t.Context(), "ludus", "ludusnat", 0, false); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatalf("expected 1 PUT, got %d", puts)
	}
}

func TestEnsureVNet_UpdatesTagMismatch(t *testing.T) {
	var puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/r7", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"vnet": "r7", "tag": 4000, "vlanaware": 1}})
		case "PUT":
			atomic.AddInt32(&puts, 1)
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("tag"); got != "4007" {
				t.Fatalf("expected tag=4007, got %q", got)
			}
			if got := r.Form.Get("vlanaware"); got != "1" {
				t.Fatalf("expected vlanaware=1, got %q", got)
			}
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.EnsureVNet(t.Context(), "ludus", "r7", 4007, true); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatalf("expected 1 PUT, got %d", puts)
	}
}

func TestEnsureACL_AlwaysPUT(t *testing.T) {
	var puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/acl", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			atomic.AddInt32(&puts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureACL(t.Context(), "/pool/SHARED", "LudusUser", []string{"ludus_users"}, nil)
	_ = c.EnsureACL(t.Context(), "/pool/SHARED", "LudusUser", []string{"ludus_users"}, nil)
	if puts != 2 {
		t.Fatalf("expected 2 PUT (server-side idempotent), got %d", puts)
	}
}

func TestEnsureSubnet_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/ludusnat/subnets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			data := []map[string]any{}
			if atomic.LoadInt32(&posts) > 0 {
				data = append(data, map[string]any{"subnet": "ludus-192.0.2.0-24"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "POST":
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureSubnet(t.Context(), "ludusnat", "192.0.2.0/24", "192.0.2.254", true)
	_ = c.EnsureSubnet(t.Context(), "ludusnat", "192.0.2.0/24", "192.0.2.254", true)
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestEnsureSubnet_NoFalseMatchOnPrefixSubstring(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/vnets/test/subnets", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			// /16 exists; ensure asking for /1 still POSTs (does not false-match)
			json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
				{"subnet": "ludus-10.0.0.0-16"},
			}})
		case "POST":
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureSubnet(t.Context(), "test", "10.0.0.0/1", "10.0.0.1", false)
	if atomic.LoadInt32(&posts) != 1 {
		t.Fatal("expected POST: /1 must not match existing /16")
	}
}

func TestApplySDN(t *testing.T) {
	var puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			atomic.AddInt32(&puts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.ApplySDN(t.Context()); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatalf("expected 1 PUT, got %d", puts)
	}
}

func TestReloadNodeNetwork(t *testing.T) {
	var puts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/network", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PUT" {
			atomic.AddInt32(&puts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.ReloadNodeNetwork(t.Context(), "pve"); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatalf("expected 1 PUT, got %d", puts)
	}
}
