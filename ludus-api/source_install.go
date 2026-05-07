package ludusapi

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"gopkg.in/yaml.v3"
)

func registerTemplates(app core.App, src *core.Record, walked *WalkedSource, force bool) []ArtifactResult {
	var results []ArtifactResult
	dirs := append([]string{}, walked.SharedTemplates...)
	for _, bp := range walked.Blueprints {
		dirs = append(dirs, bp.ScopedTemplates...)
	}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		if err := addTemplateFromDirectory(app, dir, force); err != nil {
			results = append(results, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		if err := ensureTemplateRow(app, name, dir); err != nil {
			results = append(results, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		insertSourceArtifact(app, src.Id, "template", name, "")
		results = append(results, ArtifactResult{Name: name, OK: true})
	}
	return results
}

// ensureTemplateRow creates a templates collection row for a registered
// template if one doesn't already exist. Provenance lives in source_artifacts,
// so the row carries no source claim — multiple sources can claim the same
// template without colliding here.
func ensureTemplateRow(app core.App, name, srcDir string) error {
	collection, err := app.FindCollectionByNameOrId("templates")
	if err != nil {
		return err
	}
	rec, err := app.FindFirstRecordByData("templates", "name", name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if rec != nil {
		return nil
	}
	rec = core.NewRecord(collection)
	rec.Set("name", name)
	os := ""
	if hclFiles, _ := findFiles(srcDir, "pkr.hcl", "pkr.json"); len(hclFiles) > 0 {
		os = string(extractOSFromHCL(hclFiles[0]))
	}
	if os == "" {
		os = string(osFromTemplateName(name))
	}
	rec.Set("os", os)
	rec.Set("shared", true)
	return app.Save(rec)
}

func registerLocalRoles(app core.App, src *core.Record, walked *WalkedSource, opts SyncOptions) []ArtifactResult {
	var results []ArtifactResult
	dirs := append([]string{}, walked.SharedLocalRoles...)
	for _, bp := range walked.Blueprints {
		dirs = append(dirs, bp.ScopedLocalRoles...)
	}
	for _, dir := range dirs {
		name := filepath.Base(dir)
		err := addLocalRoleFromDirectory(app, dir, opts.GlobalRoles, opts.Force)
		if err != nil {
			results = append(results, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		reconcileArtifactProvenance(app, src.Id, "local_role", name, "")
		results = append(results, ArtifactResult{Name: name, OK: true})
	}
	return results
}

// insertSourceArtifact idempotently records (source, kind, name, version).
// Galaxy roles use this directly — two sources sharing a role at the same
// version is normal. Local roles go through reconcileArtifactProvenance.
func insertSourceArtifact(app core.App, sourceID, kind, name, version string) {
	collection, err := app.FindCollectionByNameOrId("source_artifacts")
	if err != nil {
		return
	}
	existing, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = {:k} && name = {:n} && version = {:v}", "", 1, 0,
		map[string]any{"s": sourceID, "k": kind, "n": name, "v": version})
	if len(existing) > 0 {
		return
	}
	r := core.NewRecord(collection)
	r.Set("source", sourceID)
	r.Set("kind", kind)
	r.Set("name", name)
	r.Set("version", version)
	_ = app.Save(r)
}

// reconcileArtifactProvenance upserts the local-role row, deleting any
// other-source rows for the same (kind, name) first — local roles install to
// a single on-disk location, so two sources claiming the same name would
// silently overwrite each other; we make ownership explicit.
func reconcileArtifactProvenance(app core.App, sourceID, kind, name, version string) {
	others, _ := app.FindRecordsByFilter("source_artifacts",
		"kind = {:k} && name = {:n} && source != {:s}", "", 0, 0,
		map[string]any{"k": kind, "n": name, "s": sourceID})
	for _, row := range others {
		_ = app.Delete(row)
	}
	insertSourceArtifact(app, sourceID, kind, name, version)
}

// addTemplateFromDirectory copies a packer template into the global packer
// dir. Requires exactly one *.pkr.hcl or *.pkr.json. force=true overwrites.
func addTemplateFromDirectory(_ core.App, dir string, force bool) error {
	name := filepath.Base(dir)
	destDir := filepath.Join(ludusInstallPath, "packer", name)

	packerFiles, err := findFiles(dir, "pkr.hcl", "pkr.json")
	if err != nil {
		return fmt.Errorf("scanning template directory %s: %w", dir, err)
	}
	switch len(packerFiles) {
	case 0:
		return fmt.Errorf("no *.pkr.hcl or *.pkr.json found in %s", dir)
	case 1:
		// proceed
	default:
		return fmt.Errorf("more than one packer file found in %s: %v", dir, packerFiles)
	}

	if _, err := os.Stat(destDir); err == nil {
		if !force {
			return fmt.Errorf("template %q already exists; use force to overwrite", name)
		}
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("removing existing template directory %s: %w", destDir, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
		return fmt.Errorf("creating packer directory: %w", err)
	}
	if err := copyDir(dir, destDir); err != nil {
		return fmt.Errorf("copying template %s to %s: %w", dir, destDir, err)
	}
	return nil
}

// addLocalRoleFromDirectory copies a role dir into either the global-roles
// path (when global=true) or the shared roles path. force=true overwrites.
func addLocalRoleFromDirectory(_ core.App, dir string, global, force bool) error {
	name := filepath.Base(dir)

	var destDir string
	if global {
		destDir = filepath.Join(ludusInstallPath, "resources", "global-roles", name)
	} else {
		destDir = filepath.Join(ludusInstallPath, "resources", "roles", name)
	}

	hasRoleStructure := false
	for _, subdir := range []string{"tasks", "meta", "defaults", "handlers", "vars", "files", "templates", "library"} {
		if _, err := os.Stat(filepath.Join(dir, subdir)); err == nil {
			hasRoleStructure = true
			break
		}
	}
	if !hasRoleStructure {
		return fmt.Errorf("directory %s does not appear to contain an ansible role (no tasks/, meta/, defaults/, etc.)", dir)
	}

	if _, err := os.Stat(destDir); err == nil {
		if !force {
			return fmt.Errorf("role %q already exists at %s; use force to overwrite", name, destDir)
		}
		if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("removing existing role directory %s: %w", destDir, err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
		return fmt.Errorf("creating roles directory: %w", err)
	}
	if err := copyDir(dir, destDir); err != nil {
		return fmt.Errorf("copying role %s to %s: %w", dir, destDir, err)
	}

	_, _ = reflectRoleVersionToGalaxyInfo(destDir)
	return nil
}

// installUnionedRoles installs galaxy and subscription roles for every
// blueprint in a source. registerTemplates and registerLocalRoles already
// handle the shared/per-blueprint local artifacts, so this only deals with
// galaxy + subscription roles.
func installUnionedRoles(e *core.RequestEvent, app core.App, src *core.Record, walked *WalkedSource, opts SyncOptions) []RoleInstallResult {
	ownerProxmoxUser := ""
	ownerID := src.GetString("owner")
	if ownerID != "" {
		if user, err := app.FindRecordById("users", ownerID); err == nil {
			ownerProxmoxUser = user.GetString("proxmoxUsername")
		}
	}

	var out []RoleInstallResult
	for _, bp := range walked.Blueprints {
		augmentedBP := bp
		augmentedBP.ScopedLocalRoles = appendUnique(bp.ScopedLocalRoles, walked.SharedLocalRoles...)

		results := installRolesForBlueprint(e, app, augmentedBP, ResolverOpts{
			ForceRoles:       opts.Force,
			GlobalRoles:      opts.GlobalRoles,
			OwnerProxmoxUser: ownerProxmoxUser,
			AnsibleHome:      ansibleHomeForSourceOwner(app, src, opts.GlobalRoles),
			SourceRecordID:   src.Id,
		})
		out = append(out, results...)
	}
	return out
}

func appendUnique(dst []string, extras ...string) []string {
	have := map[string]struct{}{}
	for _, s := range dst {
		have[s] = struct{}{}
	}
	result := append([]string{}, dst...)
	for _, s := range extras {
		if _, ok := have[s]; !ok {
			result = append(result, s)
			have[s] = struct{}{}
		}
	}
	return result
}

func hasRequirementsRoles(reqYAML []byte) bool {
	if len(reqYAML) == 0 {
		return false
	}
	var doc RequirementsDoc
	_ = yaml.Unmarshal(reqYAML, &doc)
	return len(doc.Roles) > 0
}

// augmentRequirementsWithBareNames adds entries to the merged requirements.yml
// for any public role name that doesn't already have an explicit entry. Bare
// names resolve to galaxy.ansible.com lookups.
func augmentRequirementsWithBareNames(reqYAML []byte, names []string) []byte {
	var doc RequirementsDoc
	_ = yaml.Unmarshal(reqYAML, &doc)
	have := map[string]bool{}
	for _, r := range doc.Roles {
		have[r.Name] = true
	}
	for _, n := range names {
		if !have[n] {
			doc.Roles = append(doc.Roles, RequirementsRole{Name: n})
		}
	}
	out, _ := yaml.Marshal(doc)
	return out
}

func pickRolesPathForSourceOwner(app core.App, src *core.Record, global bool) string {
	if global {
		return globalRolesPath()
	}
	ownerID := src.GetString("owner")
	user, err := app.FindRecordById("users", ownerID)
	if err != nil {
		return ""
	}
	return userRolesPath(user.GetString("proxmoxUsername"))
}

func globalRolesPath() string {
	return filepath.Join(ludusInstallPath, "resources", "global-roles")
}

// userRolesPath returns the per-user ansible roles directory keyed by
// proxmoxUsername. Empty username yields "" so callers fall back to
// ansible-galaxy's default.
func userRolesPath(proxmoxUsername string) string {
	if proxmoxUsername == "" {
		return ""
	}
	return filepath.Join(ludusInstallPath, "users", proxmoxUsername, ".ansible", "roles")
}

// subtractLocalRoleNames returns roleSet minus role names that match a
// directory shipped by the source as a local role. Those are installed by
// registerLocalRoles and must not be sent to ansible-galaxy.
func subtractLocalRoleNames(roleSet []string, walked *WalkedSource) []string {
	local := map[string]struct{}{}
	for _, dir := range walked.SharedLocalRoles {
		local[filepath.Base(dir)] = struct{}{}
	}
	for _, bp := range walked.Blueprints {
		for _, dir := range bp.ScopedLocalRoles {
			local[filepath.Base(dir)] = struct{}{}
		}
	}
	out := roleSet[:0:0]
	for _, n := range roleSet {
		if _, hit := local[n]; hit {
			continue
		}
		out = append(out, n)
	}
	return out
}

func userAnsibleHome(proxmoxUsername string) string {
	if proxmoxUsername == "" {
		return ""
	}
	return filepath.Join(ludusInstallPath, "users", proxmoxUsername, ".ansible")
}

func ansibleHomeForSourceOwner(app core.App, src *core.Record, global bool) string {
	if global {
		return ""
	}
	user, err := app.FindRecordById("users", src.GetString("owner"))
	if err != nil {
		return ""
	}
	return userAnsibleHome(user.GetString("proxmoxUsername"))
}

// getSubscriptionCatalogNames returns nil for unlicensed (community)
// instances. Failures are logged so a silently-misrouted subscription
// role going through galaxy would at least leave a breadcrumb.
func getSubscriptionCatalogNames(e *core.RequestEvent) []string {
	roles, err := GetSubscriptionRolesMetadata(e)
	if err != nil {
		log.Printf("[blueprint-sources] subscription catalog fetch failed: %v", err)
		return nil
	}
	if len(roles) == 0 {
		return nil
	}
	names := make([]string, 0, len(roles))
	for _, item := range roles {
		if item.Role != "" {
			names = append(names, item.Role)
		}
	}
	return names
}

// installSubscriptionRoleByName installs a single subscription role via
// the license-gated download path. ansibleHome keeps galaxy off the
// systemd-protected /home default.
func installSubscriptionRoleByName(e *core.RequestEvent, name, ansibleHome string) error {
	if server == nil || !server.LicenseValid || server.LicenseKey == "" {
		return fmt.Errorf("a valid Ludus license key is required to install subscription role %q", name)
	}
	if ansibleHome == "" {
		ansibleHome = filepath.Join(ludusInstallPath, "resources", ".ansible-subscription")
	}
	if err := os.MkdirAll(ansibleHome, 0755); err != nil {
		return fmt.Errorf("create ansible home: %w", err)
	}
	tempDir := filepath.Join(os.TempDir(), "ludus-sub-roles")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}

	roleFileName, err := DownloadRoleUsingLicenseKey(e, name, tempDir)
	if err != nil {
		return fmt.Errorf("download subscription role %q: %w", name, err)
	}
	tarPath := filepath.Join(tempDir, roleFileName)
	defer os.Remove(tarPath)

	// ansible-galaxy resolves a local tar by bare name; rename the
	// versioned tar to <role> and run galaxy from the containing dir.
	namedPath := filepath.Join(tempDir, name)
	if err := os.Rename(tarPath, namedPath); err != nil {
		return fmt.Errorf("rename role tar: %w", err)
	}
	defer os.Remove(namedPath)

	globalRolesPath := filepath.Join(ludusInstallPath, "resources", "global-roles")
	cmd := exec.Command("ansible-galaxy", "role", "install", name,
		"--roles-path", globalRolesPath)
	cmd.Dir = tempDir
	cmd.Env = append(os.Environ(), "ANSIBLE_HOME="+ansibleHome)
	output, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(output), "was installed successfully") {
		return fmt.Errorf("ansible-galaxy install %q failed: %s: %w", name, strings.TrimSpace(string(output)), err)
	}
	return nil
}
