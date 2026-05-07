package ludusapi

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"gopkg.in/yaml.v3"
)

var blueprintIDRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`)

// blueprintRebuildLocks serializes per-blueprint bundle rebuilds; without
// it, concurrent Update* requests would race the on-disk rename-swap and
// silently lose one writer's bytes.
var blueprintRebuildLocks sync.Map

func lockBlueprintRebuild(blueprintRecordID string) func() {
	val, _ := blueprintRebuildLocks.LoadOrStore(blueprintRecordID, &sync.Mutex{})
	mu := val.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func normalizeBlueprintID(blueprintID string) string {
	return strings.TrimSpace(blueprintID)
}

func getBlueprintPublicID(blueprintRecord *core.Record) string {
	blueprintID := normalizeBlueprintID(blueprintRecord.GetString("blueprintID"))
	if blueprintID == "" {
		return blueprintRecord.Id
	}
	return blueprintID
}

func blueprintThumbnailURL(blueprintRecord *core.Record) string {
	if blueprintRecord == nil {
		return ""
	}

	thumbnail := blueprintRecord.GetString("thumbnail")
	if thumbnail == "" {
		return ""
	}

	return fmt.Sprintf("/api/files/blueprints/%s/%s", blueprintRecord.Id, url.PathEscape(thumbnail))
}

func validateBlueprintID(blueprintID string) error {
	if blueprintID == "" {
		return fmt.Errorf("Blueprint ID is required")
	}
	if !blueprintIDRegex.MatchString(blueprintID) {
		return fmt.Errorf("Blueprint ID must be a valid ID (e.g. 'blueprint-1', 'team/windows' or 'my_blueprint')")
	}
	return nil
}

func blueprintIDExists(e *core.RequestEvent, blueprintID string) (bool, error) {
	existingBlueprintRecord, err := e.App.FindFirstRecordByData("blueprints", "blueprintID", blueprintID)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return existingBlueprintRecord != nil, nil
}

func getNextCopyBlueprintID(e *core.RequestEvent, sourceBlueprintID string) (string, error) {
	baseBlueprintID := normalizeBlueprintID(sourceBlueprintID)
	if !blueprintIDRegex.MatchString(baseBlueprintID) {
		baseBlueprintID = "blueprint"
	}

	candidateBlueprintID := fmt.Sprintf("%s-copy", baseBlueprintID)
	for copyNumber := 2; ; copyNumber++ {
		exists, err := blueprintIDExists(e, candidateBlueprintID)
		if err != nil {
			return "", err
		}
		if !exists {
			return candidateBlueprintID, nil
		}
		candidateBlueprintID = fmt.Sprintf("%s-copy-%d", baseBlueprintID, copyNumber)
	}
}

func getBlueprintRecordFromRequest(e *core.RequestEvent) (*core.Record, error) {
	blueprintID := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if blueprintID == "" {
		return nil, JSONError(e, http.StatusBadRequest, "Blueprint ID is required")
	}

	blueprintRecord, err := e.App.FindFirstRecordByData("blueprints", "blueprintID", blueprintID)
	if err == nil {
		return blueprintRecord, nil
	}
	if err != sql.ErrNoRows {
		return nil, JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding blueprint %s: %v", blueprintID, err))
	}

	// Backward compatibility for existing blueprints created before blueprintID existed.
	blueprintRecord, err = e.App.FindRecordById("blueprints", blueprintID)
	if err != nil {
		return nil, JSONError(e, http.StatusNotFound, fmt.Sprintf("Blueprint %s not found", blueprintID))
	}

	return blueprintRecord, nil
}


func buildBlueprintListItem(e *core.RequestEvent, user *models.User, blueprintRecord *core.Record) dto.ListBlueprintsResponseItem {
	sourceID := ""
	if srcRecID := blueprintRecord.GetString("source"); srcRecID != "" {
		if src, err := e.App.FindRecordById("sources", srcRecID); err == nil && src != nil {
			sourceID = src.GetString("sourceID")
		}
	}
	return dto.ListBlueprintsResponseItem{
		BlueprintID:  getBlueprintPublicID(blueprintRecord),
		Name:         blueprintRecord.GetString("name"),
		Description:  blueprintRecord.GetString("description"),
		ThumbnailURL: blueprintThumbnailURL(blueprintRecord),
		OwnerUserID:  resolveOwnerUserID(e, blueprintRecord.GetString("owner")),
		SharedUsers:  resolveUserIDs(e, blueprintRecord.GetStringSlice("sharedUsers")),
		SharedGroups: resolveGroupNames(e, blueprintRecord.GetStringSlice("sharedGroups")),
		AccessType:   getBlueprintAccessType(e, user, blueprintRecord),
		SourceID:     sourceID,
		Tags:         blueprintRecord.GetStringSlice("tags"),
		Created:      blueprintRecord.GetDateTime("created").Time(),
		Updated:      blueprintRecord.GetDateTime("updated").Time(),
	}
}

type blueprintAccessUserAccumulator struct {
	UserID    string
	Name      string
	AccessSet map[string]struct{}
	GroupSet  map[string]struct{}
}

func sortedKeysFromSet(values map[string]struct{}) []string {
	items := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		items = append(items, value)
	}
	sort.Strings(items)
	return items
}

func resolveUserIdentity(e *core.RequestEvent, userRecordID string) (string, string) {
	userID := strings.TrimSpace(userRecordID)
	userName := ""

	if userRecordID == "" {
		return userID, userName
	}

	userRecord, err := e.App.FindRecordById("users", userRecordID)
	if err != nil {
		return userID, userName
	}

	resolvedUserID := strings.TrimSpace(userRecord.GetString("userID"))
	if resolvedUserID != "" {
		userID = resolvedUserID
	}

	userName = strings.TrimSpace(userRecord.GetString("name"))
	return userID, userName
}

// readBlueprintConfigBytes reads the authoritative config.yml from the
// blueprint's bundle dir on disk. The bundle is the single source of truth
// for blueprint bytes — no PocketBase FileField is involved.
func readBlueprintConfigBytes(blueprintRecord *core.Record) ([]byte, error) {
	if srcRecID := blueprintRecord.GetString("source"); srcRecID != "" {
		configPath := filepath.Join(SourceCheckoutDir(srcRecID), blueprintRecord.GetString("config_path"))
		data, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("read source blueprint config: %w", err)
		}
		return data, nil
	}
	bundlePath := blueprintRecord.GetString("bundlePath")
	if bundlePath == "" {
		return nil, fmt.Errorf("blueprint bundle is missing")
	}
	data, err := os.ReadFile(filepath.Join(bundlePath, "range-config.yml"))
	if err != nil {
		return nil, fmt.Errorf("read blueprint config: %w", err)
	}
	return data, nil
}

func blueprintBundleDir(blueprintRecord *core.Record) string {
	if srcRecID := blueprintRecord.GetString("source"); srcRecID != "" {
		return filepath.Join(SourceCheckoutDir(srcRecID), filepath.Dir(blueprintRecord.GetString("config_path")))
	}
	return blueprintRecord.GetString("bundlePath")
}

func validateBlueprintConfigBytes(configBytes []byte) error {
	schemaBytes, err := loadYaml(ludusInstallPath + "/ansible/user-files/range-config.jsonschema")
	if err != nil {
		return fmt.Errorf("can't parse schema: %s", err.Error())
	}

	if err := validateBytes(configBytes, schemaBytes); err != nil {
		return err
	}

	return nil
}

func createBlueprintRecord(e *core.RequestEvent, owner *models.User, blueprintID, name, description string) (*core.Record, error) {
	blueprintsCollection, err := e.App.FindCollectionByNameOrId("blueprints")
	if err != nil {
		return nil, err
	}

	blueprintRecord := core.NewRecord(blueprintsCollection)
	blueprintRecord.Set("blueprintID", blueprintID)
	blueprintRecord.Set("name", name)
	blueprintRecord.Set("description", description)
	blueprintRecord.Set("owner", owner.Id)

	if err := e.App.Save(blueprintRecord); err != nil {
		return nil, err
	}

	return blueprintRecord, nil
}

// blueprintBundleRoot returns the parent directory under which all blueprint
// bundles live. Bundles are keyed by blueprint record ID (ownership-agnostic),
// so an ownership transfer is a pure DB update with no disk moves.
func blueprintBundleRoot() string {
	return filepath.Join(ludusInstallPath, "blueprints")
}

// createBlueprintWithBundle creates the blueprint record and materialises a
// self-contained bundle dir at <blueprintBundleRoot>/<record.Id>/. After this
// returns, callers should run ResolveAndInstall against the new bundle to
// register/install bundled artifacts and reach a healthy install state.
//
// The bundle is built BEFORE the record is created (so we can fail atomically
// without an orphan DB row), then renamed to its final record-id-keyed path
// once the record exists. The bundle dir is the single source of truth for
// the blueprint's bytes — there is no PocketBase FileField backing them.
// BlueprintMeta carries the editable metadata fields applied to a fresh
// blueprint record. Empty fields fall through to bundle defaults.
type BlueprintMeta struct {
	Version         string
	Tags            []string
	MinLudusVersion string
}

func createBlueprintWithBundle(
	e *core.RequestEvent,
	owner *models.User,
	rolesProxmoxUsername string,
	blueprintID, name, description string,
	configBytes []byte,
	meta BlueprintMeta,
) (*core.Record, string, error) {
	app := e.App

	bundleRoot := blueprintBundleRoot()
	if err := os.MkdirAll(bundleRoot, 0755); err != nil {
		return nil, "", fmt.Errorf("create bundle root: %w", err)
	}

	if rolesProxmoxUsername == "" {
		rolesProxmoxUsername = owner.ProxmoxUsername()
	}
	rolesPath := userRolesPath(rolesProxmoxUsername)
	packerDir := filepath.Join(ludusInstallPath, "packer")
	subCatalog := getSubscriptionCatalogNames(e)

	// Step 1: build bundle keyed by BlueprintID (temporary), so atomic rollback
	// works if the DB save fails. Missing template HCL or roles are tolerated;
	// the bundle is marked incomplete in that case.
	br, err := BuildBundle(BundleInputs{
		BundleRoot:      bundleRoot,
		BlueprintID:     blueprintID,
		Name:            name,
		Description:     description,
		Version:         meta.Version,
		Tags:            meta.Tags,
		MinLudusVersion: meta.MinLudusVersion,
		ConfigBytes:     configBytes,
		RolesPath:       rolesPath,
		GlobalRolesPath: globalRolesPath(),
		PackerDir:       packerDir,
		SubCatalog:      subCatalog,
	})
	if err != nil {
		return nil, "", fmt.Errorf("build bundle: %w", err)
	}
	tmpBundleDir := br.Dir
	if !br.Complete {
		logger.Warn(fmt.Sprintf("blueprint %q bundle incomplete — skipped templates=%v skipped roles=%v",
			blueprintID, br.SkippedTemplates, br.SkippedRoles))
	}

	// Step 2: create the DB record (which gives us the record ID).
	bp, err := createBlueprintRecord(e, owner, blueprintID, name, description)
	if err != nil {
		_ = os.RemoveAll(tmpBundleDir)
		return nil, "", err
	}
	bp.Set("version", meta.Version)
	if len(meta.Tags) > 0 {
		bp.Set("tags", meta.Tags)
	}
	if meta.MinLudusVersion != "" {
		bp.Set("min_ludus_version", meta.MinLudusVersion)
	}

	// Step 3: rename the bundle dir from BlueprintID to record-id-keyed path.
	finalBundleDir := filepath.Join(bundleRoot, bp.Id)
	if tmpBundleDir != finalBundleDir {
		if err := os.Rename(tmpBundleDir, finalBundleDir); err != nil {
			_ = app.Delete(bp)
			_ = os.RemoveAll(tmpBundleDir)
			return nil, "", fmt.Errorf("rename bundle dir: %w", err)
		}
	}

	// Step 4: persist the bundle path. bundle_complete reflects whether every
	// referenced template and role made it into the bundle.
	bp.Set("bundlePath", finalBundleDir)
	bp.Set("bundle_complete", br.Complete)
	if saveErr := app.Save(bp); saveErr != nil {
		_ = os.RemoveAll(finalBundleDir)
		_ = app.Delete(bp)
		return nil, "", saveErr
	}

	return bp, finalBundleDir, nil
}

// rebuildBlueprintBundle materialises a fresh bundle for an existing blueprint
// record from new config bytes, then atomically swaps it in. It is the single
// path through which a blueprint's config gets updated post-creation: the new
// bundle reflects the fresh roles/templates/subscription_refs derivation, and
// bundle_complete is recomputed.
//
// Implementation is build-into-staging + rename-swap (with rolling .old backup)
// so a failed rebuild leaves the existing bundle untouched. A per-blueprint
// mutex serialises concurrent rebuilds on the same record.
func rebuildBlueprintBundle(e *core.RequestEvent, bp *core.Record, configBytes []byte) error {
	unlock := lockBlueprintRebuild(bp.Id)
	defer unlock()

	app := e.App
	bundleRoot := blueprintBundleRoot()
	if err := os.MkdirAll(bundleRoot, 0755); err != nil {
		return fmt.Errorf("create bundle root: %w", err)
	}

	// We bundle local roles from the OWNER's roles dir, not the editor's. The
	// bundle is the blueprint, and a blueprint owned by Bob should reflect what
	// Bob has installed — not whatever's in an admin editor's home dir. Don't
	// "fix" this to use the editor's roles without re-evaluating the design.
	ownerRec, ownerErr := app.FindRecordById("users", bp.GetString("owner"))
	if ownerErr != nil {
		return fmt.Errorf("look up blueprint owner: %w", ownerErr)
	}
	owner := &models.User{}
	owner.SetProxyRecord(ownerRec)
	rolesProxmoxUsername := owner.ProxmoxUsername()
	rolesPath := userRolesPath(rolesProxmoxUsername)
	packerDir := filepath.Join(ludusInstallPath, "packer")
	subCatalog := getSubscriptionCatalogNames(e)

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return fmt.Errorf("generate staging suffix: %w", err)
	}
	stagingKey := bp.Id + ".rebuild-" + hex.EncodeToString(suffix)

	br, err := BuildBundle(BundleInputs{
		BundleRoot:      bundleRoot,
		BundleDirName:   stagingKey,
		BlueprintID:     bp.GetString("blueprintID"),
		Name:            bp.GetString("name"),
		Description:     bp.GetString("description"),
		Version:         bp.GetString("version"),
		Tags:            anySliceToStrings(bp.Get("tags")),
		MinLudusVersion: bp.GetString("min_ludus_version"),
		ConfigBytes:     configBytes,
		RolesPath:       rolesPath,
		GlobalRolesPath: globalRolesPath(),
		PackerDir:       packerDir,
		SubCatalog:      subCatalog,
	})
	if err != nil {
		return fmt.Errorf("build bundle: %w", err)
	}
	stagingDir := br.Dir
	finalDir := filepath.Join(bundleRoot, bp.Id)
	backupDir := finalDir + ".old-" + hex.EncodeToString(suffix)

	currentExists := false
	if _, statErr := os.Stat(finalDir); statErr == nil {
		currentExists = true
		if rnErr := os.Rename(finalDir, backupDir); rnErr != nil {
			logger.Error(fmt.Sprintf("rebuildBlueprintBundle %s: rotate old bundle failed: %v", bp.Id, rnErr))
			_ = os.RemoveAll(stagingDir)
			return fmt.Errorf("rotate old bundle: %w", rnErr)
		}
	}
	if rnErr := os.Rename(stagingDir, finalDir); rnErr != nil {
		logger.Error(fmt.Sprintf("rebuildBlueprintBundle %s: install new bundle failed: %v (staging=%s backup=%s)",
			bp.Id, rnErr, stagingDir, backupDir))
		if currentExists {
			if rbErr := os.Rename(backupDir, finalDir); rbErr != nil {
				logger.Error(fmt.Sprintf("rebuildBlueprintBundle %s: rollback of backup failed: %v (manual recovery required: rename %s -> %s)",
					bp.Id, rbErr, backupDir, finalDir))
			}
		}
		_ = os.RemoveAll(stagingDir)
		return fmt.Errorf("install new bundle: %w", rnErr)
	}
	if currentExists {
		if rmErr := os.RemoveAll(backupDir); rmErr != nil {
			logger.Warn(fmt.Sprintf("rebuildBlueprintBundle %s: failed to remove backup dir %s: %v",
				bp.Id, backupDir, rmErr))
		}
	}

	bp.Set("bundlePath", finalDir)
	bp.Set("bundle_complete", br.Complete)
	if saveErr := app.Save(bp); saveErr != nil {
		return fmt.Errorf("save bundle metadata: %w", saveErr)
	}
	return nil
}

func ListBlueprints(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	fromSource := strings.TrimSpace(e.Request.URL.Query().Get("from-source"))
	tagFilter := strings.TrimSpace(e.Request.URL.Query().Get("tag"))

	blueprints := make([]dto.ListBlueprintsResponseItem, 0)

	var fromSourceRecID string
	if fromSource != "" {
		src, err := e.App.FindFirstRecordByData("sources", "sourceID", fromSource)
		if err != nil {
			return e.JSON(http.StatusOK, blueprints)
		}
		fromSourceRecID = src.Id
	}

	clauses := []string{}
	params := map[string]any{}
	if !user.IsAdmin() {
		clauses = append(clauses,
			"(owner = {:u} || sharedUsers.id ?= {:u} || sharedGroups.members.id ?= {:u} || sharedGroups.managers.id ?= {:u})",
		)
		params["u"] = user.Id
	}
	if fromSourceRecID != "" {
		clauses = append(clauses, "source = {:s}")
		params["s"] = fromSourceRecID
	}
	filter := strings.Join(clauses, " && ")

	records, err := e.App.FindRecordsByFilter("blueprints", filter, "-updated", 0, 0, params)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error listing blueprints: %v", err))
	}

	// Tag filter runs in Go; PocketBase filter syntax can't match array elements in a JSON column.
	for _, rec := range records {
		if tagFilter != "" && !slices.Contains(rec.GetStringSlice("tags"), tagFilter) {
			continue
		}
		blueprints = append(blueprints, buildBlueprintListItem(e, user, rec))
	}

	return e.JSON(http.StatusOK, blueprints)
}

func DeleteBlueprint(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && blueprintRecord.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "You do not own this blueprint and cannot delete it")
	}

	if blueprintRecord.GetString("source") != "" {
		return JSONError(e, http.StatusConflict,
			"cannot delete a source-derived blueprint; remove or sync the source instead")
	}

	bundlePath := blueprintRecord.GetString("bundlePath")

	if err := e.App.Delete(blueprintRecord); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting blueprint: %v", err))
	}

	// Clean up the bundle dir from disk. Errors are logged but not surfaced to
	// the caller — an orphan dir is tolerable; a failed user-visible delete is not.
	if bundlePath != "" {
		if rmErr := os.RemoveAll(bundlePath); rmErr != nil {
			logger.Error(fmt.Sprintf("DeleteBlueprint: failed to remove bundle dir %s: %v", bundlePath, rmErr))
		}
	}

	return JSONResult(e, http.StatusOK, "Blueprint deleted successfully")
}

func getRangeByID(e *core.RequestEvent, rangeID string) (*models.Range, error) {
	rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("range %s not found", rangeID)
		}
		return nil, err
	}

	rangeObj := &models.Range{}
	rangeObj.SetProxyRecord(rangeRecord)
	return rangeObj, nil
}

// CreateBlueprint creates a blueprint from scratch. Optional `config`
// seeds range-config.yml; otherwise the seed mirrors `range create` (the
// example AD lab) so authors start from a working layout. Falls back to
// `ludus: []` if the example file is missing.
//
// Sibling endpoints: POST /blueprints/from-range, /blueprints/{id}/copy, /blueprints/import.
func CreateBlueprint(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var payload dto.CreateBlueprintRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	blueprintID := normalizeBlueprintID(payload.BlueprintID)
	if err := validateBlueprintID(blueprintID); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}
	exists, err := blueprintIDExists(e, blueprintID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError,
			fmt.Sprintf("Error checking if blueprint ID %s is already in use: %v", blueprintID, err))
	}
	if exists {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Blueprint ID %s already in use", blueprintID))
	}

	configBytes := []byte(payload.Config)
	if len(configBytes) == 0 {
		examplePath := filepath.Join(ludusInstallPath, "ansible", "user-files", "range-config.example.yml")
		if data, readErr := os.ReadFile(examplePath); readErr == nil && len(data) > 0 {
			configBytes = data
		} else {
			configBytes = []byte("ludus: []\n")
		}
	}
	if err := validateBlueprintConfigBytes(configBytes); err != nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid blueprint config: %v", err))
	}

	name := payload.Name
	if name == "" {
		name = blueprintID
	}
	version := payload.Version
	if version == "" {
		version = "1.0.0"
	}

	bp, bundleDir, err := createBlueprintWithBundle(
		e, user, user.ProxmoxUsername(),
		blueprintID, name, payload.Description, configBytes,
		BlueprintMeta{
			Version:         version,
			Tags:            payload.Tags,
			MinLudusVersion: payload.MinLudusVersion,
		},
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating blueprint: %v", err))
	}

	resp := map[string]any{
		"result":      "Blueprint created successfully",
		"blueprintID": getBlueprintPublicID(bp),
	}
	walked, werr := WalkBlueprintBundle(bundleDir)
	if werr == nil && walked != nil {
		res := ResolveAndInstall(e, e.App, *walked, ResolverOpts{
			OwnerProxmoxUser: user.ProxmoxUsername(),
			AnsibleHome:      ansibleHomeForUser(user, false),
		})
		applyResolverResultToStatus(e.App, bp, res)
		embedArtifactResults(resp, res.TemplateResults, res.LocalRoleResults, res.RoleResults)
	}
	return e.JSON(http.StatusCreated, resp)
}

func CreateBlueprintFromRange(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var payload dto.CreateBlueprintFromRangeRequest
	if e.Request.ContentLength > 0 {
		if err := e.BindBody(&payload); err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}
	blueprintID := normalizeBlueprintID(payload.BlueprintID)
	if err := validateBlueprintID(blueprintID); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}
	blueprintExists, err := blueprintIDExists(e, blueprintID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if blueprint ID %s is already in use: %v", blueprintID, err))
	}
	if blueprintExists {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Blueprint ID %s already in use", blueprintID))
	}

	sourceRange := &models.Range{}
	if payload.RangeID != "" {
		rangeNumber, err := GetRangeNumberFromRangeID(payload.RangeID)
		if err != nil {
			return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found", payload.RangeID))
		}
		if !user.IsAdmin() && !HasRangeAccess(e, user.UserId(), rangeNumber) {
			return JSONError(e, http.StatusForbidden, fmt.Sprintf("You do not have access to range %s", payload.RangeID))
		}
		sourceRange, err = GetRangeObjectByNumber(rangeNumber)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting range object: %v", err))
		}
	} else {
		var err error
		sourceRange, err = GetRange(e)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "No source range provided and no range found in request context")
		}
	}

	// Use only the caller's roles dir; reaching into another user's home for
	// admin-creates-from-other-user's-range was rejected as a privilege smell.
	// New blueprints ship roles inline, so this only affects legacy bundles.
	rolesProxmoxUsername := user.ProxmoxUsername()

	rangeConfigBytes, err := os.ReadFile(fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, sourceRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading range config: %v", err))
	}

	name := payload.Name
	if name == "" {
		name = fmt.Sprintf("%s Blueprint", sourceRange.Name())
	}

	description := payload.Description
	if description == "" {
		description = fmt.Sprintf("Blueprint created from range %s", sourceRange.RangeId())
	}

	blueprintRecord, bundleDir, err := createBlueprintWithBundle(e, user, rolesProxmoxUsername, blueprintID, name, description, rangeConfigBytes, BlueprintMeta{Version: "1.0.0"})
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating blueprint: %v", err))
	}

	// Run install inline so the caller sees per-artifact failures.
	resp := map[string]any{
		"result":      "Blueprint created successfully",
		"blueprintID": getBlueprintPublicID(blueprintRecord),
	}
	walked, werr := WalkBlueprintBundle(bundleDir)
	if werr == nil && walked != nil {
		res := ResolveAndInstall(e, e.App, *walked, ResolverOpts{
			OwnerProxmoxUser: user.ProxmoxUsername(),
			AnsibleHome:      ansibleHomeForUser(user, false),
		})
		applyResolverResultToStatus(e.App, blueprintRecord, res)
		embedArtifactResults(resp, res.TemplateResults, res.LocalRoleResults, res.RoleResults)
	}
	return e.JSON(http.StatusCreated, resp)
}

func CopyBlueprint(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var payload dto.CopyBlueprintRequest
	if e.Request.ContentLength > 0 {
		if err := e.BindBody(&payload); err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}

	srcBundle := blueprintBundleDir(blueprintRecord)
	if srcBundle == "" {
		return JSONError(e, http.StatusConflict,
			"source blueprint has no bundle on disk; cannot copy. Re-create or re-import the source blueprint.")
	}
	if _, err := readBlueprintConfigBytes(blueprintRecord); err != nil {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("source blueprint bundle is unavailable: %v", err))
	}

	name := payload.Name
	if name == "" {
		name = fmt.Sprintf("%s (Copy)", blueprintRecord.GetString("name"))
	}

	description := payload.Description
	if description == "" {
		description = blueprintRecord.GetString("description")
	}
	copyBlueprintID := normalizeBlueprintID(payload.BlueprintID)
	if copyBlueprintID == "" {
		copyBlueprintID, err = getNextCopyBlueprintID(e, getBlueprintPublicID(blueprintRecord))
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error generating copy blueprint ID: %v", err))
		}
	}
	if err := validateBlueprintID(copyBlueprintID); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}
	blueprintExists, err := blueprintIDExists(e, copyBlueprintID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if blueprint ID %s is already in use: %v", copyBlueprintID, err))
	}
	if blueprintExists {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Blueprint ID %s already in use", copyBlueprintID))
	}

	copyBlueprintRecord, err := createBlueprintRecord(
		e,
		user,
		copyBlueprintID,
		name,
		description,
	)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error copying blueprint: %v", err))
	}

	// Deep-copy the source bundle for the new record. No ResolveAndInstall runs
	// here — the source's bundled content is already registered globally; the
	// copy just inherits the same on-disk shape, including its bundle_complete
	// flag (which is surfaced on the response so callers can warn the user).
	bundleRoot := blueprintBundleRoot()
	if mkErr := os.MkdirAll(bundleRoot, 0755); mkErr != nil {
		_ = e.App.Delete(copyBlueprintRecord)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("create bundle root: %v", mkErr))
	}
	dstBundle := filepath.Join(bundleRoot, copyBlueprintRecord.Id)
	if cpErr := copyDir(srcBundle, dstBundle); cpErr != nil {
		_ = os.RemoveAll(dstBundle)
		_ = e.App.Delete(copyBlueprintRecord)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("copy bundle: %v", cpErr))
	}
	bundleComplete := blueprintRecord.GetBool("bundle_complete")
	copyBlueprintRecord.Set("bundlePath", dstBundle)
	copyBlueprintRecord.Set("bundle_complete", bundleComplete)
	// Carry the source's release metadata so the copy starts from the same
	// baseline; the user can bump/clear afterwards. Without this, the DB record
	// disagrees with the bundle's blueprint.yml (which still has the source's
	// values) until the next metadata update rewrites the manifest.
	copyBlueprintRecord.Set("version", blueprintRecord.GetString("version"))
	if tags := blueprintRecord.Get("tags"); tags != nil {
		copyBlueprintRecord.Set("tags", tags)
	}
	copyBlueprintRecord.Set("min_ludus_version", blueprintRecord.GetString("min_ludus_version"))
	if saveErr := e.App.Save(copyBlueprintRecord); saveErr != nil {
		_ = os.RemoveAll(dstBundle)
		_ = e.App.Delete(copyBlueprintRecord)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("save bundle path: %v", saveErr))
	}
	// Bundle was deep-copied verbatim, so its blueprint.yml still claims the
	// source's id and name. Rewrite to match the new record so export round-trips.
	if rwErr := rewriteBundleManifest(copyBlueprintRecord); rwErr != nil {
		_ = os.RemoveAll(dstBundle)
		_ = e.App.Delete(copyBlueprintRecord)
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("rewrite copy manifest: %v", rwErr))
	}

	return e.JSON(http.StatusCreated, map[string]any{
		"result":         "Blueprint copied successfully",
		"blueprintID":    getBlueprintPublicID(copyBlueprintRecord),
		"bundleComplete": bundleComplete,
	})
}

func applyConfigBytesToRange(e *core.RequestEvent, targetRange *models.Range, configBytes []byte, force bool) error {
	if status, err := writeRangeConfig(e, targetRange, configBytes, force); err != nil {
		return JSONError(e, status, err.Error())
	}
	return JSONResult(e, http.StatusOK, fmt.Sprintf("Blueprint applied to range %s", targetRange.RangeId()))
}

// writeRangeConfig writes configBytes to <range>/range-config.yml after schema
// validation and optional user-defined-role resolution. Returns (0, nil) on
// success; on failure returns a suggested HTTP status and the underlying error
// without writing an HTTP response. Used by both the apply-blueprint handler
// and the create-range-from-blueprint flow.
func writeRangeConfig(e *core.RequestEvent, targetRange *models.Range, configBytes []byte, force bool) (int, error) {
	if targetRange.TestingEnabled() && !force {
		return http.StatusConflict, errors.New("Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled. Use --force to override.")
	}

	filePath := fmt.Sprintf("%s/ranges/%s/.tmp-range-config.yml", ludusInstallPath, targetRange.RangeId())
	if err := os.WriteFile(filePath, configBytes, 0644); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("Unable to save temporary range config: %w", err)
	}

	originalRange := e.Get("range")
	e.Set("range", targetRange)
	defer func() {
		if originalRange != nil {
			e.Set("range", originalRange)
		}
	}()

	if err := validateFile(e, filePath, ludusInstallPath+"/ansible/user-files/range-config.jsonschema"); err != nil {
		return http.StatusBadRequest, fmt.Errorf("Configuration error: %w", err)
	}

	rangeHasRoles := e.Get("rangeHasRoles")
	if rangeHasRoles != nil && rangeHasRoles.(bool) {
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), "Resolving dependencies for user-defined roles..\n", false)
		rolesOutput, err := RunLocalAnsiblePlaybookOnTmpRangeConfig(e, []string{fmt.Sprintf("%s/ansible/range-management/user-defined-roles.yml", ludusInstallPath)})
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), rolesOutput, true)
		if err != nil {
			targetRange.SetRangeState(LudusRangeStateError)
			if saveErr := e.App.Save(targetRange); saveErr != nil {
				logger.Error(fmt.Sprintf("Error saving range: %s", saveErr.Error()))
			}
			errorLine := regexp.MustCompile(`ERROR[^"]*`)
			if errorMatch := errorLine.FindString(rolesOutput); errorMatch != "" {
				return http.StatusBadRequest, fmt.Errorf("Configuration error: %s", errorMatch)
			}
			return http.StatusBadRequest, fmt.Errorf("Error generating ordered roles: %s %v", rolesOutput, err)
		}
	}

	if err := os.Rename(filePath, fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, targetRange.RangeId())); err != nil {
		return http.StatusInternalServerError, errors.New("Unable to save the range config")
	}
	return 0, nil
}

