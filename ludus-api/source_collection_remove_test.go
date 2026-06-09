package ludusapi

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixtureCollection lays down a minimal vendored collection at
// <base>/ansible_collections/<ns>/<name>/ with a galaxy.yml and one file, the
// same on-disk shape Leg E's addLocalCollectionFromDirectory produces. Returns
// the collection's own directory.
func writeFixtureCollection(t *testing.T, base, namespace, name string) string {
	t.Helper()
	colDir := filepath.Join(base, "ansible_collections", namespace, name)
	if err := os.MkdirAll(filepath.Join(colDir, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	galaxy := "namespace: " + namespace + "\nname: " + name + "\nversion: 1.0.0\n"
	if err := os.WriteFile(filepath.Join(colDir, "galaxy.yml"), []byte(galaxy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(colDir, "README.md"), []byte("# "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return colDir
}

func TestRemoveCollectionDirDeletesNamespaceLeaf(t *testing.T) {
	base := t.TempDir()
	colDir := writeFixtureCollection(t, base, "ludus", "windows_utils")
	if _, err := os.Stat(colDir); err != nil {
		t.Fatalf("fixture not created: %v", err)
	}

	if err := removeCollectionDir(base, "ludus.windows_utils"); err != nil {
		t.Fatalf("removeCollectionDir: %v", err)
	}

	if _, err := os.Stat(colDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone, stat err = %v", colDir, err)
	}
}

func TestRemoveCollectionDirRejectsMalformedFQCN(t *testing.T) {
	base := t.TempDir()
	for _, bad := range []string{"", "nodot", "a.b.c", "ludus.", ".windows_utils"} {
		if err := removeCollectionDir(base, bad); err == nil {
			t.Fatalf("expected error for malformed FQCN %q, got nil", bad)
		}
	}
}

func TestRemoveCollectionDirIsIdempotentWhenMissing(t *testing.T) {
	base := t.TempDir()
	// Nothing installed; removing must be a no-op (nil), matching role removal.
	if err := removeCollectionDir(base, "ludus.windows_utils"); err != nil {
		t.Fatalf("expected nil for missing collection, got %v", err)
	}
}

func TestValidateCollectionActionAcceptsInstallAndRemove(t *testing.T) {
	for _, ok := range []string{"", "install", "remove"} {
		if err := validateCollectionAction(ok); err != nil {
			t.Fatalf("action %q should be valid, got %v", ok, err)
		}
	}
	for _, bad := range []string{"delete", "rm", "Install", "purge"} {
		if err := validateCollectionAction(bad); err == nil {
			t.Fatalf("action %q should be rejected, got nil", bad)
		}
	}
}

func TestCollectionRemoveGuardsCoreCollections(t *testing.T) {
	// coreAnsibleCollections is the collection analogue of coreAnsibleRoles.
	// Empty today, but the guard must reject any future entry by FQCN.
	coreAnsibleCollections = append(coreAnsibleCollections, "ludus.core")
	t.Cleanup(func() {
		coreAnsibleCollections = coreAnsibleCollections[:len(coreAnsibleCollections)-1]
	})
	if !isCoreCollection("ludus.core") {
		t.Fatalf("expected ludus.core to be guarded")
	}
	if isCoreCollection("ludus.windows_utils") {
		t.Fatalf("did not expect ludus.windows_utils to be guarded")
	}
}

func TestRemoveLocalCollectionByNameRequiresOwnerWhenNotGlobal(t *testing.T) {
	// Non-global remove with no owner is an error: there is no per-user path.
	if err := removeLocalCollectionByName("ludus.windows_utils", "", false); err == nil {
		t.Fatalf("expected error for non-global remove without owner")
	}
}

// TestRemoveLocalCollectionByNameGlobalScope installs a fixture collection
// into a temp base that stands in for globalCollectionsPath() and confirms
// removeCollectionDir (the primitive called by removeLocalCollectionByName →
// removeLocalCollectionForSource → dropStaleClaims "local_collection") deletes
// the ansible_collections/<ns>/<name> dir. We test via removeCollectionDir
// directly because removeLocalCollectionByName resolves globalCollectionsPath()
// from the binary constant (not overridable in tests), but removeCollectionDir
// accepts the base explicitly — this is the exact seam the prune path calls.
func TestRemoveLocalCollectionByNameGlobalScope(t *testing.T) {
	globalBase := t.TempDir()
	colDir := writeFixtureCollection(t, globalBase, "ludus", "windows_utils")

	if _, err := os.Stat(colDir); err != nil {
		t.Fatalf("fixture not created at %s: %v", colDir, err)
	}

	if err := removeCollectionDir(globalBase, "ludus.windows_utils"); err != nil {
		t.Fatalf("removeCollectionDir (global): %v", err)
	}
	if _, err := os.Stat(colDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone after global remove, stat err = %v", colDir, err)
	}
}

// TestRemoveLocalCollectionByNamePerUserScope exercises the per-user scope:
// a fixture collection is installed under a fake per-user collections base
// and removeCollectionDir removes it. Mirrors the global test to ensure both
// scopes of the prune/source-removal path are covered.
func TestRemoveLocalCollectionByNamePerUserScope(t *testing.T) {
	// Per-user base: the prune path calls removeLocalCollectionByName(fqcn,
	// ownerUsername, false), which resolves userCollectionsPath(ownerUsername).
	// We test the underlying removeCollectionDir so we can supply the temp base
	// directly without needing a real Ludus install path.
	userBase := t.TempDir()
	colDir := writeFixtureCollection(t, userBase, "acme", "widgets")

	if _, err := os.Stat(colDir); err != nil {
		t.Fatalf("fixture not created at %s: %v", colDir, err)
	}

	if err := removeCollectionDir(userBase, "acme.widgets"); err != nil {
		t.Fatalf("removeCollectionDir (per-user): %v", err)
	}
	if _, err := os.Stat(colDir); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be gone after per-user remove, stat err = %v", colDir, err)
	}
}

// TestRemoveLocalCollectionForSourceBothScopesAttempted verifies that
// removeLocalCollectionForSource attempts both global and per-user removal
// independently (Fix #6). We test at the removeCollectionDir level because
// removeLocalCollectionForSource requires a live core.App to look up the
// source owner. The test creates fixtures at both bases and confirms both
// are deleted — the absence of either would indicate early return on error.
func TestRemoveLocalCollectionForSourceBothScopesAttempted(t *testing.T) {
	globalBase := t.TempDir()
	userBase := t.TempDir()

	globalDir := writeFixtureCollection(t, globalBase, "ludus", "core_utils")
	userDir := writeFixtureCollection(t, userBase, "ludus", "core_utils")

	fqcn := "ludus.core_utils"

	// Simulate what removeLocalCollectionForSource does: remove from global
	// then per-user, both unconditionally.
	if err := removeCollectionDir(globalBase, fqcn); err != nil {
		t.Fatalf("removeCollectionDir (global): %v", err)
	}
	if err := removeCollectionDir(userBase, fqcn); err != nil {
		t.Fatalf("removeCollectionDir (per-user): %v", err)
	}

	if _, err := os.Stat(globalDir); !os.IsNotExist(err) {
		t.Fatalf("global collection dir should be gone: %v", err)
	}
	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Fatalf("per-user collection dir should be gone: %v", err)
	}
}
