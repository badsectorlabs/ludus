package ludusapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

// SyncResult groups per-artifact outcomes by the source-repo dir the content
// came from, mirroring the catalog shape: templates/ at the root,
// ansible/ (vendored roles + collections) under localAnsibleResults, and the
// blueprints' galaxy dependency closure under blueprintResults.
type SyncResult struct {
	TemplateResults     []ArtifactResult    `json:"templateResults"`
	LocalAnsibleResults LocalAnsibleResults `json:"localAnsibleResults"`
	BlueprintResults    BlueprintResults    `json:"blueprintResults"`
}

type LocalAnsibleResults struct {
	RoleResults       []ArtifactResult `json:"roleResults"`
	CollectionResults []ArtifactResult `json:"collectionResults"`
}

type BlueprintResults struct {
	AnsibleResults         []AnsibleInstallResult `json:"ansibleResults"`
	UndeclaredDependencies []UndeclaredDependency `json:"undeclaredDependencies,omitempty"`
}

type UndeclaredDependency struct {
	BlueprintID string `json:"blueprintID"`
	Role        string `json:"role"`
	// Kind is the classification used by the renderer to compose grouped
	// guidance. Server emits structured kinds so consumers don't re-parse
	// the ref string. Values: "missing_role" | "missing_collection".
	Kind string `json:"kind"`
	// ParentCollection is populated when Kind == "missing_collection" and
	// names the collection the FQCN role belongs to (e.g.
	// "community.crypto" for ref "community.crypto.openssl_certificate").
	ParentCollection string `json:"parentCollection,omitempty"`
}

const (
	UndeclaredKindRole       = "missing_role"
	UndeclaredKindCollection = "missing_collection"
)

type ArtifactResult struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type SyncOptions struct {
	Global bool
	Force  bool
	// NoDeps skips the galaxy dependency install (installUnionedAnsible): the
	// selected blueprints register, but their role/collection deps are not
	// fetched from ansible-galaxy — only what's already on disk is used.
	NoDeps bool
	// InitiatorIsAdmin gates deletion of the global-roles dir during a prune —
	// the only instance-wide resource a source installs into. Templates are
	// per-user, so a non-admin's remove only ever touches their own files and
	// can't break what other users rely on. Set from the request user at the
	// runSourceInstall call sites.
	InitiatorIsAdmin bool
	// InitiatorProxmoxUsername is the Proxmox username of the request user.
	// Source templates install into this user's per-user packer dir, which
	// survives server updates (the embedded global packer dir is wiped and
	// re-extracted on every update). Set from the request user at the
	// runSourceInstall call sites.
	InitiatorProxmoxUsername string
	// Archive: tarball bytes for upload-type sources. Empty means "use whatever
	// is already on disk" — startup re-syncs of upload sources rely on that.
	Archive         []byte
	ArchiveFilename string

	// Selection scopes which walked items get registered/installed. nil means
	// "install everything walked" — set when the install handler's caller
	// omits the selection field. Nothing is persisted: each install acts only
	// on the selection it was given.
	Selection *InstallSelection

	// SelectionFromClaims derives the selection from what this source has
	// already installed (blueprint rows + source_artifacts claims),
	// intersected with the new walk. Used by upload-archive updates so
	// pushing new content re-applies what's actually installed rather than
	// everything the archive ships.
	SelectionFromClaims bool
}

// sourceSyncLocks serialises runSourceInstall per source record. Concurrent syncs on
// the same source would race the git checkout, the disk artifact copies, and
// the blueprint upserts.
var sourceSyncLocks sync.Map