// checkSubscriptionRefs reads subscription_refs.yml from a bundle and verifies
// the instance has license coverage for every named role. Returns the list of
// unmet role names; empty slice means OK.
//
// Returns (nil, nil) when subscription_refs.yml is absent (no subscription deps),
// or when bundleDir is empty (legacy / pre-bundle blueprint).
//
// Tolerates two YAML shapes for entries (BuildBundle writes the bare-name shape
// today, but the structured shape is also valid):
//
//	roles:
//	  - ludus_ghosts_client                    # bare scalar
//	  - name: ludus_ghosts_client              # structured
func checkSubscriptionRefs(e *core.RequestEvent, bundleDir string) ([]string, error) {
	if bundleDir == "" {
		return nil, nil
	}
	refPath := filepath.Join(bundleDir, "subscription_refs.yml")
	data, err := os.ReadFile(refPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Tolerant parse: roles list may contain bare strings or {name: ...} maps.
	var doc struct {
		Roles []yaml.Node `yaml:"roles"`
	}
	if uErr := yaml.Unmarshal(data, &doc); uErr != nil {
		return nil, fmt.Errorf("parse subscription_refs.yml: %w", uErr)
	}

	var names []string
	for _, n := range doc.Roles {
		switch n.Kind {
		case yaml.ScalarNode:
			if n.Value != "" {
				names = append(names, n.Value)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				if n.Content[i].Value == "name" {
					if v := n.Content[i+1].Value; v != "" {
						names = append(names, v)
					}
				}
			}
		}
	}

	catalog := getSubscriptionCatalogNames(e)
	have := map[string]bool{}
	for _, c := range catalog {
		have[c] = true
	}
	var unmet []string
	for _, n := range names {
		if !have[n] {
			unmet = append(unmet, n)
		}
	}
	return unmet, nil
}

func ApplyBlueprintToRange(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	blueprintID := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if blueprintID == "" {
		return JSONError(e, http.StatusBadRequest, "Blueprint ID is required")
	}

	var payload dto.ApplyBlueprintRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	var (
		targetRange *models.Range
		rangeErr    error
	)
	if payload.RangeID != "" {
		targetRange, rangeErr = getRangeByID(e, payload.RangeID)
		if rangeErr != nil {
			return JSONError(e, http.StatusNotFound, rangeErr.Error())
		}
	} else {
		targetRange, rangeErr = GetRange(e)
		if rangeErr != nil {
			return rangeErr
		}
	}

	if !user.IsAdmin() && !HasRangeAccess(e, user.UserId(), targetRange.RangeNumber()) {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You do not have access to range %s", targetRange.RangeId()))
	}

	configBytes, err := resolveBlueprintConfigForApply(e, user, blueprintID)
	if err != nil {
		return err
	}
	return applyConfigBytesToRange(e, targetRange, configBytes, payload.Force)
}

// resolveBlueprintConfigForApply gates a blueprint for apply: resolves it,
// checks access, checks subscription refs, and returns the config bytes ready
// to write. Source-derived and user-created blueprints share the same
// lookup; the storage location of the config (source checkout vs bundlePath)
// is hidden inside readBlueprintConfigBytes.
func resolveBlueprintConfigForApply(e *core.RequestEvent, user *models.User, blueprintID string) ([]byte, error) {
	bp, err := findLocalBlueprintByID(e, blueprintID, user)
	if err != nil {
		return nil, err
	}
	configBytes, err := readBlueprintConfigBytes(bp)
	if err != nil {
		return nil, JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading blueprint config: %v", err))
	}
	if unmet, cErr := checkSubscriptionRefs(e, blueprintBundleDir(bp)); cErr != nil {
		return nil, JSONError(e, http.StatusInternalServerError, fmt.Sprintf("check subscription refs: %v", cErr))
	} else if len(unmet) > 0 {
		return nil, JSONError(e, http.StatusForbidden,
			fmt.Sprintf("blueprint requires subscription roles %v; this instance has no valid license. Acquire a license or remove the role from config.yml.", unmet))
	}
	return configBytes, nil
}

func GetBlueprintConfig(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, user, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	blueprintConfigBytes, err := readBlueprintConfigBytes(blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error reading blueprint config: %v", err))
	}

	download := false
	downloadQuery := e.Request.URL.Query().Get("download")
	if downloadQuery != "" {
		download, err = strconv.ParseBool(downloadQuery)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid download query parameter")
		}
	}

	if download {
		blueprintName := strings.TrimSpace(blueprintRecord.GetString("name"))
		if blueprintName == "" {
			blueprintName = getBlueprintPublicID(blueprintRecord)
		}
		downloadFileName := strings.ToLower(blueprintName)
		downloadFileName = strings.ReplaceAll(downloadFileName, " ", "-")
		downloadFileName = strings.ReplaceAll(downloadFileName, "/", "-")
		e.Response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.yml", downloadFileName))
		e.Response.Header().Set("Content-Type", "application/x-yaml")
		_, writeErr := e.Response.Write(blueprintConfigBytes)
		return writeErr
	}

	return JSONResult(e, http.StatusOK, string(blueprintConfigBytes))
}

