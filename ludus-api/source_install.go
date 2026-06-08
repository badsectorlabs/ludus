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
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"gopkg.in/yaml.v3"
)

func registerTemplates(app core.App, src *core.Record, walked *WalkedSource, opts SyncOptions) []ArtifactResult {
	var results []ArtifactResult
	for _, dir := range walked.Templates {
		name := templateNameForDir(dir)
		if opts.Selection != nil && !slices.Contains(opts.Selection.Templates, name) {
			continue
		}
		if err := installTemplateDir(dir, opts.InitiatorProxmoxUsername, opts.Force, true); err != nil {
			results = append(results, ArtifactResult{Name: name, OK: false, Message: err.Error()})
			continue
		}
		if err := ensureTemplateRow(app, name, dir, templateIconPath(dir), opts.Force); err != nil {
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
// template without colliding here. When iconPath is non-empty the bundled
// image is applied: on new rows unconditionally, on existing rows only when the
// row has no icon yet (bundle-defaults), or when force is true.
func ensureTemplateRow(app core.App, name, srcDir, iconPath string, force bool) error {
	collection, err := app.FindCollectionByNameOrId("templates")
	if err != nil {
		return err
	}
	rec, err := app.FindFirstRecordByData("templates", "name", name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if rec != nil {
		// Existing row: apply the bundled icon only when the row has none
		// (bundle-defaults), or unconditionally on --force. A hand-edited
		// icon otherwise sticks.
		if iconPath != "" && (force || rec.GetString("icon") == "") {
			if file, ferr := filesystem.NewFileFromPath(iconPath); ferr == nil {
				rec.Set("icon", file)
				return app.Save(rec)
			}
		}
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
	if iconPath != "" {
		if file, ferr := filesystem.NewFileFromPath(iconPath); ferr == nil {
			rec.Set("icon", file)
		}
	}
	return app.Save(rec)
}

func registerLocalRoles(app core.App, src *core.Record, walked *WalkedSource, opts SyncOptions) []ArtifactResult {
	var results []ArtifactResult
	// Install into the requesting user's per-user roles dir (or global). Where
	// artifacts land is never a function of the source owner — every user
	// installs into their own home, matching the catalog's viewer-relative read.
	installProxmoxUsername := ""
	if !opts.GlobalRoles {
		installProxmoxUsername = opts.InitiatorProxmoxUsername
	}
	for _, dir := range walked.LocalRoles {
		name := filepath.Base(dir)
		if opts.Selection != nil && !slices.Contains(opts.Selection.LocalRoles, name) {
			continue
		}
		if err := addLocalRoleFromDirectory(app, dir, installProxmoxUsername, opts.GlobalRoles, opts.Force); err != nil {
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
// (source, kind, name) tuple is per-source, so cross-source claims live as
// distinct rows.
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

// installTemplateDir validates the packer template in srcDir and copies it into
// the given user's per-user packer dir, which survives server updates (the
// embedded global packer dir is backed up and re-extracted on every update).
// srcDir's basename becomes the packer subdir. Requires exactly one *.pkr.hcl
// or *.pkr.json. A name matching a built-in/global template is rejected — it
// would shadow the built-in and show up twice in the merged template list.
// When the user already has this template: overwrite if force; a no-op if
// idempotent (source re-sync, so re-installing an unchanged template is fine);
// otherwise an error (one-shot CLI add). Shared by source install and
// PutTemplateTar so both paths apply the same validation + guard.
func installTemplateDir(srcDir, proxmoxUsername string, force, idempotent bool) error {
	if proxmoxUsername == "" {
		return fmt.Errorf("no initiating user for template install")
	}

	packerFiles, err := findFiles(srcDir, "pkr.hcl", "pkr.json")
	if err != nil {
		return fmt.Errorf("scanning template directory %s: %w", srcDir, err)
	}
	switch len(packerFiles) {
	case 0:
		return fmt.Errorf("no *.pkr.hcl or *.pkr.json found in %s", srcDir)
	case 1:
		// proceed
	default:
		return fmt.Errorf("more than one packer file found in %s: %v", srcDir, packerFiles)
	}

	// Identify the template by its *-template name (the same name the templates
	// API reports) so the install folder, catalog, and ledger all agree.
	name := templateNameForDir(srcDir)
	// A name matching a built-in would shadow it and appear twice in the merged
	// (global + user) template list. Reject instead.
	if templateNameInGlobalPacker(name) {
		return fmt.Errorf("template %q matches a built-in template name and cannot be installed from a source", name)
	}

	// Duplicate guard keyed on the template NAME, not the destination folder: a
	// copy may already exist under a different folder (e.g. an older basename
	// folder, or a CLI upload whose tar root differs from the *-template name).
	// Checking the name keeps us from installing the same template twice.
	if userPackerTemplateNames(proxmoxUsername)[name] {
		if !force {
			if idempotent {
				return nil
			}
			return fmt.Errorf("template %q already exists, use force to overwrite", name)
		}
	}

	destDir := filepath.Join(ludusInstallPath, "users", proxmoxUsername, "packer", name)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("removing existing template directory %s: %w", destDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0755); err != nil {
		return fmt.Errorf("creating packer directory: %w", err)
	}
	if err := copyDir(srcDir, destDir); err != nil {
		return fmt.Errorf("copying template %s to %s: %w", srcDir, destDir, err)
	}
	return nil
}

// userPackerTemplateNames returns the set of *-template names present in the
// given user's packer dir — every template that user's `templates list` would
// show from their own scope. Keyed on the template name (not folder) so callers
// detect a template regardless of the folder it was installed under.
func userPackerTemplateNames(proxmoxUsername string) map[string]bool {
	out := map[string]bool{}
	if proxmoxUsername == "" {
		return out
	}
	files, err := findFiles(filepath.Join(ludusInstallPath, "users", proxmoxUsername, "packer"), "pkr.hcl", "pkr.json")
	if err != nil {
		return out
	}
	for _, f := range files {
		n, err := extractTemplateNameFromHCL(f)
		if err != nil {
			continue
		}
		out[n] = true

	}
	return out
}

// templateIconPath resolves a template's bundled icon to an absolute,
// existing path from the static `icon_path` variable in its packer file.
// Returns "" when the variable is unset or the file is missing. The value is
// validated as a relative, in-dir path (the packer variable carries no schema
// check of its own, so we validate here).
func templateIconPath(dir string) string {
	thumb := packerVarFromDir(dir, "icon_path")
	if thumb == "" {
		return ""
	}
	if err := validateRelativePath("icon", thumb); err != nil {
		return ""
	}
	p := filepath.Join(dir, thumb)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// templateNameForDir returns the *-template name declared in the single packer
// file under dir — the same identity the templates API reports — falling back
// to the directory basename when the file has no *-template name. Used so the
// source catalog, selection, ledger, and install folder all key on one name.
func templateNameForDir(dir string) string {
	files, err := findFiles(dir, "pkr.hcl", "pkr.json")
	if err != nil || len(files) == 0 {
		return filepath.Base(dir)
	}
	name, err := extractTemplateNameFromHCL(files[0])
	if err != nil || name == "" {
		return filepath.Base(dir)
	}
	return name
}

// templateNameInGlobalPacker reports whether hclName matches the *-template name
// of any built-in template shipped in the global packer dir.
func templateNameInGlobalPacker(hclName string) bool {
	files, err := findFiles(filepath.Join(ludusInstallPath, "packer"), "pkr.hcl", "pkr.json")
	if err != nil {
		return false
	}
	for _, f := range files {
		parsedName, err := extractTemplateNameFromHCL(f)
		if err != nil {
			return false
		}
		if parsedName == hclName {
			return true
		}
	}
	return false
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
	// Deps install into the requesting user's home — the same user the catalog
	// reads install-state for — never the source owner's.
	installProxmoxUser := opts.InitiatorProxmoxUsername

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
			ForceRoles:     opts.Force,
			GlobalRoles:    opts.GlobalRoles,
			ProxmoxUser:    installProxmoxUser,
			AnsibleHome:    userAnsibleHome(installProxmoxUser),
			SourceRecordID: src.Id,
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
