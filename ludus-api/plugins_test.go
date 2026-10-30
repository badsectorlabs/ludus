package ludusapi

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// writeTestRootPackage writes an approved uploaded root-run package the way
// the admin service does, owned by the test user.
func writeTestRootPackage(t *testing.T, s *Server, resourceID string, manifest protocol.Manifest, executable string) string {
	t.Helper()
	pkg := &pluginPackage{manifest: manifest, files: map[string][]byte{protocol.ExecutableFile: []byte(executable)}}
	pkg.files[protocol.ManifestFile], _ = json.Marshal(manifest)
	dir := s.uploadedRootPackageDir(resourceID)
	if err := writeRootPackage(dir, pkg, pluginApproval{Source: protocol.SourceUploaded, ResourceID: resourceID}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{s.pluginsRoot(), filepath.Dir(dir)} {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRootRunPackagesStartOnlyFromRootOwnedApprovedCopies(t *testing.T) {
	s := &Server{LudusInstallPath: t.TempDir()}
	setRootPluginOwner(t, uint32(os.Getuid()))
	manifest := testExecutableManifest("root-plugin")
	manifest.RunAs = protocol.RunAsRoot
	const resourceID = "abcdefghijklmno"
	dir := writeTestRootPackage(t, s, resourceID, manifest, "#!/bin/sh\nexit 0\n")
	executable := filepath.Join(dir, protocol.ExecutableFile)
	verify := func() error { return s.verifyRootPackage(dir, manifest, protocol.SourceUploaded, resourceID) }
	if err := verify(); err != nil {
		t.Fatalf("approved root-owned copy refused: %v", err)
	}

	refused := func(name, want string, mutate, restore func()) {
		t.Helper()
		mutate()
		err := verify()
		restore()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", name, err, want)
		}
	}
	chmod := func(path string, mode os.FileMode) func() {
		return func() {
			if err := os.Chmod(path, mode); err != nil {
				t.Fatal(err)
			}
		}
	}
	refused("group-writable plugins root", "group- or world-writable", chmod(s.pluginsRoot(), 0775), chmod(s.pluginsRoot(), 0755))
	refused("world-writable package directory", "group- or world-writable", chmod(dir, 0757), chmod(dir, 0755))
	refused("group-writable executable", "group- or world-writable", chmod(executable, 0575), chmod(executable, 0555))
	refused("owner is not root", "not owned by root",
		func() { rootPluginOwnerUID = uint32(os.Getuid()) + 1 }, func() { rootPluginOwnerUID = uint32(os.Getuid()) })
	original, _ := os.ReadFile(executable)
	replace := func(content []byte) func() {
		return func() {
			chmod(executable, 0755)()
			if err := os.WriteFile(executable, content, 0755); err != nil {
				t.Fatal(err)
			}
			chmod(executable, 0555)()
		}
	}
	refused("executable differs from approval", "SHA-256", replace([]byte("#!/bin/sh\necho swapped\n")), replace(original))
	if err := s.verifyRootPackage(dir, manifest, protocol.SourceUploaded, "zzzzzzzzzzzzzzz"); err == nil {
		t.Error("approval for another resource accepted")
	}
	systemManifest := manifest
	systemManifest.Level = protocol.LevelSystem
	if err := s.verifyRootPackage(dir, systemManifest, protocol.SourceUploaded, resourceID); err == nil {
		t.Error("declarations that differ from the approval accepted")
	}
	approval := filepath.Join(dir, pluginApprovalFile)
	saved, _ := os.ReadFile(approval)
	refused("no approval in the root-owned copy", "no administrator approval",
		func() { _ = os.Remove(approval) }, func() { _ = os.WriteFile(approval, saved, 0644) })
	outside := t.TempDir()
	if err := s.verifyRootPackage(outside, manifest, protocol.SourceUploaded, resourceID); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("package outside the plugins root: %v", err)
	}

	// The running service decides: a ludus service never starts root-run plugins.
	setPluginServiceRunAs(t, protocol.RunAsLudus)
	if _, err := s.startPlugin(dir, manifest, protocol.SourceUploaded, resourceID); err == nil || !strings.Contains(err.Error(), "runs as root") {
		t.Errorf("ludus service started a root-run plugin: %v", err)
	}
}

func TestDatabaseApprovalNeverStartsRootRunPlugins(t *testing.T) {
	env := newPluginTestEnv(t)
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	setRootPluginOwner(t, uint32(os.Getuid()))
	manifest := testExecutableManifest("root-plugin")
	manifest.RunAs = protocol.RunAsRoot

	// An approved record without a root-owned copy starts nothing.
	dbOnly := env.upload(env.adminToken, "?allUsers=true", testPackage(t, manifest, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))
	record, _ := env.pb.FindRecordById("plugin_resources", dbOnly)
	record.Set("state", "approved")
	if err := env.pb.Save(record); err != nil {
		t.Fatal(err)
	}
	env.server.reconcileUploadedRootPackages(true)
	if len(env.server.pluginSnapshot()) != 0 {
		t.Fatal("admin service started a plugin approved only in the database")
	}
	if _, err := env.server.startUploadedRootPackage(dbOnly); err == nil {
		t.Fatal("started an uploaded plugin without a root-owned copy")
	}

	// A root-owned copy whose record is gone or no longer approved is withdrawn.
	orphan := writeTestRootPackage(t, env.server, "orphanorphanorp", manifest, "#!/bin/sh\n")
	pending := env.upload(env.ownerToken, "", testPackage(t, manifest, map[string]string{protocol.ExecutableFile: "#!/bin/sh\necho pending\n"}))
	withdrawn := writeTestRootPackage(t, env.server, pending, manifest, "#!/bin/sh\n")
	env.server.reconcileUploadedRootPackages(false)
	for _, dir := range []string{orphan, withdrawn} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("unapproved root-owned copy %s was kept", dir)
		}
	}
}

