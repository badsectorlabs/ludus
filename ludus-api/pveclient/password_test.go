package pveclient

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPasswordSessionDoesNotReplayOrUseAPIToken(t *testing.T) {
	var mutations, failoverRequests atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "" || r.Form.Get("username") != "alice@pam" || r.Form.Get("password") != "old & password" {
			t.Error("password login did not use the target's form credentials")
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": "alice@pam", "ticket": "PVE:user-ticket", "CSRFPreventionToken": "csrf-token"}})
	})
	mux.HandleFunc("/api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		mutations.Add(1)
		r.ParseForm()
		cookie, err := r.Cookie("PVEAuthCookie")
		if err != nil || cookie.Value != "PVE:user-ticket" || r.Header.Get("CSRFPreventionToken") != "csrf-token" || r.Header.Get("Authorization") != "" {
			t.Error("password mutation did not use only a ticket and CSRF token")
		}
		if r.Method != http.MethodPut || r.Form.Get("userid") != "alice@pam" || r.Form.Get("confirmation-password") != "old & password" || r.Form.Get("password") != "new + password" {
			t.Error("password mutation did not include the correct target and confirmation password")
		}
		http.Error(w, "old & password new + password PVE:user-ticket", http.StatusBadGateway)
	})
	c := fakeAPI(t, mux)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { failoverRequests.Add(1) }))
	defer other.Close()
	c.mu.Lock()
	c.endpoints = append(c.endpoints, endpoint{url: other.URL, healthy: true})
	c.mu.Unlock()
	session, err := c.AuthenticatePassword(t.Context(), c.ActiveEndpoint(), "alice@pam", "old & password")
	if err != nil {
		t.Fatal(err)
	}
	err = session.ChangePassword(t.Context(), "new + password")
	if !errors.Is(err, ErrPasswordUnknown) || mutations.Load() != 1 || failoverRequests.Load() != 0 {
		t.Fatalf("ambiguous mutation was not kept on its original endpoint: %v", err)
	}
	if strings.Contains(err.Error(), "password PVE:") || strings.Contains(err.Error(), "new + password") {
		t.Fatal("error leaked response credentials")
	}
}

func TestPasswordAuthenticationRejectsChallengesAndRedirects(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer other.Close()
	for _, scenario := range []string{"stale-password", "mfa", "redirect", "wrong-user"} {
		t.Run(scenario, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "stale-password":
					http.Error(w, "secret-response", http.StatusUnauthorized)
				case "redirect":
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
				case "mfa":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": "alice@pve", "ticket": "PVE:!tfa!challenge"}})
				case "wrong-user":
					json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": "root@pam", "ticket": "PVE:ticket", "CSRFPreventionToken": "csrf"}})
				}
			})
			c := fakeAPI(t, mux)
			_, err := c.AuthenticatePassword(t.Context(), c.ActiveEndpoint(), "alice@pve", "secret-request")
			if err == nil || strings.Contains(err.Error(), "secret-") {
				t.Fatalf("authentication was accepted or leaked credentials: %v", err)
			}
			if scenario == "stale-password" && !errors.Is(err, ErrPasswordAuthentication) {
				t.Fatal(err)
			}
			if scenario == "mfa" && !errors.Is(err, ErrPasswordMFA) {
				t.Fatal(err)
			}
		})
	}
	if redirected.Load() != 0 {
		t.Fatal("authentication credentials followed a redirect")
	}
}

func TestPasswordEndpointRejectsMultiNodePAM(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"type": "node", "name": "one"}, {"type": "node", "name": "two"}}})
	})
	c := fakeAPI(t, mux)
	if _, err := c.PasswordEndpoint(t.Context(), "pam"); !errors.Is(err, ErrPasswordUnsupported) {
		t.Fatalf("multi-node PAM rotation allowed: %v", err)
	}
	if _, err := c.PasswordEndpoint(t.Context(), "pve"); err != nil {
		t.Fatal(err)
	}
}
