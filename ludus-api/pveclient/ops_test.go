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
