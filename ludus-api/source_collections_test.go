package ludusapi

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildKeptLocalCollectionsAbortOnReadError verifies that when ANY walked
// collection dir has an unreadable galaxy.yml, buildKeptLocalCollections
// returns hadError=true. The caller (pruneSourceArtifactClaims) must then
// skip the dropStaleClaims("local_collection", ...) call for this cycle to
// avoid silent data-loss.
func TestBuildKeptLocalCollectionsAbortOnReadError(t *testing.T) {
	// Good collection dir.
	goodDir := t.TempDir()
	writeGalaxyYml(t, goodDir, "acme", "widgets", "1.0.0", "desc")

	// Bad collection dir: galaxy.yml missing (unreadable).
	badDir := t.TempDir()

	walked := &WalkedSource{
		LocalCollections: []string{goodDir, badDir},
	}

	kept, hadError := buildKeptLocalCollections(walked, nil)
	if !hadError {
		t.Fatalf("expected hadError=true when a collection dir has no galaxy.yml, got false")
	}
	// The kept set should still contain the good collection (we built up to the error).
	_ = kept // content of kept is secondary; the important thing is hadError
}

// TestBuildKeptLocalCollectionsAllGood verifies that a fully valid walked set
// returns hadError=false and all FQCNs in the kept set.
func TestBuildKeptLocalCollectionsAllGood(t *testing.T) {
	dir := t.TempDir()
	writeGalaxyYml(t, dir, "acme", "widgets", "1.0.0", "desc")

	walked := &WalkedSource{LocalCollections: []string{dir}}
	kept, hadError := buildKeptLocalCollections(walked, nil)
	if hadError {
		t.Fatalf("expected hadError=false for valid collection dirs, got true")
	}
	if _, ok := kept["acme.widgets"]; !ok {
		t.Fatalf("expected acme.widgets in kept set, got %v", kept)
	}
}

// writeGalaxyYml writes a minimal galaxy.yml for a source collection dir.
func writeGalaxyYml(t *testing.T, dir, namespace, name, version, description string) {
	t.Helper()
	content := "namespace: " + namespace + "\nname: " + name +
		"\nversion: " + version + "\ndescription: " + description + "\n"
	writeRepoFile(t, dir, "galaxy.yml", content)
}

func TestWalkSourceRepoFindsAnsibleRolesAndCollections(t *testing.T) {
	root := t.TempDir()

	// v2 layout: roles under ansible/roles, collections under ansible/collections.
	writeRepoFile(t, root, "ansible/roles/myrole/tasks/main.yml", "- debug: msg=hi\n")
	writeRepoFile(t, root, "ansible/roles/myrole/meta/main.yml",
		"galaxy_info:\n  role_name: myrole\n")
	writeGalaxyYml(t, filepath.Join(root, "ansible/collections/mycoll"),
		"acme", "mycoll", "1.0.0", "an example collection")
	writeRepoFile(t, root, "ansible/collections/mycoll/roles/inner/tasks/main.yml",
		"- debug: msg=inner\n")
	// A blueprint so the "source has nothing" guard never trips on real callers
	// (not exercised here, but keeps the fixture a realistic source).
	writeRepoFile(t, root, "templates/win/win.pkr.hcl", "source \"proxmox-iso\" \"x\" {}\n")

	walked, err := WalkSourceRepo(root)
	if err != nil {
		t.Fatalf("WalkSourceRepo: %v", err)
	}

	if len(walked.LocalRoles) != 1 ||
		filepath.Base(walked.LocalRoles[0]) != "myrole" {
		t.Fatalf("expected one local role 'myrole' under ansible/roles, got %v", walked.LocalRoles)
	}
	wantRole := filepath.Join(root, "ansible", "roles", "myrole")
	if walked.LocalRoles[0] != wantRole {
		t.Fatalf("expected role path %s, got %s", wantRole, walked.LocalRoles[0])
	}

	if len(walked.LocalCollections) != 1 ||
		filepath.Base(walked.LocalCollections[0]) != "mycoll" {
		t.Fatalf("expected one local collection 'mycoll' under ansible/collections, got %v", walked.LocalCollections)
	}
	wantColl := filepath.Join(root, "ansible", "collections", "mycoll")
	if walked.LocalCollections[0] != wantColl {
		t.Fatalf("expected collection path %s, got %s", wantColl, walked.LocalCollections[0])
	}

	// The old top-level roles/ dir must no longer be scanned.
	writeRepoFile(t, root, "roles/legacy/tasks/main.yml", "- debug: msg=old\n")
	walked2, err := WalkSourceRepo(root)
	if err != nil {
		t.Fatalf("WalkSourceRepo (2): %v", err)
	}
	for _, r := range walked2.LocalRoles {
		if filepath.Base(r) == "legacy" {
			t.Fatalf("top-level roles/ must not be walked in v2 layout, but found %s", r)
		}
	}
	_ = os.Stat // keep os imported for later tasks in this file
}