func UpdateBlueprintConfig(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && blueprintRecord.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "You do not own this blueprint and cannot update it")
	}

	if blueprintRecord.GetString("source") != "" {
		return JSONError(e, http.StatusConflict,
			"cannot edit a source-derived blueprint; copy it to a local blueprint first or edit the source")
	}

	var payload dto.UpdateBlueprintConfigRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	if strings.TrimSpace(payload.Config) == "" {
		return JSONError(e, http.StatusBadRequest, "Blueprint config cannot be empty")
	}

	configBytes := []byte(payload.Config)
	if err := validateBlueprintConfigBytes(configBytes); err != nil {
		return JSONError(e, http.StatusBadRequest, "Configuration error: "+err.Error())
	}

	if err := rebuildBlueprintBundle(e, blueprintRecord, configBytes); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error rebuilding blueprint bundle: %v", err))
	}

	return JSONResult(e, http.StatusOK, "Blueprint config updated successfully")
}

func ShareBlueprintWithGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkShareBlueprintWithGroupsRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}
	groupNames := normalizeBulkIdentifiers(bulkRequest.GroupNames)
	if len(groupNames) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, groupName := range groupNames {
		groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error finding group: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Group %s not found", groupName),
			})
			continue
		}

		if !actingUser.IsAdmin() && !slices.Contains(groupRecord.GetStringSlice("managers"), actingUser.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("You are not a manager of group %s", groupName),
			})
			continue
		}

		if slices.Contains(blueprintRecord.GetStringSlice("sharedGroups"), groupRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Blueprint already shared with group %s", groupName),
			})
			continue
		}

		blueprintRecord.Set("sharedGroups+", groupRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedGroups-", groupRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error sharing blueprint with group: %v", err),
			})
			continue
		}

		success = append(success, groupName)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func UnshareBlueprintWithGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkUnshareBlueprintWithGroupsRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}
	groupNames := normalizeBulkIdentifiers(bulkRequest.GroupNames)
	if len(groupNames) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with groupNames is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, groupName := range groupNames {
		groupRecord, err := e.App.FindFirstRecordByData("groups", "name", groupName)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error finding group: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Group %s not found", groupName),
			})
			continue
		}

		if !actingUser.IsAdmin() && !slices.Contains(groupRecord.GetStringSlice("managers"), actingUser.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("You are not a manager of group %s", groupName),
			})
			continue
		}

		if !slices.Contains(blueprintRecord.GetStringSlice("sharedGroups"), groupRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Blueprint is not shared with group %s", groupName),
			})
			continue
		}

		blueprintRecord.Set("sharedGroups-", groupRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedGroups+", groupRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   groupName,
				Reason: fmt.Sprintf("Error removing group share: %v", err),
			})
			continue
		}

		success = append(success, groupName)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func ShareBlueprintWithUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkShareBlueprintWithUsersRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := normalizeBulkIdentifiers(bulkRequest.UserIDs)
	if len(userIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, userID := range userIDs {
		targetUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}

		canShareWithUser, err := userCanShareBlueprintWithUser(e, actingUser, targetUserRecord)
		if err != nil {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error checking manager permissions: %v", err),
			})
			continue
		}
		if !canShareWithUser {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("You are not a manager of a group that contains user %s", userID),
			})
			continue
		}

		if slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), targetUserRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Blueprint already shared with user %s", userID),
			})
			continue
		}

		blueprintRecord.Set("sharedUsers+", targetUserRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedUsers-", targetUserRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error sharing blueprint with user: %v", err),
			})
			continue
		}

		success = append(success, userID)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func UnshareBlueprintWithUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	var bulkRequest dto.BulkUnshareBlueprintWithUsersRequest
	if err := e.BindBody(&bulkRequest); err != nil {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}
	userIDs := normalizeBulkIdentifiers(bulkRequest.UserIDs)
	if len(userIDs) == 0 {
		return JSONError(e, http.StatusBadRequest, "Request body with userIDs is required")
	}

	var success []string
	var errors []dto.BulkBlueprintOperationErrorItem

	for _, userID := range userIDs {
		targetUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil && err != sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error finding user: %v", err),
			})
			continue
		}
		if err == sql.ErrNoRows {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("User %s not found", userID),
			})
			continue
		}

		canShareWithUser, err := userCanShareBlueprintWithUser(e, actingUser, targetUserRecord)
		if err != nil {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error checking manager permissions: %v", err),
			})
			continue
		}
		if !canShareWithUser {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("You are not a manager of a group that contains user %s", userID),
			})
			continue
		}

		if !slices.Contains(blueprintRecord.GetStringSlice("sharedUsers"), targetUserRecord.Id) {
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Blueprint is not shared with user %s", userID),
			})
			continue
		}

		blueprintRecord.Set("sharedUsers-", targetUserRecord.Id)
		if err := e.App.Save(blueprintRecord); err != nil {
			blueprintRecord.Set("sharedUsers+", targetUserRecord.Id)
			errors = append(errors, dto.BulkBlueprintOperationErrorItem{
				Item:   userID,
				Reason: fmt.Sprintf("Error removing user share: %v", err),
			})
			continue
		}

		success = append(success, userID)
	}

	return e.JSON(http.StatusOK, dto.BulkBlueprintOperationResponse{
		Success: success,
		Errors:  errors,
	})
}

