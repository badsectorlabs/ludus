package ludusapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestAdminRoutingPreservesPocketBaseAuthorization(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("admin routing requires the root service identity")
	}
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })

	superuser := core.NewRecord(core.NewAuthCollection(core.CollectionNameSuperusers))
	user := core.NewRecord(core.NewAuthCollection("users"))
	for _, test := range []struct {
		name   string
		path   string
		auth   *core.Record
		status int
	}{
		{"superuser reaches PocketBase", "/api/collections/users/records", superuser, http.StatusOK},
		{"missing native API authentication", "/api/collections/users/records", nil, http.StatusUnauthorized},
		{"ordinary user lacks superuser access", "/api/collections/users/records", user, http.StatusForbidden},
		{"Ludus route remains on regular service", APIBasePath + "/kms/status", superuser, http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			event := &core.RequestEvent{
				Auth: test.auth,
				Event: router.Event{
					Request:  httptest.NewRequest(http.MethodGet, test.path, nil),
					Response: response,
				},
			}
			chain := hook.Hook[*core.RequestEvent]{}
			chain.BindFunc(limitRootEndpoints)
			chain.Bind(apis.RequireSuperuserAuth())
			err := chain.Trigger(event, func(e *core.RequestEvent) error {
				return e.JSON(http.StatusOK, map[string]bool{"authorized": true})
			})
			status := response.Code
			if err != nil {
				var apiError *router.ApiError
				if !errors.As(err, &apiError) {
					t.Fatal(err)
				}
				status = apiError.Status
			}
			if status != test.status {
				t.Fatalf("HTTP status = %d, want %d; body: %s", status, test.status, response.Body.String())
			}
		})
	}
}

func TestRangeRevocationStaysOnRegularService(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })
	for _, path := range []string{"/ranges/revoke/USER/RANGE", "/range", "/range/RANGE/vms"} {
		t.Run(path, func(t *testing.T) {
			if os.Geteuid() != 0 && path != "/ranges/revoke/USER/RANGE" {
				return // The actual admin proxy requires a running peer service.
			}
			response := httptest.NewRecorder()
			event := &core.RequestEvent{Event: router.Event{
				Request: httptest.NewRequest(http.MethodDelete, APIBasePath+path, nil), Response: response,
			}}
			chain := hook.Hook[*core.RequestEvent]{}
			chain.BindFunc(limitRootEndpoints)
			if err := chain.Trigger(event, func(e *core.RequestEvent) error {
				return e.JSON(http.StatusOK, map[string]bool{"handled": true})
			}); err != nil {
				t.Fatal(err)
			}
			want := http.StatusOK
			if os.Geteuid() == 0 && path == "/ranges/revoke/USER/RANGE" {
				want = http.StatusInternalServerError
			}
			if response.Code != want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, want, response.Body.String())
			}
		})
	}
}

func TestLocalPluginAPIRoutesStayOnRegularService(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("prefix proxying happens in the regular service")
	}
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })
	admin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(admin.Close)
	adminURL, _ := url.Parse(admin.URL)
	previousProxy := adminProxy
	adminProxy = httputil.NewSingleHostReverseProxy(adminURL)
	t.Cleanup(func() { adminProxy = previousProxy })

	m := testExecutableManifest("user-tools")
	m.Level = protocol.LevelSystem
	m.Routes = []protocol.Route{{Name: "profile", Method: "GET", Pattern: "/user/tools/profile", Scope: protocol.ScopeAPI}}
	previousServer := server
	server = &Server{plugins: []*managedPlugin{{manifest: m, source: protocol.SourceServer}}}
	t.Cleanup(func() { server = previousServer })

	for _, test := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/user/tools/profile", http.StatusOK},
		{http.MethodPost, "/user/tools/profile", http.StatusTeapot},
		{http.MethodGet, "/user/other", http.StatusTeapot},
	} {
		response := httptest.NewRecorder()
		event := &core.RequestEvent{Event: router.Event{Request: httptest.NewRequest(test.method, APIBasePath+test.path, nil), Response: response}}
		chain := hook.Hook[*core.RequestEvent]{}
		chain.BindFunc(limitRootEndpoints)
		if err := chain.Trigger(event, func(e *core.RequestEvent) error {
			return e.JSON(http.StatusOK, map[string]bool{"local": true})
		}); err != nil {
			t.Fatal(err)
		}
		if response.Code != test.want {
			t.Errorf("%s %s: status %d, want %d", test.method, test.path, response.Code, test.want)
		}
	}
}
