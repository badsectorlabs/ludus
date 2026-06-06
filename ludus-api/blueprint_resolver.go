package ludusapi

import (
	"fmt"
	"os"
	"strings"
	"time"

	"ludusapi/dto"
	"ludusapi/models"

	"github.com/pocketbase/pocketbase/core"
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

func computeRoleStatus(e *core.RequestEvent, user *models.User, names []string) []RoleStatusEntry {
	catalog := getSubscriptionCatalogNames(e)
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

// ansibleHomeForUser returns the writable ANSIBLE_HOME for a galaxy install.
// ANSIBLE_HOME must stay on a writable /opt/ludus path regardless of install
// scope: global vs per-user only selects the roles install --path (via
// ResolverOpts.GlobalRoles), never the home. Returning "" would leave
// ANSIBLE_HOME unset, so ansible-galaxy's local_tmp falls back to
// $HOME/.ansible/tmp — read-only under the service's ProtectHome=read-only
// sandbox — and the install fails.
func ansibleHomeForUser(user *models.User) string {
	if user == nil {
		return ""
	}
	return userAnsibleHome(user.ProxmoxUsername())
}

type ResolverOpts struct {
	ForceRoles  bool
	GlobalRoles bool
	// ProxmoxUser is the user whose per-user roles dir non-global installs land
	// in — always the requesting user, never the source owner.
	ProxmoxUser string
	AnsibleHome string
	// SourceRecordID: when set, registered artifacts are tracked in
	// source_artifacts; empty for local blueprints.
	SourceRecordID string
}

// installRolesForBlueprint installs every dependency declared in the
// blueprint's requirements.yml: galaxy roles (`roles:`), collections
// (`collections:`), and subscription roles (`subscription_roles:`).
// Returns per-role results.
func installRolesForBlueprint(e *core.RequestEvent, app core.App, walked WalkedBlueprint, opts ResolverOpts) []RoleInstallResult {
	declaredSub := subscriptionRolesFromRequirements(walked.RequirementsYAML)
	catalog := getSubscriptionCatalogNames(e)

	var out []RoleInstallResult

	if len(declaredSub) > 0 {
		switch {
		case server == nil || !server.LicenseValid || server.LicenseKey == "":
			out = append(out, RoleInstallResult{
				OK:    false,
				Error: fmt.Sprintf("blueprint declares Ludus subscription roles, but this instance has no valid license: %v", declaredSub),
			})
			return out
		case len(catalog) == 0:
			out = append(out, RoleInstallResult{
				OK:    false,
				Error: fmt.Sprintf("blueprint declares Ludus subscription roles, but the live subscription catalog is empty (license-server unreachable, missing entitlement, or community license): %v", declaredSub),
			})
			return out
		}
	}

	if hasRequirementsRoles(walked.RequirementsYAML) {
		rolesPath := ""
		if opts.GlobalRoles {
			rolesPath = globalRolesPath()
		} else if opts.ProxmoxUser != "" {
			rolesPath = userRolesPath(opts.ProxmoxUser)
		}
		results, err := InstallRolesFromRequirementsWithHome(walked.RequirementsYAML, rolesPath, opts.AnsibleHome, opts.ForceRoles)
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

	// Collections install via `ansible-galaxy collection install -r`. The same
	// requirements YAML is reused; the role and collection subcommands ignore
	// each other's sections.
	//
	// Collection rows are recorded in source_artifacts for display/provenance.
	// ansible-galaxy has no `collection remove` subcommand, so removing a
	// source or de-selecting a collection only drops the row — the on-disk
	// install is left in place. The row is a claim ("this source declared this
	// collection"), not a lifecycle anchor.
	if hasRequirementsCollections(walked.RequirementsYAML) {
		colResults, err := InstallCollectionsFromRequirementsWithHome(walked.RequirementsYAML, opts.AnsibleHome, opts.ForceRoles)
		if err != nil && len(colResults) == 0 {
			out = append(out, RoleInstallResult{OK: false, Error: err.Error()})
		}
		for _, r := range colResults {
			if r.OK && opts.SourceRecordID != "" && r.Name != "" {
				insertSourceArtifact(app, opts.SourceRecordID, "collection", r.Name, r.Version)
			}
		}
		out = append(out, colResults...)
	}

	// Subscription roles via the licensed-pipeline helper. Tracked in
	// source_artifacts with kind="subscription_role" so this source's claim
	// shows up in provenance listings. Like galaxy roles, subscription roles
	// aren't swept by source-level removal (pruneSourceArtifactClaims covers
	// templates and local roles only); their on-disk install is left in place.
	//
	// We install every declared name. A declared name absent from the live
	// catalog will fail at download time and surface as a per-role error;
	// we don't pre-filter here because the catalog can lag (e.g. fetch in
	// progress) and we'd rather attempt and fail loudly than silently skip.
	for _, name := range declaredSub {
		if err := installSubscriptionRoleByName(e, name, opts.AnsibleHome); err != nil {
			out = append(out, RoleInstallResult{Name: name, OK: false, Error: err.Error()})
		} else {
			out = append(out, RoleInstallResult{Name: name, OK: true})
			if opts.SourceRecordID != "" {
				insertSourceArtifact(app, opts.SourceRecordID, "subscription_role", name, "")
			}
		}
	}

	return out
}

func applyRoleResultsToStatus(app core.App, rec *core.Record, roles []RoleInstallResult) {
	failures := collectArtifactFailures(nil, nil, roles)
	markInstallStatusFromFailures(app, rec, failures)
}

func roleResultsToDTO(in []RoleInstallResult) []dto.BlueprintCreatedResponseRoleResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]dto.BlueprintCreatedResponseRoleResult, len(in))
	for i, r := range in {
		out[i] = dto.BlueprintCreatedResponseRoleResult{
			Name:    r.Name,
			Version: r.Version,
			OK:      r.OK,
			Error:   r.Error,
		}
	}
	return out
}

// collectArtifactFailures walks per-artifact results and returns one
// "<kind> <name>: <reason>" string per failed entry.
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