func ListBlueprintAccessUsers(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	userIdentityCache := make(map[string]struct {
		UserID string
		Name   string
	})
	accessByUserRecordID := make(map[string]*blueprintAccessUserAccumulator)

	addUserAccess := func(userRecordID string, accessType string, groupName string) {
		recordID := strings.TrimSpace(userRecordID)
		if recordID == "" {
			return
		}

		identity, exists := userIdentityCache[recordID]
		if !exists {
			resolvedUserID, resolvedName := resolveUserIdentity(e, recordID)
			identity = struct {
				UserID string
				Name   string
			}{
				UserID: resolvedUserID,
				Name:   resolvedName,
			}
			userIdentityCache[recordID] = identity
		}

		if strings.TrimSpace(identity.UserID) == "" {
			return
		}

		accessEntry, exists := accessByUserRecordID[recordID]
		if !exists {
			accessEntry = &blueprintAccessUserAccumulator{
				UserID:    identity.UserID,
				Name:      identity.Name,
				AccessSet: make(map[string]struct{}),
				GroupSet:  make(map[string]struct{}),
			}
			accessByUserRecordID[recordID] = accessEntry
		}

		normalizedAccessType := strings.TrimSpace(accessType)
		if normalizedAccessType != "" {
			accessEntry.AccessSet[normalizedAccessType] = struct{}{}
		}

		normalizedGroupName := strings.TrimSpace(groupName)
		if normalizedGroupName != "" {
			accessEntry.GroupSet[normalizedGroupName] = struct{}{}
		}
	}

	addUserAccess(blueprintRecord.GetString("owner"), "owner", "")

	for _, sharedUserRecordID := range blueprintRecord.GetStringSlice("sharedUsers") {
		addUserAccess(sharedUserRecordID, "direct", "")
	}

	for _, sharedGroupRecordID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", sharedGroupRecordID)
		if err != nil {
			continue
		}

		groupName := strings.TrimSpace(groupRecord.GetString("name"))
		if groupName == "" {
			groupName = sharedGroupRecordID
		}

		for _, managerRecordID := range groupRecord.GetStringSlice("managers") {
			addUserAccess(managerRecordID, "group-manager", groupName)
		}
		for _, memberRecordID := range groupRecord.GetStringSlice("members") {
			addUserAccess(memberRecordID, "group-member", groupName)
		}
	}

	response := make([]dto.ListBlueprintAccessUsersResponseItem, 0, len(accessByUserRecordID))
	for _, accessEntry := range accessByUserRecordID {
		response = append(response, dto.ListBlueprintAccessUsersResponseItem{
			UserID: accessEntry.UserID,
			Name:   accessEntry.Name,
			Access: sortedKeysFromSet(accessEntry.AccessSet),
			Groups: sortedKeysFromSet(accessEntry.GroupSet),
		})
	}

	sort.SliceStable(response, func(i, j int) bool {
		return response[i].UserID < response[j].UserID
	})

	return e.JSON(http.StatusOK, response)
}