func TestAPIRouteConflicts(t *testing.T) {
	m := testExecutableManifest("route-plugin")
	m.Level = protocol.LevelSystem
	route := func(method, pattern string) protocol.Manifest {
		routed := m
		routed.Routes = []protocol.Route{{Name: "route", Method: method, Pattern: pattern, Scope: protocol.ScopeAPI}}
		return routed
	}
	other := []pluginAPIRoute{{key: "installed/other-plugin", pluginID: "other-plugin", route: protocol.Route{Name: "status", Method: "GET", Pattern: "/other/status", Scope: protocol.ScopeAPI}}}
	for _, test := range []struct {
		name     string
		manifest protocol.Manifest
		key      string
		conflict bool
	}{
		{"exact core route", route("GET", "/license"), "uploaded/a", true},
		{"core wildcard route", route("DELETE", "/vm/123"), "uploaded/a", true},
		{"core version route", route("GET", "/"), "uploaded/a", true},
		{"core plugin management route", route("GET", "/plugins/x/ui"), "uploaded/a", true},
		{"same path, method without a core route", route("GET", "/vm/123"), "uploaded/a", false},
		{"route of another plugin", route("GET", "/other/status"), "uploaded/a", true},
		{"same route, other method", route("POST", "/other/status"), "uploaded/a", false},
		{"plugin's own route", route("GET", "/other/status"), "installed/other-plugin", false},
		{"plugin-scope route ignores core routes", func() protocol.Manifest {
			scoped := m
			scoped.Routes = []protocol.Route{{Name: "license", Method: "GET", Pattern: "/license", Scope: protocol.ScopePlugin}}
			return scoped
		}(), "uploaded/a", false},
	} {
		if err := apiRouteConflict(test.manifest, test.key, other); (err != nil) != test.conflict {
			t.Errorf("%s: conflict=%v, want %v", test.name, err, test.conflict)
		}
	}
}

func TestLicenseStateHandOff(t *testing.T) {
	install := t.TempDir()
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	admin := &Server{LudusInstallPath: install}
	expiry := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	granted := protocol.License{Valid: true, Message: "License active", Name: "Example Org", Expiry: &expiry, Entitlements: []string{enterpriseEntitlement, "ANTISANDBOX_PLUGIN"}}
	admin.setLicenseFromPlugin(granted)
	info, err := os.Stat(admin.licenseStatePath())
	if err != nil || info.Mode().Perm() != 0644 {
		t.Fatalf("license-state.json: %v, %v", info, err)
	}

	setPluginServiceRunAs(t, protocol.RunAsLudus)
	regular := &Server{LudusInstallPath: install, Entitlements: []string{"STALE"}}
	if err := regular.loadLicenseState(); err != nil {
		t.Fatal(err)
	}
	got := regular.licenseSnapshot()
	if !got.Valid || got.Message != granted.Message || got.Name != granted.Name || got.Expiry == nil || !got.Expiry.Equal(expiry) ||
		!slices.Equal(got.Entitlements, granted.Entitlements) {
		t.Fatalf("regular service license = %+v, want %+v", got, granted)
	}

	// An authoritative empty list revokes cached grants.
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	admin.setLicenseFromPlugin(protocol.License{Valid: true, Message: "License active without add-ons"})
	setPluginServiceRunAs(t, protocol.RunAsLudus)
	if err := regular.loadLicenseState(); err != nil {
		t.Fatal(err)
	}
	if regular.HasEntitlement(enterpriseEntitlement) || regular.EntitlementsSnapshot() == nil || !regular.licenseSnapshot().Valid {
		t.Fatalf("revoked entitlements were kept: %+v", regular.licenseSnapshot())
	}

	// The regular service applies plugin license state but never writes the file.
	regular.setLicenseFromPlugin(protocol.License{Valid: false, Message: "forged"})
	if err := admin.loadLicenseState(); err != nil || admin.licenseSnapshot().Message != "License active without add-ons" {
		t.Fatalf("regular service wrote license-state.json: %+v, %v", admin.licenseSnapshot(), err)
	}
}

