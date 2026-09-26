package ludusapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ludusapi/pluginrpc"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

func testPackage(t *testing.T, m PluginManifest, extras map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	w, _ := z.Create("plugin.json")
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

func TestPluginPackageRejectsUnsafeArchives(t *testing.T) {
	m := PluginManifest{SchemaVersion: 1, ID: "example-plugin", Name: "Example", Version: "1", UI: "ui/index.html"}
	for _, name := range []string{"../plugin", "/plugin", "ui/../../plugin", "ui/script.js"} {
		t.Run(name, func(t *testing.T) {
			if _, err := readPluginPackage(testPackage(t, m, map[string]string{"ui/index.html": "ok", name: "bad"})); err == nil {
				t.Fatal("unsafe entry accepted")
			}
		})
	}
	data := testPackage(t, m, map[string]string{"ui/index.html": strings.Repeat("x", pluginUILimit+1)})
	if _, err := readPluginPackage(data); err == nil {
		t.Fatal("ZIP expansion limit not enforced")
	}
	m.Executable = "plugin"
	m.Protocol = 999
	if _, err := readPluginPackage(testPackage(t, m, map[string]string{"plugin": "no", "ui/index.html": "ok"})); err == nil {
		t.Fatal("incompatible protocol accepted")
	}
}

type resourceFakeRPC struct{ calls int }

func (f *resourceFakeRPC) Metadata() (pluginrpc.Metadata, error) { return pluginrpc.Metadata{}, nil }
func (f *resourceFakeRPC) Initialize(pluginrpc.InitializeRequest) (pluginrpc.InitializeResponse, error) {
	return pluginrpc.InitializeResponse{}, nil
}
func (f *resourceFakeRPC) Handle(r pluginrpc.Request) (pluginrpc.Response, error) {
	f.calls++
	return pluginrpc.Response{Status: 200, Body: []byte(`{"ok":true}`), Header: map[string][]string{"Content-Type": {"application/json"}, "Set-Cookie": {"escape=1"}}}, nil
}
func (f *resourceFakeRPC) RunJob(string) (pluginrpc.JobResponse, error) {
	return pluginrpc.JobResponse{}, nil
}
func (f *resourceFakeRPC) Shutdown() error { return nil }

type resourceProcessRPC struct{ resourceFakeRPC }

func (f *resourceProcessRPC) Metadata() (pluginrpc.Metadata, error) {
	return pluginrpc.Metadata{ID: "process-plugin", Name: "Example", Version: "1", Routes: []pluginrpc.Route{{Name: "Status", Method: "GET", Pattern: "/status"}}}, nil
}

// Re-exec the test binary as a real RPC subprocess, without a Go compiler or
// platform-specific fixture binary in the test suite.
func TestPluginResourceProcessHelper(t *testing.T) {
	if os.Getenv("LUDUS_RESOURCE_PROCESS_HELPER") != "1" {
		return
	}
	pluginrpc.Serve(&resourceProcessRPC{})
	os.Exit(0)
}

func TestPluginResourcesAccessAndLifecycle(t *testing.T) {
	t.Setenv("LUDUS_INSTALL_PATH", t.TempDir())
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })
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
	users, _ := pb.FindCollectionByNameOrId("users")
	newUser := func(name string, admin bool) (*core.Record, string) {
		r := core.NewRecord(users)
		r.Set("userID", name)
		r.Set("userNumber", 1)
		r.Set("isAdmin", admin)
		r.SetEmail(name + "@example.com")
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
	owner, ownerToken := newUser("OWNER", false)
	_, peerToken := newUser("PEER", false)
	_, adminToken := newUser("ADMIN", true)
	s := &Server{Logger: logger, PluginAPIURL: "http://127.0.0.1:1", pluginAPIToken: "test-token"}
	p := newPluginResources(pb, s)
	t.Cleanup(p.Close)
	s.PluginResources = p
	router, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	router.Bind(&hook.Handler[*core.RequestEvent]{Func: userAndRangesLookupMiddleware, Priority: 1001})
	registerPluginResourceRoutes(&core.ServeEvent{App: pb, Router: router}, p)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, method, path string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, r)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	base := APIBasePath + "/plugins"
	call("", "GET", base, nil, 401)
	m := PluginManifest{SchemaVersion: 1, ID: "example-plugin", Name: "Example", Version: "1", UI: "ui/index.html"}
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "<h1>Example</h1>"}), 201)
	r, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	if r.GetString("owner") != owner.Id || r.GetBool("allUsers") {
		t.Fatal("upload not private to authenticated owner")
	}
	if strings.Contains(call(peerToken, "GET", base, nil, 200).Body.String(), "Example") {
		t.Fatal("private plugin leaked into peer inventory")
	}
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 404)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 409)
	call(peerToken, "POST", base+"/example-plugin/activate", nil, 404)
	call(ownerToken, "POST", base+"/example-plugin/activate", nil, 200)
	call(adminToken, "POST", base+"/example-plugin/activate", nil, 200)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 200)
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 404)
	call(ownerToken, "PUT", base+"/example-plugin/access", []byte(`{"allUsers":true}`), 403)
	call(ownerToken, "PUT", base+"/example-plugin/access", []byte(`{"allowedUsers":["PEER"]}`), 200)
	call(peerToken, "GET", base+"/example-plugin/ui", nil, 200)
	call(peerToken, "POST", base+"/example-plugin/activate", nil, 403)
	call(peerToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 403)
	call(peerToken, "PUT", base+"/example-plugin/access", []byte(`{"allUsers":true}`), 403)

	fake := &resourceFakeRPC{}
	p.runtimes[r.Id] = &resourceRuntime{plugin: &managedPlugin{rpc: fake, metadata: pluginrpc.Metadata{Routes: []pluginrpc.Route{{Name: "Status", Method: "GET", Pattern: "/status"}}}}, system: true}
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
	p.root = filepath.Join(packageRoot, r.Id, "ui/index.html")
	call(adminToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 500)
	p.root = packageRoot
	if _, err := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID); err != nil {
		t.Fatal("failed package staging deleted the plugin record")
	}
	call(adminToken, "DELETE", base+"/example-plugin?uninstall=true", nil, 200)
	call(ownerToken, "GET", base+"/example-plugin/rpc/status", nil, 404)
	if _, err := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID); err == nil {
		t.Fatal("uninstall retained the plugin record")
	}
	if _, err := os.Stat(filepath.Join(p.root, r.Id)); !os.IsNotExist(err) {
		t.Fatal("uninstall retained package files")
	}
	if leftovers, err := filepath.Glob(filepath.Join(p.root, ".deleting-"+r.Id+"-*")); err != nil || len(leftovers) != 0 {
		t.Fatalf("uninstall retained staged package files: %v, %v", leftovers, err)
	}
	if strings.Contains(call(adminToken, "GET", base, nil, 200).Body.String(), "example-plugin") {
		t.Fatal("uninstalled plugin remains in the administrator list")
	}
	p.startup()
	if p.runtimes[r.Id] != nil {
		t.Fatal("deleted plugin restarted")
	}
	call(adminToken, "POST", base+"/example-plugin/activate", nil, 404)
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "reinstalled"}), 201)
	call(ownerToken, "POST", base+"/example-plugin/activate", nil, 200)
	call(ownerToken, "GET", base+"/example-plugin/ui", nil, 200)
	// Direct PocketBase writes cannot grant access or bypass activation.
	call(ownerToken, "PATCH", "/api/collections/plugin_resources/records/"+r.Id, []byte(`{"allUsers":true}`), 403)

	// Global uploads require an administrator; personal owners can delete and
	// upload the same plugin ID again without administrator approval.
	m.ID = "personal-plugin"
	personalPackage := testPackage(t, m, map[string]string{"ui/index.html": "personal"})
	call(ownerToken, "POST", base+"/install?allUsers=true", personalPackage, 403)
	call(ownerToken, "POST", base+"/install?allUsers=invalid", personalPackage, 400)
	call(ownerToken, "POST", base+"/install", personalPackage, 201)
	call(ownerToken, "POST", base+"/personal-plugin/activate", nil, 200)
	personal, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	call(ownerToken, "DELETE", base+"/personal-plugin?uninstall=true", nil, 200)
	if _, err := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID); err == nil || p.runtimes[personal.Id] != nil {
		t.Fatal("owner deletion did not stop and remove personal plugin")
	}
	if strings.Contains(call(ownerToken, "GET", base, nil, 200).Body.String(), "personal-plugin") {
		t.Fatal("owner still sees deleted plugin")
	}
	call(peerToken, "POST", base+"/personal-plugin/activate", nil, 404)
	call(ownerToken, "POST", base+"/install", personalPackage, 201)
	call(ownerToken, "POST", base+"/personal-plugin/activate", nil, 200)
	call(adminToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":true}`), 200)
	call(ownerToken, "POST", base+"/personal-plugin/activate", nil, 403)
	call(ownerToken, "DELETE", base+"/personal-plugin?uninstall=true", nil, 403)
	call(ownerToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":false}`), 403)
	call(adminToken, "PUT", base+"/personal-plugin/access", []byte(`{"allUsers":false}`), 200)
	call(ownerToken, "DELETE", base+"/personal-plugin", nil, 200)
	// Tombstones left by older Ludus releases can also be permanently deleted.
	m.ID = "legacy-removed-plugin"
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "legacy"}), 201)
	legacy, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	legacy.Set("state", "removed")
	if err := pb.Save(legacy); err != nil {
		t.Fatal(err)
	}
	call(ownerToken, "DELETE", base+"/legacy-removed-plugin?uninstall=true", nil, 200)
	if _, err := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID); err == nil {
		t.Fatal("legacy tombstone remains after deletion")
	}
	if _, err := os.Stat(filepath.Join(p.root, legacy.Id)); !os.IsNotExist(err) {
		t.Fatal("legacy tombstone package remains after deletion")
	}
	m.ID = "global-plugin"
	call(adminToken, "POST", base+"/install?allUsers=true", testPackage(t, m, map[string]string{"ui/index.html": "global"}), 201)
	global, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	if !global.GetBool("allUsers") {
		t.Fatal("administrator's global upload was private")
	}
	call(adminToken, "POST", base+"/global-plugin/activate", nil, 200)
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 200)
	// Replacements require explicit intent and management access, preserve scope,
	// and leave the old plugin intact when storage fails.
	m.Version = "2"
	replacement := testPackage(t, m, map[string]string{"ui/index.html": "new version"})
	call(peerToken, "POST", base+"/install?replace=true&resourceID="+global.Id, replacement, 403)
	call(adminToken, "POST", base+"/install?allUsers=true", replacement, 409)
	root := p.root
	p.root = filepath.Join(root, global.Id, "ui/index.html", "not-a-directory")
	call(adminToken, "POST", base+"/install?replace=true&resourceID="+global.Id, replacement, 500)
	p.root = root
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 200)
	call(adminToken, "POST", base+"/install?replace=true&resourceID="+global.Id, replacement, 201)
	updated, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	if updated.GetString("owner") != global.GetString("owner") || !updated.GetBool("allUsers") || resourceManifest(updated).Version != "2" || p.runtimes[global.Id] != nil {
		t.Fatal("replacement did not preserve ownership/scope or stop old version")
	}
	call(peerToken, "GET", base+"/global-plugin/ui", nil, 409)
	call(adminToken, "POST", base+"/global-plugin/activate", nil, 200)
	if !strings.Contains(call(peerToken, "GET", base+"/global-plugin/ui", nil, 200).Body.String(), "new version") {
		t.Fatal("replacement served old UI")
	}
	if _, err := os.Stat(filepath.Join(root, global.Id)); !os.IsNotExist(err) {
		t.Fatal("replacement left superseded package files")
	}
	m.ID = "personal-plugin"
	call(ownerToken, "POST", base+"/install?replace=true", testPackage(t, m, map[string]string{"ui/index.html": "personal v2"}), 201)
	call(ownerToken, "POST", base+"/personal-plugin/activate", nil, 200)
	call(peerToken, "GET", base+"/personal-plugin/ui", nil, 404)
	m.Version = "1"

	// Identical manifests may be installed independently by two users and globally.
	m.ID = "same-plugin"
	samePackage := testPackage(t, m, map[string]string{"ui/index.html": "same package"})
	uploadID := func(token, query string, pkg []byte) string {
		t.Helper()
		var result map[string]any
		if err := json.Unmarshal(call(token, "POST", base+"/install"+query, pkg, 201).Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result["resourceID"].(string)
	}
	aID := uploadID(ownerToken, "", samePackage)
	bID := uploadID(peerToken, "", samePackage)
	gID := uploadID(adminToken, "?allUsers=true", samePackage)
	if aID == bID || aID == gID || bID == gID {
		t.Fatal("scoped copies share a resource ID")
	}
	for _, pair := range [][2]string{{ownerToken, aID}, {peerToken, bID}, {adminToken, gID}} {
		call(pair[0], "POST", base+"/"+pair[1]+"/activate", nil, 200)
	}
	call(ownerToken, "GET", base+"/"+bID+"/ui", nil, 404)
	call(peerToken, "DELETE", base+"/"+aID+"?uninstall=true", nil, 404)
	call(peerToken, "POST", base+"/install?replace=true&resourceID="+aID, samePackage, 404)
	call(ownerToken, "POST", base+"/install", samePackage, 409)
	call(adminToken, "PUT", base+"/"+aID+"/access", []byte(`{"allUsers":true}`), 409)
	call(ownerToken, "PUT", base+"/"+aID+"/access", []byte(`{"allowedUsers":["PEER"]}`), 200)
	resolved, err := p.resolve(owner, "same-plugin", false)
	if err != nil || resolved.Id != aID {
		t.Fatal("legacy URL did not select owner's personal copy")
	}
	m.Version = "2"
	aV2 := uploadID(ownerToken, "?replace=true&resourceID="+aID, testPackage(t, m, map[string]string{"ui/index.html": "new owner version"}))
	call(ownerToken, "POST", base+"/"+aV2+"/activate", nil, 200)
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
	m.Version = "1"

	marker := filepath.Join(t.TempDir(), "executed")
	m.ID = "inert-upload"
	m.Executable = "plugin"
	m.Protocol = pluginrpc.ProtocolVersion
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "ok", "plugin": "#!/bin/sh\ntouch '" + marker + "'\n"}), 201)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("upload executed unapproved plugin")
	}
	inert, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", "inert-upload")
	info, err := os.Stat(filepath.Join(p.root, inert.Id, "plugin"))
	if err != nil || info.Mode().Perm()&0111 != 0 {
		t.Fatal("pending package executable bit set")
	}

	// Approval launches a real process. Uninstall kills it, and a new upload
	// launches a different process; pending uploads never start it.
	t.Setenv("LUDUS_RESOURCE_PROCESS_HELPER", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	m.ID = "process-plugin"
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestPluginResourceProcessHelper$'\n"
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "ok", "plugin": script}), 201)
	call(ownerToken, "POST", base+"/process-plugin/activate", nil, 200)
	processRecord, _ := pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	first := p.runtimes[processRecord.Id].plugin.client
	call(ownerToken, "GET", base+"/process-plugin/rpc/status", nil, 200)
	call(ownerToken, "DELETE", base+"/process-plugin?uninstall=true", nil, 200)
	if !first.Exited() {
		t.Fatal("uninstall did not stop process")
	}
	if _, err := os.Stat(filepath.Join(p.root, processRecord.Id)); !os.IsNotExist(err) {
		t.Fatal("uninstall retained process package")
	}
	call(ownerToken, "POST", base+"/install", testPackage(t, m, map[string]string{"ui/index.html": "ok", "plugin": script}), 201)
	call(ownerToken, "POST", base+"/process-plugin/activate", nil, 200)
	processRecord, _ = pb.FindFirstRecordByData("plugin_resources", "pluginID", m.ID)
	if p.runtimes[processRecord.Id].plugin.client == first {
		t.Fatal("reinstall reused stopped process")
	}
	call(ownerToken, "GET", base+"/process-plugin/rpc/status", nil, 200)

	// Run through the real range-access middleware, not only the resource ACL.
	ranges, _ := pb.FindCollectionByNameOrId("ranges")
	rng := core.NewRecord(ranges)
	rng.Set("rangeID", "OWNER-RANGE")
	rng.Set("name", "Owner range")
	rng.Set("rangeNumber", 42)
	if err := pb.Save(rng); err != nil {
		t.Fatal(err)
	}
	owner.Set("ranges+", rng.Id)
	if err := pb.Save(owner); err != nil {
		t.Fatal(err)
	}
	m.RangeScoped = true
	processRecord.Set("manifest", m)
	if err := pb.Save(processRecord); err != nil {
		t.Fatal(err)
	}
	call(adminToken, "PUT", base+"/process-plugin/access", []byte(`{"allUsers":true}`), 200)
	call(ownerToken, "GET", base+"/process-plugin/rpc/status", nil, 400)
	call(ownerToken, "GET", base+"/process-plugin/rpc/status?rangeID=OWNER-RANGE", nil, 200)
	call(peerToken, "GET", base+"/process-plugin/rpc/status?rangeID=OWNER-RANGE", nil, 403)
	owner.Set("ranges", []string{})
	if err := pb.Save(owner); err != nil {
		t.Fatal(err)
	}
	call(ownerToken, "GET", base+"/process-plugin/rpc/status?rangeID=OWNER-RANGE", nil, 403)
}