// UpdateBlueprintMetadata handles PATCH /blueprints/{blueprintID}.
// Updates local blueprint fields (name, description, version, author, homepage,
// license, tags, min_ludus_version) and optionally replaces the config file.
//
// IDs containing slashes are first checked against registered sources; if the first
// segment matches a source the request is rejected as read-only (source-blueprints
// can't be patched). Otherwise the id is treated as a local subpath (e.g. team/windows).
func UpdateBlueprintMetadata(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	id := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if id == "" {
		return JSONError(e, http.StatusBadRequest, "blueprintID is required")
	}
	if isSourcePrefixedID(id) {
		parts := splitSourcePrefixedID(id)
		if _, srcErr := findSourceByVisibleID(e, parts[0]); srcErr == nil {
			return JSONError(e, http.StatusMethodNotAllowed,
				"source-blueprints are read-only; fork via apply + create --from-range to edit")
		}
		// First segment isn't a known source → fall through to local lookup
		// (subpath IDs like "team/windows" are valid blueprintID values).
	}

	bp, err := findLocalBlueprintByID(e, id, user)
	if err != nil {
		return err
	}
	if !user.IsAdmin() && bp.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "only the owner or an admin can update a blueprint")
	}

	// Parse body as a generic map so any subset of editable fields is accepted.
	var body map[string]any
	if err := e.BindBody(&body); err != nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid body: %v", err))
	}

	// Scalar editable fields. License, homepage, and authors are source-level
	// concerns and live on the parent source manifest; they are not editable
	// on the blueprint record.
	editable := map[string]bool{
		"name":              true,
		"description":       true,
		"version":           true,
		"tags":              true,
		"min_ludus_version": true,
	}
	// Validate config FIRST so a bad config string doesn't leave scalars
	// half-applied. The bundle rebuild does its own Save (covering scalars +
	// bundle metadata atomically); the scalar-only path falls back to a plain
	// Save below.
	cfgRaw, hasConfig := body["config"].(string)
	hasConfig = hasConfig && strings.TrimSpace(cfgRaw) != ""
	if hasConfig {
		if err := validateBlueprintConfigBytes([]byte(cfgRaw)); err != nil {
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid blueprint config: %v", err))
		}
	}

	metadataChanged := false
	for k, v := range body {
		if editable[k] {
			bp.Set(k, v)
			metadataChanged = true
		}
	}

	if hasConfig {
		if err := rebuildBlueprintBundle(e, bp, []byte(cfgRaw)); err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("rebuild bundle: %v", err))
		}
	} else {
		if err := e.App.Save(bp); err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("save blueprint: %v", err))
		}
		// Sync the bundle's blueprint.yml so export/import roundtrips preserve
		// the new metadata. Cheaper than a full rebuild; only the manifest is
		// touched, not the role/template copies.
		if metadataChanged {
			if err := rewriteBundleManifest(bp); err != nil {
				return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("rewrite bundle manifest: %v", err))
			}
		}
	}

	return e.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// rewriteBundleManifest atomically rewrites <bundleDir>/blueprint.yml from the