func TestParseGalaxyManifest(t *testing.T) {
	data := []byte("namespace: acme\nname: widgets\nversion: 2.3.1\n" +
		"description: Acme widget automation\nreadme: README.md\n")
	gm, err := ParseGalaxyManifest(data)
	if err != nil {
		t.Fatalf("ParseGalaxyManifest: %v", err)
	}
	if gm.Namespace != "acme" || gm.Name != "widgets" {
		t.Fatalf("expected acme.widgets, got %s.%s", gm.Namespace, gm.Name)
	}
	if gm.Version != "2.3.1" {
		t.Fatalf("expected version 2.3.1, got %q", gm.Version)
	}
	if gm.Description != "Acme widget automation" {
		t.Fatalf("expected description, got %q", gm.Description)
	}

	if _, err := ParseGalaxyManifest([]byte(": not yaml :\n\t- broken")); err == nil {
		t.Fatalf("expected error for malformed galaxy.yml")
	}
}

func TestCollectionFQCNForDir(t *testing.T) {
	dir := t.TempDir()
	writeGalaxyYml(t, dir, "ns", "name", "1.0.0", "desc")
	fqcn, err := collectionFQCNForDir(dir)
	if err != nil {
		t.Fatalf("collectionFQCNForDir: %v", err)
	}
	if fqcn != "ns.name" {
		t.Fatalf("expected ns.name, got %q", fqcn)
	}

	// missing galaxy.yml → error
	if _, err := collectionFQCNForDir(t.TempDir()); err == nil {
		t.Fatalf("expected error for missing galaxy.yml")
	}

	// empty namespace → error
	emptyNsDir := t.TempDir()
	writeGalaxyYml(t, emptyNsDir, "", "name", "1.0.0", "")
	if _, err := collectionFQCNForDir(emptyNsDir); err == nil {
		t.Fatalf("expected error for empty namespace")
	}

	// empty name → error
	emptyNameDir := t.TempDir()
	writeGalaxyYml(t, emptyNameDir, "ns", "", "1.0.0", "")
	if _, err := collectionFQCNForDir(emptyNameDir); err == nil {
		t.Fatalf("expected error for empty name")
	}
}

func TestLocalCollectionDescription(t *testing.T) {
	dir := t.TempDir()
	writeGalaxyYml(t, dir, "acme", "widgets", "1.0.0", "  spaced description  ")
	if got := localCollectionDescription(dir); got != "spaced description" {
		t.Fatalf("expected trimmed description, got %q", got)
	}

	if got := localCollectionDescription(t.TempDir()); got != "" {
		t.Fatalf("expected empty description for missing galaxy.yml, got %q", got)
	}
}

