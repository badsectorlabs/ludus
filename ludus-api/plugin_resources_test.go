package ludusapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ludusapi/scheduler"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func testManifest(id string) protocol.Manifest {
	m := protocol.Manifest{SchemaVersion: protocol.ManifestSchemaVersion, ID: id, Name: "Example", Version: "1"}
	m.Normalize()
	return m
}

// testExecutableManifest declares an executable package; tests add routes,
// hooks, level, and runAs.
func testExecutableManifest(id string) protocol.Manifest {
	m := testManifest(id)
	m.Executable = protocol.ExecutableFile
	m.ProtocolVersion = protocol.ProtocolVersion
	return m
}

func testPackage(t *testing.T, m protocol.Manifest, extras map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	w, _ := z.Create(protocol.ManifestFile)
	if err := json.NewEncoder(w).Encode(m); err != nil {
		t.Fatal(err)
	}
	for name, body := range extras {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func setPluginServiceRunAs(t *testing.T, runAs protocol.RunAs) {
	t.Helper()
	previous := pluginServiceRunAs
	pluginServiceRunAs = func() protocol.RunAs { return runAs }
	t.Cleanup(func() { pluginServiceRunAs = previous })
}

func setRootPluginOwner(t *testing.T, uid uint32) {
	t.Helper()
	previous := rootPluginOwnerUID
	rootPluginOwnerUID = uid
	t.Cleanup(func() { rootPluginOwnerUID = previous })
}

func TestPluginPackageRejectsUnsafeArchives(t *testing.T) {
	m := testManifest("example-plugin")
	m.UI = protocol.UIFile
	for _, name := range []string{"../plugin", "/plugin", "ui/../../plugin", "ui/script.js"} {
		t.Run(name, func(t *testing.T) {
			if _, err := readPluginPackage(testPackage(t, m, map[string]string{protocol.UIFile: "ok", name: "bad"})); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
	data := testPackage(t, m, map[string]string{protocol.UIFile: strings.Repeat("x", pluginUILimit+1)})
	if _, err := readPluginPackage(data); err == nil {
		t.Fatal("ZIP expansion limit not enforced")
	}
	m.Executable = protocol.ExecutableFile
	m.ProtocolVersion = 999
	if _, err := readPluginPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "no", protocol.UIFile: "ok"})); err == nil {
		t.Fatal("incompatible protocol accepted")
	}
}

type pluginTestEnv struct {
	t                                 *testing.T
	pb                                *pocketbase.PocketBase
	server                            *Server
	resources                         *PluginResources
	mux                               http.Handler
	owner, peer, admin                *core.Record
	ownerToken, peerToken, adminToken string
}

func newPluginTestEnv(t *testing.T) *pluginTestEnv {
	t.Helper()
	t.Setenv("LUDUS_INSTALL_PATH", t.TempDir())
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })
	ConfigMu.Lock()
	previousConfiguration := ServerConfiguration
	ServerConfiguration.DataDirectory = t.TempDir()
	ConfigMu.Unlock()
	t.Cleanup(func() {
		ConfigMu.Lock()
		ServerConfiguration = previousConfiguration
		ConfigMu.Unlock()
	})
	previousStateBase := pluginRootStateBase
	pluginRootStateBase = t.TempDir()
	t.Cleanup(func() { pluginRootStateBase = previousStateBase })

	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pb.ResetBootstrapState() })
	if err := pb.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	previousApp := app
	app = pb
	t.Cleanup(func() { app = previousApp })

	env := &pluginTestEnv{t: t, pb: pb}
	users, _ := pb.FindCollectionByNameOrId("users")
	newUser := func(name string, admin bool) (*core.Record, string) {
		r := core.NewRecord(users)
		r.Set("userID", name)
		r.Set("userNumber", 1)
		r.Set("isAdmin", admin)
		r.SetEmail(strings.ToLower(name) + "@example.com")
		r.SetPassword("resource-test-password")
		if err := pb.Save(r); err != nil {
			t.Fatal(err)
		}
		token, err := r.NewAuthToken()
		if err != nil {
			t.Fatal(err)
		}
		return r, token
	}
	env.owner, env.ownerToken = newUser("OWNER", false)
	env.peer, env.peerToken = newUser("PEER", false)
	env.admin, env.adminToken = newUser("ADMIN", true)

	jobs := scheduler.New(logger)
	jobs.Start()
	t.Cleanup(jobs.Stop)
	env.server = &Server{Logger: logger, LudusInstallPath: t.TempDir(), Scheduler: jobs}
	env.resources = newPluginResources(pb, env.server)
	env.server.PluginResources = env.resources
	t.Cleanup(env.server.ShutdownPlugins)
	previousServer := server
	server = env.server
	t.Cleanup(func() { server = previousServer })

	router, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	router.Bind(&hook.Handler[*core.RequestEvent]{Func: userAndRangesLookupMiddleware, Priority: 1001})
	serveEvent := &core.ServeEvent{App: pb, Router: router}
	RegisterRoutesWithPocketBase(serveEvent, pluginResourceRoutes(env.resources))
	registerPluginAPIRoutes(serveEvent, env.server)
	if env.mux, err = router.BuildMux(); err != nil {
		t.Fatal(err)
	}
	return env
}