func lockSourceSync(sourceRecordID string) func() {
	val, _ := sourceSyncLocks.LoadOrStore(sourceRecordID, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// fetchAndWalkSource brings the source's on-disk checkout to its declared
// ref (git) or extracted archive bytes (upload), then walks the result. It's
// the shared "make the working tree current and tell me what's there" step
// for runSourceInstall, the register-only branch of CreateSource, and the
// catalog/install-preview handlers. Caller must already hold the per-source
// sync lock.
func fetchAndWalkSource(app core.App, sourceRecord *core.Record, opts SyncOptions) (*WalkedSource, error) {
	checkoutDir := SourceCheckoutDir(sourceRecord.Id)

	switch sourceRecord.GetString("type") {
	case "git":
		if err := CloneOrUpdateGit(checkoutDir, sourceRecord.GetString("url"), sourceRecord.GetString("ref")); err != nil {
			markSyncFailed(app, sourceRecord, err)
			return nil, err
		}
	case "upload":
		if len(opts.Archive) > 0 {
			tmpDir, err := os.MkdirTemp("", "ludus-archive-*")
			if err != nil {
				markSyncFailed(app, sourceRecord, err)
				return nil, err
			}
			defer os.RemoveAll(tmpDir)
			tmpPath := filepath.Join(tmpDir, sanitiseArchiveFilename(opts.ArchiveFilename))
			if err := os.WriteFile(tmpPath, opts.Archive, 0644); err != nil {
				markSyncFailed(app, sourceRecord, err)
				return nil, err
			}
			// Extract into a sibling staging dir so a corrupt or oversized
			// archive can't wipe the existing checkout. On success we swap;
			// on failure the original is untouched.
			stagingDir := checkoutDir + ".incoming"
			_ = os.RemoveAll(stagingDir)
			if err := ExtractArchive(stagingDir, tmpPath); err != nil {
				_ = os.RemoveAll(stagingDir)
				markSyncFailed(app, sourceRecord, err)
				return nil, err
			}
			backupDir := checkoutDir + ".old"
			_ = os.RemoveAll(backupDir)
			renamedOld := false
			if _, statErr := os.Stat(checkoutDir); statErr == nil {
				if err := os.Rename(checkoutDir, backupDir); err != nil {
					_ = os.RemoveAll(stagingDir)
					markSyncFailed(app, sourceRecord, err)
					return nil, err
				}
				renamedOld = true
			}
			if err := os.Rename(stagingDir, checkoutDir); err != nil {
				if renamedOld {
					_ = os.Rename(backupDir, checkoutDir)
				}
				_ = os.RemoveAll(stagingDir)
				markSyncFailed(app, sourceRecord, err)
				return nil, err
			}
			_ = os.RemoveAll(backupDir)
		} else if _, err := os.Stat(checkoutDir); err != nil {
			err := fmt.Errorf("upload source has no on-disk content; re-upload via PATCH /sources/%s", sourceRecord.GetString("sourceID"))
			markSyncFailed(app, sourceRecord, err)
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unknown source type: %q", sourceRecord.GetString("type"))
	}

	walked, err := WalkSourceRepo(checkoutDir)
	if err != nil {
		markSyncFailed(app, sourceRecord, err)
		return nil, err
	}

	if len(walked.Blueprints) == 0 && len(walked.Templates) == 0 && len(walked.LocalRoles) == 0 {
		err := fmt.Errorf("source has no blueprints, templates, or roles to register")
		markSyncFailed(app, sourceRecord, err)
		return nil, err
	}

	return walked, nil
}

// RefreshResult is what a read-only refresh reports back. The walked source
// is the live catalog view; UndeclaredDependencies surfaces blueprint role
// references that aren't declared anywhere ansible-galaxy can resolve them.
// No install side-effects happen on this path — apply a selection through
// InstallSource to commit any state changes.
type RefreshResult struct {
	Walked                 *WalkedSource
	UndeclaredDependencies []UndeclaredDependency
}

// runSourceRefresh refreshes the on-disk checkout and updates the source
// record's manifest fields and sync metadata. NO artifact installs, removes,
// or DB writes for blueprints/templates/roles. Used by POST /sources/{id}/sync
// and SyncAllSourcesOnStartup so syncing is cheap and observation-only.
func runSourceRefresh(app core.App, sourceRecord *core.Record, opts SyncOptions) (*RefreshResult, error) {
	defer lockSourceSync(sourceRecord.Id)()

	walked, err := fetchAndWalkSource(app, sourceRecord, opts)
	if err != nil {
		return nil, err
	}

	applySourceManifestToRecord(sourceRecord, walked.Source)
	sourceRecord.Set("lastSyncedAt", time.Now().UTC().Format(time.RFC3339))
	sourceRecord.Set("lastSyncStatus", "ok")
	sourceRecord.Set("lastSyncError", "")

	if err := app.Save(sourceRecord); err != nil {
		return nil, err
	}
	return &RefreshResult{
		Walked:                 walked,
		UndeclaredDependencies: findUndeclaredDependencies(walked),
	}, nil
}

// runSourceInstall is the install path. It refreshes, then applies the
// requested selection: upserting blueprint rows, registering template/role
// files, installing galaxy roles.
//
// Install is additive and stateless: it acts only on the selection it was
// given and nothing is ever uninstalled here. There is no persisted
// selection — what's installed is recorded by the claims ledger
// (source_artifacts + blueprint rows), and removal goes through the
// individual delete APIs (templates rm, ansible role/collection rm,
// blueprint rm). opts.Selection == nil means "install everything currently
// walked".
func runSourceInstall(ctx context.Context, e *core.RequestEvent, app core.App, sourceRecord *core.Record, opts SyncOptions) (*SyncResult, error) {
	defer lockSourceSync(sourceRecord.Id)()

	walked, err := fetchAndWalkSource(app, sourceRecord, opts)
	if err != nil {
		return nil, err
	}

	switch {
	case opts.SelectionFromClaims:
		opts.Selection = selectionFromClaims(app, sourceRecord, walked)
	case opts.Selection == nil:
		opts.Selection = snapshotWalkedAsSelection(walked)
	default:
		if err := validateSelectionAgainstWalk(opts.Selection, walked); err != nil {
			return nil, err
		}
	}

	applySourceManifestToRecord(sourceRecord, walked.Source)
	if err := app.Save(sourceRecord); err != nil {
		return nil, err
	}

	if err := upsertSourceBlueprints(app, sourceRecord, walked, opts.Selection); err != nil {
		markSyncFailed(app, sourceRecord, err)
		return nil, err
	}

	res := &SyncResult{}
	res.TemplateResults = registerTemplates(app, sourceRecord, walked, opts)
	res.LocalAnsibleResults.RoleResults = registerLocalRoles(app, sourceRecord, walked, opts)
	res.LocalAnsibleResults.CollectionResults = registerLocalCollections(app, sourceRecord, walked, opts)
	if !opts.NoDeps {
		res.BlueprintResults.AnsibleResults = installUnionedAnsible(e, app, sourceRecord, walked, opts)
	}
	res.BlueprintResults.UndeclaredDependencies = findUndeclaredDependencies(walked)

	sourceRecord.Set("lastSyncedAt", time.Now().UTC().Format(time.RFC3339))
	failures := collectSyncFailures(res)
	if len(failures) == 0 {
		sourceRecord.Set("lastSyncStatus", "ok")
		sourceRecord.Set("lastSyncError", "")
	} else {
		sourceRecord.Set("lastSyncStatus", "partial")
		sourceRecord.Set("lastSyncError", truncateError(strings.Join(failures, "; "), 4000))
	}
	if err := app.Save(sourceRecord); err != nil {
		return res, err
	}
	return res, nil
}

// snapshotWalkedAsSelection turns "everything in this walk" into an
// InstallSelection — the expansion of an absent selection ("install
// everything") and the available-set used to validate explicit ones.
func snapshotWalkedAsSelection(walked *WalkedSource) *InstallSelection {
	sel := &InstallSelection{}
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		sel.Blueprints = append(sel.Blueprints, bp.Manifest.ID)
	}
	for _, dir := range walked.Templates {
		sel.Templates = append(sel.Templates, templateNameForDir(dir))
	}
	for _, dir := range walked.LocalRoles {
		sel.LocalRoles = append(sel.LocalRoles, filepath.Base(dir))
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
		sel.LocalCollections = append(sel.LocalCollections, gm.Namespace+"."+gm.Name)
	}
	return sel
}

// selectionFromClaims rebuilds "what this source has installed" from the
// claims ledger — blueprint rows plus template/role/collection
// source_artifacts — intersected with the walk so content the new archive or
// ref no longer ships falls away. Used by upload-archive updates to re-apply
// the actually-installed set against new content.
func selectionFromClaims(app core.App, src *core.Record, walked *WalkedSource) *InstallSelection {
	available := snapshotWalkedAsSelection(walked)
	sel := &InstallSelection{}

	if rows, err := app.FindRecordsByFilter("blueprints",
		"source = {:s}", "", 0, 0, map[string]any{"s": src.Id}); err == nil {
		for _, r := range rows {
			if id := r.GetString("sourceBlueprintID"); slices.Contains(available.Blueprints, id) {
				sel.Blueprints = append(sel.Blueprints, id)
			}
		}
	}

	listFor := func(kind string) []string {
		switch kind {
		case "template":
			return available.Templates
		case "local_role":
			return available.LocalRoles
		default:
			return available.LocalCollections
		}
	}
	for _, kind := range []string{"template", "local_role", "local_collection"} {
		rows, err := app.FindRecordsByFilter("source_artifacts",
			"source = {:s} && kind = {:k}", "", 0, 0,
			map[string]any{"s": src.Id, "k": kind})
		if err != nil {
			continue
		}
		avail := listFor(kind)
		for _, r := range rows {
			name := r.GetString("name")
			if !slices.Contains(avail, name) {
				continue
			}
			switch kind {
			case "template":
				sel.Templates = append(sel.Templates, name)
			case "local_role":
				sel.LocalRoles = append(sel.LocalRoles, name)
			case "local_collection":
				sel.LocalCollections = append(sel.LocalCollections, name)
			}
		}
	}
	return sel
}

// errSelectionNotAvailable marks an install whose requested selection names an
// item the source doesn't provide — a user input error (HTTP 400), distinct
// from a genuine sync failure.
var errSelectionNotAvailable = errors.New("requested items are not available in this source")

// validateSelectionAgainstWalk rejects an install that requests an item the
// walk doesn't provide, so a mistyped or stale name (the dir "debian10" vs the
// template "debian-10-x64-server-template") fails loudly instead of silently
// installing nothing and reporting success. Empty lists (uninstall-everything)
// validate trivially.
func validateSelectionAgainstWalk(sel *InstallSelection, walked *WalkedSource) error {
	available := snapshotWalkedAsSelection(walked)
	var missing []string
	for _, name := range sel.Templates {
		if !slices.Contains(available.Templates, name) {
			missing = append(missing, fmt.Sprintf("template %q", name))
		}
	}
	for _, name := range sel.Blueprints {
		if !slices.Contains(available.Blueprints, name) {
			missing = append(missing, fmt.Sprintf("blueprint %q", name))
		}
	}
	for _, name := range sel.LocalRoles {
		if !slices.Contains(available.LocalRoles, name) {
			missing = append(missing, fmt.Sprintf("role %q", name))
		}
	}
	for _, name := range sel.LocalCollections {
		if !slices.Contains(available.LocalCollections, name) {
			missing = append(missing, fmt.Sprintf("collection %q", name))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", errSelectionNotAvailable, strings.Join(missing, ", "))
	}
	return nil
}

func collectSyncFailures(res *SyncResult) []string {
	failures := collectArtifactFailures(res.TemplateResults, res.LocalAnsibleResults.RoleResults, res.BlueprintResults.AnsibleResults)
	for _, r := range res.LocalAnsibleResults.CollectionResults {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("local_collection %s: %s", r.Name, r.Message))
		}
	}
	return failures
}

func markSyncFailed(app core.App, src *core.Record, err error) {
	src.Set("lastSyncedAt", time.Now().UTC().Format(time.RFC3339))
	src.Set("lastSyncStatus", "error")
	src.Set("lastSyncError", truncateError(err.Error(), 2000))
	_ = app.Save(src)
}

func truncateError(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func applySourceManifestToRecord(src *core.Record, sm *SourceManifest) {
	if sm == nil {
		return
	}
	if sm.Name != "" {
		src.Set("name", sm.Name)
	}
	if sm.Description != "" {
		src.Set("description", sm.Description)
	}
	if len(sm.Authors) > 0 {
		src.Set("authors", sm.Authors)
	}
	if sm.Homepage != "" {
		src.Set("homepage", sm.Homepage)
	}
	if sm.License != "" {
		src.Set("license", sm.License)
	}
}

// upsertSourceBlueprints writes the walked source's blueprints to the
// `blueprints` collection. New rows get blueprintID="<sourceID>/<sub>",
// owner=source.owner, and empty sharing. Existing rows (matched by
// source+sourceBlueprintID) have their content fields refreshed but their
// blueprintID, owner, sharedUsers, and sharedGroups left untouched so
// permission grants persist across syncs. Rows for blueprints removed from
// the source are pruned.
func upsertSourceBlueprints(app core.App, src *core.Record, walked *WalkedSource, selection *InstallSelection) error {
	collection, err := app.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return fmt.Errorf("find blueprints collection: %w", err)
	}
	checkoutDir := SourceCheckoutDir(src.Id)
	sourceID := src.GetString("sourceID")
	owner := src.GetString("owner")

	selectedBP := func(bpID string) bool {
		if selection == nil {
			return true
		}
		return slices.Contains(selection.Blueprints, bpID)
	}

	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		if !selectedBP(bp.Manifest.ID) {
			continue
		}

		records, err := app.FindRecordsByFilter("blueprints",
			"source = {:src} && sourceBlueprintID = {:bp}", "", 1, 0,
			map[string]any{"src": src.Id, "bp": bp.Manifest.ID})
		var rec *core.Record
		isNew := false
		if err == nil && len(records) > 0 {
			rec = records[0]
		} else {
			rec = core.NewRecord(collection)
			isNew = true
		}

		if isNew {
			publicID := sourceID + "/" + bp.Manifest.ID
			conflict, _ := app.FindFirstRecordByData("blueprints", "blueprintID", publicID)
			if conflict != nil {
				return fmt.Errorf("blueprint id %q is already in use; rename the conflicting blueprint or pick a different source id", publicID)
			}
		}

		if isNew {
			rec.Set("source", src.Id)
			rec.Set("sourceBlueprintID", bp.Manifest.ID)
			rec.Set("blueprintID", sourceID+"/"+bp.Manifest.ID)
			rec.Set("owner", owner)
			rec.Set("sharedUsers", []string{})
			rec.Set("sharedGroups", []string{})
		}

		rec.Set("name", bp.Manifest.Name)
		rec.Set("description", bp.Manifest.Description)
		rec.Set("version", bp.Manifest.Version)
		rec.Set("tags", bp.Manifest.Tags)
		rec.Set("min_ludus_version", bp.Manifest.MinLudusVersion)
		rec.Set("blueprint_path", relativeToCheckout(checkoutDir, filepath.Join(bp.Dir, "blueprint.yml")))
		rec.Set("config_path", relativeToCheckout(checkoutDir, bp.ConfigPath))
		rec.Set("requirements_yaml", string(bp.RequirementsYAML))

		if bp.ThumbnailPath != "" {
			file, ferr := filesystem.NewFileFromPath(bp.ThumbnailPath)
			if ferr == nil {
				rec.Set("thumbnail", file)
			}
		}
		if err := app.Save(rec); err != nil {
			return fmt.Errorf("save blueprint %s/%s: %w", sourceID, bp.Manifest.ID, err)
		}
	}

	// Prune rows only for blueprints the repo no longer ships. An existing row
	// for a walked-but-not-currently-requested blueprint stays — installs are
	// additive and act only on what they were asked for; rows are removed by
	// blueprint rm, not by being left out of a later install.
	walkedIDs := map[string]struct{}{}
	for _, bp := range walked.Blueprints {
		if bp.Manifest != nil {
			walkedIDs[bp.Manifest.ID] = struct{}{}
		}
	}
	existing, err := app.FindRecordsByFilter("blueprints",
		"source = {:src}", "", 0, 0, map[string]any{"src": src.Id})
	if err == nil {
		for _, rec := range existing {
			if _, ok := walkedIDs[rec.GetString("sourceBlueprintID")]; ok {
				continue
			}
			_ = app.Delete(rec)
		}
	}
	return nil
}

