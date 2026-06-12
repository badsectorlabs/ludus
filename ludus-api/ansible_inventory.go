package ludusapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// This file is the single source of truth for "what ansible content is
// installed, and where". GET /ansible (the list behind `ludus ansible list`
// and the GUI's Ansible page) and the source catalog's install-state join
// both read from these scanners, so the two views cannot disagree about
// what's on disk.

// InstalledRole is one role directory under a Ludus-managed roles path. Name
// is the directory basename — the identity ansible resolves at deploy time
// and the name `ansible-galaxy role list` prints.
type InstalledRole struct {
	Name    string
	Version string // meta/.galaxy_install_info version; "" when absent
	Scope   string // "global" | "user"
}

// InstalledCollection is one collection tree under a Ludus-managed
// collections base, identified by its FQCN.
type InstalledCollection struct {
	FQCN    string
	Version string
	Scope   string // "global" | "user"
}

// scanInstalledRoles lists installed roles: scope "user" under userRolesPath,
// scope "global" under globalRolesPath. A directory counts as a role when it
// carries meta/main.yml (or .yaml) — the same rule `ansible-galaxy role list`
// applies — so this scan agrees with the galaxy CLI about what exists.
// Sorted global-first, then by name.
func scanInstalledRoles(userRolesPath, globalRolesPath string) []InstalledRole {
	var out []InstalledRole
	scan := func(base, scope string) {
		if base == "" {
			return
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			return
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			if !roleMetaExists(filepath.Join(base, name)) {
				continue
			}
			out = append(out, InstalledRole{
				Name:    name,
				Version: readGalaxyInstalledVersion(base, name),
				Scope:   scope,
			})
		}
	}
	scan(globalRolesPath, "global")
	scan(userRolesPath, "user")
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func roleMetaExists(roleDir string) bool {
	for _, name := range []string{"main.yml", "main.yaml"} {
		if info, err := os.Stat(filepath.Join(roleDir, "meta", name)); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// scanInstalledCollections lists installed collections: scope "global" under
// globalBase (ansible_collections/ directly beneath it), scope "user" under
// each ansible home's collections/ subtree. Homes are scanned in priority
// order and the first home carrying a given FQCN wins — one "user" copy per
// collection, matching how ANSIBLE_COLLECTIONS_PATH resolution takes the
// first hit. A collection counts as installed when its dir carries a
// MANIFEST.json (galaxy receipt) or a galaxy.yml (vendored copy).
// Sorted global-first, then by FQCN.
func scanInstalledCollections(ansibleHomes []string, globalBase string) []InstalledCollection {
	var out []InstalledCollection
	if globalBase != "" {
		out = append(out, scanCollectionBase(filepath.Join(globalBase, "ansible_collections"), "global", nil)...)
	}
	seen := map[string]bool{}
	for _, home := range ansibleHomes {
		if home == "" {
			continue
		}
		out = append(out, scanCollectionBase(filepath.Join(home, "collections", "ansible_collections"), "user", seen)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].FQCN < out[j].FQCN
	})
	return out
}

// scanCollectionBase walks one ansible_collections tree
// (<base>/<namespace>/<name>). seen, when non-nil, dedupes FQCNs across
// multiple bases sharing a scope.
func scanCollectionBase(base, scope string, seen map[string]bool) []InstalledCollection {
	namespaces, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []InstalledCollection
	for _, nsEntry := range namespaces {
		if !nsEntry.IsDir() {
			continue
		}
		names, err := os.ReadDir(filepath.Join(base, nsEntry.Name()))
		if err != nil {
			continue
		}
		for _, nEntry := range names {
			if !nEntry.IsDir() {
				continue
			}
			fqcn := nsEntry.Name() + "." + nEntry.Name()
			if seen != nil && seen[fqcn] {
				continue
			}
			v, ok := readInstalledCollectionVersion(filepath.Join(base, nsEntry.Name(), nEntry.Name()))
			if !ok {
				continue
			}
			if seen != nil {
				seen[fqcn] = true
			}
			out = append(out, InstalledCollection{FQCN: fqcn, Version: v, Scope: scope})
		}
	}
	return out
}

// ansibleInventory indexes one scan for the catalog join: roles by their
// install (directory) name, collections by FQCN.
type ansibleInventory struct {
	roles       map[string][]InstalledRole
	collections map[string][]InstalledCollection
}

// loadAnsibleInventory scans the viewer's role and collection paths once.
// The subscription resource home is included (the licensed pipeline installs
// collections there) under scope "user", after the user's own home.
func loadAnsibleInventory(proxmoxUsername string) ansibleInventory {
	inv := ansibleInventory{
		roles:       map[string][]InstalledRole{},
		collections: map[string][]InstalledCollection{},
	}
	for _, r := range scanInstalledRoles(userRolesPath(proxmoxUsername), globalRolesPath()) {
		inv.roles[r.Name] = append(inv.roles[r.Name], r)
	}
	for _, c := range scanInstalledCollections(candidateAnsibleHomes(proxmoxUsername), globalCollectionsPath()) {
		inv.collections[c.FQCN] = append(inv.collections[c.FQCN], c)
	}
	return inv
}

// roleArtifact reports the installed copies answering to any of the given
// names — an install name plus optional aliases, e.g. a vendored role's dir
// basename and its galaxy identity. globalOnly restricts the match to
// global-scope copies (subscription roles only count when installed there).
func (inv ansibleInventory) roleArtifact(globalOnly bool, names ...string) (installedArtifact, bool) {
	var copies []InstalledRole
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" {
			continue
		}
		for _, r := range inv.roles[n] {
			if globalOnly && r.Scope != "global" {
				continue
			}
			key := r.Scope + "/" + r.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			copies = append(copies, r)
		}
	}
	if len(copies) == 0 {
		return installedArtifact{}, false
	}
	sort.Slice(copies, func(i, j int) bool {
		if copies[i].Scope != copies[j].Scope {
			return copies[i].Scope < copies[j].Scope
		}
		return copies[i].Name < copies[j].Name
	})
	return artifactFromScopes(scopesOfRoles(copies)), true
}