func (env *pluginTestEnv) call(token, method, path string, body []byte, want int) *httptest.ResponseRecorder {
	env.t.Helper()
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	env.mux.ServeHTTP(response, r)
	if response.Code != want {
		env.t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
	}
	return response
}

// upload installs a package and returns its resource ID.
func (env *pluginTestEnv) upload(token, query string, pkg []byte) string {
	env.t.Helper()
	var result map[string]any
	if err := json.Unmarshal(env.call(token, "POST", APIBasePath+"/plugins/install"+query, pkg, 201).Body.Bytes(), &result); err != nil {
		env.t.Fatal(err)
	}
	return result["resourceID"].(string)
}

// review returns the activation echo an administrator sees for a record.
func (env *pluginTestEnv) review(resourceID string) activationRequest {
	env.t.Helper()
	r, err := env.pb.FindRecordById("plugin_resources", resourceID)
	if err != nil {
		env.t.Fatal(err)
	}
	m := resourceManifest(r)
	return activationRequest{SHA256: r.GetString("sha256"), Level: m.Level, RunAs: m.RunAs}
}

func (env *pluginTestEnv) activate(token, address string, echo activationRequest, want int) *httptest.ResponseRecorder {
	env.t.Helper()
	body, _ := json.Marshal(echo)
	return env.call(token, "POST", APIBasePath+"/plugins/"+address+"/activate", body, want)
}

func (env *pluginTestEnv) approve(resourceID string) {
	env.t.Helper()
	env.activate(env.adminToken, resourceID, env.review(resourceID), 200)
}

type resourceFakeRPC struct{ calls int }

func (f *resourceFakeRPC) Metadata() (protocol.Metadata, error) { return protocol.Metadata{}, nil }
func (f *resourceFakeRPC) Initialize(protocol.InitializeRequest) (protocol.InitializeResponse, error) {
	return protocol.InitializeResponse{}, nil
}
func (f *resourceFakeRPC) Handle(protocol.Request) (protocol.Response, error) {
	f.calls++
	return protocol.Response{Status: 200, Body: []byte(`{"ok":true}`), Header: map[string][]string{"Content-Type": {"application/json"}, "Set-Cookie": {"escape=1"}}}, nil
}
func (f *resourceFakeRPC) RunJob(protocol.JobRequest) error { return nil }
func (f *resourceFakeRPC) VMHook(protocol.VMHookRequest) (protocol.VMHookResponse, error) {
	return protocol.VMHookResponse{}, nil
}
func (f *resourceFakeRPC) Shutdown() error { return nil }

// registerFakePlugin puts a fake process for a record into the registry.
func (env *pluginTestEnv) registerFakePlugin(resourceID string, manifest protocol.Manifest) *resourceFakeRPC {
	fake := &resourceFakeRPC{}
	plugin := &managedPlugin{rpc: fake, manifest: manifest, source: protocol.SourceUploaded, resourceID: resourceID, initialized: true}
	plugin.host = newHostService(env.server, plugin)
	if err := env.server.registerPlugin(plugin, nil); err != nil {
		env.t.Fatal(err)
	}
	return fake
}