func relativeToCheckout(checkoutDir, target string) string {
	rel, err := filepath.Rel(checkoutDir, target)
	if err != nil {
		return target
	}
	return rel
}

// startupSyncConcurrency bounds the boot-time refresh fan-out. We don't
// want N simultaneous git fetches racing for disk and network on boot.
const startupSyncConcurrency = 4

// defaultSourceBSL is the Bad Sector Labs source that ships Ludus's templates,
// blueprints, and roles. We auto-register it (owned by ROOT, so the sources
// list rule `isAdmin || owner` surfaces it to every admin) on startup, so a
// fresh instance can sync its catalog and install assets without anyone
// hand-running `source add`. Register-only — the catalog is fetched by the
// normal startup refresh (and on demand), keeping this offline-tolerant.
const (
	defaultSourceBSLID  = "ludus-source-bsl"
	defaultSourceBSLURL = "https://github.com/badsectorlabs/ludus-source-bsl.git"
)

// seedDefaultSourceBSL registers the default BSL source if it isn't already
// present. Idempotent (keyed on owner+sourceID); re-registers if an admin
// removed it, so the default stays available. Opt out with
// register_default_source: false in config.yml.
func seedDefaultSourceBSL(app core.App) {
	ConfigMu.RLock()
	enabled := ServerConfiguration.RegisterDefaultSource
	ConfigMu.RUnlock()
	if !enabled {
		return
	}
	// ROOT may not exist yet on the very first boot — skip and let a later
	// boot register it.
	root, err := app.FindFirstRecordByData("users", "userID", "ROOT")
	if err != nil {
		return
	}
	existing, _ := app.FindRecordsByFilter("sources",
		"owner = {:o} && sourceID = {:s}", "", 1, 0,
		map[string]any{"o": root.Id, "s": defaultSourceBSLID})
	if len(existing) > 0 {
		return
	}
	collection, err := app.FindCollectionByNameOrId("sources")
	if err != nil {
		return
	}
	src := core.NewRecord(collection)
	src.Set("sourceID", defaultSourceBSLID)
	src.Set("name", defaultSourceBSLID) // refresh overwrites from source.yml
	src.Set("type", "git")
	src.Set("owner", root.Id)
	src.Set("url", defaultSourceBSLURL)
	src.Set("lastSyncStatus", "")
	src.Set("lastSyncError", "")
	if err := app.Save(src); err != nil {
		log.Printf("[blueprint-sources] seed default source %q: %v", defaultSourceBSLID, err)
		return
	}
	log.Printf("[blueprint-sources] registered default source %q (%s)", defaultSourceBSLID, defaultSourceBSLURL)
}

// SyncAllSourcesOnStartup refreshes every registered source's catalog
// asynchronously at server start. Refresh-only — no artifact installs,
// removes, or DB writes for blueprints/templates/roles. Failures don't
// block startup. Disabled with sync_sources_on_startup: false in config.yml.
func SyncAllSourcesOnStartup(app core.App) {
	ConfigMu.RLock()
	enabled := ServerConfiguration.SyncSourcesOnStartup
	ConfigMu.RUnlock()
	if !enabled {
		return
	}
	go func() {
		records, err := app.FindRecordsByFilter("sources", "", "", 0, 0, nil)
		if err != nil {
			return
		}
		sem := make(chan struct{}, startupSyncConcurrency)
		for _, src := range records {
			sem <- struct{}{}
			go func(s *core.Record) {
				defer func() { <-sem }()
				_, _ = runSourceRefresh(app, s, SyncOptions{})
			}(src)
		}
		for i := 0; i < cap(sem); i++ {
			sem <- struct{}{}
		}
	}()
}
