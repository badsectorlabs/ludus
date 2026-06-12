package ludusapi

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ludusapi/models"

	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// InstallSelection is what the user picked for a source. Persisted on the
// source record as JSON. nil/missing means "install everything" — preserves
// backward-compatible sync behavior for pre-existing rows.
type InstallSelection struct {
	Blueprints       []string `json:"blueprints"`
	Templates        []string `json:"templates"`
	LocalRoles       []string `json:"localRoles"`
	LocalCollections []string `json:"localCollections"`
}

// SourceCatalog is the picker-facing view of a registered source: what the
// walk found, joined with what is currently installed. State enrichment is
// computed by ComputeSourceCatalog at request time, not stored.
type SourceCatalog struct {
	SourceID   string `json:"sourceID"`
	SourceName string `json:"sourceName"`
	// SourceDescription / SourceType / LastSyncedAt are display metadata for
	// picker headers (CLI TUI, GUI detail page), read off the source record.
	SourceDescription      string                 `json:"description,omitempty"`
	SourceType             string                 `json:"sourceType,omitempty"` // "git" | "upload"
	LastSyncedAt           string                 `json:"lastSyncedAt,omitempty"`
	Blueprints             []CatalogBlueprint     `json:"blueprints"`
	Templates              []CatalogItem          `json:"templates"`
	LocalRoles             []CatalogItem          `json:"localRoles"`
	LocalCollections       []CatalogItem          `json:"localCollections"`
	GalaxyRoles            []CatalogItem          `json:"galaxyRoles"`
	GalaxyCollections      []CatalogItem          `json:"galaxyCollections"`
	SubscriptionRoles      []CatalogItem          `json:"subscriptionRoles"`
	UndeclaredDependencies []UndeclaredDependency `json:"undeclaredDependencies,omitempty"`
}

type CatalogBlueprint struct {
	ID                        string   `json:"id"`
	Name                      string   `json:"name"`
	Description               string   `json:"description,omitempty"`
	Version                   string   `json:"version"`
	State                     string   `json:"state"` // StateNotInstalled | StateInstalled | StateUpgradeAvailable
	InstalledVersion          string   `json:"installedVersion,omitempty"`
	RequiredTemplates         []string `json:"requiredTemplates,omitempty"`
	RequiredLocalRoles        []string `json:"requiredLocalRoles,omitempty"`
	RequiredGalaxyRoles       []string `json:"requiredGalaxyRoles,omitempty"`
	RequiredGalaxyCollections []string `json:"requiredGalaxyCollections,omitempty"`
}

// ScopeInstall is one installed copy of a role or vendored collection: which
// scope it lives in ("global"/"user"), the version on disk there, and its
// state against the required pin ("installed" when it satisfies,
// "upgrade_available" on a mismatch). An artifact can have more than one —
// global and per-user copies can sit at different versions. State is empty at
// the disk-scan layer and filled once the catalog knows the pin.
type ScopeInstall struct {
	Scope   string `json:"scope"`
	Version string `json:"version,omitempty"`
	State   string `json:"state,omitempty"`
}

