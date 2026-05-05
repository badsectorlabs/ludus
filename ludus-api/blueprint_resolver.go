package ludusapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"ludusapi/models"
)

type TemplateStatusEntry struct {
	Name  string `json:"name"`
	Built bool   `json:"built"`
}

type RoleStatusEntry struct {
	Name         string `json:"name"`
	Installed    bool   `json:"installed"`
	Subscription bool   `json:"subscription"`
}

// computeTemplateStatus delegates to getTemplatesStatus (the same Proxmox-
// backed check `templates list` runs) so the answer matches what the user
// sees elsewhere. If Proxmox is unreachable, entries return Built=false
// rather than failing the surrounding request.
func computeTemplateStatus(e *core.RequestEvent, names []string) []TemplateStatusEntry {
	built := map[string]bool{}
	if statuses, err := getTemplatesStatus(e); err == nil {
		for _, s := range statuses {
			built[s.Name] = s.Built
		}
	}
	out := make([]TemplateStatusEntry, 0, len(names))
	for _, n := range names {
		out = append(out, TemplateStatusEntry{Name: n, Built: built[n]})
	}
	return out
}

func computeRoleStatus(app core.App, user *models.User, names []string) []RoleStatusEntry {
	catalog := getSubscriptionCatalogNames(app)
	subSet := make(map[string]struct{}, len(catalog))
	for _, n := range catalog {
		subSet[n] = struct{}{}
	}
	out := make([]RoleStatusEntry, 0, len(names))
	for _, n := range names {
		_, isSub := subSet[n]
		out = append(out, RoleStatusEntry{
			Name:         n,
			Installed:    isRoleInstalledForUser(user, n),
			Subscription: isSub,
		})
	}
	return out
}

