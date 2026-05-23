package ludusapi

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"gopkg.in/yaml.v3"
)

func registerTemplates(app core.App, src *core.Record, walked *WalkedSource, force bool, selection *InstallSelection) []ArtifactResult {
	var results []ArtifactResult
	for _, dir := range walked.Templates {
		name := filepath.Base(dir)
		if selection != nil && !slices.Contains(selection.Templates, name) {
			continue
		}
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
	ownerProxmoxUsername := ""
	if !opts.GlobalRoles {
		if owner, err := app.FindRecordById("users", src.GetString("owner")); err == nil {
			ownerProxmoxUsername = owner.GetString("proxmoxUsername")
		}
	}
	for _, dir := range walked.LocalRoles {
		name := filepath.Base(dir)
		if opts.Selection != nil && !slices.Contains(opts.Selection.LocalRoles, name) {
			continue
		}
		if err := addLocalRoleFromDirectory(app, dir, ownerProxmoxUsername, opts.GlobalRoles, opts.Force); err != nil {
			results = append(results, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		insertSourceArtifact(app, src.Id, "local_role", name, "")
		results = append(results, ArtifactResult{Name: name, OK: true})
	}
	return results
}

// insertSourceArtifact upserts the (source, kind, name) claim. If a row
// already exists for the same source claiming the same name, its version is
// updated in place — re-syncing a source with a bumped role version must
// not produce duplicate rows.
//
// Multiple sources sharing the same role/template name is allowed; the
// (source, kind, name) tuple is per-source so cross-source claims live as
// distinct rows. The frontend can join across source_artifacts to surface
// co-claims when relevant.
func insertSourceArtifact(app core.App, sourceID, kind, name, version string) {
	collection, err := app.FindCollectionByNameOrId("source_artifacts")
	if err != nil {
		return
	}
	existing, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = {:k} && name = {:n}", "", 1, 0,
		map[string]any{"s": sourceID, "k": kind, "n": name})
	if len(existing) > 0 {
		rec := existing[0]
		if rec.GetString("version") != version {
			rec.Set("version", version)
			_ = app.Save(rec)
		}
		return
	}
	r := core.NewRecord(collection)
	r.Set("source", sourceID)
	r.Set("kind", kind)
	r.Set("name", name)
	r.Set("version", version)
	_ = app.Save(r)
}

// addTemplateFromDirectory copies a packer template into the global packer
// dir. Requires exactly one *.pkr.hcl or *.pkr.json. force=true overwrites.
// When the destination already exists and force=false, the existing on-disk
// template is preserved and we return nil so the caller can still record
// the source's claim — multiple sources owning the same name is allowed.
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
			return nil
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
// path (when global=true) or the owner's per-user roles dir. force=true
// overwrites. ownerProxmoxUsername is required for non-global installs.
// When the destination already exists and force=false, the existing files
// are preserved and we return nil so the caller can still record the
// source's claim — multiple sources owning the same name is allowed.
func addLocalRoleFromDirectory(_ core.App, dir, ownerProxmoxUsername string, global, force bool) error {
	name := filepath.Base(dir)

	var destDir string
	if global {
		destDir = filepath.Join(ludusInstallPath, "resources", "global-roles", name)
	} else {
		if ownerProxmoxUsername == "" {
			return fmt.Errorf("ownerProxmoxUsername is required for non-global role install of %q", name)
		}
		destDir = filepath.Join(userRolesPath(ownerProxmoxUsername), name)
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

	existedAlready := false
	if _, err := os.Stat(destDir); err == nil {
		if !force {
			existedAlready = true
		} else if err := os.RemoveAll(destDir); err != nil {
			return fmt.Errorf("removing existing role directory %s: %w", destDir, err)
		}
	}

	if !existedAlready {
		if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
			return fmt.Errorf("creating roles directory: %w", err)
		}
		if err := copyDir(dir, destDir); err != nil {
			return fmt.Errorf("copying role %s to %s: %w", dir, destDir, err)
		}
	}

	// `ansible-galaxy role list` only enumerates directories that contain a
	// meta/main.yml (or .yaml). Some real-world community sources ship roles
	// with only tasks/ + README, which leaves them invisible after install.
	// Synthesize a minimal stub so the role enumerates correctly. This is a
	// Ludus implementation detail — log it for operator visibility but don't
	// bubble it into the user-facing artifact result.
	if synthesized, err := synthesizeRoleMetaIfMissing(destDir, name); err != nil {
		return fmt.Errorf("writing meta stub for %s: %w", name, err)
	} else if synthesized {
		log.Printf("source role %q ships without meta/main.yml — synthesized a minimal stub at %s", name, destDir)
	}

	_, _ = reflectRoleVersionToGalaxyInfo(destDir)
	return nil
}

// synthesizeRoleMetaIfMissing writes a minimal meta/main.yml stub when the
// role has no meta/main.{yml,yaml}. Returns true if a stub was written.
func synthesizeRoleMetaIfMissing(roleDir, name string) (bool, error) {
	metaDir := filepath.Join(roleDir, "meta")
	for _, candidate := range []string{"main.yml", "main.yaml"} {
		if _, err := os.Stat(filepath.Join(metaDir, candidate)); err == nil {
			return false, nil
		}
	}
	if err := os.MkdirAll(metaDir, 0755); err != nil {
		return false, err
	}
	stub := fmt.Sprintf("galaxy_info:\n  role_name: %s\n  author: ludus-source\n  description: \"Auto-generated by Ludus source install; replace with real metadata.\"\n", name)
	if err := os.WriteFile(filepath.Join(metaDir, "main.yml"), []byte(stub), 0644); err != nil {
		return false, err
	}
	return true, nil
}

// installUnionedRoles installs galaxy and subscription roles for every
// blueprint in a source. registerTemplates and registerLocalRoles handle
// the source-root local artifacts; this only deals with galaxy + subscription
// roles declared in each blueprint's requirements.yml.
func installUnionedRoles(e *core.RequestEvent, app core.App, src *core.Record, walked *WalkedSource, opts SyncOptions) []RoleInstallResult {
	ownerProxmoxUser := ""
	ownerID := src.GetString("owner")
	if ownerID != "" {
		if user, err := app.FindRecordById("users", ownerID); err == nil {
			ownerProxmoxUser = user.GetString("proxmoxUsername")
		}
	}

	selectedBP := func(bpID string) bool {
		if opts.Selection == nil {
			return true
		}
		return slices.Contains(opts.Selection.Blueprints, bpID)
	}

	var out []RoleInstallResult
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil || !selectedBP(bp.Manifest.ID) {
			continue
		}
		results := installRolesForBlueprint(e, app, bp, ResolverOpts{
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

func hasRequirementsRoles(reqYAML []byte) bool {
	if len(reqYAML) == 0 {
		return false
	}
	var doc RequirementsDoc
	_ = yaml.Unmarshal(reqYAML, &doc)
	return len(doc.Roles) > 0
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
