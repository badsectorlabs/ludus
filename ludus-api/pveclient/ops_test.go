package pveclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAPI builds a single-endpoint client backed by the given mux.
func fakeAPI(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2.4", "release": "1"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoints: []string{srv.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestVersion(t *testing.T) {
	c := fakeAPI(t, http.NewServeMux())
	v, err := c.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "8.2.4" {
		t.Fatalf("got %q", v.Version)
	}
	if !v.AtLeast(8, 0) {
		t.Fatal("AtLeast(8,0) should be true")
	}
	if v.AtLeast(9, 0) {
		t.Fatal("AtLeast(9,0) should be false")
	}
}

func TestStorageStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/storage", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"storage": "local", "type": "dir", "status": "available", "total": 100, "used": 40, "avail": 60, "content": "iso,vztmpl"},
		}})
	})
	c := fakeAPI(t, mux)
	s, err := c.StorageStatus(t.Context(), "pve")
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 || s[0].Storage != "local" || s[0].Avail != 60 {
		t.Fatalf("bad parse: %+v", s)
	}
}

func TestClusterNodeCount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"type": "cluster", "name": "c1"},
			{"type": "node", "name": "pve1"},
			{"type": "node", "name": "pve2"},
		}})
	})
	c := fakeAPI(t, mux)
	n, err := c.ClusterNodeCount(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCreateUser_PVERealmSendsPassword(t *testing.T) {
	var gotBody url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, _ = url.ParseQuery(string(b))
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	warn, err := c.CreateUser(t.Context(), "alice@pve", "s3cret", []string{"ludus_users"})
	if err != nil {
		t.Fatal(err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if gotBody.Get("password") != "s3cret" {
		t.Fatalf("password not sent: %v", gotBody)
	}
	if gotBody.Get("groups") != "ludus_users" {
		t.Fatalf("groups not sent: %v", gotBody)
	}
}

func TestCreateUser_PAMRealmOmitsPassword(t *testing.T) {
	var gotBody url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, _ = url.ParseQuery(string(b))
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	warn, err := c.CreateUser(t.Context(), "bob@pam", "ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "@pam") {
		t.Fatalf("expected pam warning, got %q", warn)
	}
	if gotBody.Get("password") != "" {
		t.Fatalf("password should be omitted for @pam: %v", gotBody)
	}
}

func TestCreateUser_ReconcilesAmbiguousGatewayResponse(t *testing.T) {
	created := false
	badPostHits := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "9.0"}})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api2/json/access/users" {
			badPostHits++
			created = true
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.NotFound(w, r)
	}))
	defer bad.Close()

	goodPostHits := 0
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "9.0"}})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api2/json/access/users/alice@pve" && created {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"userid": "alice@pve"}})
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api2/json/access/users" {
			goodPostHits++
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
			return
		}
		http.NotFound(w, r)
	}))
	defer good.Close()

	c, err := New(Config{Endpoints: []string{bad.URL, good.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.CreateUser(t.Context(), "alice@pve", "s3cret", nil); err != nil {
		t.Fatal(err)
	}
	if badPostHits != 1 || goodPostHits != 0 {
		t.Fatalf("ambiguous POST was replayed: bad hits=%d good hits=%d", badPostHits, goodPostHits)
	}
}

func TestCreateToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users/alice@pve/token/ludus", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"full-tokenid": "alice@pve!ludus",
			"value":        "uuid-secret",
		}})
	})
	c := fakeAPI(t, mux)
	tok, err := c.CreateToken(t.Context(), "alice@pve", "ludus", false)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "uuid-secret" || tok.FullTokenID != "alice@pve!ludus" {
		t.Fatalf("bad token: %+v", tok)
	}
}

func TestNodeStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"cpu":     0.05,
			"uptime":  12345,
			"memory":  map[string]any{"total": 8192, "used": 4096, "free": 4096},
			"loadavg": []string{"0.10", "0.05", "0.01"},
		}})
	})
	c := fakeAPI(t, mux)
	ns, err := c.NodeStatus(t.Context(), "pve")
	if err != nil {
		t.Fatal(err)
	}
	if ns.CPU != 0.05 || ns.Uptime != 12345 || ns.Memory.Total != 8192 {
		t.Fatalf("bad parse: %+v", ns)
	}
}

func TestClusterNodeIPs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"type": "cluster", "name": "c1", "ip": ""},
			{"type": "node", "name": "pve1", "ip": "10.0.0.1"},
			{"type": "node", "name": "pve2", "ip": "10.0.0.2"},
		}})
	})
	c := fakeAPI(t, mux)
	ips, err := c.ClusterNodeIPs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 2 || ips[0] != "10.0.0.1" || ips[1] != "10.0.0.2" {
		t.Fatalf("bad ips: %v", ips)
	}
}

func TestNextVMID_String(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/nextid", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": "200"})
	})
	c := fakeAPI(t, mux)
	id, err := c.NextVMID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if id != 200 {
		t.Fatalf("expected 200, got %d", id)
	}
}

func TestNextVMID_Float(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/nextid", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": 201.0})
	})
	c := fakeAPI(t, mux)
	id, err := c.NextVMID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if id != 201 {
		t.Fatalf("expected 201, got %d", id)
	}
}

func TestDeleteUser(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.DeleteUser(t.Context(), "alice@pve"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("DELETE not called")
	}
}

func TestUserExists(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users/alice@pve", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"userid": "alice@pve"}})
	})
	mux.HandleFunc("/api2/json/access/users/ghost@pve", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("user does not exist"))
	})
	c := fakeAPI(t, mux)

	exists, err := c.UserExists(t.Context(), "alice@pve")
	if err != nil || !exists {
		t.Fatalf("expected alice to exist: exists=%v err=%v", exists, err)
	}

	exists, err = c.UserExists(t.Context(), "ghost@pve")
	if err != nil || exists {
		t.Fatalf("expected ghost to not exist: exists=%v err=%v", exists, err)
	}
}

func TestDeleteToken(t *testing.T) {
	var deleted bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users/alice@pve/token/ludus", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deleted = true
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.DeleteToken(t.Context(), "alice@pve", "ludus"); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Fatal("DELETE not called")
	}
}

func TestVerifyTokenOnAll_AllGood(t *testing.T) {
	c := fakeAPI(t, http.NewServeMux())
	if err := c.VerifyTokenOnAll(t.Context()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestRaw_NotNil(t *testing.T) {
	c := fakeAPI(t, http.NewServeMux())
	if c.Raw() == nil {
		t.Fatal("Raw() should not be nil after successful New")
	}
}

func TestRaw_FailsOverOn503(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
			return
		}
		w.WriteHeader(503)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": "ok"})
	}))
	defer good.Close()

	c, err := New(Config{Endpoints: []string{bad.URL, good.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Raw().Get(t.Context(), "/cluster/nextid", nil); err != nil {
		t.Fatalf("expected raw failover success, got %v", err)
	}
	if c.ActiveEndpoint() != good.URL {
		t.Fatalf("did not fail over: active=%s", c.ActiveEndpoint())
	}
}
