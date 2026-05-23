package ludusapi

import (
	"context"
	"encoding/json"
	"fmt"
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
	// Archive: tarball bytes for upload-type sources. Empty means "use whatever
	// is already on disk" — startup re-syncs of upload sources rely on that.
	Archive         []byte
	ArchiveFilename string

	// Selection scopes which walked items get registered/installed. nil means
	// "install everything walked" — set by the install handler when
	// installAll=true, and the default for backfilled / pre-existing source
	// rows. Future syncs honor whatever the source's stored installSelection
	// is.
	Selection *InstallSelection
}

// sourceSyncLocks serialises runSourceSync per source record. Concurrent syncs on
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
// for runSourceSync, the register-only branch of CreateSource, and the
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

// runSourceSync fetches/extracts the source on disk, walks it, upserts
// source-derived blueprints, registers shipped templates and local roles, and
// runs the unioned role install. Used by both source-add (the heavy first run)
// and source-sync (idempotent re-application). Synchronous.
func runSourceSync(ctx context.Context, e *core.RequestEvent, app core.App, sourceRecord *core.Record, opts SyncOptions) (*SyncResult, error) {
	defer lockSourceSync(sourceRecord.Id)()

	walked, err := fetchAndWalkSource(app, sourceRecord, opts)
	if err != nil {
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
	res.TemplateResults = registerTemplates(app, sourceRecord, walked, opts.Force, opts.Selection)
	res.LocalRoleResults = registerLocalRoles(app, sourceRecord, walked, opts)
	res.RoleResults = installUnionedRoles(e, app, sourceRecord, walked, opts)
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
	if opts.Selection != nil {
		sourceRecord.Set("installSelection", opts.Selection)
	}
	if err := app.Save(sourceRecord); err != nil {
		return res, err
	}
	return res, nil
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

	existing, err := app.FindRecordsByFilter("blueprints",
		"source = {:src}", "", 0, 0, map[string]any{"src": src.Id})
	if err == nil {
		for _, rec := range existing {
			if _, ok := seen[rec.GetString("sourceBlueprintID")]; !ok {
				_ = app.Delete(rec)
			}
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

// startupSyncConcurrency bounds the boot-time sync fan-out. We don't want N
// simultaneous git fetches and ansible-galaxy installs racing for disk and
// network on boot.
const startupSyncConcurrency = 4

// SyncAllSourcesOnStartup refreshes every registered source asynchronously at
// server start. Failures don't block startup. Disabled when the env var
// LUDUS_SYNC_SOURCES_ON_STARTUP=false.
func SyncAllSourcesOnStartup(app core.App) {
	if os.Getenv("LUDUS_SYNC_SOURCES_ON_STARTUP") == "false" {
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
				opts := SyncOptions{Selection: loadInstallSelection(s)}
				_, _ = runSourceSync(context.Background(), nil, app, s, opts)
			}(src)
		}
		for i := 0; i < cap(sem); i++ {
			sem <- struct{}{}
		}
	}()
}

// loadInstallSelection reads the persisted picker selection off a source
// record. nil means "no selection on file — install everything" (matches
// the runSourceSync filter semantics).
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

