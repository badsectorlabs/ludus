package ludusapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"ludusapi/models"
	"ludusapi/scheduler"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestRPCPluginInteroperability(t *testing.T) {
	type pluginCase struct {
		path    string
		name    string
		test    string
		method  string
		route   string
		request string
	}

	var plugins []pluginCase
	if path := os.Getenv("LUDUS_ENTERPRISE_PLUGIN"); path != "" {
		plugins = append(plugins, pluginCase{
			path:    path,
			name:    EnterprisePluginName,
			test:    "enterprise KMS status",
			method:  http.MethodGet,
			route:   "/kms/status",
			request: "/api/v2/kms/status",
		})
	}
	if path := os.Getenv("LUDUS_ANTISANDBOX_PLUGIN"); path != "" {
		plugins = append(plugins, pluginCase{
			path:    path,
			name:    AntiSandboxPluginName,
			test:    "anti-sandbox package status",
			method:  http.MethodGet,
			route:   "/antisandbox/status",
			request: "/api/v2/antisandbox/status",
		})
	}
	if len(plugins) == 0 {
		t.Skip("set LUDUS_ENTERPRISE_PLUGIN and/or LUDUS_ANTISANDBOX_PLUGIN to test external plugin executables")
	}

	ConfigMu.Lock()
	previousConfiguration := ServerConfiguration
	ServerConfiguration = Configuration{
		DataDirectory:         t.TempDir(),
		DatabaseEncryptionKey: "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48",
	}
	ConfigMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		ServerConfiguration = previousConfiguration
		ConfigMu.Unlock()
	})

	encryptedRootToken, err := EncryptStringForDatabase("rpc-proxmox-token")
	if err != nil {
		t.Fatal(err)
	}

	var updatedTimeout string
	apiServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api2/json/cluster/status" || request.URL.Path == "/api2/json/cluster/resources" {
			if request.Header.Get("Authorization") != "PVEAPIToken=root@pam!rpc=rpc-proxmox-token" {
				http.Error(response, "invalid Proxmox token", http.StatusUnauthorized)
				return
			}
			response.Header().Set("Content-Type", "application/json")
			fmt.Fprint(response, `{"data":[]}`)
			return
		}
		if token := request.Header.Get("Authorization"); token != "rpc-integration-test-token" {
			http.Error(response, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/users/records/user-record":
			fmt.Fprint(response, `{
				"id":"user-record",
				"collectionName":"users",
				"userID":"admin",
				"isAdmin":true,
				"expand":{
					"ranges":[{
						"id":"range-record",
						"collectionName":"ranges",
						"rangeID":"1",
						"name":"Integration Range"
					}],
					"groups":[]
				}
			}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/ranges/records/range-record":
			fmt.Fprint(response, `{
				"id":"range-record",
				"collectionName":"ranges",
				"rangeID":"1",
				"name":"Integration Range",
				"inactivityShutdownTimeout":""
			}`)
		case request.Method == http.MethodPatch && request.URL.Path == "/api/collections/ranges/records/range-record":
			var fields map[string]any
			if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
				http.Error(response, err.Error(), http.StatusBadRequest)
				return
			}
			updatedTimeout, _ = fields["inactivityShutdownTimeout"].(string)
			fmt.Fprintf(response, `{
				"id":"range-record",
				"collectionName":"ranges",
				"rangeID":"1",
				"name":"Integration Range",
				"inactivityShutdownTimeout":%q
			}`, updatedTimeout)
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/ranges/records":
			fmt.Fprint(response, `{"items":[{"id":"range-record","rangeID":"1","inactivityShutdownTimeout":""}],"totalPages":1}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/vms/records":
			fmt.Fprint(response, `{"items":[],"totalPages":1}`)
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/users/records":
			fmt.Fprintf(response, `{"items":[{"id":"root-user","userID":"ROOT","proxmoxTokenID":"root@pam!rpc","proxmoxTokenSecret":%q}],"totalPages":1}`, encryptedRootToken)
		default:
			http.NotFound(response, request)
		}
	}))
	defer apiServer.Close()
	ConfigMu.Lock()
	ServerConfiguration.ProxmoxURL = apiServer.URL
	ConfigMu.Unlock()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	server := &Server{
		Version:          "rpc-integration-test",
		VersionString:    "rpc-integration-test",
		LudusInstallPath: t.TempDir(),
		Logger:           logger,
		Scheduler:        scheduler.New(logger),
		PluginAPIURL:     apiServer.URL,
		pluginAPIToken:   "rpc-integration-test-token",
	}
	t.Cleanup(server.ShutdownPlugins)

	wantNames := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		if err := server.LoadPlugin(plugin.path); err != nil {
			t.Fatalf("LoadPlugin(%q) failed: %v", plugin.path, err)
		}
		wantNames = append(wantNames, plugin.name)
	}
	if err := server.InitializePlugins(); err != nil {
		t.Fatalf("InitializePlugins() failed: %v", err)
	}
	server.RegisterPluginRoutes(nil)

	if names := server.LoadedPluginNames(); !slices.Equal(names, wantNames) {
		t.Fatalf("loaded plugin names = %v, want %v", names, wantNames)
	}

	// An installed server has range records even before any VMs are deployed.
	// Its first inactivity job must not kill the executable serving routes below.
	for _, plugin := range server.plugins {
		if plugin.metadata.Name == EnterprisePluginName {
			if _, err := plugin.rpc.RunJob("inactivity-shutdown"); err != nil {
				t.Fatalf("enterprise inactivity job failed: %v", err)
			}
		}
	}

	for _, plugin := range plugins {
		t.Run(plugin.test, func(t *testing.T) {
			handler, ok := LudusPluginHandlerManager.GetHandler(plugin.method, plugin.route)
			if !ok {
				t.Fatalf("route %s %s was not registered", plugin.method, plugin.route)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(plugin.method, plugin.request, nil)
			event := &core.RequestEvent{
				Event: router.Event{Response: recorder, Request: request},
			}
			if err := handler(event); err != nil {
				t.Fatalf("plugin handler returned an error: %v", err)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			var status struct {
				Result struct {
					Status string `json:"status"`
				} `json:"result"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
				t.Fatalf("decode plugin status: %v", err)
			}
			validStatuses := []string{"running", "not_running"}
			if plugin.name == AntiSandboxPluginName {
				validStatuses = []string{"standard", "custom", "not_installed"}
			}
			if !slices.Contains(validStatuses, status.Result.Status) {
				t.Fatalf("unexpected plugin status %q", status.Result.Status)
			}
		})
	}

	if slices.Contains(wantNames, EnterprisePluginName) {
		t.Run("enterprise PocketBase update", func(t *testing.T) {
			handler, ok := LudusPluginHandlerManager.GetHandler(http.MethodPut, "/range/auto-shutdown")
			if !ok {
				t.Fatal("enterprise auto-shutdown route was not registered")
			}

			authRecord := core.NewRecord(core.NewAuthCollection("users"))
			authRecord.Id = "user-record"
			authRecord.Set("userID", "admin")
			authRecord.Set("isAdmin", true)
			user := &models.User{}
			user.SetProxyRecord(authRecord)

			rangeRecord := core.NewRecord(core.NewBaseCollection("ranges"))
			rangeRecord.Id = "range-record"
			rangeRecord.Set("rangeID", "1")
			rangeRecord.Set("name", "Integration Range")
			usersRange := &models.Range{}
			usersRange.SetProxyRecord(rangeRecord)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPut,
				"/api/v2/range/auto-shutdown",
				strings.NewReader(`{"autoShutdownTimeout":"45m"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			event := &core.RequestEvent{
				Auth:  authRecord,
				Event: router.Event{Response: recorder, Request: request},
			}
			event.Set("user", user)
			event.Set("range", usersRange)

			if err := handler(event); err != nil {
				t.Fatalf("plugin handler returned an error: %v", err)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
			}
			if updatedTimeout != "45m" {
				t.Fatalf("PocketBase update timeout = %q, want 45m", updatedTimeout)
			}
		})
	}
}
