package ludusapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ludusapi/models"
	"ludusapi/pveclient"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const rotationOldPassword = "original-password"
const rotationNewPassword = "replacement & password"

type passwordFixture struct {
	mu                      sync.Mutex
	password                string
	mode                    string
	verificationUnavailable bool
	mutations               int
	entered, release        chan struct{}
	rotator                 passwordRotator
	user                    *core.Record
	client                  *pveclient.Client
}

func passwordTestApp(t *testing.T, dir string) *pocketbase.PocketBase {
	t.Helper()
	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: dir, HideStartBanner: true, DBConnect: connectLudusSQLite})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pb.ResetBootstrapState() })
	if err := pb.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	preserveConcurrentUserCredentials(pb)
	return pb
}

func newPasswordFixture(t *testing.T, realm string) *passwordFixture {
	t.Helper()
	t.Setenv("LUDUS_INSTALL_PATH", t.TempDir())
	previous := ServerConfiguration
	ServerConfiguration.DatabaseEncryptionKey = "01234567890123456789012345678901"
	t.Cleanup(func() { ServerConfiguration = previous })
	pb := passwordTestApp(t, t.TempDir())
	collection, err := pb.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(collection)
	user.SetEmail("rotation@example.com")
	user.SetPassword(rotationOldPassword)
	user.Set("userID", "ROT")
	user.Set("userNumber", 1)
	user.Set("proxmoxUsername", "rotation")
	user.Set("proxmoxRealm", realm)
	encrypted, err := EncryptStringForDatabase(rotationOldPassword)
	if err != nil {
		t.Fatal(err)
	}
	user.Set("proxmoxPassword", encrypted)
	user.Set("hashedAPIKey", "unchanged-api-key-hash")
	user.Set("proxmoxTokenID", "rotation@"+realm+"!ludus")
	user.Set("proxmoxTokenSecret", "unchanged-encrypted-token")
	if err := pb.Save(user); err != nil {
		t.Fatal(err)
	}
	user, err = pb.FindRecordById("users", user.Id)
	if err != nil {
		t.Fatal(err)
	}
	f := &passwordFixture{password: rotationOldPassword, user: user}
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":{"version":"8.2.4"}}`)) })
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"type":"node","name":"pve"}]}`))
	})
	mux.HandleFunc("/api2/json/access/ticket", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.verificationUnavailable {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.Form.Get("username") != "rotation@"+realm || r.Form.Get("password") != f.password {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"username": "rotation@" + realm, "ticket": "PVE:rotation", "CSRFPreventionToken": "rotation-csrf"}})
	})
	mux.HandleFunc("/api2/json/access/password", func(w http.ResponseWriter, r *http.Request) {
		if f.entered != nil {
			f.entered <- struct{}{}
			<-f.release
		}
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		f.mutations++
		if f.mode == "rejected" {
			http.Error(w, "invalid password", http.StatusBadRequest)
			return
		}
		if f.mode == "unknown" {
			http.Error(w, "gateway failure", http.StatusBadGateway)
			return
		}
		if r.Form.Get("confirmation-password") != f.password {
			http.Error(w, "invalid confirmation", http.StatusUnauthorized)
			return
		}
		f.password = r.Form.Get("password")
		if f.mode == "verification-outage" {
			f.verificationUnavailable = true
		}
		if f.mode == "lost-response" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		w.Write([]byte(`{"data":null}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.client, err = pveclient.New(pveclient.Config{Endpoints: []string{srv.URL}, TokenID: "root@pam!test", TokenSecret: "test-token"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.client.Close)
	f.rotator = passwordRotator{app: pb, client: func() (*pveclient.Client, error) { return f.client, nil }}
	return f
}

func (f *passwordFixture) assertCredentials(t *testing.T, expected string, pending bool) {
	t.Helper()
	user, err := f.rotator.app.FindRecordById("users", f.user.Id)
	if err != nil {
		t.Fatal(err)
	}
	password, err := DecryptStringFromDatabase(user.GetString("proxmoxPassword"))
	if err != nil || password != expected || !user.ValidatePassword(expected) {
		t.Fatal("local credentials are inconsistent")
	}
	if user.GetString("hashedAPIKey") != f.user.GetString("hashedAPIKey") || user.GetString("proxmoxTokenID") != f.user.GetString("proxmoxTokenID") || user.GetString("proxmoxTokenSecret") != f.user.GetString("proxmoxTokenSecret") {
		t.Fatal("password rotation changed API credentials")
	}
	journal, err := f.rotator.pending(f.user.Id)
	if err != nil || (journal != nil) != pending {
		t.Fatalf("unexpected recovery journal state: %v", err)
	}
	if journal != nil {
		if journal.OldPassword == rotationOldPassword || journal.NewPassword == rotationNewPassword {
			t.Fatal("recovery journal stored a plaintext password")
		}
		if value, err := DecryptStringFromDatabase(journal.NewPassword); err != nil || value != rotationNewPassword {
			t.Fatal("pending password is not recoverable")
		}
	}
}

