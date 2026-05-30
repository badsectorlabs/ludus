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
