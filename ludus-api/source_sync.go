package ludusapi

import (
	"context"
	"encoding/json"
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
	"github.com/pocketbase/pocketbase/tools/types"
)

type SyncResult struct {
	TemplateResults        []ArtifactResult       `json:"templateResults"`
	LocalRoleResults       []ArtifactResult       `json:"localRoleResults"`
	RoleResults            []RoleInstallResult    `json:"roleResults"`
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
	GlobalRoles bool
	Force       bool
	// NoDeps skips the galaxy dependency install (installUnionedRoles): the
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
	// omits the selection field, and the default for backfilled / pre-existing
	// source rows. Future syncs honor whatever the source's stored
	// installSelection is.
	Selection *InstallSelection
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
//
// One-shot legacy migration: when the source record's installSelection is
// null (pre-upgrade rows that ran under the install-all regime), this path
// snapshots the current walk into installSelection so the next install has
// a concrete starting point. After the snapshot lands, every subsequent
// refresh is fully read-only.
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

	if loadInstallSelection(sourceRecord) == nil {
		sourceRecord.Set("installSelection", snapshotWalkedAsSelection(walked))
	}

	if err := app.Save(sourceRecord); err != nil {
		return nil, err
	}
	return &RefreshResult{
		Walked:                 walked,
		UndeclaredDependencies: findUndeclaredDependencies(walked),
	}, nil
}

// runSourceInstall is the install path. It refreshes, applies the selection
// (upserting blueprint rows, registering template/role files, pruning stale
// claims, installing galaxy roles), and persists the result.
//
// When opts.Selection is nil the install behaves as "install everything
// currently walked" — the walked set is snapshotted into installSelection
// before the apply runs, so future syncs honor a concrete set.
func runSourceInstall(ctx context.Context, e *core.RequestEvent, app core.App, sourceRecord *core.Record, opts SyncOptions) (*SyncResult, error) {
	defer lockSourceSync(sourceRecord.Id)()

	walked, err := fetchAndWalkSource(app, sourceRecord, opts)
	if err != nil {
		return nil, err
	}

	if opts.Selection == nil {
		opts.Selection = snapshotWalkedAsSelection(walked)
	} else if err := validateSelectionAgainstWalk(opts.Selection, walked); err != nil {
		return nil, err
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
	res.LocalRoleResults = registerLocalRoles(app, sourceRecord, walked, opts)
	pruneSourceArtifactClaims(app, sourceRecord, walked, opts.Selection, opts.InitiatorIsAdmin, opts.InitiatorProxmoxUsername)
	if !opts.NoDeps {
		res.RoleResults = installUnionedRoles(e, app, sourceRecord, walked, opts)
	}
	res.UndeclaredDependencies = findUndeclaredDependencies(walked)

	sourceRecord.Set("lastSyncedAt", time.Now().UTC().Format(time.RFC3339))
	failures := collectSyncFailures(res)
	if len(failures) == 0 {
		sourceRecord.Set("lastSyncStatus", "ok")
		sourceRecord.Set("lastSyncError", "")
	} else {
		sourceRecord.Set("lastSyncStatus", "partial")
		sourceRecord.Set("lastSyncError", truncateError(strings.Join(failures, "; "), 4000))
	}
	sourceRecord.Set("installSelection", opts.Selection)
	if err := app.Save(sourceRecord); err != nil {
		return res, err
	}
	return res, nil
}

// snapshotWalkedAsSelection turns "everything in this walk" into an
// InstallSelection. Used as the migration shim for legacy source rows that
// pre-date the "selection always set" contract — first sync after upgrade
// persists this snapshot, and subsequent syncs honor it.
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
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", errSelectionNotAvailable, strings.Join(missing, ", "))
	}
	return nil
}

