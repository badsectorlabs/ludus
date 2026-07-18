package ludusapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// splitCollectionFQCN parses a collection FQCN "namespace.name" into its two
// parts by splitting on the FIRST ".". A valid collection name is exactly two
// dot-free segments (ansible's own rule), so both parts must be non-empty AND
// the name part must not contain a further "." — that rejects malformed input
// like "a.b.c" so a bad identifier can never resolve to an unexpected directory.
func splitCollectionFQCN(fqcn string) (namespace, name string, err error) {
	namespace, name, found := strings.Cut(strings.TrimSpace(fqcn), ".")
	if !found || namespace == "" || name == "" || strings.Contains(name, ".") {
		return "", "", fmt.Errorf("collection %q must be a fully-qualified name in the form namespace.name", fqcn)
	}
	return namespace, name, nil
}

// removeCollectionDir os.RemoveAll's <base>/ansible_collections/<namespace>/
// <name>/ for the given FQCN. base is passed in explicitly (not derived from
// the ludusInstallPath const) so the logic is unit testable;
// removeLocalCollectionByName resolves the real base. Idempotent: a missing dir
// is not an error.
func removeCollectionDir(base, fqcn string) error {
	namespace, name, err := splitCollectionFQCN(fqcn)
	if err != nil {
		return err
	}
	dir := filepath.Join(base, "ansible_collections", namespace, name)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing collection directory %s: %w", dir, err)
	}
	return nil
}

// removeLocalCollectionByName removes a vendored collection from disk by FQCN
// (namespace.name). global=true targets the shared global-collections path;
// otherwise the owner's per-user collections path. Empty ownerProxmoxUsername
// for a non-global remove is an error (no path to act on), matching
// addLocalRoleFromDirectory's owner requirement. Idempotent when the dir is
// absent. ansible-galaxy has no `collection remove`, so this rm IS the remove.
// This is the single removal helper the endpoint, the CLI, and the de-select
// prune all route through.
func removeLocalCollectionByName(fqcn, ownerProxmoxUsername string, global bool) error {
	var base string
	if global {
		base = globalCollectionsPath()
	} else {
		if ownerProxmoxUsername == "" {
			return fmt.Errorf("ownerProxmoxUsername is required for non-global removal of collection %q", fqcn)
		}
		base = userCollectionsPath(ownerProxmoxUsername)
	}
	return removeCollectionDir(base, fqcn)
}
