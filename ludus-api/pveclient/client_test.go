package pveclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func versionHandler(ver string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": ver, "release": "1"}})
			return
		}
		http.NotFound(w, r)
	}
}

func TestNew_PicksFirstHealthyEndpoint(t *testing.T) {
	dead := httptest.NewUnstartedServer(nil) // never started → conn refused
	deadURL := "http://" + dead.Listener.Addr().String()
	dead.Listener.Close()

	live := httptest.NewServer(versionHandler("8.2.4"))
	defer live.Close()

	c, err := New(Config{
		Endpoints:   []string{deadURL, live.URL},
		TokenID:     "root@pam!t",
		TokenSecret: "s",
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if c.ActiveEndpoint() != live.URL {
		t.Fatalf("expected active=%s got %s", live.URL, c.ActiveEndpoint())
	}
}

func TestNew_AllDead(t *testing.T) {
	_, err := New(Config{
		Endpoints:   []string{"http://127.0.0.1:1", "http://127.0.0.1:2"},
		TokenID:     "root@pam!t",
		TokenSecret: "s",
		Timeout:     500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error when all endpoints unreachable")
	}
}

func TestDo_FailsOverOn503(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
			return
		}
		w.WriteHeader(503)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
	}))
	defer good.Close()

	c, err := New(Config{Endpoints: []string{bad.URL, good.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out apiResp[map[string]any]
	if err := c.do(t.Context(), "GET", "/api2/json/cluster/nextid", nil, &out); err != nil {
		t.Fatalf("expected failover success, got %v", err)
	}
	if c.ActiveEndpoint() != good.URL {
		t.Fatalf("did not fail over: active=%s", c.ActiveEndpoint())
	}
}

func TestDo_NoFailoverOn403(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
			return
		}
		hits++
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoints: []string{srv.URL, srv.URL}, TokenID: "t", TokenSecret: "s"})
	defer c.Close()
	err := c.do(t.Context(), "GET", "/api2/json/access/users", nil, nil)
	if err == nil {
		t.Fatal("expected 403 error")
	}
	if hits != 1 {
		t.Fatalf("expected 1 hit (no retry), got %d", hits)
	}
}