// current record state. Used for metadata-only updates so the bundle stays in
// sync with the DB without re-copying roles/templates. Held under the same
// per-blueprint lock as full rebuilds.
func rewriteBundleManifest(bp *core.Record) error {
	unlock := lockBlueprintRebuild(bp.Id)
	defer unlock()

	bundleDir := blueprintBundleDir(bp)
	if _, err := os.Stat(bundleDir); err != nil {
		return fmt.Errorf("bundle dir missing: %w", err)
	}

	manifest := BlueprintManifest{
		ManifestVersion: SupportedManifestVersion,
		ID:              bp.GetString("blueprintID"),
		Name:            bp.GetString("name"),
		Description:     bp.GetString("description"),
		Version:         bp.GetString("version"),
		Tags:            anySliceToStrings(bp.Get("tags")),
		MinLudusVersion: bp.GetString("min_ludus_version"),
		Config:          "range-config.yml",
	}
	data, err := yaml.Marshal(&manifest)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	finalPath := filepath.Join(bundleDir, "blueprint.yml")
	tmpPath := finalPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("install manifest: %w", err)
	}
	return nil
}

func ListBlueprintAccessGroups(e *core.RequestEvent) error {
	blueprintRecord, err := getBlueprintRecordFromRequest(e)
	if err != nil {
		return err
	}

	actingUser := e.Get("user").(*models.User)
	canAccess, err := userCanAccessBlueprint(e, actingUser, blueprintRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking blueprint access: %v", err))
	}
	if !canAccess {
		return JSONError(e, http.StatusForbidden, "You do not have access to this blueprint")
	}

	response := make([]dto.ListBlueprintAccessGroupsResponseItem, 0, len(blueprintRecord.GetStringSlice("sharedGroups")))
	for _, sharedGroupRecordID := range blueprintRecord.GetStringSlice("sharedGroups") {
		groupRecord, err := e.App.FindRecordById("groups", sharedGroupRecordID)
		if err != nil {
			continue
		}

		groupName := strings.TrimSpace(groupRecord.GetString("name"))
		if groupName == "" {
			groupName = sharedGroupRecordID
		}

		managers := resolveUserIDs(e, groupRecord.GetStringSlice("managers"))
		members := resolveUserIDs(e, groupRecord.GetStringSlice("members"))
		sort.Strings(managers)
		sort.Strings(members)

		response = append(response, dto.ListBlueprintAccessGroupsResponseItem{
			GroupName: groupName,
			Managers:  managers,
			Members:   members,
		})
	}

	sort.SliceStable(response, func(i, j int) bool {
		return response[i].GroupName < response[j].GroupName
	})

	return e.JSON(http.StatusOK, response)
}
// ───────────────────────────────────────────────────────────────
// merged from api_blueprint_show.go
// ───────────────────────────────────────────────────────────────