// collectionArtifact reports the installed copies of one collection FQCN.
func (inv ansibleInventory) collectionArtifact(fqcn string) (installedArtifact, bool) {
	copies := inv.collections[fqcn]
	if len(copies) == 0 {
		return installedArtifact{}, false
	}
	scopes := make([]ScopeInstall, 0, len(copies))
	for _, c := range copies {
		scopes = append(scopes, ScopeInstall{Scope: c.Scope, Version: c.Version})
	}
	return artifactFromScopes(scopes), true
}

func scopesOfRoles(copies []InstalledRole) []ScopeInstall {
	scopes := make([]ScopeInstall, 0, len(copies))
	for _, c := range copies {
		scopes = append(scopes, ScopeInstall{Scope: c.Scope, Version: c.Version})
	}
	return scopes
}

// artifactFromScopes builds the catalog's installedArtifact from per-scope
// copies: primary Version is the first scope's (global-first ordering), and
// Global reports whether any copy is global.
func artifactFromScopes(scopes []ScopeInstall) installedArtifact {
	art := installedArtifact{Version: scopes[0].Version, Scopes: scopes}
	for _, s := range scopes {
		if s.Scope == "global" {
			art.Global = true
		}
	}
	return art
}

// roleGalaxyMeta is the slice of a role's meta/main.yml galaxy_info that
// determines its galaxy identity — the name `ansible-galaxy role install
// <namespace>.<role_name>` writes it to disk under. namespace is the modern
// key; classic roles carry the org in author. role_name defaults to the dir
// basename.
type roleGalaxyMeta struct {
	GalaxyInfo struct {
		RoleName  string `yaml:"role_name"`
		Namespace string `yaml:"namespace"`
		Author    string `yaml:"author"`
	} `yaml:"galaxy_info"`
}

// roleGalaxyName derives the galaxy install name of a role dir from its meta,
// or "" when the meta doesn't yield one — no meta at all, or an author value
// that isn't a galaxy namespace (a human name with spaces).
func roleGalaxyName(roleDir, basename string) string {
	data, err := os.ReadFile(filepath.Join(roleDir, "meta", "main.yml"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(roleDir, "meta", "main.yaml"))
		if err != nil {
			return ""
		}
	}
	var m roleGalaxyMeta
	if yaml.Unmarshal(data, &m) != nil {
		return ""
	}
	ns := strings.TrimSpace(m.GalaxyInfo.Namespace)
	if ns == "" {
		ns = strings.TrimSpace(m.GalaxyInfo.Author)
	}
	name := strings.TrimSpace(m.GalaxyInfo.RoleName)
	if name == "" {
		name = basename
	}
	if !isGalaxyToken(ns) || !isGalaxyToken(name) {
		return ""
	}
	return ns + "." + name
}

// isGalaxyToken accepts strings shaped like a galaxy namespace or role name:
// letters, digits, underscores, hyphens — no spaces or dots, so a human
// author ("Jane Doe") never masquerades as a namespace.
func isGalaxyToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
