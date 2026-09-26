package ludusapi

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"ludusapi/pluginrpc"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

func TestPluginsWithoutVMHookMetadataAreNeverCalled(t *testing.T) {
	legacy := testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{}, true, func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
		t.Fatal("plugin without VM hook metadata was called")
		return pluginrpc.VMHookResponse{}, nil
	})
	for _, plugins := range [][]*managedPlugin{nil, {legacy}} {
		s := &Server{plugins: plugins}
		ctx := context.Background()
		if s.hasVMLifecycleHooks() {
			t.Fatal("plugin without capabilities detected as lifecycle provider")
		}
		if handled, err := s.runStartVMHooks(ctx, VMHookRequest{}); handled || err != nil {
			t.Fatalf("start: %v %v", handled, err)
		}
		if handled, err := s.runStopVMHooks(ctx, VMHookRequest{}); handled || err != nil {
			t.Fatalf("stop: %v %v", handled, err)
		}
		if _, handled, err := s.resolveVMStatus(ctx, VMHookRequest{}); handled || err != nil {
			t.Fatalf("status: %v %v", handled, err)
		}
		if err := s.runBeforeDeleteVMHooks(ctx, VMHookRequest{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestVMLifecycleProxmoxFallbackCompatibility(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	defer func() { logger = previousLogger }()

	legacy := func() *managedPlugin {
		return testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{}, true, nil)
	}
	statusOnly := func() *managedPlugin {
		return testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{Status: true}, true, func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
			return pluginrpc.VMHookResponse{Decision: StartVMContinue}, nil
		})
	}
	continuing := func() *managedPlugin {
		return testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{Start: true, Stop: true, BeforeDelete: true}, true, func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
			return pluginrpc.VMHookResponse{Decision: StartVMContinue}, nil
		})
	}
	unselected := func() *managedPlugin {
		return scopedVMTestPlugin(&scopedVMTestState{}, pluginrpc.VMHookCapabilities{Select: true, Start: true, Stop: true, BeforeDelete: true})
	}

	cases := []struct {
		name         string
		plugins      []*managedPlugin
		selectedPool string
		template     int
	}{
		{"no plugins", nil, "different-range", 0},
		{"plugin without hook metadata", []*managedPlugin{legacy()}, "different-range", 0},
		{"unrelated capability", []*managedPlugin{statusOnly()}, "different-range", 0},
		{"template without hooks", []*managedPlugin{legacy()}, "", 1},
		{"declining hooks", []*managedPlugin{continuing()}, "actual-range", 0},
		{"unselected VM in another pool", []*managedPlugin{unselected()}, "different-range", 0},
		{"unselected template", []*managedPlugin{unselected()}, "", 1},
	}
	for _, test := range cases {
		for _, action := range []string{"on", "off", "delete", "already-on", "already-off"} {
			t.Run(test.name+"/"+action, func(t *testing.T) {
				var paths []string
				status := "stopped"
				if action == "off" || action == "already-on" {
					status = "running"
				}
				api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
					paths = append(paths, request.Method+" "+request.URL.RequestURI())
					response.Header().Set("Content-Type", "application/json")
					switch request.URL.Path {
					case "/cluster/status":
						fmt.Fprint(response, `{"data":[]}`)
					case "/cluster/resources":
						fmt.Fprintf(response, `{"data":[{"type":"qemu","vmid":126,"name":"vm","node":"pve","pool":"actual-range","template":%d,"status":%q}]}`, test.template, status)
					case "/nodes/pve/status", "/nodes/pve/qemu/126/config":
						fmt.Fprint(response, `{"data":{}}`)
					case "/nodes/pve/qemu/126/status/current":
						fmt.Fprintf(response, `{"data":{"vmid":126,"status":%q}}`, status)
					case "/nodes/pve/qemu/126/status/start", "/nodes/pve/qemu/126/status/stop", "/nodes/pve/qemu/126":
						http.Error(response, "compatibility mutation reached", http.StatusInternalServerError)
					default:
						t.Errorf("unexpected request %s", request.URL)
						http.Error(response, "unexpected", http.StatusInternalServerError)
					}
				}))
				defer api.Close()
				client := goproxmox.NewClient(api.URL)
				ctx := context.Background()
				s := &Server{plugins: test.plugins}
				run := func(original bool) string {
					var errs []error
					switch action {
					case "delete":
						var err error
						if original {
							err = destroyVM(ctx, client, 126)
						} else {
							err = s.DestroyVM(ctx, client, test.selectedPool, 126)
						}
						if err != nil {
							errs = []error{err}
						}
					case "on", "already-on":
						if original {
							errs = PowerOnVMs(ctx, client, []int{126})
						} else {
							errs = s.PowerOnVMs(ctx, client, test.selectedPool, []int{126})
						}
					default:
						if original {
							errs = PowerOffVMs(ctx, client, []int{126})
						} else {
							errs = s.PowerOffVMs(ctx, client, test.selectedPool, []int{126})
						}
					}
					return fmt.Sprint(errs)
				}
				baselineErr := run(true)
				baselinePaths := append([]string(nil), paths...)
				paths = nil
				gotErr := run(false)
				if gotErr != baselineErr {
					t.Fatalf("changed result: %s; original: %s", gotErr, baselineErr)
				}
				if s.hasBeforeDeleteVMHooks() && action == "delete" {
					lookup := []string{"GET /cluster/status", "GET /cluster/resources?type=vm"}
					if len(paths) < len(lookup) || !reflect.DeepEqual(paths[:len(lookup)], lookup) {
						t.Fatalf("unexpected metadata lookup: %v", paths)
					}
					paths = paths[len(lookup):]
				}
				if !reflect.DeepEqual(paths, baselinePaths) {
					t.Fatalf("changed requests: %v; original: %v", paths, baselinePaths)
				}
				if strings.HasPrefix(action, "already-") {
					if gotErr != "[]" {
						t.Fatalf("idempotent operation failed: %s", gotErr)
					}
				} else {
					want := map[string]string{
						"on":     "POST /nodes/pve/qemu/126/status/start",
						"off":    "POST /nodes/pve/qemu/126/status/stop",
						"delete": "DELETE /nodes/pve/qemu/126",
					}[action]
					if len(paths) == 0 || paths[len(paths)-1] != want {
						t.Fatalf("did not reach original Proxmox operation %q: %v", want, paths)
					}
				}
			})
		}
	}
}