func TestPasswordRotationAndRecovery(t *testing.T) {
	for _, scenario := range []string{"pve", "pam", "lost-response", "database-failure", "verification-outage", "rejected", "stale-password", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			realm := "pve"
			if scenario == "pam" {
				realm = "pam"
			}
			f := newPasswordFixture(t, realm)
			f.mode = scenario
			if scenario == "stale-password" {
				f.password = "changed-outside-ludus"
			}
			var failSave atomic.Bool
			failSave.Store(scenario == "database-failure")
			f.rotator.app.OnRecordUpdateExecute("users").BindFunc(func(e *core.RecordEvent) error {
				if failSave.Load() {
					return errors.New("injected commit failure")
				}
				return e.Next()
			})
			err := f.rotator.rotate(t.Context(), f.user.Id, rotationNewPassword)
			switch scenario {
			case "rejected", "stale-password":
				if err == nil {
					t.Fatal("invalid credentials accepted")
				}
				f.assertCredentials(t, rotationOldPassword, false)
				return
			case "unknown":
				if !errors.Is(err, errPasswordRotationPending) {
					t.Fatalf("ambiguous outcome lost: %v", err)
				}
				if _, err := f.rotator.credentials(t.Context(), f.user.Id); !errors.Is(err, errPasswordRotationPending) {
					t.Fatal("returned stale credentials while mutation outcome was unknown")
				}
				if err := f.rotator.rotate(t.Context(), f.user.Id, "another-password"); !errors.Is(err, errPasswordRotationPending) {
					t.Fatal("overwrote an unresolved rotation")
				}
				f.assertCredentials(t, rotationOldPassword, true)
				f.mu.Lock()
				mutations := f.mutations
				f.mu.Unlock()
				if mutations != 1 {
					t.Fatal("replayed an ambiguous password mutation")
				}
				return
			case "database-failure", "verification-outage":
				if !errors.Is(err, errPasswordRotationPending) {
					t.Fatalf("failure was not recoverable: %v", err)
				}
				f.assertCredentials(t, rotationOldPassword, true)
				// Reopen the actual database to exercise durable recovery after restart.
				dir := f.rotator.app.DataDir()
				if err := f.rotator.app.ResetBootstrapState(); err != nil {
					t.Fatal(err)
				}
				f.rotator.app = passwordTestApp(t, dir)
				f.mu.Lock()
				f.verificationUnavailable = false
				f.mu.Unlock()
				if _, err := f.rotator.credentials(t.Context(), f.user.Id); err != nil {
					t.Fatal(err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			f.assertCredentials(t, rotationNewPassword, false)
			user, _ := f.rotator.app.FindRecordById("users", f.user.Id)
			if user.ValidatePassword(rotationOldPassword) {
				t.Fatal("old Ludus password is still valid")
			}
			if _, err := f.client.AuthenticatePassword(t.Context(), f.client.ActiveEndpoint(), "rotation@"+realm, rotationOldPassword); !errors.Is(err, pveclient.ErrPasswordAuthentication) {
				t.Fatal("old Proxmox password is still valid")
			}
			if err := f.rotator.rotate(t.Context(), f.user.Id, rotationNewPassword); err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			mutations := f.mutations
			f.mu.Unlock()
			if mutations != 1 {
				t.Fatal("repeating a completed rotation sent another password mutation")
			}
			// Simulate an unrelated request saving its pre-rotation user snapshot.
			f.user.Set("lastActive", time.Now().UTC())
			if err := f.rotator.app.Save(f.user); err != nil {
				t.Fatal(err)
			}
			f.assertCredentials(t, rotationNewPassword, false)
		})
	}
}

func TestPasswordRotationPreservesRangeAccessRollback(t *testing.T) {
	for _, grant := range []bool{true, false} {
		name := "grant"
		if !grant {
			name = "revoke"
		}
		t.Run(name, func(t *testing.T) {
			f := newPasswordFixture(t, "pve")
			collection, err := f.rotator.app.FindCollectionByNameOrId("ranges")
			if err != nil {
				t.Fatal(err)
			}
			target := core.NewRecord(collection)
			target.Set("rangeID", "ROTATION")
			target.Set("rangeNumber", 42)
			target.Set("name", "ROTATION")
			if err := f.rotator.app.Save(target); err != nil {
				t.Fatal(err)
			}
			change := "ranges+"
			if !grant {
				f.user.Set("ranges+", target.Id)
				if err := f.rotator.app.Save(f.user); err != nil {
					t.Fatal(err)
				}
				f.user, err = f.rotator.app.FindRecordById("users", f.user.Id)
				if err != nil {
					t.Fatal(err)
				}
				change = "ranges-"
			}
			// Rollback must reload the user under the credential preservation hook.
			f.user.Set(change, target.Id)
			if err := f.rotator.app.Save(f.user); err != nil {
				t.Fatal(err)
			}
			if err := f.rotator.rotate(t.Context(), f.user.Id, rotationNewPassword); err != nil {
				t.Fatal(err)
			}
			if err := restoreUserRangeMembership(f.rotator.app, f.user.Id, target.Id, !grant); err != nil {
				t.Fatal(err)
			}
			fresh, err := f.rotator.app.FindRecordById("users", f.user.Id)
			if err != nil {
				t.Fatal(err)
			}
			if slices.Contains(fresh.GetStringSlice("ranges"), target.Id) == grant {
				t.Fatal("credential preservation prevented range access rollback")
			}
			f.assertCredentials(t, rotationNewPassword, false)
		})
	}
}

func TestPasswordRotationSerializesDatabaseClients(t *testing.T) {
	f := newPasswordFixture(t, "pve")
	f.entered, f.release = make(chan struct{}, 2), make(chan struct{})
	second := passwordRotator{app: passwordTestApp(t, f.rotator.app.DataDir()), client: f.rotator.client}
	result := make(chan error, 1)
	go func() { result <- f.rotator.rotate(t.Context(), f.user.Id, rotationNewPassword) }()
	select {
	case <-f.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first rotation did not reach Proxmox")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	err := second.rotate(ctx, f.user.Id, "second-password")
	cancel()
	close(f.release)
	if err == nil {
		t.Fatal("second database client bypassed the user lock")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := second.rotate(t.Context(), f.user.Id, "second-password"); err != nil {
		t.Fatal(err)
	}
	f.assertCredentials(t, "second-password", false)
}

func TestPasswordRotationHandlerRejectsUnauthorizedAndInvalidRequests(t *testing.T) {
	f := newPasswordFixture(t, "pve")
	previousApp := app
	app = f.rotator.app
	t.Cleanup(func() { app = previousApp })
	root := core.NewRecord(f.user.Collection())
	root.SetEmail("root@example.com")
	root.SetPassword(rotationOldPassword)
	root.Set("userID", "ROOT")
	root.Set("userNumber", 2)
	if err := app.Save(root); err != nil {
		t.Fatal(err)
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	router.POST("/credentials", PostCredentials).BindFunc(func(e *core.RequestEvent) error {
		acting := &models.User{}
		acting.SetProxyRecord(f.user.Clone())
		acting.SetUserId("OTHER")
		if e.Request.Header.Get("X-Test-Self") != "" {
			acting.SetUserId("ROT")
		}
		e.Set("user", acting)
		e.Auth = acting.Record
		return e.Next()
	})
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body   string
		self   bool
		status int
	}{
		{`{"userID":"ROT","proxmoxPassword":"replacement-password"}`, false, 403},
		{`{"userID":"ROOT","proxmoxPassword":"replacement-password"}`, true, 400},
		{`{"userID":"ROT","proxmoxPassword":"short"}`, true, 400},
		{`{"userID":"ROT","proxmoxPassword":"` + strings.Repeat("é", 40) + `"}`, true, 400},
		{`{"userID":"ROT"}`, true, 400},
		{`{"userID":"MISSING","proxmoxPassword":"replacement-password"}`, true, 404},
		{`{`, true, 400},
	} {
		req := httptest.NewRequest(http.MethodPost, "/credentials", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		if tc.self {
			req.Header.Set("X-Test-Self", "true")
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != tc.status {
			t.Fatalf("got HTTP %d, want %d", response.Code, tc.status)
		}
	}
	f.assertCredentials(t, rotationOldPassword, false)
}