func collectSyncFailures(res *SyncResult) []string {
	return collectArtifactFailures(res.TemplateResults, res.LocalRoleResults, res.RoleResults)
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

	seen := map[string]struct{}{}
	for _, bp := range walked.Blueprints {
		if bp.Manifest == nil {
			continue
		}
		if !selectedBP(bp.Manifest.ID) {
			continue
		}
		seen[bp.Manifest.ID] = struct{}{}

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

	// Prune rows for blueprints the user no longer wants. A row is kept when
	// either (a) it's still walked AND selected (the seen set), or (b) the
	// persisted selection still names it — upstream may have removed the
	// blueprint, but user intent wins over upstream churn. In install-all
	// mode (selection == nil), the seen-only check applies and orphan rows
	// for items removed upstream are pruned as before.
	existing, err := app.FindRecordsByFilter("blueprints",
		"source = {:src}", "", 0, 0, map[string]any{"src": src.Id})
	if err == nil {
		for _, rec := range existing {
			sourceBPID := rec.GetString("sourceBlueprintID")
			if _, walked := seen[sourceBPID]; walked {
				continue
			}
			if selection != nil && slices.Contains(selection.Blueprints, sourceBPID) {
				continue
			}
			_ = app.Delete(rec)
		}
	}
	return nil
}

// pruneSourceArtifactClaims drops source_artifacts rows for template and
// local_role names this source no longer wants — either because the user
// de-selected them, or because upstream removed the directory AND the
// persisted selection no longer names them. The on-disk files go too: there is
// no cross-source refcount, so a de-selected name is uninstalled even if
// another source ships the same name (that source can reinstall it).
//
// Galaxy roles and collections are not pruned here: their claims come from
// blueprint requirements.yml, and installUnionedRoles already filters by
// the selected blueprints, so a stale row would imply a blueprint-level
// inconsistency rather than a template/role one. Left for a follow-up if
// it becomes a real problem.
func pruneSourceArtifactClaims(app core.App, src *core.Record, walked *WalkedSource, selection *InstallSelection, initiatorIsAdmin bool, initiatorProxmoxUsername string) {
	keep := func(dirs []string, selected []string, nameFn func(string) string) map[string]struct{} {
		out := map[string]struct{}{}
		for _, dir := range dirs {
			name := nameFn(dir)
			if selection != nil && !slices.Contains(selected, name) {
				continue
			}
			out[name] = struct{}{}
		}
		return out
	}

	// Templates are keyed by their *-template name (matching the catalog +
	// ledger); local roles by directory basename.
	keptTemplates := keep(walked.Templates, selectionTemplates(selection), templateNameForDir)
	keptLocalRoles := keep(walked.LocalRoles, selectionLocalRoles(selection), filepath.Base)

	dropStaleClaims(app, src, "template", keptTemplates, selectionTemplates(selection), selection != nil, initiatorIsAdmin, initiatorProxmoxUsername)
	dropStaleClaims(app, src, "local_role", keptLocalRoles, selectionLocalRoles(selection), selection != nil, initiatorIsAdmin, initiatorProxmoxUsername)
}

func selectionTemplates(s *InstallSelection) []string {
	if s == nil {
		return nil
	}
	return s.Templates
}

func selectionLocalRoles(s *InstallSelection) []string {
	if s == nil {
		return nil
	}
	return s.LocalRoles
}

// dropStaleClaims removes (source, kind, name) rows whose name is neither
// currently kept nor named in the persisted selection. When the source is
// in install-all mode (haveSelection=false), the second guard is moot and
// only the kept set decides.
//
// After dropping our row, the on-disk files are removed too. Templates live in
// the initiator's own per-user packer dir, so removing one only affects that
// user — no gate. The global-roles dir IS shared instance-wide, so deleting
// from it stays admin-only (initiatorIsAdmin); a non-admin's role remove
// touches only the source owner's own per-user roles. There is NO cross-source
// refcount — uninstall means uninstall, so this deletes the artifact even if
// another source also ships the same name (that source can reinstall it).
// Failures from file removal are logged but not propagated — a stranded file
// is annoying but not corrupting, and we never want a prune error to
// short-circuit the rest of the sync.
func dropStaleClaims(app core.App, src *core.Record, kind string, kept map[string]struct{}, selected []string, haveSelection, initiatorIsAdmin bool, initiatorProxmoxUsername string) {
	existing, err := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = {:k}", "", 0, 0,
		map[string]any{"s": src.Id, "k": kind})
	if err != nil {
		return
	}
	for _, rec := range existing {
		name := rec.GetString("name")
		if _, ok := kept[name]; ok {
			continue
		}
		if haveSelection && slices.Contains(selected, name) {
			continue
		}
		if err := app.Delete(rec); err != nil {
			continue
		}
		switch kind {
		case "template":
			// Templates live in the initiator's own per-user packer dir, so a
			// remove only ever deletes that user's copy — no admin gate. The
			// templates-collection row is dropped too; if another user still
			// has the same template, their next `templates list` disk-scan
			// re-adds it.
			if rmErr := removeTemplateByName(name, initiatorProxmoxUsername); rmErr != nil {
				log.Printf("[blueprint-sources] sync prune: remove template %q: %v", name, rmErr)
				continue
			}
			if tplRec, _ := app.FindFirstRecordByData("templates", "name", name); tplRec != nil {
				_ = app.Delete(tplRec)
			}
		case "local_role":
			// global-roles deletion is admin-only; a non-admin's remove is
			// confined to the source owner's own per-user roles dir.
			if rmErr := removeLocalRoleByName(app, name, src, initiatorIsAdmin); rmErr != nil {
				log.Printf("[blueprint-sources] sync prune: remove local role %q: %v", name, rmErr)
			}
		}
	}
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

// loadInstallSelection reads the persisted picker selection off a source
// record. nil means "no selection on file — install everything" (matches
// the runSourceInstall filter semantics).
func loadInstallSelection(src *core.Record) *InstallSelection {
	return decodeInstallSelectionRaw(src.Get("installSelection"))
}

// decodeInstallSelectionRaw converts a JSON-field value into an
// InstallSelection. The PocketBase JSONField surfaces as a few different Go
// types depending on how it was stored (types.JSONRaw on direct reads,
// []byte / string on some code paths, nil when unset), so we type-switch
// instead of relying on cast.ToString — that's been load-bearing behavior
// across pocketbase versions and we want to lock the read path. Returns nil
// for any null/empty/parse-error case so callers always get the "install
// everything" fallback rather than a partial selection.
func decodeInstallSelectionRaw(raw any) *InstallSelection {
	var bytes []byte
	switch v := raw.(type) {
	case nil:
		return nil
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	case types.JSONRaw:
		bytes = []byte(v)
	default:
		marshaled, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		bytes = marshaled
	}
	if len(bytes) == 0 || string(bytes) == "null" {
		return nil
	}
	var sel InstallSelection
	if err := json.Unmarshal(bytes, &sel); err != nil {
		return nil
	}
	return &sel
}