// GetBlueprintDetail handles GET /blueprints/{blueprintID}.
//
// Returns the full blueprint record plus computed dependency status. Works for both
// local blueprints (id = blueprintID) and source-blueprints (id = "<sourceID>/<bpID>").
func GetBlueprintDetail(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	id := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if id == "" {
		return JSONError(e, http.StatusBadRequest, "blueprintID is required")
	}

	bp, err := findLocalBlueprintByID(e, id, user)
	if err != nil {
		return err
	}

	var templates, roles []string
	if srcRecID := bp.GetString("source"); srcRecID != "" {
		// For source-derived blueprints, the inferred lists were precomputed
		// during sync.
		templates = anySliceToStrings(bp.Get("inferred_templates"))
		roles = anySliceToStrings(bp.Get("inferred_roles"))
	} else {
		configBytes, _ := readBlueprintConfigBytes(bp)
		templates, roles, _ = InferFromRangeConfig(configBytes)
	}

	resp := map[string]any{
		"id":                bp.GetString("blueprintID"),
		"name":              bp.GetString("name"),
		"description":       bp.GetString("description"),
		"version":           bp.GetString("version"),
		"tags":              anySliceToStrings(bp.Get("tags")),
		"min_ludus_version": bp.GetString("min_ludus_version"),
		"long_description":  bp.GetString("long_description"),
		"templateStatus":    computeTemplateStatus(e, templates),
		"roleStatus":        computeRoleStatus(e, user, roles),
		"lastInstallStatus": bp.GetString("lastInstallStatus"),
		"lastInstallError":  bp.GetString("lastInstallError"),
		"lastInstalledAt":   bp.GetString("lastInstalledAt"),
	}
	// Publisher fields (authors, homepage, license) come from the parent source
	// for source-derived blueprints.
	if srcRecID := bp.GetString("source"); srcRecID != "" {
		if src, sErr := e.App.FindRecordById("sources", srcRecID); sErr == nil && src != nil {
			resp["authors"] = anySliceToStrings(src.Get("authors"))
			resp["homepage"] = src.GetString("homepage")
			resp["license"] = src.GetString("license")
		}
	}
	return e.JSON(http.StatusOK, resp)
}

// anySliceToStrings converts whatever PocketBase stored for a JSON-array field into a
// concrete []string. PocketBase round-trips JSON columns through different shapes
// depending on driver state ([]any, []string, json.RawMessage, types.JsonArray[string]),
// so we fall back to a JSON marshal/unmarshal pair to handle any of them.
func anySliceToStrings(v any) []string {
	if v == nil {
		return []string{}
	}
	if ss, ok := v.([]string); ok {
		return append([]string{}, ss...)
	}
	if arr, ok := v.([]any); ok {
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	b, err := json.Marshal(v)
	if err != nil {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return []string{}
	}
	return out
}

// ───────────────────────────────────────────────────────────────
// merged from api_blueprint_install.go
// ───────────────────────────────────────────────────────────────

// InstallBlueprintDeps handles POST /blueprints/{blueprintID}/install.
//
// Installs the galaxy/git role dependencies a single blueprint declares. Idempotent:
// re-running on a fully-installed blueprint is fast (ansible-galaxy is idempotent).
//
// blueprintID may be a local blueprintID (e.g. "my-lab") or a source-prefixed ID
// (e.g. "bsl/goad"). Source-prefixed IDs use the <sourceID>/<sourceBlueprintID> form.
func InstallBlueprintDeps(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	id := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if id == "" {
		return JSONError(e, http.StatusBadRequest, "blueprint id is required")
	}

	var req dto.InstallBlueprintDepsRequest
	_ = e.BindBody(&req) // body is optional; defaults leave req zero-valued
	if req.GlobalRoles && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "globalRoles requires admin caller")
	}

	bpRec, err := findLocalBlueprintByID(e, id, user)
	if err != nil {
		return err
	}

	var (
		roleSet          []string
		requirementsYAML []byte
		sourceRecordID   = bpRec.GetString("source") // empty for local blueprints
	)
	statusRec := bpRec

	if sourceRecordID != "" {
		roleSet = append(roleSet, anySliceToStrings(bpRec.Get("inferred_roles"))...)
		requirementsYAML = []byte(bpRec.GetString("requirements_yaml"))
	} else {
		configBytes, cerr := readBlueprintConfigBytes(bpRec)
		if cerr != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("read blueprint config: %v", cerr))
		}
		_, roles, _ := InferFromRangeConfig(configBytes)
		roleSet = roles
	}

	// Dir lets the resolver read subscription_refs.yml from the bundle so
	// declared subscription roles route through the licensed pipeline
	// instead of resolving to a same-named galaxy role.
	walked := WalkedBlueprint{
		Manifest:         &BlueprintManifest{ID: id, Config: "range-config.yml"},
		RequirementsYAML: requirementsYAML,
		Dir:              blueprintBundleDir(bpRec),
	}
	results := installRolesForBlueprint(e, e.App, walked, ResolverOpts{
		ForceRoles:       req.ForceRoles,
		GlobalRoles:      req.GlobalRoles,
		OwnerProxmoxUser: user.ProxmoxUsername(),
		AnsibleHome:      ansibleHomeForUser(user, req.GlobalRoles),
		SourceRecordID:   sourceRecordID,
		PreInferredRoles: roleSet,
	})

	failures := collectArtifactFailures(nil, nil, results)
	markInstallStatusFromFailures(e.App, statusRec, failures)

	return e.JSON(http.StatusOK, map[string]any{
		"blueprintID": id,
		"roleResults": results,
	})
}

func isSourcePrefixedID(id string) bool {
	// blueprintIDRegex already constrains local IDs to at most one internal slash
	// (for subpath IDs like "team/windows"). Source-prefixed IDs always start with
	// a simple slug followed by a slash, so we detect by counting segments:
	// local IDs may also contain a slash (e.g. "team/windows"), so we rely on
	// findSourceByVisibleID returning not-found for those — callers handle the fallthrough.
	//
	// For robustness, check whether the first segment (before the first slash) is a
	// registered sourceID.  We do NOT do the DB lookup here — that would require an
	// *e; instead, we defer the distinction to the handler which already branches on
	// the slash.
	for _, b := range id {
		if b == '/' {
			return true
		}
	}
	return false
}

// splitSourcePrefixedID splits "<sourceID>/<rest>" into a two-element slice.
// Caller must check isSourcePrefixedID first.
func splitSourcePrefixedID(id string) [2]string {
	for i, b := range id {
		if b == '/' {
			return [2]string{id[:i], id[i+1:]}
		}
	}
	return [2]string{id, ""}
}

// findLocalBlueprintByID looks up a local blueprint by blueprintID (with
// fallback to record ID) and runs the access check, returning a JSONError-
// wrapped error so handlers can return it directly.
func findLocalBlueprintByID(e *core.RequestEvent, id string, user *models.User) (*core.Record, error) {
	// Primary lookup: blueprintID field.
	bp, err := e.App.FindFirstRecordByData("blueprints", "blueprintID", id)
	if err != nil && err != sql.ErrNoRows {
		return nil, JSONError(e, http.StatusInternalServerError,
			fmt.Sprintf("error finding blueprint %q: %v", id, err))
	}

	if bp == nil || err == sql.ErrNoRows {
		// Backward compat: try by PocketBase record ID.
		bp, err = e.App.FindRecordById("blueprints", id)
		if err != nil {
			return nil, JSONError(e, http.StatusNotFound,
				fmt.Sprintf("blueprint %q not found", id))
		}
	}

	// Access check — mirrors userCanAccessBlueprint logic used by other handlers.
	canAccess, accessErr := userCanAccessBlueprint(e, user, bp)
	if accessErr != nil {
		return nil, JSONError(e, http.StatusInternalServerError, accessErr.Error())
	}
	if !canAccess {
		return nil, JSONError(e, http.StatusForbidden, "you do not have access to this blueprint")
	}
	return bp, nil
}



