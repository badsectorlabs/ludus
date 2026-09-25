package ludusapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"ludusapi/dto"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/auth"
	"golang.org/x/oauth2"
)

type ssoTestProvider struct {
	auth.BaseProvider
	user *auth.AuthUser
}

func (p *ssoTestProvider) FetchToken(_ string, _ ...oauth2.AuthCodeOption) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: "test-token"}, nil
}

func (p *ssoTestProvider) FetchAuthUser(_ *oauth2.Token) (*auth.AuthUser, error) {
	return p.user, nil
}

func TestSSOExistingAccountPolicy(t *testing.T) {
	originalConfig, originalApp, originalLogger := ServerConfiguration, app, logger
	t.Cleanup(func() {
		ConfigMu.Lock()
		ServerConfiguration = originalConfig
		ConfigMu.Unlock()
		app, logger = originalApp, originalLogger
	})

	const providerName = "ludus-sso-test"
	originalProvider, hadProvider := auth.Providers[providerName]
	t.Cleanup(func() {
		if hadProvider {
			auth.Providers[providerName] = originalProvider
		} else {
			delete(auth.Providers, providerName)
		}
	})

	for _, test := range []struct {
		name            string
		requireExisting bool
		providerEmail   string
		linked          bool
		wantStatus      int
		provision       bool
	}{
		{"unknown email is denied", true, "unknown@example.com", false, http.StatusForbidden, false},
		{"matching email links existing account", true, "existing@example.com", false, http.StatusOK, false},
		{"linked identity cannot use a different email", true, "changed@example.com", true, http.StatusForbidden, false},
		{"linked identity must supply an email", true, "", true, http.StatusForbidden, false},
		{"opt out preserves existing linked login", false, "changed@example.com", true, http.StatusOK, false},
		{"opt out allows automatic provisioning", false, "new@example.com", false, http.StatusOK, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			pb := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
			if err := pb.Bootstrap(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { pb.ResetBootstrapState() })
			if err := pb.RunSystemMigrations(); err != nil {
				t.Fatal(err)
			}
			pb.Settings().Logs.MaxDays = 0
			app = pb
			logger = slog.New(slog.NewTextHandler(io.Discard, nil))
			ConfigMu.Lock()
			ServerConfiguration = Configuration{
				SSORequireExistingUser: test.requireExisting,
				DatabaseEncryptionKey:  strings.Repeat("0", 32),
			}
			ConfigMu.Unlock()

			auth.Providers[providerName] = func() auth.Provider {
				return &ssoTestProvider{user: &auth.AuthUser{Id: "provider-user", Email: test.providerEmail, Name: "SSO Regression Account"}}
			}
			collection, err := pb.FindCollectionByNameOrId("users")
			if err != nil {
				collection = core.NewAuthCollection("users")
			}
			collection.Fields.Add(&core.TextField{Name: "userID"}, &core.TextField{Name: "proxmoxUsername"})
			collection.OAuth2.Enabled = true
			collection.OAuth2.Providers = []core.OAuth2ProviderConfig{{Name: providerName, ClientId: "test-client", ClientSecret: "test-secret"}}
			if err := pb.Save(collection); err != nil {
				t.Fatal(err)
			}
			existing := core.NewRecord(collection)
			existing.SetEmail("existing@example.com")
			existing.SetPassword("test-password-123")
			existing.SetVerified(true)
			existing.Set("userID", "EXISTING")
			if err := pb.Save(existing); err != nil {
				t.Fatal(err)
			}
			if test.linked {
				relation := core.NewExternalAuth(pb)
				relation.SetCollectionRef(collection.Id)
				relation.SetRecordRef(existing.Id)
				relation.SetProvider(providerName)
				relation.SetProviderId("provider-user")
				if err := pb.Save(relation); err != nil {
					t.Fatal(err)
				}
			}

			// Substitute only the privileged provisioning service; the public OAuth
			// route, account selection, Ludus hook, and token issuance remain real.
			admin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !test.provision {
					t.Error("SSO attempted to provision an account when it should not")
					http.Error(w, "unexpected provisioning", http.StatusForbidden)
					return
				}
				var request dto.ProvisionOAuth2UserRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				record := core.NewRecord(collection)
				record.SetEmail(request.Email)
				record.SetPassword(request.Password)
				record.SetVerified(true)
				record.Set("userID", request.UserID)
				if err := pb.Save(record); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusCreated)
				json.NewEncoder(w).Encode(dto.ProvisionOAuth2UserResponse{RecordID: record.Id})
			}))
			t.Cleanup(admin.Close)
			_, port, err := net.SplitHostPort(admin.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			ConfigMu.Lock()
			ServerConfiguration.AdminPort, err = strconv.Atoi(port)
			ConfigMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}

			pb.OnRecordAuthWithOAuth2Request("users").BindFunc(populateUserFieldsFromOAuth2Provider)
			routes, err := apis.NewRouter(pb)
			if err != nil {
				t.Fatal(err)
			}
			mux, err := routes.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/collections/users/auth-with-oauth2", strings.NewReader(`{"provider":"`+providerName+`","code":"test-code","redirectURL":"http://localhost/callback"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("SSO returned HTTP %d, want %d: %s", response.Code, test.wantStatus, response.Body.String())
			}
			var result struct {
				Token   string `json:"token"`
				Message string `json:"message"`
				Record  struct {
					ID string `json:"id"`
				} `json:"record"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if test.wantStatus == http.StatusForbidden {
				if result.Token != "" || !strings.Contains(result.Message, "administrator") || !strings.Contains(result.Message, "sso_require_existing_user: false") {
					t.Fatalf("blocked SSO must explain how an admin can allow access without issuing a token: %s", response.Body.String())
				}
			} else {
				wantID := existing.Id
				if test.provision {
					created, err := pb.FindAuthRecordByEmail("users", test.providerEmail)
					if err != nil {
						t.Fatal(err)
					}
					wantID = created.Id
				}
				if result.Token == "" || result.Record.ID == "" || result.Record.ID != wantID {
					t.Fatalf("SSO did not authenticate the expected account: %s", response.Body.String())
				}
			}
			count, err := pb.CountRecords("users")
			if err != nil {
				t.Fatal(err)
			}
			wantCount := int64(1)
			if test.provision {
				wantCount++
			}
			if count != wantCount {
				t.Fatalf("SSO changed the account count: got %d, want %d", count, wantCount)
			}
		})
	}
}