func TestPluginCatchAllYieldsToCoreRoutes(t *testing.T) {
	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	router, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	previousServer := server
	server = &Server{LicenseMessage: "community license"}
	t.Cleanup(func() { server = previousServer })
	serveEvent := &core.ServeEvent{App: pb, Router: router}
	RegisterRoutesWithPocketBase(serveEvent, routes)
	RegisterRoutesWithPocketBase(serveEvent, pluginResourceRoutes(nil))
	registerPluginAPIRoutes(serveEvent, server)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatalf("core routes conflict with the plugin catch-all: %v", err)
	}
	for _, test := range []struct {
		method, path string
		status       int
		body         string
	}{
		{"GET", APIBasePath + "/", 200, "community license"},
		{"GET", APIBasePath + "/not-a-route", 404, `"not found"`},
		{"POST", APIBasePath + "/range/not-a-route", 404, `"not found"`},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.body) {
			t.Errorf("%s %s: %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestWithdrawOnlyRemovesUnapprovedRootCopies(t *testing.T) {
	env := newPluginTestEnv(t)
	setRootPluginOwner(t, uint32(os.Getuid()))
	manifest := testExecutableManifest("root-plugin")
	manifest.RunAs = protocol.RunAsRoot
	id := env.upload(env.ownerToken, "", testPackage(t, manifest, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))
	withdraw := APIBasePath + "/plugins/" + id + "/withdraw"
	env.call(env.ownerToken, "POST", withdraw, nil, 409) // the regular service manages no root-owned copies

	setPluginServiceRunAs(t, protocol.RunAsRoot)
	record, _ := env.pb.FindRecordById("plugin_resources", id)
	record.Set("state", "approved")
	if err := env.pb.Save(record); err != nil {
		t.Fatal(err)
	}
	dir := writeTestRootPackage(t, env.server, id, manifest, "#!/bin/sh\n")
	env.call("", "POST", withdraw, nil, 401)
	env.call(env.ownerToken, "POST", withdraw, nil, 409)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("withdraw removed an approved root-owned copy")
	}
	// A replacement upload deletes the superseded record.
	if err := env.pb.Delete(record); err != nil {
		t.Fatal(err)
	}
	env.call(env.ownerToken, "POST", withdraw, nil, 200)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("withdraw kept the root-owned copy of a deleted record")
	}
}