func TestPluginResourcesAccessAndLifecycle(t *testing.T) {
	env := newPluginTestEnv(t)
	p, s := env.resources, env.server
	ownerToken, peerToken, adminToken := env.ownerToken, env.peerToken, env.adminToken
	call := env.call
	base := APIBasePath + "/plugins"
	call("", "GET", base, nil, 401)
	m := testManifest("example-plugin")
	m.UI = protocol.UIFile
	exampleID := env.upload(ownerToken, "", testPackage(t, m, map[string]string{protocol.UIFile: "<h1>Example</h1>"}))
	r, _ := env.pb.FindRecordById("plugin_resources", exampleID)
	if r.GetString("owner") != env.owner.Id || r.GetBool("allUsers") || recordSource(r) != protocol.SourceUploaded {
		t.Fatal("upload not private to authenticated owner")
	}
	if strings.Contains(call(peerToken, "GET", base, nil, 200).Body.String(), "Example") {
		t.Fatal("private plugin leaked into peer inventory")
	}
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 404)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 409)

	// Only administrators approve, and only what they reviewed.
	echo := env.review(exampleID)
	env.activate(peerToken, "example-plugin", echo, 404)
	env.activate(ownerToken, "example-plugin", echo, 403)
	for _, changed := range []activationRequest{
		{SHA256: strings.Repeat("0", 64), Level: echo.Level, RunAs: echo.RunAs},
		{SHA256: echo.SHA256, Level: protocol.LevelSystem, RunAs: echo.RunAs},
		{SHA256: echo.SHA256, Level: echo.Level, RunAs: protocol.RunAsRoot},
		{},
	} {
		if !strings.Contains(env.activate(adminToken, "example-plugin", changed, 409).Body.String(), `"error"`) {
			t.Fatal("echo mismatch did not return an error body")
		}
	}
	env.activate(adminToken, "example-plugin", echo, 200)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 200)
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 404)
	call(ownerToken, "PUT", base+"/example-plugin/access", []byte(`{"allUsers":true}`), 403)
	call(ownerToken, "PUT", base+"/example-plugin/access", []byte(`{"allowedUsers":["PEER"]}`), 200)
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 200)
	env.activate(peerToken, "example-plugin", echo, 403)
	call(peerToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 403)
	call(peerToken, "PUT", base+"/example-plugin/access", []byte(`{"allUsers":true}`), 403)

	var listed struct {
		Plugins []map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(call(adminToken, "GET", base, nil, 200).Body.Bytes(), &listed); err != nil || len(listed.Plugins) != 1 {
		t.Fatalf("admin list: %v %v", listed, err)
	}
	item := listed.Plugins[0]
	if item["source"] != "uploaded" || item["level"] != "standard" || item["runAs"] != "ludus" || item["state"] != "running" ||
		item["canActivate"] != true || item["sha256"] != echo.SHA256 || item["apiRoutes"] == nil || item["vmHooks"] == nil {
		t.Fatalf("plugin list item = %v", item)
	}

	routed := m
	routed.Routes = []protocol.Route{{Name: "Status", Method: "GET", Pattern: "/status", Scope: protocol.ScopePlugin}}
	fake := env.registerFakePlugin(exampleID, routed)
	response := call(peerToken, "GET", base+"/example-plugin/rpc/status", nil, 200)
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("plugin set a host cookie")
	}
	call(ownerToken, "PUT", base+"/example-plugin/access", []byte(`{"allowedUsers":[]}`), 200)
	call(peerToken, "GET", base+"/example-plugin/rpc/status", nil, 404)
	if fake.calls != 1 {
		t.Fatal("revoked user's request reached plugin")
	}
	call(ownerToken, "GET", base+"/example-plugin/rpc/undeclared", nil, 404)
	call(adminToken, "PUT", base+"/example-plugin/access", []byte(`{"allUsers":true}`), 200)
	call(peerToken, "GET", base+"/example-plugin/rpc/status", nil, 200)
	call(peerToken, "DELETE", base+"/example-plugin", nil, 200)
	call(peerToken, "GET", base+"/example-plugin/rpc/status", nil, 404)
	call(peerToken, "POST", base+"/example-plugin/add", nil, 200)
	call(peerToken, "GET", base+"/example-plugin/rpc/status", nil, 200)
	call(ownerToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 403)
	packageRoot := p.root
	p.root = filepath.Join(packageRoot, exampleID, "ui/index.html")
	call(adminToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 500)
	p.root = packageRoot
	if _, err := env.pb.FindRecordById("plugin_resources", exampleID); err != nil {
		t.Fatal("failed package staging deleted the plugin record")
	}
	call(adminToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 200)
	call(ownerToken, "GET", base+"/example-plugin/rpc/status", nil, 404)
	if _, err := env.pb.FindRecordById("plugin_resources", exampleID); err == nil {
		t.Fatal("uninstall retained the plugin record")
	}
	if s.pluginByKey(pluginKey(protocol.SourceUploaded, "", exampleID)) != nil {
		t.Fatal("uninstall left the plugin process registered")
	}
	if _, err := os.Stat(filepath.Join(p.root, exampleID)); !os.IsNotExist(err) {
		t.Fatal("uninstall retained package files")
	}
	if leftovers, err := filepath.Glob(filepath.Join(p.root, ".deleting-"+exampleID+"-*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("uninstall retained staged package files: %v, %v", leftovers, err)
	}
	if strings.Contains(call(adminToken, "GET", base, nil, 200).Body.String(), "example-plugin") {
		t.Fatal("uninstalled plugin remains in the administrator list")
	}
	env.activate(adminToken, "example-plugin", echo, 404)
	reinstalled := env.upload(ownerToken, "", testPackage(t, m, map[string]string{protocol.UIFile: "reinstalled"}))
	env.approve(reinstalled)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 200)
	// Direct PocketBase writes cannot grant access or bypass activation.
	call(ownerToken, "PATCH", "/api/collections/plugin_resources/records/"+reinstalled, []byte(`{"allUsers":true}`), 403)

	// Global uploads require an administrator; personal owners can delete and
	// upload the same plugin ID again.
	m.ID = "personal-plugin"
	personalPackage := testPackage(t, m, map[string]string{protocol.UIFile: "personal"})
	call(ownerToken, "POST", base+"/install?allUsers=true", personalPackage, 403)
	call(ownerToken, "POST", base+"/install?allUsers=invalid", personalPackage, 400)
	personal := env.upload(ownerToken, "", personalPackage)
	env.approve(personal)
	call(ownerToken, "DELETE", base+"/personal-plugin?uninstall=true", nil, 200)
	if strings.Contains(call(ownerToken, "GET", base, nil, 200).Body.String(), "personal-plugin") {
		t.Fatal("owner still sees deleted plugin")
	}
	personal = env.upload(ownerToken, "", personalPackage)
	env.approve(personal)
	call(adminToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":true}`), 200)
	call(ownerToken, "DELETE", base+"/personal-plugin?uninstall=true", nil, 403)
	call(ownerToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":false}`), 403)
	call(adminToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":false}`), 200)
	call(ownerToken, "DELETE", base+"/personal-plugin", nil, 200)
	m.ID = "global-plugin"
	globalID := env.upload(adminToken, "?allUsers=true", testPackage(t, m, map[string]string{protocol.UIFile: "global"}))
	global, _ := env.pb.FindRecordById("plugin_resources", globalID)
	if !global.GetBool("allUsers") {
		t.Fatal("administrator's global upload was private")
	}
	env.approve(globalID)
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 200)

	// Replacements require explicit intent and management access, preserve
	// scope, return to pending for review, and leave the old plugin intact
	// when storage fails.
	m.Version = "2"
	replacement := testPackage(t, m, map[string]string{protocol.UIFile: "new version"})
	call(peerToken, "POST", base+"/install?replace=true&resourceID="+globalID, replacement, 403)
	call(adminToken, "POST", base+"/install?allUsers=true", replacement, 409)
	root := p.root
	p.root = filepath.Join(root, globalID, "ui/index.html", "not-a-directory")
	call(adminToken, "POST", base+"/install?replace=true&resourceID="+globalID, replacement, 500)
	p.root = root
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 200)
	updatedID := env.upload(adminToken, "?replace=true&resourceID="+globalID, replacement)
	updated, _ := env.pb.FindRecordById("plugin_resources", updatedID)
	if updated.GetString("owner") != global.GetString("owner") || !updated.GetBool("allUsers") || resourceManifest(updated).Version != "2" {
		t.Fatal("replacement did not preserve ownership/scope")
	}
	if updated.GetString("state") != "pending" {
		t.Fatalf("replacement upload state = %q, want pending", updated.GetString("state"))
	}
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 409)
	env.approve(updatedID)
	if !strings.Contains(call(peerToken, "GET", base+"/global-plugin/ui", nil, 200).Body.String(), "new version") {
		t.Fatal("replacement served old UI")
	}
	if _, err := os.Stat(filepath.Join(root, globalID)); !os.IsNotExist(err) {
		t.Fatal("replacement left superseded package files")
	}
	m.Version = "1"

	// Identical manifests may be installed independently by two users and globally.
	m.ID = "same-plugin"
	samePackage := testPackage(t, m, map[string]string{protocol.UIFile: "same package"})
	aID := env.upload(ownerToken, "", samePackage)
	bID := env.upload(peerToken, "", samePackage)
	gID := env.upload(adminToken, "?allUsers=true", samePackage)
	if aID == bID || aID == gID || bID == gID {
		t.Fatal("scoped copies share a resource ID")
	}
	for _, id := range []string{aID, bID, gID} {
		env.approve(id)
	}
	call(ownerToken, "GET", base+"/"+bID+"/ui", nil, 404)
	call(peerToken, "DELETE", base+"/"+aID+"?uninstall=true", nil, 404)
	call(peerToken, "POST", base+"/install?replace=true&resourceID="+aID, samePackage, 404)
	call(ownerToken, "POST", base+"/install", samePackage, 409)
	call(adminToken, "PUT", base+"/"+aID+"/access", []byte(`{"allUsers":true}`), 409)
	call(ownerToken, "PUT", base+"/"+aID+"/access", []byte(`{"allowedUsers":["PEER"]}`), 200)
	resolved, err := p.resolve(env.owner, "same-plugin", false)
	if err != nil || resolved.Id != aID {
		t.Fatal("slug URL did not select owner's personal copy")
	}
	m.Version = "2"
	aV2 := env.upload(ownerToken, "?replace=true&resourceID="+aID, testPackage(t, m, map[string]string{protocol.UIFile: "new owner version"}))
	env.approve(aV2)
	if !strings.Contains(call(ownerToken, "GET", base+"/same-plugin/ui", nil, 200).Body.String(), "new owner version") {
		t.Fatal("owner slug loaded the wrong version")
	}
	if !strings.Contains(call(peerToken, "GET", base+"/same-plugin/ui", nil, 200).Body.String(), "same package") {
		t.Fatal("peer copy changed with owner replacement")
	}
	call(ownerToken, "DELETE", base+"/"+aV2+"?uninstall=true", nil, 200)
	call(peerToken, "GET", base+"/"+bID+"/ui", nil, 200)
	call(ownerToken, "GET", base+"/"+gID+"/ui", nil, 200)
	call(peerToken, "DELETE", base+"/"+bID+"?uninstall=true", nil, 200)
	call(adminToken, "DELETE", base+"/"+gID+"?uninstall=true", nil, 200)

	marker := filepath.Join(t.TempDir(), "executed")
	inert := testExecutableManifest("inert-upload")
	inertID := env.upload(ownerToken, "", testPackage(t, inert, map[string]string{protocol.ExecutableFile: "#!/bin/sh\ntouch '" + marker + "'\n"}))
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("upload executed unapproved plugin")
	}
	info, err := os.Stat(filepath.Join(p.root, inertID, protocol.ExecutableFile))
	if err != nil || info.Mode().Perm()&0111 != 0 {
		t.Fatal("pending package executable bit set")
	}
	if _, err := os.Stat(filepath.Join(p.root, inertID, pluginPackageArchive)); err != nil {
		t.Fatal("upload did not keep the reviewed package archive")
	}
}