func TestPoolResourceMembershipReachesLifecycleHooks(t *testing.T) {
	previousConfiguration := ServerConfiguration
	closeRootPVEClient()
	t.Cleanup(func() {
		closeRootPVEClient()
		ServerConfiguration = previousConfiguration
	})

	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api2/json/version":
			fmt.Fprint(response, `{"data":{"version":"9.0"}}`)
		case "/api2/json/pools/test-range":
			fmt.Fprint(response, `{"data":{"members":[{"vmid":201,"name":"test-range-windows","node":"pve","type":"qemu","status":"running"},{"vmid":202,"name":"template","node":"pve","type":"qemu","template":1},{"vmid":203,"name":"container","node":"pve","type":"lxc"}]}}`)
		case "/api2/json/nodes/pve/qemu/201/status/current":
			fmt.Fprint(response, `{"data":{"name":"test-range-windows","status":"running"}}`)
		case "/api2/json/nodes/pve/qemu/201/config":
			fmt.Fprint(response, `{"data":{"name":"test-range-windows"}}`)
		case "/api2/json/nodes/pve/qemu/202/status/current":
			fmt.Fprint(response, `{"data":{"name":"template","status":"stopped"}}`)
		case "/api2/json/nodes/pve/qemu/202/config":
			fmt.Fprint(response, `{"data":{"name":"template","template":1}}`)
		default:
			t.Errorf("unexpected request: %s", request.URL)
			http.NotFound(response, request)
		}
	}))
	defer api.Close()
	ServerConfiguration.ProxmoxEndpoints = []string{api.URL}
	ServerConfiguration.ProxmoxTokenID = "root@pam!test"
	ServerConfiguration.ProxmoxTokenSecret = "secret"

	event := &core.RequestEvent{}
	resources, err := getVMsForPool(event, context.Background(), "test-range", goproxmox.NewClient(api.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Pool != "test-range" || int(resources[0].VMID) != 201 {
		t.Fatalf("pool membership was lost: %+v", resources)
	}
	cached, err := getVMsForPool(event, context.Background(), "test-range", nil)
	if err != nil || !reflect.DeepEqual(cached, resources) {
		t.Fatalf("cached membership: %+v, %v", cached, err)
	}
}
