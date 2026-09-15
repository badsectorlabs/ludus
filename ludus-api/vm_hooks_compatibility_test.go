package ludusapi

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// Implements only the original plugin contract.
type legacyVMTestPlugin struct{}

func (*legacyVMTestPlugin) Name() string             { return "legacy" }
func (*legacyVMTestPlugin) Initialize(*Server) error { return nil }
func (*legacyVMTestPlugin) RegisterRoutes(*core.App) {}
func (*legacyVMTestPlugin) GetEmbeddedFSs() []fs.FS  { return nil }
func (*legacyVMTestPlugin) Shutdown() error          { return nil }
func (*legacyVMTestPlugin) Initialized() bool        { return true }
func (*legacyVMTestPlugin) RoutesRegistered() bool   { return true }

type statusOnlyVMTestPlugin struct{ legacyVMTestPlugin }

func (*statusOnlyVMTestPlugin) VMStatus(context.Context, VMHookRequest) (VMStatusHookResult, error) {
	return VMStatusHookResult{Decision: StartVMContinue}, nil
}

type continuingVMTestPlugin struct{ legacyVMTestPlugin }

func (*continuingVMTestPlugin) StartVM(context.Context, VMHookRequest) (StartVMHookResult, error) {
	return StartVMContinue, nil
}
func (*continuingVMTestPlugin) StopVM(context.Context, VMHookRequest) (StartVMHookResult, error) {
	return StartVMContinue, nil
}
func (*continuingVMTestPlugin) BeforeDeleteVM(context.Context, VMHookRequest) error { return nil }

func TestLegacyVMPluginDispatch(t *testing.T) {
	for _, plugins := range [][]LudusPlugin{nil, {&legacyVMTestPlugin{}}} {
		s := &Server{plugins: plugins}
		ctx := context.Background()
		if s.hasVMLifecycleHooks() {
			t.Fatal("legacy plugin detected as lifecycle provider")
		}
		if h, e := s.runStartVMHooks(ctx, VMHookRequest{}); h || e != nil {
			t.Fatalf("start: %v %v", h, e)
		}
		if h, e := s.runStopVMHooks(ctx, VMHookRequest{}); h || e != nil {
			t.Fatalf("stop: %v %v", h, e)
		}
		if _, h, e := s.resolveVMStatus(ctx, VMHookRequest{}); h || e != nil {
			t.Fatalf("status: %v %v", h, e)
		}
		if e := s.runBeforeDeleteVMHooks(ctx, VMHookRequest{}); e != nil {
			t.Fatal(e)
		}
	}
}

func TestVMLifecycleProxmoxFallbackCompatibility(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	defer func() { logger = previousLogger }()
	cases := []struct {
		name         string
		plugins      []LudusPlugin
		selectedPool string
		template     int
	}{
		{"no plugins", nil, "different-range", 0},
		{"legacy plugin", []LudusPlugin{&legacyVMTestPlugin{}}, "different-range", 0},
		{"unrelated capability", []LudusPlugin{&statusOnlyVMTestPlugin{}}, "different-range", 0},
		{"legacy template", []LudusPlugin{&legacyVMTestPlugin{}}, "", 1},
		{"declining hooks", []LudusPlugin{&continuingVMTestPlugin{}}, "actual-range", 0},
		{"unselected VM in another pool", []LudusPlugin{&scopedVMTestPlugin{}}, "different-range", 0},
		{"unselected template", []LudusPlugin{&scopedVMTestPlugin{}}, "", 1},
	}
	for _, tc := range cases {
		for _, action := range []string{"on", "off", "delete", "already-on", "already-off"} {
			t.Run(tc.name+"/"+action, func(t *testing.T) {
				var paths []string
				status := "stopped"
				if action == "off" || action == "already-on" {
					status = "running"
				}
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					paths = append(paths, r.Method+" "+r.URL.RequestURI())
					w.Header().Set("Content-Type", "application/json")
					switch r.URL.Path {
					case "/cluster/status":
						fmt.Fprint(w, `{"data":[]}`)
					case "/cluster/resources":
						fmt.Fprintf(w, `{"data":[{"type":"qemu","vmid":126,"name":"vm","node":"pve","pool":"actual-range","template":%d,"status":%q}]}`, tc.template, status)
					case "/nodes/pve/status", "/nodes/pve/qemu/126/config":
						fmt.Fprint(w, `{"data":{}}`)
					case "/nodes/pve/qemu/126/status/current":
						fmt.Fprintf(w, `{"data":{"vmid":126,"status":%q}}`, status)
					case "/nodes/pve/qemu/126/status/start", "/nodes/pve/qemu/126/status/stop", "/nodes/pve/qemu/126":
						// Stop at the mutation boundary; no live VM or task polling is needed.
						http.Error(w, "compatibility mutation reached", http.StatusInternalServerError)
					default:
						t.Errorf("unexpected request %s", r.URL)
						http.Error(w, "unexpected", 500)
					}
				}))
				defer api.Close()
				client := goproxmox.NewClient(api.URL)
				ctx := context.Background()
				s := &Server{plugins: tc.plugins}
				run := func(legacy bool) string {
					var errs []error
					switch action {
					case "delete":
						var err error
						if legacy {
							err = destroyVM(ctx, client, 126)
						} else {
							err = s.DestroyVM(ctx, client, tc.selectedPool, 126)
						}
						if err != nil {
							errs = []error{err}
						}
					case "on", "already-on":
						if legacy {
							errs = PowerOnVMs(ctx, client, []int{126})
						} else {
							errs = s.PowerOnVMs(ctx, client, tc.selectedPool, []int{126})
						}
					default:
						if legacy {
							errs = PowerOffVMs(ctx, client, []int{126})
						} else {
							errs = s.PowerOffVMs(ctx, client, tc.selectedPool, []int{126})
						}
					}
					return fmt.Sprint(errs)
				}
				baselineErr := run(true)
				baselinePaths := append([]string(nil), paths...)
				paths = nil
				gotErr := run(false)
				if gotErr != baselineErr {
					t.Fatalf("changed result: %s; legacy: %s", gotErr, baselineErr)
				}
				// Delete hooks require an extra verified metadata lookup; absent hooks must not.
				if s.hasBeforeDeleteVMHooks() && action == "delete" {
					paths = paths[2:]
				}
				if !reflect.DeepEqual(paths, baselinePaths) {
					t.Fatalf("changed requests: %v; legacy: %v", paths, baselinePaths)
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
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pools/" || r.URL.Query().Get("poolid") != "HOOKLAB" || r.URL.Query().Get("type") != "qemu" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"poolid":"HOOKLAB","members":[{"vmid":114,"name":"HOOKLAB-windows","type":"qemu","status":"running"},{"vmid":115,"name":"template","type":"qemu","template":1},{"vmid":116,"name":"container","type":"lxc"}]}]}`)
	}))
	defer api.Close()
	event := &core.RequestEvent{}
	resources, err := getVMsForPool(event, context.Background(), "HOOKLAB", goproxmox.NewClient(api.URL))
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].Pool != "HOOKLAB" || int(resources[0].VMID) != 114 {
		t.Fatalf("pool membership was lost: %+v", resources)
	}
	cached, err := getVMsForPool(event, context.Background(), "HOOKLAB", nil)
	if err != nil || !reflect.DeepEqual(cached, resources) {
		t.Fatalf("cached membership: %+v, %v", cached, err)
	}
}