type CatalogItem struct {
	Name             string `json:"name"`
	Description      string `json:"description,omitempty"`
	Version          string `json:"version,omitempty"`
	State            string `json:"state"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	// Global reports, for an installed role, whether it lives in the
	// system-wide global-roles path (true) or the owner's per-user roles
	// path (false). Drives the role-remove flow: ansible-galaxy must be
	// pointed at the right path or it reports "not installed, skipping".
	// Meaningless (always false) for templates and collections.
	Global bool `json:"global,omitempty"`
	// Scopes lists every installed copy of a role or vendored collection with
	// its scope, on-disk version, and per-scope state vs the pin. An artifact
	// can occupy both global and user at different versions. Drives the
	// per-scope subrows and the per-scope remove submenu. nil for templates,
	// galaxy deps, and not-installed items.
	Scopes []ScopeInstall `json:"scopes,omitempty"`
	// Type is the requirements.yml install type for a collection — "git"
	// when the collection is sourced from a git repo (Name holds the repo
	// URL), empty for a normal galaxy collection. Lets the GUI install via
	// the git path and label the row.
	Type string `json:"type,omitempty"`
	// Fqcn is the resolved namespace.name for a git collection (Name holds
	// the URL in that case). Derived from the repo URL; empty when it can't
	// be inferred or the item isn't a git collection.
	Fqcn       string   `json:"fqcn,omitempty"`
	RequiredBy []string `json:"requiredBy,omitempty"`
	// VersionByBlueprint records per-blueprint version pins for galaxy roles
	// and collections. Two blueprints can pin the same role/collection at
	// different versions; the picker resolves which version to display based
	// on which blueprint(s) the user selected. Top-level Version is a
	// best-effort default (typically the first pin encountered) for callers
	// that don't have a selection context.
	VersionByBlueprint map[string]string `json:"versionByBlueprint,omitempty"`
}

const (
	StateNotInstalled     = "not_installed"
	StateInstalled        = "installed"
	StateUpgradeAvailable = "upgrade_available"
)

// blueprintConfigRefs holds the artifacts referenced by a single blueprint.
type blueprintConfigRefs struct {
	Templates         []string
	LocalRoles        []string
	GalaxyRoles       []string
	GalaxyCollections []string
}

// parseBlueprintConfigRefs extracts all artifact references for one blueprint:
//   - Templates and roles from the range-config file (via InferFromRangeConfig).
//   - Galaxy roles and subscription roles from requirements.yml
//     (via declaredFromRequirements).
//
// Roles that appear in requirements.yml under subscription_roles are
// subscription roles; roles under roles: are galaxy roles. Roles referenced in
// the config but not in requirements.yml and not local are left to the caller
// (findUndeclaredDependencies handles that separately).
func parseBlueprintConfigRefs(bp WalkedBlueprint, localRoleNames map[string]bool) blueprintConfigRefs {
	var refs blueprintConfigRefs

	galaxyRoles, galaxyCollections, subRoles := declaredFromRequirements(bp.RequirementsYAML)
	refs.GalaxyRoles = galaxyRoles
	refs.GalaxyCollections = galaxyCollections

	if bp.ConfigPath != "" {
		if data, err := os.ReadFile(bp.ConfigPath); err == nil {
			templates, roleRefs, _ := InferFromRangeConfig(data)
			refs.Templates = templates

			galaxySet := make(map[string]bool, len(galaxyRoles))
			for _, n := range galaxyRoles {
				galaxySet[n] = true
			}
			subSet := make(map[string]bool, len(subRoles))
			for _, n := range subRoles {
				subSet[n] = true
			}

			for _, role := range roleRefs {
				switch {
				case localRoleNames[role]:
					refs.LocalRoles = appendUnique(refs.LocalRoles, role)
				case subSet[role]:
					// subscription roles referenced in config covered by requirements.yml
				case galaxySet[role]:
					// galaxy roles referenced in config covered by requirements.yml
				}
			}
		}
	}

	return refs
}

func appendUnique(slice []string, s string) []string {
	for _, existing := range slice {
		if existing == s {
			return slice
		}
	}
	return append(slice, s)
}

// templateDescription reads a template's human description from the static
// `description` variable in its packer file. Returns "" when unset.
func templateDescription(dir string) string {
	return packerVarFromDir(dir, "description")
}

// localRoleDescription reads a source-bundled role's meta/main.yml (or
// main.yaml) for its galaxy_info.description. Best-effort — roles without
// standard meta carry no description.
func localRoleDescription(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "meta", "main.yml"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(dir, "meta", "main.yaml"))
		if err != nil {
			return ""
		}
	}
	return roleDescriptionFromMeta(data)
}

// localCollectionDescription reads a source-bundled collection's galaxy.yml
// for its description. Best-effort — a collection whose galaxy.yml is missing
// or has no description carries none. Mirrors localRoleDescription.
func localCollectionDescription(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "galaxy.yml"))
	if err != nil {
		return ""
	}
	gm, err := ParseGalaxyManifest(data)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(gm.Description)
}

// ComputeSourceCatalog joins a walked source with installed-artifact state to
// produce the picker-facing catalog view. State is read from the live system
// (filesystem + Proxmox), not from the source_artifacts ledger — see
// loadInstalledArtifacts for the rationale. No writes.
func ComputeSourceCatalog(e *core.RequestEvent, src *core.Record, walked *WalkedSource) *SourceCatalog {
	c := &SourceCatalog{
		SourceID:          src.GetString("sourceID"),
		SourceName:        src.GetString("name"),
		SourceDescription: src.GetString("description"),
		SourceType:        src.GetString("type"),
		LastSyncedAt:      src.GetString("lastSyncedAt"),
		Blueprints:        []CatalogBlueprint{},
		Templates:         []CatalogItem{},
		LocalRoles:        []CatalogItem{},
		LocalCollections:  []CatalogItem{},
	}
	if walked == nil {
		return c
	}
	app := e.App

	installed := loadInstalledArtifacts(e, walked)

	// Build a set of local role names for parseBlueprintConfigRefs.
	localRoleNames := make(map[string]bool, len(walked.LocalRoles))
	for _, dir := range walked.LocalRoles {
		localRoleNames[filepath.Base(dir)] = true
	}

	// Single pass: compute refs for every blueprint to avoid double disk reads.
	blueprintRefs := make(map[string]blueprintConfigRefs, len(walked.Blueprints))
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		blueprintRefs[bp.Manifest.ID] = parseBlueprintConfigRefs(bp, localRoleNames)
	}

	// Blueprints — state comes from the blueprints table, not source_artifacts.
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		state, installedVer := blueprintState(app, src.Id, bp.Manifest.ID, bp.Manifest.Version)
		refs := blueprintRefs[bp.Manifest.ID]

		c.Blueprints = append(c.Blueprints, CatalogBlueprint{
			ID:                        bp.Manifest.ID,
			Name:                      bp.Manifest.Name,
			Description:               bp.Manifest.Description,
			Version:                   bp.Manifest.Version,
			State:                     state,
			InstalledVersion:          installedVer,
			RequiredTemplates:         nilIfEmpty(refs.Templates),
			RequiredLocalRoles:        nilIfEmpty(refs.LocalRoles),
			RequiredGalaxyRoles:       nilIfEmpty(refs.GalaxyRoles),
			RequiredGalaxyCollections: nilIfEmpty(refs.GalaxyCollections),
		})
	}

	// Templates — one CatalogItem per source-root templates/ dir, named by the
	// template's *-template name so the catalog matches the templates API.
	for _, dir := range walked.Templates {
		name := templateNameForDir(dir)
		state, installedVer := artifactState(installed, "template", name, "")
		c.Templates = append(c.Templates, CatalogItem{
			Name:             name,
			Description:      templateDescription(dir),
			State:            state,
			InstalledVersion: installedVer,
		})
	}

	// Local roles — one CatalogItem per source-root roles/ dir. RequiredBy lists
	// blueprints whose config references this role.
	localRoleRequiredBy := map[string][]string{}
	for bpID, refs := range blueprintRefs {
		for _, name := range refs.LocalRoles {
			localRoleRequiredBy[name] = appendUnique(localRoleRequiredBy[name], bpID)
		}
	}
	for name := range localRoleRequiredBy {
		sort.Strings(localRoleRequiredBy[name])
	}
	for _, dir := range walked.LocalRoles {
		name := filepath.Base(dir)
		state, installedVer := artifactState(installed, "local_role", name, "")
		c.LocalRoles = append(c.LocalRoles, CatalogItem{
			Name:             name,
			Description:      localRoleDescription(dir),
			Version:          localRoleVersion(dir),
			State:            state,
			InstalledVersion: installedVer,
			Global:           installed["local_role/"+name].Global,
			Scopes:           annotateScopeStates(installed["local_role/"+name].Scopes, ""),
			RequiredBy:       nilIfEmpty(localRoleRequiredBy[name]),
		})
	}

	// Local collections — one CatalogItem per source-root ansible/collections/
	// dir. The item's identity is the FQCN (<namespace>.<name>) read from
	// galaxy.yml, NOT the dir basename, so it matches the source_artifacts claim
	// and the local_collection/<FQCN> install-state key. A dir whose galaxy.yml
	// is missing/invalid carries no resolvable identity and is skipped.
	// RequiredBy lists blueprints whose requirements.yml declares this FQCN.
	collectionRequiredBy := map[string][]string{}
	for bpID, refs := range blueprintRefs {
		for _, name := range refs.GalaxyCollections {
			collectionRequiredBy[name] = appendUnique(collectionRequiredBy[name], bpID)
		}
	}
	for name := range collectionRequiredBy {
		sort.Strings(collectionRequiredBy[name])
	}
	for _, dir := range walked.LocalCollections {
		data, err := os.ReadFile(filepath.Join(dir, "galaxy.yml"))
		if err != nil {
			continue
		}
		gm, perr := ParseGalaxyManifest(data)
		if perr != nil || gm.Namespace == "" || gm.Name == "" {
			continue
		}
		fqcn := gm.Namespace + "." + gm.Name
		state, installedVer := artifactState(installed, "local_collection", fqcn, "")
		c.LocalCollections = append(c.LocalCollections, CatalogItem{
			Name:             fqcn,
			Description:      localCollectionDescription(dir),
			Version:          gm.Version,
			State:            state,
			InstalledVersion: installedVer,
			Global:           installed["local_collection/"+fqcn].Global,
			Scopes:           annotateScopeStates(installed["local_collection/"+fqcn].Scopes, ""),
			RequiredBy:       nilIfEmpty(collectionRequiredBy[fqcn]),
		})
	}

	// Galaxy roles, galaxy collections, and subscription roles — unioned
	// across all blueprints' requirements.yml files.
	c.GalaxyRoles = collectImpliedRoles(walked, "galaxy_role", installed)
	c.GalaxyCollections = collectImpliedRoles(walked, "collection", installed)
	annotateGitCollections(walked, c.GalaxyCollections)
	c.SubscriptionRoles = collectImpliedRoles(walked, "subscription_role", installed)

	c.UndeclaredDependencies = findUndeclaredDependencies(walked)

	sortCatalog(c)
	return c
}

// loadInstalledArtifacts returns a map keyed by "<kind>/<name>" → installed
// version by consulting the live filesystem — per-user packer dirs for
// templates, role and collection dirs for roles. We deliberately do NOT read the
// source_artifacts ledger here.
//
// The ledger records which source claimed each artifact (per-source
// provenance), but it drifts from reality the moment
// something is removed out-of-band —
// for example, the /ansible role delete endpoint nukes a role's directory
// but doesn't reach into source_artifacts. Reading the ledger here meant
// the catalog kept reporting "installed" after a manual delete.
//
// Reality is cheaper to be honest about: stat the role dirs, read versions
// from each role's meta/.galaxy_install_info (or MANIFEST.json for
// collections), ask Proxmox what templates exist. We only check names the
// walked source actually declares.
// installedArtifact is what loadInstalledArtifacts found on disk for one
// artifact: its version (where the kind has one) and, for roles, the scope(s)
// it lives in. A role can be present in more than one place at once (e.g.
// global AND the requesting user's per-user path), so Scopes is a list. Global is a
// derived convenience (scopes contains "global") kept for the remove flow.
type installedArtifact struct {
	Version string // primary version (first scope found); aggregate-state basis
	Global  bool
	Scopes  []ScopeInstall // per-scope {scope, version}; State filled later from the pin
}

func loadInstalledArtifacts(e *core.RequestEvent, walked *WalkedSource) map[string]installedArtifact {
	out := map[string]installedArtifact{}
	if walked == nil {
		return out
	}

	// Install-state is relative to the requesting user (the viewer), not the
	// source's registrant. A source's roles, collections, and templates all
	// install into the acting user's per-user home (or the system-wide global
	// path), so the same source reads as installed for whoever is asking. A
	// shared source — e.g. one owned by ROOT — that an admin installs must
	// still read as installed when that admin views its catalog.
	viewerProxmoxUsername := ""
	if u, ok := e.Get("user").(*models.User); ok && u != nil {
		viewerProxmoxUsername = u.ProxmoxUsername()
	}

	// Disk-backed kinds (roles, collections, subscription roles) come from one
	// inventory scan — the same scan GET /ansible lists — joined against the
	// names the walked source declares.
	joinWalkedWithInventory(out, walked, loadAnsibleInventory(viewerProxmoxUsername))

	// A template counts as installed (for this viewer) when its *-template name
	// is present in the viewer's packer — detected by NAME, not folder, so a
	// copy under any folder (legacy basename, CLI upload, etc.) still reads as
	// installed and the picker won't offer a duplicating re-install. Build
	// status is a separate axis surfaced by the templates API.
	if viewerProxmoxUsername != "" {
		have := userPackerTemplateNames(viewerProxmoxUsername)
		for _, dir := range walked.Templates {
			name := templateNameForDir(dir)
			if have[name] {
				out["template/"+name] = installedArtifact{}
			}
		}
	}

	return out
}

// joinWalkedWithInventory fills `out` with the install state of every
// disk-backed artifact the walked source declares — local + galaxy +
// subscription roles, vendored + required collections — looked up against one
// inventory scan. Templates are NOT covered here; they live on Proxmox.
func joinWalkedWithInventory(out map[string]installedArtifact, walked *WalkedSource, inv ansibleInventory) {
	// Local roles: a vendored role answers to its dir basename (the name a
	// source install copies it under) AND its galaxy identity from meta (the
	// name `ansible-galaxy install <ns>.<name>` lands the same content under)
	// — the dual-identity rule vendored collections already get via their
	// galaxy.yml FQCN. Without the alias, a role installed from galaxy reads
	// not_installed here while the ansible list shows it.
	for _, dir := range walked.LocalRoles {
		name := filepath.Base(dir)
		if art, ok := inv.roleArtifact(false, name, roleGalaxyName(dir, name)); ok {
			out["local_role/"+name] = art
		}
	}

	// Galaxy roles and subscription roles are gathered by name; collections
	// need their full entry so we can tell git from galaxy.
	galaxyNames := map[string]struct{}{}
	subNames := map[string]struct{}{}
	collectionEntries := map[string]RequirementsCollection{}
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		gr, _, sr := declaredFromRequirements(bp.RequirementsYAML)
		for _, n := range gr {
			galaxyNames[n] = struct{}{}
		}
		for _, n := range sr {
			subNames[n] = struct{}{}
		}
		for _, c := range declaredCollectionEntries(bp.RequirementsYAML) {
			if c.Name != "" {
				collectionEntries[c.Name] = c
			}
		}
	}

	// Exact install-name match only for requirements: ansible-galaxy
	// (re)installs a requirement unless a dir with that exact name exists, and
	// range configs resolve roles by dir name — an aliased copy under another
	// name satisfies neither.
	for name := range galaxyNames {
		if art, ok := inv.roleArtifact(false, name); ok {
			out["galaxy_role/"+name] = art
		}
	}

	// Subscription roles only count when installed globally — the licensed
	// pipeline's destination.
	for name := range subNames {
		if art, ok := inv.roleArtifact(true, name); ok {
			out["subscription_role/"+name] = art
		}
	}

	// Collections are identified by FQCN everywhere. For a galaxy collection
	// the requirements `name` IS the FQCN; for a git collection it's the repo
	// URL, so derive the FQCN — keying the result by the requirements name
	// either way so artifactState (which looks up by that name) lines up.
	for reqName, c := range collectionEntries {
		lookup := reqName
		if c.Type == "git" {
			lookup = collectionFQCNFromGitURL(reqName)
			if lookup == "" {
				continue // can't resolve FQCN → can't confirm install on disk
			}
		}
		if art, ok := inv.collectionArtifact(lookup); ok {
			out["collection/"+reqName] = art
		}
	}

	// Local (vendored) collections, keyed by the FQCN from the source dir's
	// galaxy.yml — the catalog item's identity — never the dir basename.
	for _, dir := range walked.LocalCollections {
		data, err := os.ReadFile(filepath.Join(dir, "galaxy.yml"))
		if err != nil {
			continue
		}
		gm, perr := ParseGalaxyManifest(data)
		if perr != nil || gm.Namespace == "" || gm.Name == "" {
			continue
		}
		fqcn := gm.Namespace + "." + gm.Name
		if art, ok := inv.collectionArtifact(fqcn); ok {
			out["local_collection/"+fqcn] = art
		}
	}
}

// candidateAnsibleHomes lists ansible-home roots where collections may
// live. We check the requesting user's home first, then the subscription
// resource home that the licensed pipeline writes to.
func candidateAnsibleHomes(proxmoxUsername string) []string {
	homes := []string{}
	if h := userAnsibleHome(proxmoxUsername); h != "" {
		homes = append(homes, h)
	}
	homes = append(homes, filepath.Join(ludusInstallPath, "resources", ".ansible-subscription"))
	return homes
}

// artifactState returns (state, installedVersion) for a single named artifact.
// walkedVersion is empty for templates (no version concept) and deliberately
// empty for local roles and vendored collections — their shipped version is
// surfaced for display but not enforced as a pin.
// For galaxy roles and collections it can be either a concrete version
// ("1.2.0") or a constraint pulled from a blueprint's requirements.yml
// (">=1.2.0", "<2.0.0", etc.) — when the installed version satisfies the
// constraint, treat it as installed, not as upgrade-available.
func artifactState(installed map[string]installedArtifact, kind, name, walkedVersion string) (string, string) {
	info, ok := installed[kind+"/"+name]
	if !ok {
		return StateNotInstalled, ""
	}
	return versionState(info.Version, walkedVersion), info.Version
}

// versionState classifies one installed version against a required pin:
// StateInstalled when it matches/satisfies (or either side is unknown — we
// don't nag on an unreadable install or an absent pin), StateUpgradeAvailable
// on a real mismatch. Shared by the aggregate state and each per-scope state.
func versionState(installedVer, walkedVersion string) string {
	if walkedVersion == "" || installedVer == "" || walkedVersion == installedVer {
		return StateInstalled
	}
	if installedVersionSatisfies(installedVer, walkedVersion) {
		return StateInstalled
	}
	return StateUpgradeAvailable
}

// annotateScopeStates returns a copy of the per-scope installs with each
// State classified against the pin, so the UI can show which scopes are
// stale and which already satisfy the requirement.
func annotateScopeStates(installs []ScopeInstall, pin string) []ScopeInstall {
	if len(installs) == 0 {
		return nil
	}
	out := make([]ScopeInstall, len(installs))
	for i, s := range installs {
		out[i] = ScopeInstall{Scope: s.Scope, Version: s.Version, State: versionState(s.Version, pin)}
	}
	return out
}

// installedVersionSatisfies reports whether `installed` satisfies the
// single-operator `constraint`. The operators supported are the ones
// ansible-galaxy itself recognises in requirements.yml pins: >=, <=, ==,
// !=, >, <. A bare version (no operator) is treated as exact match. When
// either side isn't valid semver we fall back to strict string equality
// — better to surface "upgrade_available" on an unparseable pin than to
// silently call mismatched versions equal.
func installedVersionSatisfies(installed, constraint string) bool {
	installed = strings.TrimSpace(installed)
	constraint = strings.TrimSpace(constraint)
	if installed == "" || constraint == "" {
		return false
	}
	op, want := parseVersionConstraint(constraint)
	if want == "" {
		return false
	}
	iNorm := semverify(installed)
	wNorm := semverify(want)
	if !semver.IsValid(iNorm) || !semver.IsValid(wNorm) {
		return op == "==" && installed == want
	}
	cmp := semver.Compare(iNorm, wNorm)
	switch op {
	case "==":
		return cmp == 0
	case "!=":
		return cmp != 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	}
	return false
}

// parseVersionConstraint splits a single-operator pin into its operator
// and version operand. A bare version is reported as exact match ("==").
// Multiple-clause constraints (e.g. ">=1.0.0,<2.0.0") are intentionally
// not supported here — the catalog falls back to string equality, which
// flags upgrade_available; treat that as a deliberate request to stay
// loud rather than guess at multi-constraint satisfaction.
func parseVersionConstraint(s string) (op, version string) {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if strings.HasPrefix(s, prefix) {
			return prefix, strings.TrimSpace(s[len(prefix):])
		}
	}
	return "==", s
}

// semverify prepends "v" when the input is bare-numeric so it can be
// fed to golang.org/x/mod/semver, which expects the v-prefix form.
func semverify(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// blueprintState returns (state, installedVersion) for a blueprint by querying
// the blueprints table. A blueprint is "installed" if a row with matching
// source+sourceBlueprintID exists.
func blueprintState(app core.App, sourceRecordID, blueprintID, walkedVersion string) (string, string) {
	records, err := app.FindRecordsByFilter("blueprints",
		"source = {:src} && sourceBlueprintID = {:bp}", "", 1, 0,
		map[string]any{"src": sourceRecordID, "bp": blueprintID})
	if err != nil || len(records) == 0 {
		return StateNotInstalled, ""
	}
	installedVer := records[0].GetString("version")
	if walkedVersion == "" || installedVer == "" || walkedVersion == installedVer {
		return StateInstalled, installedVer
	}
	return StateUpgradeAvailable, installedVer
}

// collectImpliedRoles builds the CatalogItem list for galaxy or subscription
// roles, or galaxy collections. Items are unioned across all blueprints'
// requirements.yml; each item's RequiredBy lists the blueprint IDs that declare
// it. State for every kind is computed from on-disk reality by
// loadInstalledArtifacts — collections inclusive (MANIFEST.json check).
func collectImpliedRoles(walked *WalkedSource, kind string, installed map[string]installedArtifact) []CatalogItem {
	type entry struct {
		// versionByBlueprint records the version each blueprint pinned this
		// item at. Multiple blueprints can pin the same role/collection at
		// different versions; we hand the picker the full map so it can pick
		// the right pin based on the user's selection.
		versionByBlueprint map[string]string
		requiredBy         []string
	}
	seen := map[string]*entry{}
	order := []string{}

	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		galaxyRoles, galaxyCollections, subRoles := declaredFromRequirements(bp.RequirementsYAML)

		var names []string
		switch kind {
		case "galaxy_role":
			names = galaxyRoles
		case "collection":
			names = galaxyCollections
		default:
			names = subRoles
		}
		for _, name := range names {
			e, exists := seen[name]
			if !exists {
				e = &entry{versionByBlueprint: map[string]string{}}
				seen[name] = e
				order = append(order, name)
			}
			e.requiredBy = appendUnique(e.requiredBy, bp.Manifest.ID)
		}
	}

	// Populate per-blueprint version pins from requirements.yml. Galaxy
	// roles + collections can carry a version; subscription roles typically
	// don't.
	if kind == "galaxy_role" || kind == "collection" {
		for _, bp := range walked.Blueprints {
			if bp.Manifest == nil {
				continue
			}
			var doc RequirementsDoc
			if len(bp.RequirementsYAML) > 0 {
				_ = unmarshalRequirements(bp.RequirementsYAML, &doc)
			}
			pins := map[string]string{}
			if kind == "galaxy_role" {
				for _, r := range doc.Roles {
					if r.Name != "" && r.Version != "" {
						pins[r.Name] = r.Version
					}
				}
			} else {
				for _, c := range doc.Collections {
					if c.Name != "" && c.Version != "" {
						pins[c.Name] = c.Version
					}
				}
			}
			for name, ver := range pins {
				if e, ok := seen[name]; ok {
					e.versionByBlueprint[bp.Manifest.ID] = ver
				}
			}
		}
	}

	sort.Strings(order)
	items := make([]CatalogItem, 0, len(order))
	for _, name := range order {
		e := seen[name]
		sort.Strings(e.requiredBy)
		// Default Version is the pin from the first (sorted) blueprint that
		// requires this item — best-effort fallback for callers that don't
		// reduce by selection.
		defaultVersion := ""
		for _, bpID := range e.requiredBy {
			if v, ok := e.versionByBlueprint[bpID]; ok && v != "" {
				defaultVersion = v
				break
			}
		}
		state, installedVer := artifactState(installed, kind, name, defaultVersion)
		items = append(items, CatalogItem{
			Name:               name,
			Version:            defaultVersion,
			State:              state,
			InstalledVersion:   installedVer,
			Global:             installed[kind+"/"+name].Global,
			Scopes:             annotateScopeStates(installed[kind+"/"+name].Scopes, defaultVersion),
			RequiredBy:         nilIfEmpty(e.requiredBy),
			VersionByBlueprint: nilIfEmptyMap(e.versionByBlueprint),
		})
	}
	return items
}

// annotateGitCollections marks the collection items that come from a git
// repo (type: git in requirements.yml) and resolves their display FQCN.
// For these, the item's Name is the repo URL; Fqcn carries the friendly
// namespace.name so the GUI can label the row and the install path knows
// to use the git flow. Galaxy collections are left untouched.
func annotateGitCollections(walked *WalkedSource, items []CatalogItem) {
	byName := map[string]RequirementsCollection{}
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		for _, c := range declaredCollectionEntries(bp.RequirementsYAML) {
			if c.Name != "" {
				byName[c.Name] = c
			}
		}
	}
	for i := range items {
		c, ok := byName[items[i].Name]
		if !ok || c.Type != "git" {
			continue
		}
		items[i].Type = "git"
		items[i].Fqcn = collectionFQCNFromGitURL(items[i].Name)

		// Git pins are commit-ish (branch/tag/commit). ansible records the
		// collection's own semver in MANIFEST.json — never the ref — so a
		// branch ("main") or commit pin can't be compared to the installed
		// version and would falsely read as upgrade_available forever. Only
		// keep upgrade detection when the pin is a semver tag (which lines
		// up with the collection's declared version); otherwise an
		// on-disk collection is simply installed.
		if items[i].State == StateUpgradeAvailable && !semver.IsValid(semverify(items[i].Version)) {
			items[i].State = StateInstalled
		}
	}
}

func sortCatalog(c *SourceCatalog) {
	sort.Slice(c.Blueprints, func(i, j int) bool { return c.Blueprints[i].ID < c.Blueprints[j].ID })
	sort.Slice(c.Templates, func(i, j int) bool { return c.Templates[i].Name < c.Templates[j].Name })
	sort.Slice(c.LocalRoles, func(i, j int) bool { return c.LocalRoles[i].Name < c.LocalRoles[j].Name })
	sort.Slice(c.LocalCollections, func(i, j int) bool { return c.LocalCollections[i].Name < c.LocalCollections[j].Name })
	sort.Slice(c.GalaxyRoles, func(i, j int) bool { return c.GalaxyRoles[i].Name < c.GalaxyRoles[j].Name })
	sort.Slice(c.GalaxyCollections, func(i, j int) bool { return c.GalaxyCollections[i].Name < c.GalaxyCollections[j].Name })
	sort.Slice(c.SubscriptionRoles, func(i, j int) bool { return c.SubscriptionRoles[i].Name < c.SubscriptionRoles[j].Name })
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func nilIfEmptyMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}

// unmarshalRequirements is a thin wrapper around yaml.Unmarshal for
// RequirementsDoc, shared by callers that need the full doc (not just names).
func unmarshalRequirements(data []byte, out *RequirementsDoc) error {
	return yaml.Unmarshal(data, out)
}