func TestAPIRouteClaimsSpanBothServices(t *testing.T) {
	env := newPluginTestEnv(t)
	route := protocol.Route{Name: "status", Method: "GET", Pattern: "/shared/status", Scope: protocol.ScopeAPI}

	// An approved root-run upload claims the route first.
	upload := testExecutableManifest("root-upload")
	upload.Level, upload.RunAs = protocol.LevelSystem, protocol.RunAsRoot
	upload.Routes = []protocol.Route{route}
	uploadID := env.upload(env.adminToken, "?allUsers=true", testPackage(t, upload, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))
	record, _ := env.pb.FindRecordById("plugin_resources", uploadID)
	record.Set("state", "approved")
	if err := env.pb.Save(record); err != nil {
		t.Fatal(err)
	}
	// A ludus-run server package installed later declares the same route, and
	// a root-run one a different route.
	later := testExecutableManifest("later-plugin")
	later.Level = protocol.LevelSystem
	later.Routes = []protocol.Route{route}
	writeInstalledPackage(t, env.server, later, "#!/bin/sh\nexit 1\n")
	other := testExecutableManifest("other-plugin")
	other.Level, other.RunAs = protocol.LevelSystem, protocol.RunAsRoot
	other.Routes = []protocol.Route{{Name: "other", Method: "GET", Pattern: "/other/status", Scope: protocol.ScopeAPI}}
	writeInstalledPackage(t, env.server, other, "#!/bin/sh\nexit 1\n")

	setPluginServiceRunAs(t, protocol.RunAsLudus)
	env.server.StartPlugins()
	if len(env.server.pluginSnapshot()) != 0 {
		t.Fatal("the regular service started a plugin whose route the admin service's plugin claims")
	}
	flagged, err := env.pb.FindFirstRecordByData("plugin_resources", "pluginID", "later-plugin")
	if err != nil || flagged.GetString("state") != "error" || !strings.Contains(flagged.GetString("error"), "root-upload") {
		t.Fatalf("refused plugin was not flagged: %v %v", flagged, err)
	}
	if accepted, _ := env.pb.FindFirstRecordByData("plugin_resources", "pluginID", "other-plugin"); accepted == nil || accepted.GetString("state") != "approved" {
		t.Fatal("a plugin without conflicts was flagged")
	}
	if record, _ := env.pb.FindRecordById("plugin_resources", uploadID); record.GetString("state") != "approved" {
		t.Fatal("the earlier claimant lost its approval")
	}
	if target, ok := env.server.pluginAPIRouteFor("GET", "/shared/status"); !ok || target.plugin != nil || target.key != pluginKey(protocol.SourceUploaded, "", uploadID) {
		t.Fatalf("shared route does not go to the root-run upload: %+v %v", target, ok)
	}
	if _, ok := env.server.pluginAPIRouteFor("GET", "/other/status"); !ok {
		t.Fatal("root-run package route is not proxied")
	}

	// The admin service resolves the same claims.
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	rejected := env.server.refreshPluginDeclarations(env.server.installedPackages())
	if len(rejected) != 1 || rejected[pluginKey(protocol.SourceServer, "later-plugin", "")] == nil {
		t.Fatalf("admin service rejections = %v", rejected)
	}
	if _, err := env.server.startPlugin(filepath.Join(env.server.pluginsRoot(), "later-plugin"), later, protocol.SourceServer, ""); err == nil {
		t.Fatal("started a refused plugin")
	}

	// The license flow does not install a release claiming an active route.
	licensed := testExecutableManifest("ludus-antisandbox")
	licensed.Level, licensed.RunAs, licensed.Version = protocol.LevelSystem, protocol.RunAsRoot, "1.0.0"
	licensed.Routes = []protocol.Route{{Name: "other", Method: "GET", Pattern: "/other/status", Scope: protocol.ScopeAPI}}
	spec := licensedPluginSpec{id: "ludus-antisandbox", productID: LicenseProductLudus, packageUUID: "package-id", entitlements: []string{"ANTISANDBOX_PLUGIN"}}
	err = env.server.installLicensedPackage(testPackage(t, licensed, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}), spec, "1.0.0")
	var conflict *routeConflictError
	if !errors.As(err, &conflict) || !strings.Contains(err.Error(), "other-plugin") {
		t.Fatalf("license install with a conflicting route: %v", err)
	}
	if _, err := os.Stat(env.server.licensedPackageDir("ludus-antisandbox")); !os.IsNotExist(err) {
		t.Fatal("conflicting release was installed")
	}
}

func TestApprovalLookupFailuresWithdrawNothing(t *testing.T) {
	env := newPluginTestEnv(t)
	setPluginServiceRunAs(t, protocol.RunAsRoot)
	setRootPluginOwner(t, uint32(os.Getuid()))
	manifest := testExecutableManifest("root-plugin")
	manifest.RunAs = protocol.RunAsRoot
	id := env.upload(env.ownerToken, "", testPackage(t, manifest, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"}))
	record, _ := env.pb.FindRecordById("plugin_resources", id)
	record.Set("state", "approved")
	if err := env.pb.Save(record); err != nil {
		t.Fatal(err)
	}
	dir := writeTestRootPackage(t, env.server, id, manifest, "#!/bin/sh\n")

	// Make record lookups fail without the record being gone.
	if _, err := env.pb.DB().NewQuery("ALTER TABLE plugin_resources RENAME TO plugin_resources_unavailable").Execute(); err != nil {
		t.Fatal(err)
	}
	restore := func() {
		if _, err := env.pb.DB().NewQuery("ALTER TABLE plugin_resources_unavailable RENAME TO plugin_resources").Execute(); err != nil {
			t.Fatal(err)
		}
	}
	env.server.reconcileUploadedRootPackages(false)
	env.call(env.ownerToken, "POST", APIBasePath+"/plugins/"+id+"/withdraw", nil, 503)
	restore()
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("a failed approval lookup removed the root-owned copy")
	}
	env.server.reconcileUploadedRootPackages(false)
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("an approved root-owned copy was removed")
	}
}
