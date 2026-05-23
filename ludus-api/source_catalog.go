package ludusapi

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/pocketbase/pocketbase/core"
	"gopkg.in/yaml.v3"
)

// InstallSelection is what the user picked for a source. Persisted on the
// source record as JSON. nil/missing means "install everything" — preserves
// backward-compatible sync behavior for pre-existing rows.
type InstallSelection struct {
	Blueprints []string `json:"blueprints"`
	Templates  []string `json:"templates"`
	LocalRoles []string `json:"localRoles"`
}

// SourceCatalog is the picker-facing view of a registered source: what the
// walk found, joined with what is currently installed. State enrichment is
// computed by ComputeSourceCatalog at request time, not stored.
type SourceCatalog struct {
	SourceID               string                 `json:"sourceID"`
	SourceName             string                 `json:"sourceName"`
	Blueprints             []CatalogBlueprint     `json:"blueprints"`
	Templates              []CatalogItem          `json:"templates"`
	LocalRoles             []CatalogItem          `json:"localRoles"`
	GalaxyRoles            []CatalogItem          `json:"galaxyRoles"`
	GalaxyCollections      []CatalogItem          `json:"galaxyCollections"`
	SubscriptionRoles      []CatalogItem          `json:"subscriptionRoles"`
	UndeclaredDependencies []UndeclaredDependency `json:"undeclaredDependencies,omitempty"`
}

type CatalogBlueprint struct {
	ID                        string   `json:"id"`
	Name                      string   `json:"name"`
	Version                   string   `json:"version"`
	State                     string   `json:"state"` // StateNotInstalled | StateInstalled | StateUpgradeAvailable
	InstalledVersion          string   `json:"installedVersion,omitempty"`
	RequiredTemplates         []string `json:"requiredTemplates,omitempty"`
	RequiredLocalRoles        []string `json:"requiredLocalRoles,omitempty"`
	RequiredGalaxyRoles       []string `json:"requiredGalaxyRoles,omitempty"`
	RequiredGalaxyCollections []string `json:"requiredGalaxyCollections,omitempty"`
}