// isRoleInstalledForUser is a directory-presence check. A more authoritative
// approach would parse `ansible-galaxy role list` but that requires a
// subprocess and is heavier per-request.
func isRoleInstalledForUser(user *models.User, name string) bool {
	if user == nil {
		return false
	}
	dirs := []string{
		fmt.Sprintf("%s/resources/global-roles/%s", ludusInstallPath, name),
	}
	if username := user.ProxmoxUsername(); username != "" {
		dirs = append(dirs, fmt.Sprintf("%s/users/%s/.ansible/roles/%s", ludusInstallPath, username, name))
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func pickRolesPathForUser(_ core.App, user *models.User, global bool) string {
	if global {
		return globalRolesPath()
	}
	if user == nil {
		return ""
	}
	return userRolesPath(user.ProxmoxUsername())
}

func ansibleHomeForUser(user *models.User, global bool) string {
	if global || user == nil {
		return ""
	}
	return userAnsibleHome(user.ProxmoxUsername())
}

type ResolverOpts struct {
	ForceRoles       bool
	GlobalRoles      bool
	OwnerProxmoxUser string
	AnsibleHome      string
	// SourceRecordID: when set, registered artifacts are tracked in
	// source_artifacts; empty for local blueprints.
	SourceRecordID string
	// PreInferredRoles, when non-nil, bypasses reading and parsing
	// ConfigPath. Useful when the role set is already resolved (e.g. from DB
	// columns) and no on-disk config is available.
	PreInferredRoles []string
}

type ResolverResult struct {
	TemplateResults  []ArtifactResult
	LocalRoleResults []ArtifactResult
	RoleResults      []RoleInstallResult
}

// ResolveAndInstall is the single entry point for "install this blueprint's
// dependencies." Registers bundled templates and local roles, then installs
// galaxy and subscription roles declared in config.yml + requirements.yml.
// Idempotent.
//
// NOTE for sync callers: registerTemplates and registerLocalRoles in
// source_install.go already handle both shared and scoped artifacts for the
// full-source sync path. To avoid double-registration, installUnionedRoles
// calls only installRolesForBlueprint rather than the full ResolveAndInstall.
func ResolveAndInstall(app core.App, walked WalkedBlueprint, opts ResolverOpts) ResolverResult {
	out := ResolverResult{}

	for _, dir := range walked.ScopedTemplates {
		name := filepath.Base(dir)
		if err := addTemplateFromDirectory(app, dir, opts.ForceRoles); err != nil {
			out.TemplateResults = append(out.TemplateResults, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		if opts.SourceRecordID != "" {
			reconcileArtifactProvenance(app, opts.SourceRecordID, "template", name, "")
		}
		out.TemplateResults = append(out.TemplateResults, ArtifactResult{Name: name, OK: true})
	}

	for _, dir := range walked.ScopedLocalRoles {
		name := filepath.Base(dir)
		if err := addLocalRoleFromDirectory(app, dir, opts.GlobalRoles, opts.ForceRoles); err != nil {
			out.LocalRoleResults = append(out.LocalRoleResults, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		if opts.SourceRecordID != "" {
			reconcileArtifactProvenance(app, opts.SourceRecordID, "local_role", name, "")
		}
		out.LocalRoleResults = append(out.LocalRoleResults, ArtifactResult{Name: name, OK: true})
	}

	out.RoleResults = installRolesForBlueprint(app, walked, opts)
	return out
}

// installRolesForBlueprint reads config.yml + requirements.yml from the
// bundle, splits roles into public/subscription, installs each set, returns
// per-role results. PreInferredRoles bypasses the disk read.
func installRolesForBlueprint(app core.App, walked WalkedBlueprint, opts ResolverOpts) []RoleInstallResult {
	var inferredRoles []string

	if opts.PreInferredRoles != nil {
		inferredRoles = opts.PreInferredRoles
	} else {
		var configBytes []byte
		if walked.ConfigPath != "" {
			if data, err := os.ReadFile(walked.ConfigPath); err == nil {
				configBytes = data
			}
		}
		_, inferredRoles, _ = InferFromRangeConfig(configBytes)

		// Strip locally-bundled role names; those are registered via
		// addLocalRoleFromDirectory and must not be requested from galaxy.
		bundledNames := map[string]bool{}
		for _, dir := range walked.ScopedLocalRoles {
			bundledNames[filepath.Base(dir)] = true
		}
		filtered := inferredRoles[:0:0]
		for _, r := range inferredRoles {
			if !bundledNames[r] {
				filtered = append(filtered, r)
			}
		}
		inferredRoles = filtered
	}

	catalog := getSubscriptionCatalogNames(app)
	subRoles, pubRoles := SplitSubscriptionRoles(inferredRoles, catalog)

	var out []RoleInstallResult

	if hasRequirementsRoles(walked.RequirementsYAML) || len(pubRoles) > 0 {
		augmented := augmentRequirementsWithBareNames(walked.RequirementsYAML, pubRoles)
		rolesPath := ""
		if opts.GlobalRoles {
			rolesPath = globalRolesPath()
		} else if opts.OwnerProxmoxUser != "" {
			rolesPath = userRolesPath(opts.OwnerProxmoxUser)
		}
		results, err := InstallRolesFromRequirementsWithHome(augmented, rolesPath, opts.AnsibleHome, opts.ForceRoles)
		if err != nil && len(results) == 0 {
			out = append(out, RoleInstallResult{OK: false, Error: err.Error()})
		}
		// galaxy reports "already installed" as OK regardless of version. A
		// version pin mismatch is a real failure for our purposes — surface it
		// so the user knows their deps are stale.
		results = detectGalaxyVersionMismatches(results, walked.RequirementsYAML, rolesPath)
		for _, r := range results {
			if r.OK && opts.SourceRecordID != "" {
				insertSourceArtifact(app, opts.SourceRecordID, "galaxy_role", r.Name, r.Version)
			}
		}
		out = append(out, results...)
	}

	// Subscription roles via the licensed-pipeline helper. NOT tracked in
	// source_artifacts — they're licensed-pipeline globals shared across all
	// users and sources. Recording them would cause `source rm --purge` to
	// try deleting the global install (incorrect, and would fail on perms).
	for _, name := range subRoles {
		if err := installSubscriptionRoleByName(app, name, opts.AnsibleHome); err != nil {
			out = append(out, RoleInstallResult{Name: name, OK: false, Error: err.Error()})
		} else {
			out = append(out, RoleInstallResult{Name: name, OK: true})
		}
	}

	return out
}

func applyResolverResultToStatus(app core.App, rec *core.Record, res ResolverResult) {
	failures := collectArtifactFailures(res.TemplateResults, res.LocalRoleResults, res.RoleResults)
	markInstallStatusFromFailures(app, rec, failures)
}

// collectArtifactFailures walks per-artifact results and returns one
// "<kind> <name>: <reason>" string per failed entry. Shared between source
// sync (SyncResult) and blueprint install (ResolverResult) since the result
// shapes match.
func collectArtifactFailures(templates, localRoles []ArtifactResult, roles []RoleInstallResult) []string {
	var failures []string
	for _, r := range templates {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("template %s: %s", r.Name, r.Message))
		}
	}
	for _, r := range localRoles {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("local_role %s: %s", r.Name, r.Message))
		}
	}
	for _, r := range roles {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("role %s: %s", r.Name, r.Error))
		}
	}
	return failures
}

func markInstallStatusFromFailures(app core.App, rec *core.Record, failures []string) {
	switch {
	case len(failures) == 0:
		markInstallStatus(app, rec, "ok", "")
	case len(failures) == 1:
		markInstallStatus(app, rec, "error", failures[0])
	default:
		markInstallStatus(app, rec, "partial", strings.Join(failures, "; "))
	}
}

// markInstallStatus persists install state on the blueprint or
// source-blueprint row. Errors are intentionally swallowed: a failed save
// here isn't worth escalating, and the next call overwrites anyway.
func markInstallStatus(app core.App, rec *core.Record, status, errMsg string) {
	if rec == nil {
		return
	}
	rec.Set("lastInstallStatus", status)
	rec.Set("lastInstallError", errMsg)
	rec.Set("lastInstalledAt", time.Now().UTC().Format(time.RFC3339))
	_ = app.Save(rec)
}

func embedArtifactResults(resp map[string]any, templates, localRoles []ArtifactResult, roles []RoleInstallResult) {
	resp["templateResults"] = templates
	resp["localRoleResults"] = localRoles
	resp["roleResults"] = roles
}