// requireUser extracts the authenticated user from the request context. The
// auth middleware sets this on every authenticated request; a missing value
// means the middleware was bypassed or misconfigured. Returns a JSONError so
// callers can `return user, err` directly.
func requireUser(e *core.RequestEvent) (*models.User, error) {
	user, ok := e.Get("user").(*models.User)
	if !ok || user == nil {
		return nil, JSONError(e, http.StatusUnauthorized, "unauthenticated")
	}
	return user, nil
}
// ───────────────────────────────────────────────────────────────
// merged from api_blueprint_import.go
// ───────────────────────────────────────────────────────────────

const maxBundleArchiveBytes = int64(50 * 1024 * 1024) // 50 MB — same as source archive limit

func ImportBlueprint(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, maxBundleArchiveBytes)

	if err := e.Request.ParseMultipartForm(maxBundleArchiveBytes); err != nil {
		if strings.Contains(err.Error(), "request body too large") {
			return JSONError(e, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("upload exceeds %d-byte limit", maxBundleArchiveBytes))
		}
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("parse multipart: %v", err))
	}
	file, _, err := e.Request.FormFile("archive")
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "form field 'archive' (file) is required")
	}
	defer file.Close()

	tmpDir, err := os.MkdirTemp("", "ludus-bp-import-*")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("create tmpdir: %v", err))
	}
	committedTmp := false
	defer func() {
		if !committedTmp {
			os.RemoveAll(tmpDir)
		}
	}()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("not a gzip archive: %v", err))
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	if eErr := extractTar(tr, tmpDir); eErr != nil {
		if strings.Contains(eErr.Error(), "unsafe") ||
			strings.Contains(eErr.Error(), "unsupported entry") ||
			strings.Contains(eErr.Error(), "malformed") {
			return JSONError(e, http.StatusBadRequest, eErr.Error())
		}
		return JSONError(e, http.StatusInternalServerError, eErr.Error())
	}

	// Tarballs may be flat (legacy: blueprint.yml at root) or wrapped in a single
	// top-level directory (current export shape). If we see exactly one subdir
	// and it has blueprint.yml, treat that subdir as the bundle root.
	bundleSrc := tmpDir
	if entries, _ := os.ReadDir(tmpDir); len(entries) == 1 && entries[0].IsDir() {
		candidate := filepath.Join(tmpDir, entries[0].Name())
		if _, statErr := os.Stat(filepath.Join(candidate, "blueprint.yml")); statErr == nil {
			bundleSrc = candidate
		}
	}

	walked, wErr := WalkBlueprintBundle(bundleSrc)
	if wErr != nil || walked == nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid bundle: %v", wErr))
	}

	// Pre-check the blueprintID before extracting the bundle to its final home.
	// Without this, a collision is only caught when Save runs into the unique
	// constraint and surfaces as a generic 500.
	exists, existsErr := blueprintIDExists(e, walked.Manifest.ID)
	if existsErr != nil {
		return JSONError(e, http.StatusInternalServerError,
			fmt.Sprintf("error checking blueprint id %q: %v", walked.Manifest.ID, existsErr))
	}
	if exists {
		return JSONError(e, http.StatusConflict,
			fmt.Sprintf("blueprint %q already exists; rename it in blueprint.yml or delete the existing blueprint first", walked.Manifest.ID))
	}

	bp, crErr := createBlueprintRecord(e, user, walked.Manifest.ID, walked.Manifest.Name, walked.Manifest.Description)
	if crErr != nil {
		return crErr
	}
	// Carry version/tags/min_ludus_version from the manifest so an
	// export-then-import roundtrip preserves them.
	bp.Set("version", walked.Manifest.Version)
	if len(walked.Manifest.Tags) > 0 {
		bp.Set("tags", walked.Manifest.Tags)
	}
	if walked.Manifest.MinLudusVersion != "" {
		bp.Set("min_ludus_version", walked.Manifest.MinLudusVersion)
	}

	bundleRoot := blueprintBundleRoot()
	if mkErr := os.MkdirAll(bundleRoot, 0755); mkErr != nil {
		_ = e.App.Delete(bp)
		return JSONError(e, http.StatusInternalServerError, mkErr.Error())
	}
	finalDir := filepath.Join(bundleRoot, bp.Id)
	if mvErr := os.Rename(bundleSrc, finalDir); mvErr != nil {
		// cross-fs fallback: deep-copy then remove tmp.
		if cpErr := copyDir(bundleSrc, finalDir); cpErr != nil {
			_ = e.App.Delete(bp)
			return JSONError(e, http.StatusInternalServerError, cpErr.Error())
		}
	}
	// tmpDir may still exist as the wrapper around bundleSrc; drop it either way.
	os.RemoveAll(tmpDir)
	committedTmp = true

	bp.Set("bundlePath", finalDir)
	bp.Set("bundle_complete", true)
	if saveErr := e.App.Save(bp); saveErr != nil {
		os.RemoveAll(finalDir)
		_ = e.App.Delete(bp)
		return JSONError(e, http.StatusInternalServerError, saveErr.Error())
	}

	resp := map[string]any{
		"id":          bp.Id,
		"blueprintID": bp.GetString("blueprintID"),
	}
	walked2, _ := WalkBlueprintBundle(finalDir)
	if walked2 != nil {
		res := ResolveAndInstall(e, e.App, *walked2, ResolverOpts{
			OwnerProxmoxUser: user.ProxmoxUsername(),
			AnsibleHome:      ansibleHomeForUser(user, false),
		})
		applyResolverResultToStatus(e.App, bp, res)
		embedArtifactResults(resp, res.TemplateResults, res.LocalRoleResults, res.RoleResults)
	}
	return e.JSON(http.StatusCreated, resp)
}

func extractTar(tr *tar.Reader, tmpDir string) error {
	return extractTarReader(tr, tmpDir, ExtractOptions{RejectSymlinks: true})
}
// ───────────────────────────────────────────────────────────────
// merged from api_blueprint_export.go
// ───────────────────────────────────────────────────────────────

func ExportBlueprint(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}
	id := normalizeBlueprintID(e.Request.PathValue("blueprintID"))
	if id == "" {
		return JSONError(e, http.StatusBadRequest, "blueprint id is required")
	}
	bp, err := findLocalBlueprintByID(e, id, user)
	if err != nil {
		return err
	}
	bundleDir := blueprintBundleDir(bp)
	if bundleDir == "" {
		return JSONError(e, http.StatusUnprocessableEntity,
			"blueprint has no bundle (legacy record); cannot export until bundle is rebuilt")
	}
	if _, statErr := os.Stat(bundleDir); statErr != nil {
		return JSONError(e, http.StatusInternalServerError,
			fmt.Sprintf("bundle dir missing on disk: %v", statErr))
	}

	publicID := bp.GetString("blueprintID")
	e.Response.Header().Set("Content-Type", "application/gzip")
	e.Response.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s.tar.gz"`, sanitiseExportFilename(publicID)))

	gz := gzip.NewWriter(e.Response)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// Wrap entries in a top-level dir so extraction doesn't tar-bomb the user's
	// CWD. Slashes in the public ID (org/team/foo) are flattened to underscores
	// to keep the bundle a single directory entry.
	prefix := sanitiseExportFilename(publicID)

	return filepath.Walk(bundleDir, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, _ := filepath.Rel(bundleDir, p)
		if rel == "." {
			return nil
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(filepath.Join(prefix, rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
		}
		return nil
	})
}

// sanitiseExportFilename strips slashes and `..` so the filename can't escape
// the user's download dir.
func sanitiseExportFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	return strings.ReplaceAll(s, "..", "_")
}

// sanitiseArchiveFilename keeps an upload's filename safe to use as a path
// segment. Falls back to a generic name when the input is empty or strips to
// nothing — the extension matters for ExtractArchive's format detection.
func sanitiseArchiveFilename(s string) string {
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "..", "_")
	s = strings.TrimSpace(s)
	if s == "" {
		return "upload.tar.gz"
	}
	return s
}