func TestPluginActivationRejectsAPIRouteCollisions(t *testing.T) {
	env := newPluginTestEnv(t)
	installed := testExecutableManifest("installed-plugin")
	installed.Level = protocol.LevelSystem
	installed.Routes = []protocol.Route{{Name: "status", Method: "GET", Pattern: "/installed/status", Scope: protocol.ScopeAPI}}
	writeInstalledPackage(t, env.server, installed, "#!/bin/sh\nexit 1\n")

	for _, test := range []struct {
		name  string
		route protocol.Route
		want  int
	}{
		{"core route", protocol.Route{Name: "license", Method: "GET", Pattern: "/license", Scope: protocol.ScopeAPI}, 422},
		{"core wildcard route", protocol.Route{Name: "vm", Method: "DELETE", Pattern: "/vm/123", Scope: protocol.ScopeAPI}, 422},
		{"route of an installed plugin", protocol.Route{Name: "status", Method: "GET", Pattern: "/installed/status", Scope: protocol.ScopeAPI}, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := testExecutableManifest("colliding-" + strings.ReplaceAll(test.name, " ", "-"))
			m.Level = protocol.LevelSystem
			m.Routes = []protocol.Route{test.route}
			id := env.upload(env.adminToken, "?allUsers=true", testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\nexit 1\n"}))
			env.activate(env.adminToken, id, env.review(id), test.want)
		})
	}
}

