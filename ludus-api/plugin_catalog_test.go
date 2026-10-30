package ludusapi

import (
	"context"
	"encoding/json"
	"errors"
	"ludusapi/dto"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/keygen-sh/keygen-go/v3"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func testCatalogEntry(id, version string, entitlements ...string) pluginCatalogEntry {
	return pluginCatalogEntry{ID: id, Name: "Plugin " + id, Description: "An official plugin", Author: "Bad Sector Labs", Version: version,
		ProductID: "product-" + id, PackageID: "package-" + id, Entitlements: entitlements,
		Level: protocol.LevelStandard, RunAs: protocol.RunAsLudus, HasUI: true, DocsURL: "https://docs.ludus.cloud/" + id}
}

// useTestCatalog makes the server fetch a catalog with these entries and
// forgets any cached copy.
func useTestCatalog(t *testing.T, s *Server, entries ...pluginCatalogEntry) {
	t.Helper()
	data, err := json.Marshal(pluginCatalog{SchemaVersion: pluginCatalogSchemaVersion, Plugins: entries})
	if err != nil {
		t.Fatal(err)
	}
	s.pluginCatalogFetch = func(context.Context) ([]byte, error) { return data, nil }
	s.pluginCatalog = nil
	s.pluginCatalogFailedAt = time.Time{}
	if err := os.Remove(s.pluginCatalogPath()); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

// fakeReleases serves licensed package releases through the injectable
// lookup and download hooks.
type fakeReleases struct {
	latest      map[string]string // plugin ID to latest release version
	routes      map[string][]protocol.Route
	lookups     []licensedPluginSpec
	downloadErr error
}

func installFakeReleases(t *testing.T, s *Server) *fakeReleases {
	releases := &fakeReleases{latest: map[string]string{}, routes: map[string][]protocol.Route{}}
	s.pluginReleaseLookup = func(_ context.Context, spec licensedPluginSpec, current string) (*keygen.Release, error) {
		releases.lookups = append(releases.lookups, spec)
		latest := releases.latest[spec.id]
		if latest == "" || latest == current {
			return nil, nil
		}
		return &keygen.Release{ID: "release-" + spec.id + "-" + latest, Version: latest}, nil
	}
	s.pluginReleaseDownload = func(release *keygen.Release, spec licensedPluginSpec) error {
		if releases.downloadErr != nil {
			return releases.downloadErr
		}
		m := testExecutableManifest(spec.id)
		m.Version = release.Version
		if routes := releases.routes[spec.id]; len(routes) > 0 {
			m.Level, m.Routes = protocol.LevelSystem, routes
		}
		return s.installLicensedPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\nexit 1\n"}), spec, release.Version)
	}
	return releases
}

func (r *fakeReleases) lookedUp() []string {
	var ids []string
	for _, spec := range r.lookups {
		ids = append(ids, spec.id)
	}
	return ids
}

func TestParsePluginCatalog(t *testing.T) {
	catalog, err := parsePluginCatalog([]byte(`{"schemaVersion":1,"future":true,"plugins":[
		{"id":"console-wall-v120","name":"Console Wall","version":"1.2.0","productID":"p","packageID":"k","level":"standard","runAs":"ludus","newField":{"x":1}},
		{"id":"ludus-assistant-ui","name":"Assistant","version":"v2.0.0","productID":"p","packageID":"k2","entitlements":["PRO_PLUGINS"],"level":"system","runAs":"root","minLudusVersion":"2.4.0"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Plugins) != 2 || catalog.entry("console-wall-v120").Entitlements == nil || catalog.entry("missing") != nil {
		t.Fatalf("catalog = %+v", catalog)
	}
	if spec := catalog.entry("ludus-assistant-ui").spec(); spec.productID != "p" || spec.packageUUID != "k2" || !slices.Equal(spec.entitlements, []string{"PRO_PLUGINS"}) {
		t.Fatalf("spec = %+v", spec)
	}
	if empty, err := parsePluginCatalog([]byte(`{"schemaVersion":1}`)); err != nil || empty.Plugins == nil {
		t.Fatalf("empty catalog = %+v, %v", empty, err)
	}

	valid := `"id":"ok-plugin","name":"OK","version":"1.0.0","productID":"p","packageID":"k","level":"standard","runAs":"ludus"`
	for name, data := range map[string]string{
		"not JSON":           `{`,
		"schema version 2":   `{"schemaVersion":2,"plugins":[]}`,
		"missing schema":     `{"plugins":[]}`,
		"invalid id":         `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, "ok-plugin", "../x", 1) + `}]}`,
		"missing name":       `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, `"OK"`, `" "`, 1) + `}]}`,
		"invalid version":    `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, "1.0.0", "latest", 1) + `}]}`,
		"missing package":    `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, `"packageID":"k"`, `"packageID":""`, 1) + `}]}`,
		"invalid level":      `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, "standard", "super", 1) + `}]}`,
		"invalid runAs":      `{"schemaVersion":1,"plugins":[{` + strings.Replace(valid, `"runAs":"ludus"`, `"runAs":"admin"`, 1) + `}]}`,
		"invalid minimum":    `{"schemaVersion":1,"plugins":[{` + valid + `,"minLudusVersion":"soon"}]}`,
		"empty entitlement":  `{"schemaVersion":1,"plugins":[{` + valid + `,"entitlements":[""]}]}`,
		"duplicate entry id": `{"schemaVersion":1,"plugins":[{` + valid + `},{` + valid + `}]}`,
	} {
		if _, err := parsePluginCatalog([]byte(data)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestEntitlementShortfall(t *testing.T) {
	valid := func(codes ...string) protocol.License { return protocol.License{Valid: true, Entitlements: codes} }
	for _, test := range []struct {
		name     string
		required []string
		license  protocol.License
		want     string
	}{
		{"free without license", nil, protocol.License{}, ""},
		{"free with empty list", []string{}, protocol.License{}, ""},
		{"invalid license", []string{"PRO_PLUGINS"}, protocol.License{Entitlements: []string{"PRO_PLUGINS"}}, "license"},
		{"pro license", []string{"PRO_PLUGINS"}, valid("PRO_PLUGINS"), ""},
		{"enterprise satisfies pro", []string{"PRO_PLUGINS"}, valid("ENTERPRISE_PLUGINS"), ""},
		{"pro does not satisfy enterprise", []string{"ENTERPRISE_PLUGINS"}, valid("PRO_PLUGINS"), "entitlement"},
		{"enterprise plugin is not enterprise plugins", []string{"PRO_PLUGINS"}, valid("ENTERPRISE_PLUGIN"), "entitlement"},
		{"any of", []string{"ANTISANDBOX_PLUGIN", "ENTERPRISE_PLUGINS"}, valid("ENTERPRISE_PLUGINS"), ""},
		{"missing", []string{"ANTISANDBOX_PLUGIN"}, valid("ENTERPRISE_PLUGIN"), "entitlement"},
	} {
		if got := entitlementShortfall(test.required, test.license); got != test.want {
			t.Errorf("%s: got %q want %q", test.name, got, test.want)
		}
	}
	if got := requiresEntitlementMessage([]string{"PRO_PLUGINS"}); got != "Requires the PRO_PLUGINS entitlement" {
		t.Fatal(got)
	}
}

func TestLudusVersionAtLeast(t *testing.T) {
	for _, test := range []struct {
		server, minimum string
		want            bool
	}{
		{"2.4.0+abc1234", "2.4.0", true},
		{"v2.4.1", "2.4.0", true},
		{"2.4.0-rc1+abc1234", "2.4.0", true},
		{"2.3.9", "2.4.0", false},
		{"2.3.9", "", true},
		{"development", "2.4.0", true},
	} {
		if got := ludusVersionAtLeast(test.server, test.minimum); got != test.want {
			t.Errorf("ludusVersionAtLeast(%q, %q) = %v", test.server, test.minimum, got)
		}
	}
	if !newerVersion("1.2.0", "1.1.0") || newerVersion("1.1.0", "v1.1.0") || newerVersion("1.2.0", "") {
		t.Fatal("newerVersion")
	}
}

func TestPluginCatalogResponse(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	s.Version = "2.4.0+abc1234"
	free := testCatalogEntry("console-wall-v120", "1.2.0")
	pro := testCatalogEntry("ludus-assistant-ui", "1.0.0", "PRO_PLUGINS")
	enterprisePlugins := testCatalogEntry("ludus-guacamole", "1.0.0", "ENTERPRISE_PLUGINS")
	enterprise := testCatalogEntry(enterprisePluginID, "2.4.0", enterpriseEntitlement)
	future := testCatalogEntry("future-plugin", "1.0.0")
	future.MinLudusVersion = "9.0.0"
	useTestCatalog(t, s, free, pro, enterprisePlugins, enterprise, future)
	installed := testExecutableManifest(free.ID)
	installed.Version = "1.1.0"
	writeInstalledPackage(t, s, installed, "#!/bin/sh\n")
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{"PRO_PLUGINS", enterpriseEntitlement}})

	list := func(token string) (dto.GetPluginCatalogResponse, map[string]dto.GetPluginCatalogResponsePluginsItem) {
		t.Helper()
		body := env.call(token, "GET", APIBasePath+"/plugin-catalog", nil, 200).Body.Bytes()
		var raw struct {
			Plugins []map[string]any `json:"plugins"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatal(err)
		}
		for _, item := range raw.Plugins {
			if _, ok := item["productID"]; ok {
				t.Fatal("the catalog response exposes Keygen IDs")
			}
			if entitlements, ok := item["entitlements"].([]any); !ok || entitlements == nil {
				t.Fatalf("entitlements are not a JSON array: %v", item["entitlements"])
			}
		}
		var response dto.GetPluginCatalogResponse
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		byID := map[string]dto.GetPluginCatalogResponsePluginsItem{}
		for _, item := range response.Plugins {
			if item.UnavailableReason == "autoInstalled" {
				t.Fatalf("%s is unavailable as autoInstalled", item.ID)
			}
			byID[item.ID] = item
		}
		return response, byID
	}

	env.call("", "GET", APIBasePath+"/plugin-catalog", nil, 401)
	response, items := list(env.adminToken)
	if !response.CanInstall || response.Stale || response.FetchedAt.IsZero() || response.FetchedAt.Nanosecond() != 0 || len(response.Plugins) != 5 {
		t.Fatalf("admin catalog = %+v", response)
	}
	if got := items[free.ID]; got.InstalledVersion != "1.1.0" || !got.UpdateAvailable || !got.Entitled || !got.CanInstall || got.UnavailableReason != "" ||
		got.Name != free.Name || got.Level != "standard" || got.RunAs != "ludus" || !got.HasUI || got.DocsURL != free.DocsURL {
		t.Fatalf("free entry = %+v", got)
	}
	if got := items[pro.ID]; got.InstalledVersion != "" || got.UpdateAvailable || !got.Entitled || !got.CanInstall {
		t.Fatalf("pro entry = %+v", got)
	}
	if got := items[enterprisePlugins.ID]; got.Entitled || got.CanInstall || got.UnavailableReason != "entitlement" {
		t.Fatalf("enterprise plugins entry = %+v", got)
	}
	// Enterprise follows the normal rules; autoInstalled is informational.
	if got := items[enterprisePluginID]; !got.AutoInstalled || !got.Entitled || !got.CanInstall || got.UnavailableReason != "" {
		t.Fatalf("enterprise entry = %+v", got)
	}
	if got := items[future.ID]; got.CanInstall || got.UnavailableReason != "ludusVersion" || got.MinLudusVersion != "9.0.0" {
		t.Fatalf("future entry = %+v", got)
	}

	response, items = list(env.peerToken)
	if response.CanInstall || items[free.ID].CanInstall || items[free.ID].UnavailableReason != "permission" ||
		items[enterprisePlugins.ID].UnavailableReason != "entitlement" || !items[pro.ID].Entitled {
		t.Fatalf("user catalog = %+v", response)
	}

	// An Enterprise license grants Pro plugins; an invalid license grants nothing.
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{"ENTERPRISE_PLUGINS"}})
	if _, items = list(env.adminToken); !items[pro.ID].Entitled || !items[enterprisePlugins.ID].CanInstall ||
		!items[enterprisePluginID].AutoInstalled || items[enterprisePluginID].UnavailableReason != "entitlement" {
		t.Fatalf("enterprise plugins license: %+v", items)
	}
	s.applyLicense(protocol.License{Valid: false, Entitlements: []string{"ENTERPRISE_PLUGINS"}})
	if _, items = list(env.adminToken); items[pro.ID].UnavailableReason != "license" || items[pro.ID].Entitled || !items[free.ID].CanInstall {
		t.Fatalf("invalid license: %+v", items)
	}
}

func TestPluginCatalogCacheAndStaleFallback(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	useTestCatalog(t, s, testCatalogEntry("console-wall-v120", "1.0.0"))
	fetches := 0
	fetch := s.pluginCatalogFetch
	s.pluginCatalogFetch = func(ctx context.Context) ([]byte, error) {
		fetches++
		return fetch(ctx)
	}
	for range 2 {
		if snapshot, stale, err := s.loadPluginCatalog(context.Background()); err != nil || stale || snapshot.catalog.entry("console-wall-v120") == nil {
			t.Fatalf("load = %v, %v, %v", snapshot, stale, err)
		}
	}
	if fetches != 1 {
		t.Fatalf("fetched %d times within the cache period", fetches)
	}
	if _, err := os.Stat(s.pluginCatalogPath()); err != nil {
		t.Fatalf("catalog not cached on disk: %v", err)
	}

	// An expired copy is refetched; when that fails, the old copy is stale.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(s.pluginCatalogPath(), old, old); err != nil {
		t.Fatal(err)
	}
	s.pluginCatalog = &pluginCatalogSnapshot{catalog: s.pluginCatalog.catalog, fetchedAt: old}
	s.pluginCatalogFetch = func(context.Context) ([]byte, error) { return nil, errors.New("keygen is down") }
	response := env.call(env.peerToken, "GET", APIBasePath+"/plugin-catalog", nil, 200)
	var listed dto.GetPluginCatalogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil || !listed.Stale || len(listed.Plugins) != 1 || listed.FetchedAt.After(old.Add(time.Second)) {
		t.Fatalf("stale catalog = %s, %v", response.Body.String(), err)
	}

	// Another service instance falls back to the disk copy.
	restarted := &Server{LudusInstallPath: s.LudusInstallPath, pluginCatalogFetch: s.pluginCatalogFetch}
	if snapshot, stale, err := restarted.loadPluginCatalog(context.Background()); err != nil || !stale || snapshot.catalog.entry("console-wall-v120") == nil {
		t.Fatalf("disk fallback = %v, %v, %v", snapshot, stale, err)
	}
	// A corrupt or schema-incompatible remote catalog is a failed fetch too.
	restarted.pluginCatalogFailedAt = time.Time{}
	restarted.pluginCatalogFetch = func(context.Context) ([]byte, error) { return []byte(`{"schemaVersion":2}`), nil }
	if _, stale, err := restarted.loadPluginCatalog(context.Background()); err != nil || !stale {
		t.Fatalf("invalid remote catalog: stale=%v, %v", stale, err)
	}

	// Without any copy the request fails.
	if err := os.Remove(s.pluginCatalogPath()); err != nil {
		t.Fatal(err)
	}
	s.pluginCatalog = nil
	response = env.call(env.peerToken, "GET", APIBasePath+"/plugin-catalog", nil, 502)
	if !strings.Contains(response.Body.String(), `"error":"Unable to fetch the plugin catalog: keygen is down"`) {
		t.Fatalf("502 body = %s", response.Body.String())
	}
}

func TestInstallCatalogPlugin(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	s.Version = "2.4.0"
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	free := testCatalogEntry("console-wall-v120", "1.0.0")
	pro := testCatalogEntry("ludus-assistant-ui", "1.0.0", "PRO_PLUGINS")
	future := testCatalogEntry("future-plugin", "1.0.0")
	future.MinLudusVersion = "3.0.0"
	conflicting := testCatalogEntry("conflicting-plugin", "1.0.0")
	useTestCatalog(t, s, free, pro, future, conflicting)
	releases := installFakeReleases(t, s)
	releases.latest[free.ID] = "1.0.0"
	releases.latest[pro.ID] = "1.0.0"
	releases.latest[conflicting.ID] = "1.0.0"
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{enterpriseEntitlement}})

	install := func(token, id string, want int) map[string]string {
		t.Helper()
		var body map[string]string
		response := env.call(token, "POST", APIBasePath+"/plugin-catalog/"+id+"/install", nil, want)
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	install("", free.ID, 401)
	if body := install(env.peerToken, free.ID, 403); body["error"] == "" {
		t.Fatalf("403 body = %v", body)
	}
	install(env.adminToken, "not-in-catalog", 404)
	if body := install(env.adminToken, pro.ID, 403); body["error"] != "Requires the PRO_PLUGINS entitlement" {
		t.Fatalf("entitlement error = %v", body)
	}
	install(env.adminToken, future.ID, 422)
	if len(releases.lookups) != 0 {
		t.Fatalf("refused installs contacted Keygen: %v", releases.lookedUp())
	}

	if body := install(env.adminToken, free.ID, 200); body["id"] != free.ID || body["version"] != "1.0.0" || body["result"] != "installed" {
		t.Fatalf("install = %v", body)
	}
	if spec := releases.lookups[0]; spec.productID != free.ProductID || spec.packageUUID != free.PackageID || spec.licenseKey("key") != "" {
		t.Fatalf("free release lookup used %+v", spec)
	}
	if approval, approved, err := readPluginApproval(s.licensedPackageDir(free.ID)); err != nil || !approved || approval.Source != protocol.SourceLicensed {
		t.Fatalf("installed package approval = %+v, %v, %v", approval, approved, err)
	}
	if body := install(env.adminToken, free.ID, 200); body["result"] != "unchanged" || body["version"] != "1.0.0" {
		t.Fatalf("reinstall = %v", body)
	}
	releases.latest[free.ID] = "1.1.0"
	if body := install(env.adminToken, free.ID, 200); body["result"] != "updated" || body["version"] != "1.1.0" {
		t.Fatalf("update = %v", body)
	}

	// Entitled paid installs use the license key.
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{"ENTERPRISE_PLUGINS"}})
	if body := install(env.adminToken, pro.ID, 200); body["result"] != "installed" {
		t.Fatalf("pro install = %v", body)
	}
	if spec := releases.lookups[len(releases.lookups)-1]; spec.licenseKey("key") != "key" {
		t.Fatalf("paid release lookup used %+v", spec)
	}

	releases.latest[free.ID] = "1.2.0"
	releases.downloadErr = errors.New("signature mismatch")
	install(env.adminToken, free.ID, 502)
	releases.downloadErr = nil

	other := testExecutableManifest("other-plugin")
	other.Level = protocol.LevelSystem
	other.Routes = []protocol.Route{{Name: "status", Method: "GET", Pattern: "/shared/status", Scope: protocol.ScopeAPI}}
	writeInstalledPackage(t, s, other, "#!/bin/sh\n")
	releases.routes[conflicting.ID] = other.Routes
	if body := install(env.adminToken, conflicting.ID, 422); !strings.Contains(body["error"], "other-plugin") {
		t.Fatalf("route conflict = %v", body)
	}

	s.pluginCatalog = nil
	if err := os.Remove(s.pluginCatalogPath()); err != nil {
		t.Fatal(err)
	}
	s.pluginCatalogFetch = func(context.Context) ([]byte, error) { return nil, errors.New("offline") }
	install(env.adminToken, free.ID, 502)
}

func TestCatalogRequestsGoToTheAdminService(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	setPluginServiceRunAs(t, protocol.RunAsLudus)
	free := testCatalogEntry("console-wall-v120", "1.0.0")
	if err := s.installLicensedPackage(testPackage(t, testExecutableManifest(free.ID), map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}), free.spec(), "1"); err != nil {
		t.Fatal(err)
	}
	writeInstalledPackage(t, s, testExecutableManifest("server-plugin"), "#!/bin/sh\n")
	env.resources.mirrorInstalled(s.installedPackages(), nil)

	decide := func(auth *core.Record, method, path string) bool {
		e := &core.RequestEvent{Auth: auth}
		e.Request = httptest.NewRequest(method, APIBasePath+path, nil)
		return s.adminPluginRequest(e)
	}
	for _, test := range []struct {
		name, method, path string
		want               bool
	}{
		{"catalog install", "POST", "/plugin-catalog/" + free.ID + "/install", true},
		{"catalog listing", "GET", "/plugin-catalog", false},
		{"licensed uninstall", "DELETE", "/plugins/" + free.ID + "?uninstall=true", true},
		{"licensed removal from view", "DELETE", "/plugins/" + free.ID, false},
		{"server package uninstall", "DELETE", "/plugins/server-plugin?uninstall=true", false},
	} {
		if got := decide(env.admin, test.method, test.path); got != test.want {
			t.Errorf("%s: proxied=%v want %v", test.name, got, test.want)
		}
	}
}

func TestUninstallLicensedPlugin(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	free := testCatalogEntry("console-wall-v120", "1.0.0")
	useTestCatalog(t, s, free)
	for _, spec := range []licensedPluginSpec{free.spec(), enterprisePluginSpec} {
		m := testExecutableManifest(spec.id)
		if err := s.installLicensedPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}), spec, m.Version); err != nil {
			t.Fatal(err)
		}
	}
	env.resources.mirrorInstalled(s.installedPackages(), nil)

	var listed struct {
		Plugins []map[string]any `json:"plugins"`
	}
	if err := json.Unmarshal(env.call(env.adminToken, "GET", APIBasePath+"/plugins", nil, 200).Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	for _, plugin := range listed.Plugins {
		if want := plugin["id"] == free.ID; plugin["canUninstall"] != want {
			t.Errorf("%s: canUninstall = %v, want %v", plugin["id"], plugin["canUninstall"], want)
		}
	}

	uninstall := APIBasePath + "/plugins/" + free.ID + "?uninstall=true"
	env.call(env.peerToken, "DELETE", uninstall, nil, 404)
	env.call(env.adminToken, "DELETE", uninstall, nil, 500) // the regular service proxies it
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	env.call(env.adminToken, "DELETE", APIBasePath+"/plugins/"+enterprisePluginID+"?uninstall=true", nil, 409)
	env.call(env.adminToken, "DELETE", uninstall, nil, 200)
	if _, err := os.Stat(s.licensedPackageDir(free.ID)); !os.IsNotExist(err) {
		t.Fatalf("package left behind: %v", err)
	}
	if _, err := env.pb.FindFirstRecordByData("plugin_resources", "pluginID", free.ID); err == nil {
		t.Fatal("record left behind")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(s.pluginsRoot(), ".deleting-*")); len(leftovers) != 0 {
		t.Fatalf("staging left behind: %v", leftovers)
	}
	if _, err := os.Stat(s.licensedPackageDir(enterprisePluginID)); err != nil {
		t.Fatalf("Enterprise was removed: %v", err)
	}
}

func TestUnentitledLicensedPackagesDoNotRun(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	pro := testCatalogEntry("ludus-assistant-ui", "1.0.0", "PRO_PLUGINS")
	free := testCatalogEntry("console-wall-v120", "1.0.0")

	if block := s.licensedPackageBlock(pro.ID); !strings.Contains(block, "catalog is unavailable") {
		t.Fatalf("block without a catalog = %q", block)
	}
	if block := s.licensedPackageBlock(enterprisePluginID); block != "" {
		t.Fatalf("Enterprise blocked: %q", block)
	}
	useTestCatalog(t, s, pro, free)
	if _, _, err := s.loadPluginCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if block := s.licensedPackageBlock("unknown-plugin"); block == "" {
		t.Fatal("a package outside the catalog is not blocked")
	}
	if block := s.licensedPackageBlock(free.ID); block != "" {
		t.Fatalf("free package blocked: %q", block)
	}
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{enterpriseEntitlement}})
	if block := s.licensedPackageBlock(pro.ID); block != "Requires the PRO_PLUGINS entitlement" {
		t.Fatalf("pro block = %q", block)
	}

	manifest := testExecutableManifest(pro.ID)
	if err := s.installLicensedPackage(testPackage(t, manifest, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}), pro.spec(), manifest.Version); err != nil {
		t.Fatal(err)
	}
	packages := s.installedPackages()
	env.resources.mirrorInstalled(packages, nil)
	state := func() (string, string) {
		t.Helper()
		var plugin map[string]any
		if err := json.Unmarshal(env.call(env.adminToken, "POST", APIBasePath+"/plugins/"+pro.ID+"/add", nil, 200).Body.Bytes(), &plugin); err != nil {
			t.Fatal(err)
		}
		return plugin["state"].(string), plugin["error"].(string)
	}
	if got, message := state(); got != "unavailable" || message != "Requires the PRO_PLUGINS entitlement" {
		t.Fatalf("state = %s, %q", got, message)
	}

	// A running plugin stops when its entitlement goes away and is not
	// started again until it returns.
	fake := &resourceFakeRPC{}
	running := &managedPlugin{rpc: fake, manifest: manifest, source: protocol.SourceLicensed, initialized: true}
	running.host = newHostService(s, running)
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{"ENTERPRISE_PLUGINS"}})
	if err := s.registerPlugin(running, nil); err != nil {
		t.Fatal(err)
	}
	s.reconcileLicensedPackages(packages)
	if s.pluginByKey(pluginKey(protocol.SourceLicensed, pro.ID, "")) != running {
		t.Fatal("an entitled licensed plugin was stopped")
	}
	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{enterpriseEntitlement}})
	s.reconcileLicensedPackages(packages)
	if s.pluginByKey(pluginKey(protocol.SourceLicensed, pro.ID, "")) != nil {
		t.Fatal("an unentitled licensed plugin kept running")
	}
	s.StartPlugins()
	if s.pluginByKey(pluginKey(protocol.SourceLicensed, pro.ID, "")) != nil {
		t.Fatal("an unentitled licensed plugin was started")
	}
}

// TestAutomaticPathsNeverUpdateInstalledPlugins runs everything Ludus does on
// its own (the license flow, Enterprise's license hand-off, and the minute
// jobs of both services) while the catalog and Keygen offer newer versions:
// nothing is updated, and the catalog shows the updates instead.
func TestAutomaticPathsNeverUpdateInstalledPlugins(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	s.Version = "2.4.0"
	pro := testCatalogEntry("ludus-assistant-ui", "1.0.0", "PRO_PLUGINS")
	free := testCatalogEntry("console-wall-v120", "1.0.0")
	enterprise := testCatalogEntry(enterprisePluginID, "1.0.0", enterpriseEntitlement)
	entries := []pluginCatalogEntry{pro, free, enterprise}
	for _, entry := range entries {
		m := testExecutableManifest(entry.ID)
		m.Version = "1.0.0"
		if err := s.installLicensedPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}), entry.spec(), m.Version); err != nil {
			t.Fatal(err)
		}
	}
	releases := installFakeReleases(t, s)
	for i := range entries {
		entries[i].Version = "2.0.0"
		releases.latest[entries[i].ID] = "2.0.0"
	}
	useTestCatalog(t, s, entries...)
	if _, _, err := s.loadPluginCatalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	license := protocol.License{Valid: true, Message: "License active", Entitlements: []string{"ENTERPRISE_PLUGINS", enterpriseEntitlement}}
	s.applyLicense(license)

	if err := s.installMissingEnterprise(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.setLicenseFromPlugin(license)
	packages := s.installedPackages()
	s.refreshPluginDeclarations(packages)
	s.reconcileLicensedPackages(packages)
	setPluginServiceRunAs(t, protocol.RunAsLudus)
	if err := s.loadLicenseState(); err != nil {
		t.Fatal(err)
	}
	s.syncInstalledPackages()

	if len(releases.lookups) != 0 {
		t.Fatalf("automatic paths looked up releases of %v", releases.lookedUp())
	}
	var catalog dto.GetPluginCatalogResponse
	if err := json.Unmarshal(env.call(env.adminToken, "GET", APIBasePath+"/plugin-catalog", nil, 200).Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog.Plugins {
		if manifest, err := readPackageManifest(s.licensedPackageDir(item.ID)); err != nil || manifest.Version != "1.0.0" {
			t.Fatalf("%s was updated: %+v, %v", item.ID, manifest, err)
		}
		if item.InstalledVersion != "1.0.0" || !item.UpdateAvailable || !item.CanInstall || item.UnavailableReason != "" {
			t.Fatalf("catalog entry = %+v", item)
		}
	}
}

// TestLicenseInstallsMissingEnterpriseOnce: the license flow installs
// Enterprise when the license grants it and it is missing, and never again.
func TestLicenseInstallsMissingEnterpriseOnce(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	s.LicenseKey = "key"
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	releases := installFakeReleases(t, s)
	releases.latest[enterprisePluginID] = "2.4.0"
	installedVersion := func() string {
		t.Helper()
		manifest, err := readPackageManifest(s.licensedPackageDir(enterprisePluginID))
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return manifest.Version
	}

	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{"ENTERPRISE_PLUGINS"}})
	if err := s.installMissingEnterprise(context.Background()); err != nil || len(releases.lookups) != 0 || installedVersion() != "" {
		t.Fatalf("installed Enterprise without ENTERPRISE_PLUGIN: %v, %v", err, releases.lookedUp())
	}

	s.applyLicense(protocol.License{Valid: true, Entitlements: []string{enterpriseEntitlement}})
	if err := s.installMissingEnterprise(context.Background()); err != nil || installedVersion() != "2.4.0" {
		t.Fatalf("Enterprise was not installed: %v, %q", err, installedVersion())
	}
	if spec := releases.lookups[0]; len(releases.lookups) != 1 || spec.id != enterprisePluginID || spec.productID != LicenseProductLudus ||
		spec.packageUUID != LicensePackageLudusEnterprisePlugin || spec.licenseKey(s.LicenseKey) != "key" {
		t.Fatalf("Enterprise install used %+v", releases.lookups)
	}

	releases.lookups = nil
	releases.latest[enterprisePluginID] = "2.5.0"
	if err := s.installMissingEnterprise(context.Background()); err != nil || len(releases.lookups) != 0 || installedVersion() != "2.4.0" {
		t.Fatalf("the license flow updated Enterprise: %v, %v, %q", err, releases.lookedUp(), installedVersion())
	}
}

// TestInstallCatalogPluginUpdatesRunningEnterprise: an administrator updates
// Enterprise from the catalog and the new version runs without a Ludus
// restart, starting from the license state the host kept and re-applying it.
func TestInstallCatalogPluginUpdatesRunningEnterprise(t *testing.T) {
	env := newPluginTestEnv(t)
	s := env.server
	s.Version = "2.4.0"
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	setRootPluginOwner(t, uint32(os.Getuid()))
	enterprise := testCatalogEntry(enterprisePluginID, "2.4.0", enterpriseEntitlement)
	enterprise.ProductID, enterprise.PackageID = LicenseProductLudus, LicensePackageLudusEnterprisePlugin
	enterprise.Level, enterprise.RunAs, enterprise.HasUI = protocol.LevelSystem, protocol.RunAsRoot, false
	useTestCatalog(t, s, enterprise)
	releases := installFakeReleases(t, s)
	releases.latest[enterprisePluginID] = "2.4.0"
	// Releases run the test plugin process as Enterprise.
	s.pluginReleaseDownload = func(release *keygen.Release, spec licensedPluginSpec) error {
		m := testExecutableManifest(enterprisePluginID)
		m.Version, m.Level, m.RunAs = release.Version, protocol.LevelSystem, protocol.RunAsRoot
		return s.installLicensedPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}), spec, release.Version)
	}
	entitlements := []string{enterpriseEntitlement, "ANTISANDBOX_PLUGIN"}
	s.applyLicense(protocol.License{Valid: true, Message: "License active", Name: "Example Org", Entitlements: entitlements})
	key := pluginKey(protocol.SourceLicensed, enterprisePluginID, "")
	// waitForHandOff waits until Enterprise has handed the host this license
	// message, which the admin service applies and then writes for the regular
	// service.
	waitForHandOff := func(message string) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			var handed protocol.License
			data, err := os.ReadFile(s.licenseStatePath())
			if err == nil && json.Unmarshal(data, &handed) == nil && handed.Message == message {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("license state = %s, %v; want message %q", data, err, message)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if got := s.licenseSnapshot(); !got.Valid || got.Message != message || got.Name != "Example Org" || !slices.Equal(got.Entitlements, entitlements) {
			t.Fatalf("license = %+v", got)
		}
	}

	// The license flow installs the missing package; the minute job starts it.
	if err := s.installMissingEnterprise(context.Background()); err != nil {
		t.Fatal(err)
	}
	packages := s.installedPackages()
	s.refreshPluginDeclarations(packages)
	s.reconcileLicensedPackages(packages)
	old := s.pluginByKey(key)
	if old == nil || old.metadata.Version != "2.4.0" {
		t.Fatal("Enterprise 2.4.0 is not running")
	}
	waitForHandOff("Enterprise 2.4.0: License active")

	// A newer version is offered, not installed.
	enterprise.Version = "2.5.0"
	useTestCatalog(t, s, enterprise)
	releases.latest[enterprisePluginID] = "2.5.0"
	if err := s.installMissingEnterprise(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.reconcileLicensedPackages(s.installedPackages())
	if s.pluginByKey(key) != old || old.exited() {
		t.Fatal("Enterprise was replaced without an administrator's update")
	}
	var catalog dto.GetPluginCatalogResponse
	if err := json.Unmarshal(env.call(env.adminToken, "GET", APIBasePath+"/plugin-catalog", nil, 200).Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if item := catalog.Plugins[0]; item.InstalledVersion != "2.4.0" || !item.UpdateAvailable || !item.AutoInstalled || !item.CanInstall || item.UnavailableReason != "" {
		t.Fatalf("Enterprise catalog entry = %+v", item)
	}

	var body map[string]string
	if err := json.Unmarshal(env.call(env.adminToken, "POST", APIBasePath+"/plugin-catalog/"+enterprisePluginID+"/install", nil, 200).Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["id"] != enterprisePluginID || body["version"] != "2.5.0" || body["result"] != "updated" {
		t.Fatalf("update = %v", body)
	}
	running := s.pluginByKey(key)
	if running == nil || running == old || running.metadata.Version != "2.5.0" || running.manifest.Version != "2.5.0" {
		t.Fatal("Enterprise 2.5.0 is not running after the update")
	}
	if !old.exited() {
		t.Fatal("the Enterprise 2.4.0 process is still running")
	}
	waitForHandOff("Enterprise 2.5.0: Enterprise 2.4.0: License active")
}