type CatalogItem struct {
	Name             string   `json:"name"`
	Version          string   `json:"version,omitempty"`
	State            string   `json:"state"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
	ImpliedBy        []string `json:"impliedBy,omitempty"`
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

// ComputeSourceCatalog joins a walked source with installed-artifact state to
// produce the picker-facing catalog view. Pure function — no writes.
func ComputeSourceCatalog(app core.App, src *core.Record, walked *WalkedSource) *SourceCatalog {
	c := &SourceCatalog{
		SourceID:   src.GetString("sourceID"),
		SourceName: src.GetString("name"),
		Blueprints: []CatalogBlueprint{},
		Templates:  []CatalogItem{},
		LocalRoles: []CatalogItem{},
	}
	if walked == nil {
		return c
	}

	installed := loadInstalledArtifacts(app, src.Id)

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
			Version:                   bp.Manifest.Version,
			State:                     state,
			InstalledVersion:          installedVer,
			RequiredTemplates:         nilIfEmpty(refs.Templates),
			RequiredLocalRoles:        nilIfEmpty(refs.LocalRoles),
			RequiredGalaxyRoles:       nilIfEmpty(refs.GalaxyRoles),
			RequiredGalaxyCollections: nilIfEmpty(refs.GalaxyCollections),
		})
	}

	// Templates — one CatalogItem per source-root templates/ dir.
	for _, dir := range walked.Templates {
		name := filepath.Base(dir)
		state, installedVer := artifactState(installed, "template", name, "")
		c.Templates = append(c.Templates, CatalogItem{
			Name:             name,
			State:            state,
			InstalledVersion: installedVer,
		})
	}

	// Local roles — one CatalogItem per source-root roles/ dir. ImpliedBy lists
	// blueprints whose config references this role.
	localRoleImpliedBy := map[string][]string{}
	for bpID, refs := range blueprintRefs {
		for _, name := range refs.LocalRoles {
			localRoleImpliedBy[name] = appendUnique(localRoleImpliedBy[name], bpID)
		}
	}
	for name := range localRoleImpliedBy {
		sort.Strings(localRoleImpliedBy[name])
	}
	for _, dir := range walked.LocalRoles {
		name := filepath.Base(dir)
		state, installedVer := artifactState(installed, "local_role", name, "")
		c.LocalRoles = append(c.LocalRoles, CatalogItem{
			Name:             name,
			State:            state,
			InstalledVersion: installedVer,
			ImpliedBy:        nilIfEmpty(localRoleImpliedBy[name]),
		})
	}

	// Galaxy roles, galaxy collections, and subscription roles — unioned
	// across all blueprints' requirements.yml files.
	c.GalaxyRoles = collectImpliedRoles(walked, "galaxy_role", installed)
	c.GalaxyCollections = collectImpliedRoles(walked, "collection", installed)
	c.SubscriptionRoles = collectImpliedRoles(walked, "subscription_role", installed)

	c.UndeclaredDependencies = findUndeclaredDependencies(walked)

	sortCatalog(c)
	return c
}

// loadInstalledArtifacts returns a map keyed by "<kind>/<name>" → installed
// version for all source_artifacts rows belonging to the given source record.
func loadInstalledArtifacts(app core.App, sourceRecordID string) map[string]string {
	out := map[string]string{}
	records, err := app.FindRecordsByFilter("source_artifacts",
		"source = {:s}", "", 0, 0,
		map[string]any{"s": sourceRecordID})
	if err != nil {
		return out
	}
	for _, r := range records {
		key := r.GetString("kind") + "/" + r.GetString("name")
		out[key] = r.GetString("version")
	}
	return out
}

// artifactState returns (state, installedVersion) for a single named artifact.
// walkedVersion is empty for templates and local roles (they carry no version).
func artifactState(installed map[string]string, kind, name, walkedVersion string) (string, string) {
	key := kind + "/" + name
	installedVer, ok := installed[key]
	if !ok {
		return StateNotInstalled, ""
	}
	if walkedVersion == "" || installedVer == "" || walkedVersion == installedVer {
		return StateInstalled, installedVer
	}
	return StateUpgradeAvailable, installedVer
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
// requirements.yml; each item's ImpliedBy lists the blueprint IDs that declare
// it. Collections aren't tracked in source_artifacts so their state is always
// not_installed — the ImpliedBy chip is the useful signal.
func collectImpliedRoles(walked *WalkedSource, kind string, installed map[string]string) []CatalogItem {
	type entry struct {
		// versionByBlueprint records the version each blueprint pinned this
		// item at. Multiple blueprints can pin the same role/collection at
		// different versions; we hand the picker the full map so it can pick
		// the right pin based on the user's selection.
		versionByBlueprint map[string]string
		impliedBy          []string
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
			e.impliedBy = appendUnique(e.impliedBy, bp.Manifest.ID)
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
		sort.Strings(e.impliedBy)
		// Default Version is the pin from the first (sorted) blueprint that
		// requires this item — best-effort fallback for callers that don't
		// reduce by selection.
		defaultVersion := ""
		for _, bpID := range e.impliedBy {
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
			ImpliedBy:          nilIfEmpty(e.impliedBy),
			VersionByBlueprint: nilIfEmptyMap(e.versionByBlueprint),
		})
	}
	return items
}

func sortCatalog(c *SourceCatalog) {
	sort.Slice(c.Blueprints, func(i, j int) bool { return c.Blueprints[i].ID < c.Blueprints[j].ID })
	sort.Slice(c.Templates, func(i, j int) bool { return c.Templates[i].Name < c.Templates[j].Name })
	sort.Slice(c.LocalRoles, func(i, j int) bool { return c.LocalRoles[i].Name < c.LocalRoles[j].Name })
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