func TestAddLocalCollectionFromDirectoryInstallsToFQCNPath(t *testing.T) {
	// Source collection dir: galaxy.yml + a real collection marker (roles/).
	src := filepath.Join(t.TempDir(), "mycoll")
	writeGalaxyYml(t, src, "acme", "widgets", "1.0.0", "widgets")
	writeRepoFile(t, src, "roles/inner/tasks/main.yml", "- debug: msg=hi\n")
	writeRepoFile(t, src, "plugins/modules/widget.py", "# module\n")

	// base stands in for globalCollectionsPath()/userCollectionsPath() — passed
	// explicitly because ludusInstallPath is a const and can't be redirected.
	base := t.TempDir()
	fqcn, err := addLocalCollectionFromDirectory(nil, src, base, false)
	if err != nil {
		t.Fatalf("addLocalCollectionFromDirectory: %v", err)
	}
	// The canonical identity is the galaxy.yml FQCN, not the dir basename.
	if fqcn != "acme.widgets" {
		t.Fatalf("expected FQCN acme.widgets, got %q", fqcn)
	}

	dest := filepath.Join(base, "ansible_collections", "acme", "widgets")
	for _, rel := range []string{"galaxy.yml", "roles/inner/tasks/main.yml", "plugins/modules/widget.py"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Fatalf("expected %s to exist after install: %v", filepath.Join(dest, rel), err)
		}
	}

	// Idempotent: a second install without force is a no-op that succeeds and
	// preserves a hand-edit (mirrors addLocalRoleFromDirectory's "existed
	// already" branch).
	marker := filepath.Join(dest, "EDITED")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := addLocalCollectionFromDirectory(nil, src, base, false); err != nil {
		t.Fatalf("idempotent re-install: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("non-force re-install must preserve existing dir, but marker is gone: %v", err)
	}

	// Force overwrites: the marker is gone after a forced re-copy.
	if _, err := addLocalCollectionFromDirectory(nil, src, base, true); err != nil {
		t.Fatalf("force re-install: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("force re-install must overwrite the dir, but marker survived")
	}
}

func TestAddLocalCollectionFromDirectoryRejectsMissingFQCN(t *testing.T) {
	src := filepath.Join(t.TempDir(), "broken")
	// galaxy.yml present but no namespace/name → cannot resolve FQCN.
	writeRepoFile(t, src, "galaxy.yml", "version: 1.0.0\n")
	if _, err := addLocalCollectionFromDirectory(nil, src, t.TempDir(), false); err == nil {
		t.Fatalf("expected error when galaxy.yml has no namespace/name")
	}

	// galaxy.yml entirely missing → also an error.
	src2 := filepath.Join(t.TempDir(), "nogalaxy")
	writeRepoFile(t, src2, "roles/x/tasks/main.yml", "- debug: msg=hi\n")
	if _, err := addLocalCollectionFromDirectory(nil, src2, t.TempDir(), false); err == nil {
		t.Fatalf("expected error when galaxy.yml is missing")
	}
}

func TestRegisterLocalCollectionsHonorsSelection(t *testing.T) {
	root := t.TempDir()
	// The on-disk dir basenames deliberately DIFFER from the collection FQCN
	// names so this test pins selection/identity to the galaxy.yml FQCN, not the
	// directory basename.
	writeGalaxyYml(t, filepath.Join(root, "ansible/collections/keep-dir"),
		"acme", "keep", "1.0.0", "kept")
	writeRepoFile(t, root, "ansible/collections/keep-dir/roles/x/tasks/main.yml", "- debug: msg=hi\n")
	writeGalaxyYml(t, filepath.Join(root, "ansible/collections/skip-dir"),
		"acme", "skip", "1.0.0", "skipped")
	writeRepoFile(t, root, "ansible/collections/skip-dir/roles/y/tasks/main.yml", "- debug: msg=hi\n")

	walked, err := WalkSourceRepo(root)
	if err != nil {
		t.Fatalf("WalkSourceRepo: %v", err)
	}
	if len(walked.LocalCollections) != 2 {
		t.Fatalf("expected 2 walked collections, got %d", len(walked.LocalCollections))
	}

	// Only "acme.keep" (FQCN) is selected. selectionLocalCollections + the
	// install must skip "acme.skip" entirely.
	sel := &InstallSelection{LocalCollections: []string{"acme.keep"}}
	if got := selectionLocalCollections(sel); len(got) != 1 || got[0] != "acme.keep" {
		t.Fatalf("selectionLocalCollections = %v, want [acme.keep]", got)
	}

	// Mirror what registerLocalCollections does per item: read galaxy.yml for the
	// FQCN, gate on the FQCN, install. We exercise the install for the selected
	// one and confirm the unselected one is never written.
	base := t.TempDir()
	for _, dir := range walked.LocalCollections {
		data, rerr := os.ReadFile(filepath.Join(dir, "galaxy.yml"))
		if rerr != nil {
			t.Fatalf("read galaxy.yml for %s: %v", dir, rerr)
		}
		gm, perr := ParseGalaxyManifest(data)
		if perr != nil {
			t.Fatalf("parse galaxy.yml for %s: %v", dir, perr)
		}
		fqcn := gm.Namespace + "." + gm.Name
		if !contains(sel.LocalCollections, fqcn) {
			continue
		}
		if _, err := addLocalCollectionFromDirectory(nil, dir, base, false); err != nil {
			t.Fatalf("install %s: %v", fqcn, err)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "ansible_collections", "acme", "keep")); err != nil {
		t.Fatalf("selected collection 'acme.keep' should be installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "ansible_collections", "acme", "skip")); err == nil {
		t.Fatalf("unselected collection 'acme.skip' must not be installed")
	}
}

// contains is a tiny local helper for the test (slices.Contains is also fine,
// but keeping it explicit avoids an import churn note).
func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestFilterRequirementsDropsVendoredNames(t *testing.T) {
	reqs := []byte(`roles:
  - name: geerlingguy.docker
    version: 7.4.4
  - name: ludus_adcs
    src: https://github.com/badsectorlabs/ludus_adcs
    version: v1.2.0
collections:
  - name: community.crypto
    version: 2.16.0
  - name: acme.widgets
    version: 1.0.0
`)
	// This source vendors the role basename "ludus_adcs" and the collection
	// FQCN "acme.widgets".
	vendoredRoles := map[string]bool{"ludus_adcs": true}
	vendoredColls := map[string]bool{"acme.widgets": true}

	out, err := filterRequirements(reqs, vendoredRoles, vendoredColls)
	if err != nil {
		t.Fatalf("filterRequirements: %v", err)
	}

	var doc RequirementsDoc
	if err := unmarshalRequirements(out, &doc); err != nil {
		t.Fatalf("re-parse filtered requirements: %v", err)
	}
	if len(doc.Roles) != 1 || doc.Roles[0].Name != "geerlingguy.docker" {
		t.Fatalf("vendored role should be dropped, kept galaxy role only; got %+v", doc.Roles)
	}
	if len(doc.Collections) != 1 || doc.Collections[0].Name != "community.crypto" {
		t.Fatalf("vendored collection should be dropped, kept galaxy collection only; got %+v", doc.Collections)
	}
}

func TestFilterRequirementsNoVendoredIsPassThrough(t *testing.T) {
	reqs := []byte("roles:\n  - name: geerlingguy.docker\n    version: 7.4.4\n")

	// Empty sets → verbatim bytes (early return).
	out, err := filterRequirements(reqs, nil, nil)
	if err != nil {
		t.Fatalf("filterRequirements: %v", err)
	}
	if string(out) != string(reqs) {
		t.Fatalf("empty vendored sets must return input verbatim;\n got: %q\nwant: %q", out, reqs)
	}

	// Non-empty vendored sets that match nothing in the doc → also verbatim
	// (the no-removal branch preserves original bytes, comments included).
	out2, err := filterRequirements(reqs,
		map[string]bool{"some_other_role": true},
		map[string]bool{"other.collection": true})
	if err != nil {
		t.Fatalf("filterRequirements (no match): %v", err)
	}
	if string(out2) != string(reqs) {
		t.Fatalf("non-matching vendored sets must return input verbatim;\n got: %q\nwant: %q", out2, reqs)
	}
}

// TestLocalCollectionInstalledStateFromDiskGlobal mirrors
// TestLocalCollectionInstalledStateFromDisk but exercises the global
// collections base. The MANIFEST.json lives at
// <globalBase>/ansible_collections/<ns>/<name>/MANIFEST.json —
// NOT under a further "collections/" subdir — so localCollectionDiskState must
// stat it directly rather than routing through readGalaxyInstalledCollectionVersion
// (which wrongly prepends "collections/").
func TestLocalCollectionInstalledStateFromDiskGlobal(t *testing.T) {
	root := t.TempDir()
	writeGalaxyYml(t, filepath.Join(root, "ansible/collections/widgets"),
		"acme", "widgets", "1.0.0", "widget collection")
	writeRepoFile(t, root, "ansible/collections/widgets/roles/x/tasks/main.yml", "- debug: msg=hi\n")
	walked, err := WalkSourceRepo(root)
	if err != nil {
		t.Fatalf("WalkSourceRepo: %v", err)
	}

	globalBase := t.TempDir()

	// Before placing any file: collection should read as not installed.
	if st := localCollectionDiskState(walked, nil, globalBase); st["local_collection/acme.widgets"] {
		t.Fatalf("collection should read as not installed when global dir is empty")
	}

	// Place MANIFEST.json at <globalBase>/ansible_collections/acme/widgets/MANIFEST.json.
	manifest := filepath.Join(globalBase, "ansible_collections", "acme", "widgets", "MANIFEST.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"collection_info":{"version":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// With no per-user homes passed, the global stat alone should mark it installed.
	if st := localCollectionDiskState(walked, nil, globalBase); !st["local_collection/acme.widgets"] {
		t.Fatalf("collection should read as installed when MANIFEST.json exists in global base")
	}
}

func TestLocalCollectionInstalledStateFromDisk(t *testing.T) {
	root := t.TempDir()
	writeGalaxyYml(t, filepath.Join(root, "ansible/collections/widgets"),
		"acme", "widgets", "1.0.0", "widget collection")
	writeRepoFile(t, root, "ansible/collections/widgets/roles/x/tasks/main.yml", "- debug: msg=hi\n")
	walked, err := WalkSourceRepo(root)
	if err != nil {
		t.Fatalf("WalkSourceRepo: %v", err)
	}

	// State is keyed by the FQCN (acme.widgets), not the dir basename (widgets).
	// Not installed: an empty ansible home reports the collection absent.
	emptyHome := t.TempDir()
	if st := localCollectionDiskState(walked, []string{emptyHome}, ""); st["local_collection/acme.widgets"] {
		t.Fatalf("collection should read as not installed in an empty home")
	}

	// Installed: place ansible_collections/acme/widgets/MANIFEST.json under home.
	home := t.TempDir()
	manifest := filepath.Join(home, "collections", "ansible_collections", "acme", "widgets", "MANIFEST.json")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"collection_info":{"version":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := localCollectionDiskState(walked, []string{home}, ""); !st["local_collection/acme.widgets"] {
		t.Fatalf("collection should read as installed when its MANIFEST.json exists")
	}
}