// writeInstalledPackage places a server package in <install>/plugins/<id>/.
func writeInstalledPackage(t *testing.T, s *Server, m protocol.Manifest, executable string) string {
	t.Helper()
	dir := filepath.Join(s.pluginsRoot(), m.ID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, protocol.ManifestFile), encoded, 0644); err != nil {
		t.Fatal(err)
	}
	if m.Executable != "" {
		if err := os.WriteFile(filepath.Join(dir, protocol.ExecutableFile), []byte(executable), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRootRunPluginRequestsGoToTheAdminService(t *testing.T) {
	env := newPluginTestEnv(t)
	setPluginServiceRunAs(t, protocol.RunAsLudus)
	rootRun := testExecutableManifest("root-plugin")
	rootRun.RunAs = protocol.RunAsRoot
	rootID := env.upload(env.adminToken, "?allUsers=true", testPackage(t, rootRun, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))
	ludusRun := testExecutableManifest("ludus-plugin")
	ludusID := env.upload(env.adminToken, "?allUsers=true", testPackage(t, ludusRun, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))

	local := testExecutableManifest("local-plugin")
	local.Level = protocol.LevelSystem
	local.Routes = []protocol.Route{{Name: "local", Method: "GET", Pattern: "/local/status", Scope: protocol.ScopeAPI}}
	env.registerFakePlugin("localresource00", local)
	env.server.remotePluginRoutes = []pluginAPIRoute{{key: "installed/remote-plugin", pluginID: "remote-plugin",
		route: protocol.Route{Name: "remote", Method: "POST", Pattern: "/remote/run", Scope: protocol.ScopeAPI}}}

	decide := func(auth *core.Record, method, path string) bool {
		e := &core.RequestEvent{Auth: auth}
		e.Request = httptest.NewRequest(method, APIBasePath+path, nil)
		return env.server.adminPluginRequest(e)
	}
	for _, test := range []struct {
		name   string
		auth   *core.Record
		method string
		path   string
		want   bool
	}{
		{"root-run activation", env.admin, "POST", "/plugins/" + rootID + "/activate", true},
		{"root-run removal", env.peer, "DELETE", "/plugins/" + rootID, true},
		{"root-run RPC", env.peer, "GET", "/plugins/root-plugin/rpc/status", true},
		{"root-run UI stays regular", env.peer, "GET", "/plugins/" + rootID + "/ui", false},
		{"root-run access stays regular", env.admin, "PUT", "/plugins/" + rootID + "/access", false},
		{"unauthenticated", nil, "POST", "/plugins/" + rootID + "/activate", false},
		{"ludus-run activation", env.admin, "POST", "/plugins/" + ludusID + "/activate", false},
		{"ludus-run RPC", env.peer, "GET", "/plugins/ludus-plugin/rpc/status", false},
		{"API route of a root-run plugin", env.peer, "POST", "/remote/run", true},
		{"API route method differs", env.peer, "GET", "/remote/run", false},
		{"API route of a local plugin", env.peer, "GET", "/local/status", false},
		{"core route", env.peer, "GET", "/license", false},
	} {
		if got := decide(test.auth, test.method, test.path); got != test.want {
			t.Errorf("%s: proxied=%v want %v", test.name, got, test.want)
		}
	}

	// The admin service admits exactly the plugin requests it hosts.
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	if !decide(env.peer, "GET", "/local/status") || !decide(env.admin, "POST", "/plugins/"+rootID+"/activate") ||
		decide(env.admin, "POST", "/plugins/"+ludusID+"/activate") || decide(env.peer, "POST", "/remote/run") {
		t.Error("admin service admitted the wrong plugin requests")
	}
}
