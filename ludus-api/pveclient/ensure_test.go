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
		if posts == 0 {
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
