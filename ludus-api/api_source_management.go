package ludusapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// reservedSourceIDs collides with literal-segment routes registered under
// /sources/. A sourceID equal to any of these would shadow the route and
// make the source unreachable via /sources/{sourceID}.
var reservedSourceIDs = map[string]bool{
	"blueprints": true,
}

// maxSourceArchiveBytes caps the size of an uploaded source archive at the multipart
// parser layer. Must stay aligned with the FileField MaxSize on the sources collection
// (see migrations/1761253001_create_sources_collection.go) so the parser-layer reject
// happens before PocketBase's late save-time reject.
const maxSourceArchiveBytes = int64(50 * 1024 * 1024)

// archiveOverLimit checks Content-Length and wraps the request body with
// MaxBytesReader to enforce the cap even when Content-Length is missing or
// spoofed. Returns true if the request was rejected (response already
// written); callers MUST stop processing in that case.
func archiveOverLimit(e *core.RequestEvent) bool {
	if e.Request.ContentLength > maxSourceArchiveBytes {
		_ = JSONError(e, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("upload exceeds %d-byte limit", maxSourceArchiveBytes))
		return true
	}
	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, maxSourceArchiveBytes)
	return false
}

// CreateSource handles POST /sources. Body is JSON or multipart;
// upload-type sources require an `archive` file field. Register-only: the
// source row is created, the repo is fetched and walked, and the catalog is
// returned. The caller drives the install via POST /sources/{id}/install
// (an absent selection installs everything). Failures during register roll
// the row back so the caller can retry without rm.
func CreateSource(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	if user == nil {
		return JSONError(e, http.StatusUnauthorized, "unauthenticated")
	}

	var req dto.CreateSourceRequest

	contentType := e.Request.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if archiveOverLimit(e) {
			return nil
		}
		if err := e.Request.ParseMultipartForm(maxSourceArchiveBytes); err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				return JSONError(e, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("upload exceeds %d-byte limit", maxSourceArchiveBytes))
			}
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("failed to parse multipart form: %v", err))
		}
		req.ID = strings.TrimSpace(e.Request.FormValue("id"))
		req.Type = strings.TrimSpace(e.Request.FormValue("type"))
		req.URL = strings.TrimSpace(e.Request.FormValue("url"))
		req.Ref = strings.TrimSpace(e.Request.FormValue("ref"))
		req.Global = e.Request.FormValue("global") == "true"
		req.Force = e.Request.FormValue("force") == "true"
	} else {
		if err := e.BindBody(&req); err != nil {
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		}
	}

	if req.Global && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "global requires admin caller")
	}

	if req.Type != "git" && req.Type != "upload" {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("type must be 'git' or 'upload', got %q", req.Type))
	}

	sourceID := strings.TrimSpace(req.ID)
	explicitID := sourceID != ""
	var err error
	if sourceID == "" {
		sourceID, err = deriveSourceIDFromRequest(&req, e)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, err.Error())
		}
	}
	if !dto.SourceIDRegex.MatchString(sourceID) {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("sourceID %q does not match %s", sourceID, dto.SourceIDRegex.String()))
	}
	if reservedSourceIDs[sourceID] {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("sourceID %q is reserved; pick another", sourceID))
	}

	existing, _ := e.App.FindRecordsByFilter("sources",
		"owner = {:o} && sourceID = {:s}", "", 1, 0,
		map[string]any{"o": user.Id, "s": sourceID})
	if len(existing) > 0 {
		// Re-register against an existing source. Three sub-cases:
		//   1. Same sourceID, different type → 409 (e.g. git ID hit with an
		//      upload archive). Avoids silent content swaps.
		//   2. Same sourceID, same type, different git URL → 409. Most likely
		//      a slug collision the caller didn't realise; tell them to
		//      use a different sourceID (a source's URL can't be repointed).
		//   3. Otherwise → idempotent re-walk, preserve status and selection,
		//      return fresh catalog.
		if existing[0].GetString("type") != req.Type {
			return JSONError(e, http.StatusConflict,
				fmt.Sprintf("source %q already exists with type %q; choose a different source ID",
					sourceID, existing[0].GetString("type")))
		}
		if req.Type == "git" && req.URL != "" && req.URL != existing[0].GetString("url") {
			return JSONError(e, http.StatusConflict,
				fmt.Sprintf("source %q already exists pointing at %s; choose a different source ID, or update the existing source's url",
					sourceID, existing[0].GetString("url")))
		}
		// Register-only re-walk: fetch fresh content (for git, re-clone; for
		// upload, leave on-disk content alone unless a new archive came in),
		// recompute the catalog, return.
		walkOpts := SyncOptions{}
		if req.Type == "upload" {
			archiveBytes, archiveName, _ := readMultipartArchive(e, "archive")
			walkOpts.Archive = archiveBytes
			walkOpts.ArchiveFilename = archiveName
		}
		unlock := lockSourceSync(existing[0].Id)
		walked, walkErr := fetchAndWalkSource(e.App, existing[0], walkOpts)
		unlock()
		if walkErr != nil {
			return JSONError(e, http.StatusBadRequest, walkErr.Error())
		}
		catalog := ComputeSourceCatalog(e, existing[0], walked)
		return e.JSON(http.StatusOK, dto.RegisterSourceResponse{
			SourceID: existing[0].GetString("sourceID"),
			Catalog:  toCatalogDTO(catalog),
		})
	}

	// sourceID must be globally unique, not just per-owner. The same-owner case
	// is handled above, so any remaining collision is with another owner. Reject
	// it and have the caller pick a different id — matching how templates, users,
	// and blueprints handle id collisions (no auto-rename).
	if taken, _ := e.App.FindRecordsByFilter("sources",
		"sourceID = {:s}", "", 1, 0, map[string]any{"s": sourceID}); len(taken) > 0 {
		if explicitID {
			return JSONError(e, http.StatusConflict,
				fmt.Sprintf("source id %q is already in use; choose a different id", sourceID))
		}
		return JSONError(e, http.StatusConflict,
			fmt.Sprintf("auto-derived source id %q is already in use; provide an explicit source id", sourceID))
	}

	collection, err := e.App.FindCollectionByNameOrId("sources")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	src := core.NewRecord(collection)
	src.Set("sourceID", sourceID)
	src.Set("name", sourceID) // sync overwrites from source.yml on first run if present
	src.Set("type", req.Type)
	src.Set("owner", user.Id)

	var (
		uploadBytes    []byte
		uploadFilename string
	)
	switch req.Type {
	case "git":
		if req.URL == "" {
			return JSONError(e, http.StatusBadRequest, "url is required for git sources")
		}
		src.Set("url", req.URL)
		ref := req.Ref
		if ref == "" {
			ref = "HEAD"
		}
		src.Set("ref", ref)
	case "upload":
		archiveFile, archiveFilename, readErr := readMultipartArchive(e, "archive")
		if readErr != nil || archiveFile == nil {
			return JSONError(e, http.StatusBadRequest, "upload sources require an 'archive' file field (.tar.gz, .tgz, or .zip)")
		}
		uploadBytes = archiveFile
		uploadFilename = archiveFilename
	}

	if err := e.App.Save(src); err != nil {
		return JSONError(e, http.StatusInternalServerError,
			fmt.Sprintf("save source: %v", err))
	}

	opts := SyncOptions{
		Global:          req.Global,
		Force:           req.Force,
		Archive:         uploadBytes,
		ArchiveFilename: uploadFilename,
	}

	// Register-only: fetch+walk, capture the source manifest on the record,
	// and return the catalog so the caller can drive the picker. Status stays
	// empty until an install is committed — a sync on a register-only source
	// is a benign no-op since nothing was downloaded.
	unlock := lockSourceSync(src.Id)
	walked, walkErr := fetchAndWalkSource(e.App, src, opts)
	if walkErr != nil {
		unlock()
		_ = os.RemoveAll(SourceCheckoutDir(src.Id))
		_ = e.App.Delete(src)
		return JSONError(e, http.StatusBadRequest, walkErr.Error())
	}
	applySourceManifestToRecord(src, walked.Source)
	src.Set("lastSyncError", "")
	if saveErr := e.App.Save(src); saveErr != nil {
		unlock()
		_ = os.RemoveAll(SourceCheckoutDir(src.Id))
		_ = e.App.Delete(src)
		return JSONError(e, http.StatusInternalServerError, saveErr.Error())
	}
	catalog := ComputeSourceCatalog(e, src, walked)
	unlock()
	return e.JSON(http.StatusOK, dto.RegisterSourceResponse{
		SourceID: src.GetString("sourceID"),
		Catalog:  toCatalogDTO(catalog),
	})
}

// sourceSyncResponse wraps a SyncResult for the wire and tags the source
// the result belongs to. Used by both CreateSource and SyncSource.
func sourceSyncResponse(sourceID string, res *SyncResult) map[string]any {
	out := map[string]any{
		"sourceID": sourceID,
	}
	if res != nil {
		out["templateResults"] = res.TemplateResults
		out["localAnsibleResults"] = res.LocalAnsibleResults
		out["blueprintResults"] = res.BlueprintResults
	}
	return out
}

func deriveSourceIDFromRequest(req *dto.CreateSourceRequest, e *core.RequestEvent) (string, error) {
	var basename string
	switch req.Type {
	case "git":
		// `<org>-<repo>` so same-named repos under different orgs don't collide.
		org, repo := gitURLOrgAndRepo(req.URL)
		if org != "" {
			basename = org + "-" + repo
		} else {
			basename = repo
		}
	case "upload":
		basename = lastPathSegment(multipartArchiveFilename(e, "archive"))
	}
	for _, suf := range []string{".tar.gz", ".tgz", ".zip", ".git"} {
		lower := strings.ToLower(basename)
		if strings.HasSuffix(lower, suf) {
			basename = basename[:len(basename)-len(suf)]
			break
		}
	}
	basename = strings.ToLower(basename)
	basename = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(basename, "-")
	basename = regexp.MustCompile(`-+`).ReplaceAllString(basename, "-")
	basename = strings.Trim(basename, "-")
	if basename == "" || !dto.SourceIDRegex.MatchString(basename) || reservedSourceIDs[basename] {
		return "", fmt.Errorf("could not auto-derive sourceID; provide an explicit sourceID override")
	}
	return basename, nil
}

// gitURLOrgAndRepo handles https://, git+ssh://, and git@host:org/repo.git.
// Returns ("", repo) when the URL has no org segment.
func gitURLOrgAndRepo(rawURL string) (string, string) {
	u := strings.TrimSpace(rawURL)
	u = strings.TrimSuffix(u, "/")
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.Index(u, "/"); j >= 0 {
			u = u[j+1:]
		} else {
			u = ""
		}
	} else if i := strings.LastIndex(u, ":"); i >= 0 && !strings.HasPrefix(u, "/") {
		u = u[i+1:]
	}
	parts := strings.Split(u, "/")
	switch len(parts) {
	case 0:
		return "", ""
	case 1:
		return "", parts[0]
	default:
		return parts[len(parts)-2], parts[len(parts)-1]
	}
}

func lastPathSegment(p string) string {
	p = strings.TrimSuffix(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	if i := strings.LastIndex(p, ":"); i >= 0 { // for git@host:org/repo.git
		return p[i+1:]
	}
	return filepath.Base(p)
}

// readMultipartArchive reads the named file field from the multipart form.
// Returns the raw bytes and the original filename. Returns nil, "", nil if the field is absent.
// The caller is responsible for validating that the archive is present when required.
func readMultipartArchive(e *core.RequestEvent, fieldName string) ([]byte, string, error) {
	if e.Request.MultipartForm == nil {
		return nil, "", nil
	}
	file, header, err := e.Request.FormFile(fieldName)
	if err != nil {
		// field not present — treat as absent, not an error
		return nil, "", nil
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, "", fmt.Errorf("reading archive field: %w", err)
	}
	return data, header.Filename, nil
}

func multipartArchiveFilename(e *core.RequestEvent, fieldName string) string {
	if e.Request.MultipartForm == nil {
		return ""
	}
	_, header, err := e.Request.FormFile(fieldName)
	if err != nil {
		return ""
	}
	return header.Filename
}

func ListSources(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	var records []*core.Record
	if user.IsAdmin() {
		records, err = e.App.FindRecordsByFilter("sources", "", "-created", 0, 0, nil)
	} else {
		records, err = e.App.FindRecordsByFilter("sources",
			"owner = {:u}", "-created", 0, 0,
			map[string]any{"u": user.Id})
	}
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	out := make([]dto.SourceResponse, 0, len(records))
	for _, r := range records {
		out = append(out, sourceRecordToResponseWithKind(e.App, r))
	}
	return e.JSON(http.StatusOK, out)
}

func GetSource(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err // already a JSONError
	}
	return e.JSON(http.StatusOK, sourceRecordToResponseWithKind(e.App, src))
}

// UpdateSource handles PATCH /sources/{sourceID}. Body is multipart
// (carries an optional `archive` file plus text fields) or JSON. For
// upload-type sources, an `archive` triggers an inline sync — the response is
// the sync result, not a SourceResponse.
func UpdateSource(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	if !user.IsAdmin() && src.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "only the owner or an admin can update a source")
	}

	var (
		req            dto.UpdateSourceRequest
		uploadBytes    []byte
		uploadFilename string
	)
	contentType := e.Request.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if archiveOverLimit(e) {
			return nil
		}
		if err := e.Request.ParseMultipartForm(maxSourceArchiveBytes); err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				return JSONError(e, http.StatusRequestEntityTooLarge,
					fmt.Sprintf("upload exceeds %d-byte limit", maxSourceArchiveBytes))
			}
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("failed to parse multipart form: %v", err))
		}
		req.Ref = strings.TrimSpace(e.Request.FormValue("ref"))
		req.URL = strings.TrimSpace(e.Request.FormValue("url"))
		req.Global = e.Request.FormValue("global") == "true"
		req.Force = e.Request.FormValue("force") == "true"
		uploadBytes, uploadFilename, _ = readMultipartArchive(e, "archive")
	} else if e.Request.ContentLength > 0 {
		if err := e.BindBody(&req); err != nil {
			return JSONError(e, http.StatusBadRequest, err.Error())
		}
	}

	if req.Global && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "global requires admin caller")
	}
	if uploadBytes != nil && src.GetString("type") != "upload" {
		return JSONError(e, http.StatusBadRequest,
			"archive uploads are only valid for upload-type sources; this source is git-backed")
	}
	if req.Ref != "" && src.GetString("type") != "git" {
		return JSONError(e, http.StatusBadRequest,
			"ref is only meaningful for git-type sources")
	}
	if req.URL != "" && src.GetString("type") != "git" {
		return JSONError(e, http.StatusBadRequest,
			"url is only meaningful for git-type sources")
	}

	changed := false
	if req.Ref != "" && req.Ref != src.GetString("ref") {
		src.Set("ref", req.Ref)
		changed = true
	}
	if req.URL != "" && req.URL != src.GetString("url") {
		// The checkout's origin still points at the old URL and
		// CloneOrUpdateGit only fetches an existing checkout — drop it so
		// the next sync re-clones from the new remote.
		src.Set("url", req.URL)
		_ = os.RemoveAll(SourceCheckoutDir(src.Id))
		changed = true
	}
	if changed {
		if err := e.App.Save(src); err != nil {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
	}

	// New archive bytes on an upload source: re-extract, then re-apply what
	// this source actually has installed (derived from its claims) against
	// the new content — not everything the archive ships.
	if uploadBytes != nil {
		opts := SyncOptions{
			Global:                   req.Global,
			Force:                    req.Force,
			InitiatorIsAdmin:         user.IsAdmin(),
			InitiatorProxmoxUsername: user.ProxmoxUsername(),
			Archive:                  uploadBytes,
			ArchiveFilename:          uploadFilename,
			SelectionFromClaims:      true,
		}
		syncResult, syncErr := runSourceInstall(context.Background(), e, e.App, src, opts)
		if syncErr != nil {
			return JSONError(e, http.StatusBadRequest, syncErr.Error())
		}
		return e.JSON(http.StatusOK, sourceSyncResponse(src.GetString("sourceID"), syncResult))
	}

	return e.JSON(http.StatusOK, sourceRecordToResponseWithKind(e.App, src))
}

// DeleteSource handles DELETE /sources/{sourceID}. Registration-only: it
// drops the source record, which cascade-deletes the blueprints the source
// provided and its source_artifacts rows. Installed templates, roles, and
// collections are left on disk — templates live in each installer's per-user
// packer dir, and roles/collections may be shared with ranges or other
// blueprints. Remove those individually via the templates / ansible delete
// APIs when you actually want them gone.
func DeleteSource(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	if !user.IsAdmin() && src.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "only the owner or an admin can delete a source")
	}

	_ = os.RemoveAll(SourceCheckoutDir(src.Id))

	if err := e.App.Delete(src); err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	return e.JSON(http.StatusOK, dto.DeleteSourceResponse{Status: "deleted"})
}

// sourceRecordToResponse converts a sources PocketBase record to the wire DTO.
// Kind is left as the zero value; use sourceRecordToResponseWithKind when the
// caller can pay the extra DB queries.
func sourceRecordToResponse(r *core.Record) dto.SourceResponse {
	return dto.SourceResponse{
		ID:             r.Id,
		SourceID:       r.GetString("sourceID"),
		Name:           r.GetString("name"),
		Description:    r.GetString("description"),
		Authors:        anySliceToStrings(r.Get("authors")),
		Homepage:       r.GetString("homepage"),
		License:        r.GetString("license"),
		Type:           r.GetString("type"),
		URL:            r.GetString("url"),
		Ref:            r.GetString("ref"),
		OwnerUserID:    r.GetString("owner"), // record-id placeholder; WithKind translates
		LastSyncedAt:   r.GetString("lastSyncedAt"),
		LastSyncStatus: r.GetString("lastSyncStatus"),
		LastSyncError:  r.GetString("lastSyncError"),
	}
}

// computeSourceKind returns a "+" joined string of artifact kinds shipped by a
// source: any nonempty subset of {"templates", "roles", "collections",
// "blueprints"}. Returns "(empty)" when the source has none.
func computeSourceKind(app core.App, srcID string) string {
	hasBP, _ := app.FindRecordsByFilter("blueprints", "source = {:s}", "", 1, 0,
		map[string]any{"s": srcID})
	hasTpl, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = 'template'", "", 1, 0,
		map[string]any{"s": srcID})
	hasRole, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && (kind = 'local_role' || kind = 'galaxy_role')", "", 1, 0,
		map[string]any{"s": srcID})
	hasCol, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = 'collection'", "", 1, 0,
		map[string]any{"s": srcID})

	parts := []string{}
	if len(hasTpl) > 0 {
		parts = append(parts, "templates")
	}
	if len(hasRole) > 0 {
		parts = append(parts, "roles")
	}
	if len(hasCol) > 0 {
		parts = append(parts, "collections")
	}
	if len(hasBP) > 0 {
		parts = append(parts, "blueprints")
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, "+")
}

func sourceRecordToResponseWithKind(app core.App, r *core.Record) dto.SourceResponse {
	resp := sourceRecordToResponse(r)
	resp.Kind = computeSourceKind(app, r.Id)
	if owner, err := app.FindRecordById("users", r.GetString("owner")); err == nil {
		if userID := owner.GetString("userID"); userID != "" {
			resp.OwnerUserID = userID
		}
	}
	return resp
}

// findSourceByVisibleID looks up a source by its user-facing sourceID, scoped
// to what the caller can see (owner / admin). sourceID is globally unique, so
// at most one record ever matches.
func findSourceByVisibleID(e *core.RequestEvent, sourceID string) (*core.Record, error) {
	user, ok := e.Get("user").(*models.User)
	if !ok || user == nil {
		return nil, JSONError(e, http.StatusUnauthorized, "unauthenticated")
	}
	// An admin acting on their own behalf can target any owner's source. An
	// explicit ?userID=X impersonation (middleware already swapped `user` to X)
	// scopes to that user.
	impersonating := e.Request.URL.Query().Get("userID") != ""
	var records []*core.Record
	var err error
	if user.IsAdmin() && !impersonating {
		records, err = e.App.FindRecordsByFilter("sources",
			"sourceID = {:s}", "", 1, 0, map[string]any{"s": sourceID})
	} else {
		records, err = e.App.FindRecordsByFilter("sources",
			"sourceID = {:s} && owner = {:u}", "", 1, 0,
			map[string]any{"s": sourceID, "u": user.Id})
	}
	if err != nil || len(records) == 0 {
		return nil, JSONError(e, http.StatusNotFound, fmt.Sprintf("source %q not found", sourceID))
	}
	return records[0], nil
}

// SyncSource handles POST /sources/{sourceID}/sync. Owner-or-admin only.
// Refresh-only: for git sources, re-pulls the working tree at the source's
// ref and updates the catalog view; for upload sources, just re-walks the
// existing on-disk content. No artifact installs, removes, or DB writes for
// blueprints/templates/roles happen here. To actually apply a change to
// what's installed from a source, call POST /sources/{sourceID}/install.
func SyncSource(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	if !user.IsAdmin() && src.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "only the owner or an admin can sync a source")
	}

	refresh, refreshErr := runSourceRefresh(e.App, src, SyncOptions{})
	if refreshErr != nil {
		return JSONError(e, http.StatusInternalServerError, refreshErr.Error())
	}
	// No install side-effects to report, but undeclared-dep warnings still
	// matter — they tell the user what would be missing if they ran install.
	return e.JSON(http.StatusOK, sourceSyncResponse(src.GetString("sourceID"), &SyncResult{
		BlueprintResults: BlueprintResults{UndeclaredDependencies: refresh.UndeclaredDependencies},
	}))
}

func ListSourceBlueprints(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	records, ferr := e.App.FindRecordsByFilter("blueprints",
		"source = {:s}", "+sourceBlueprintID", 0, 0, map[string]any{"s": src.Id})
	if ferr != nil {
		return JSONError(e, http.StatusInternalServerError, ferr.Error())
	}
	out := make([]dto.SourceBlueprintListItem, 0, len(records))
	for _, r := range records {
		out = append(out, sourceBlueprintToListItem(src, r))
	}
	return e.JSON(http.StatusOK, out)
}

func ListAllSourceBlueprints(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}

	var (
		sources []*core.Record
		srcErr  error
	)
	if user.IsAdmin() {
		sources, srcErr = e.App.FindRecordsByFilter("sources", "", "+sourceID", 0, 0, nil)
	} else {
		sources, srcErr = e.App.FindRecordsByFilter("sources",
			"owner = {:u}", "+sourceID", 0, 0,
			map[string]any{"u": user.Id})
	}
	if srcErr != nil {
		return JSONError(e, http.StatusInternalServerError, srcErr.Error())
	}

	out := []dto.SourceBlueprintListItem{}
	for _, src := range sources {
		bps, _ := e.App.FindRecordsByFilter("blueprints",
			"source = {:s}", "+sourceBlueprintID", 0, 0, map[string]any{"s": src.Id})
		for _, bp := range bps {
			out = append(out, sourceBlueprintToListItem(src, bp))
		}
	}
	return e.JSON(http.StatusOK, out)
}

func GetSourceBlueprintManifest(e *core.RequestEvent) error {
	id := e.Request.PathValue("id")
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 {
		return JSONError(e, http.StatusBadRequest, "id must be in form <sourceID>/<blueprintID>")
	}
	src, err := findSourceByVisibleID(e, parts[0])
	if err != nil {
		return err
	}
	bps, ferr := e.App.FindRecordsByFilter("blueprints",
		"source = {:s} && sourceBlueprintID = {:b}", "", 1, 0,
		map[string]any{"s": src.Id, "b": parts[1]})
	if ferr != nil || len(bps) == 0 {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("source-blueprint %q not found", id))
	}
	bp := bps[0]
	configBytes, _ := readBlueprintConfigBytes(bp)
	templates, roles, _ := InferFromRangeConfig(configBytes)
	return e.JSON(http.StatusOK, map[string]any{
		"sourceBlueprintID":  bp.GetString("sourceBlueprintID"),
		"name":               bp.GetString("name"),
		"description":        bp.GetString("description"),
		"version":            bp.GetString("version"),
		"authors":            anySliceToStrings(src.Get("authors")),
		"homepage":           src.GetString("homepage"),
		"license":            src.GetString("license"),
		"tags":               anySliceToStrings(bp.Get("tags")),
		"min_ludus_version":  bp.GetString("min_ludus_version"),
		"inferred_templates": templates,
		"inferred_roles":     roles,
		"requirements_yaml":  bp.GetString("requirements_yaml"),
	})
}

func sourceBlueprintToListItem(src, bp *core.Record) dto.SourceBlueprintListItem {
	return dto.SourceBlueprintListItem{
		ID:                src.GetString("sourceID") + "/" + bp.GetString("sourceBlueprintID"),
		SourceID:          src.GetString("sourceID"),
		SourceBlueprintID: bp.GetString("sourceBlueprintID"),
		Name:              bp.GetString("name"),
		Description:       bp.GetString("description"),
		Version:           bp.GetString("version"),
		Authors:           anySliceToStrings(src.Get("authors")),
		Homepage:          src.GetString("homepage"),
		License:           src.GetString("license"),
		Tags:              anySliceToStrings(bp.Get("tags")),
		MinLudusVersion:   bp.GetString("min_ludus_version"),
	}
}

// ListSourceTemplates handles GET /sources/{sourceID}/templates.
// Returns templates this source registered, taken from source_artifacts.
func ListSourceTemplates(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	records, ferr := e.App.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = 'template'", "+name", 0, 0,
		map[string]any{"s": src.Id})
	if ferr != nil {
		return JSONError(e, http.StatusInternalServerError, ferr.Error())
	}
	out := make([]dto.ListSourceTemplatesResponseItem, 0, len(records))
	for _, r := range records {
		out = append(out, dto.ListSourceTemplatesResponseItem{
			Name:    r.GetString("name"),
			Version: r.GetString("version"),
		})
	}
	return e.JSON(http.StatusOK, out)
}

// ListSourceCollections handles GET /sources/{sourceID}/collections.
// Returns Ansible collections this source's blueprints declared in their
// requirements.yml. galaxy-declared collections are claim-only rows; a source
// that VENDORS a collection (local_collection) has it cleaned up on de-select
// like a vendored role (ansible-galaxy has no remove subcommand, so Ludus rm's
// the directory directly).
func ListSourceCollections(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	records, ferr := e.App.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = 'collection'", "+name", 0, 0,
		map[string]any{"s": src.Id})
	if ferr != nil {
		return JSONError(e, http.StatusInternalServerError, ferr.Error())
	}
	out := make([]dto.ListSourceCollectionsResponseItem, 0, len(records))
	for _, r := range records {
		out = append(out, dto.ListSourceCollectionsResponseItem{
			Name:    r.GetString("name"),
			Version: r.GetString("version"),
		})
	}
	return e.JSON(http.StatusOK, out)
}

// ListSourceRoles handles GET /sources/{sourceID}/roles.
// Returns roles this source registered. Local vs galaxy is derived from kind.
func ListSourceRoles(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	records, ferr := e.App.FindRecordsByFilter("source_artifacts",
		"source = {:s} && (kind = 'local_role' || kind = 'galaxy_role')",
		"+name", 0, 0,
		map[string]any{"s": src.Id})
	if ferr != nil {
		return JSONError(e, http.StatusInternalServerError, ferr.Error())
	}
	out := make([]dto.ListSourceRolesResponseItem, 0, len(records))
	for _, r := range records {
		scope := "local"
		if r.GetString("kind") == "galaxy_role" {
			scope = "galaxy"
		}
		out = append(out, dto.ListSourceRolesResponseItem{
			Name:    r.GetString("name"),
			Version: r.GetString("version"),
			Scope:   scope,
		})
	}
	return e.JSON(http.StatusOK, out)
}

// GetSourceCatalog handles GET /sources/{sourceID}/catalog. Re-walks the
// existing on-disk checkout, joins it with installed-artifact state, and
// returns the picker-facing view. Read-only — does not refetch from the
// remote.
func GetSourceCatalog(e *core.RequestEvent) error {
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	checkoutDir := SourceCheckoutDir(src.Id)
	walked, werr := WalkSourceRepo(checkoutDir)
	if werr != nil {
		return JSONError(e, http.StatusInternalServerError, werr.Error())
	}
	catalog := ComputeSourceCatalog(e, src, walked)
	return e.JSON(http.StatusOK, toCatalogDTO(catalog))
}

// InstallSource handles POST /sources/{sourceID}/install. Install is
// additive and stateless — it acts only on the selection in the request and
// never uninstalls anything. Selection is optional:
//
//   - absent → install everything the walk ships
//   - present → validated against the walk, then installed as-is
//
// Nothing is persisted: what's installed is recorded by the claims ledger
// (source_artifacts + blueprint rows). Removal goes through the individual
// delete APIs (templates, ansible role/collection, blueprint); a removed
// item stays gone until an install names it (or installs everything) again.
func InstallSource(e *core.RequestEvent) error {
	user, err := requireUser(e)
	if err != nil {
		return err
	}
	src, err := findSourceByVisibleID(e, e.Request.PathValue("sourceID"))
	if err != nil {
		return err
	}
	if !user.IsAdmin() && src.GetString("owner") != user.Id {
		return JSONError(e, http.StatusForbidden, "only the owner or an admin can install a source")
	}

	var req dto.InstallRequest
	if err := e.BindBody(&req); err != nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
	}
	if req.Global && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "global requires admin caller")
	}

	opts := SyncOptions{
		Global:                   req.Global,
		Force:                    req.Force,
		NoDeps:                   req.NoDeps,
		InitiatorIsAdmin:         user.IsAdmin(),
		InitiatorProxmoxUsername: user.ProxmoxUsername(),
	}
	if req.Selection != nil {
		// Caller supplied selection (even if empty). Honor it verbatim;
		// empty arrays are the explicit "uninstall everything" signal.
		opts.Selection = &InstallSelection{
			Blueprints:       req.Selection.Blueprints,
			Templates:        req.Selection.Templates,
			LocalRoles:       req.Selection.LocalRoles,
			LocalCollections: req.Selection.LocalCollections,
		}
	}
	// opts.Selection stays nil only when the caller omitted the selection
	// field entirely. runSourceInstall interprets that as "snapshot the walk."
	result, syncErr := runSourceInstall(context.Background(), e, e.App, src, opts)
	if syncErr != nil {
		if errors.Is(syncErr, errSelectionNotAvailable) {
			return JSONError(e, http.StatusBadRequest, syncErr.Error())
		}
		return JSONError(e, http.StatusInternalServerError, syncErr.Error())
	}
	return e.JSON(http.StatusOK, sourceSyncResponse(src.GetString("sourceID"), result))
}

func isEmptySelection(s dto.InstallSelectionDTO) bool {
	return len(s.Blueprints) == 0 && len(s.Templates) == 0 && len(s.LocalRoles) == 0 && len(s.LocalCollections) == 0
}

// toCatalogDTO copies an internal SourceCatalog onto the wire DTO. Trivial
// field-for-field — kept here so the DTO package stays free of the internal
// catalog types.
func toCatalogDTO(c *SourceCatalog) dto.SourceCatalogDTO {
	if c == nil {
		return dto.SourceCatalogDTO{}
	}
	out := dto.SourceCatalogDTO{
		SourceID:     c.SourceID,
		SourceName:   c.SourceName,
		Description:  c.SourceDescription,
		SourceType:   c.SourceType,
		LastSyncedAt: c.LastSyncedAt,
		Templates:    make([]dto.CatalogItemDTO, 0, len(c.Templates)),
		LocalRoles:   make([]dto.CatalogItemDTO, 0, len(c.LocalRoles)),
	}
	out.Blueprints.Items = make([]dto.CatalogBlueprintDTO, 0, len(c.Blueprints))
	for _, bp := range c.Blueprints {
		out.Blueprints.Items = append(out.Blueprints.Items, dto.CatalogBlueprintDTO{
			ID:                  bp.ID,
			Name:                bp.Name,
			Description:         bp.Description,
			Version:             bp.Version,
			State:               bp.State,
			InstalledVersion:    bp.InstalledVersion,
			RequiredTemplates:   bp.RequiredTemplates,
			RequiredLocalRoles:  bp.RequiredLocalRoles,
			RequiredRoles:       bp.RequiredGalaxyRoles,
			RequiredCollections: bp.RequiredGalaxyCollections,
		})
	}
	out.Templates = catalogItemsToDTO(c.Templates)
	out.LocalRoles = catalogItemsToDTO(c.LocalRoles)
	out.LocalCollections = catalogItemsToDTO(c.LocalCollections)
	out.Blueprints.RequiredRoles = catalogItemsToDTO(c.GalaxyRoles)
	out.Blueprints.RequiredCollections = catalogItemsToDTO(c.GalaxyCollections)
	out.Blueprints.SubscriptionRoles = catalogItemsToDTO(c.SubscriptionRoles)
	out.Blueprints.UndeclaredDependencies = undeclaredDepsToDTO(c.UndeclaredDependencies)
	return out
}

func scopeInstallsToDTO(installs []ScopeInstall) []dto.ScopeInstallDTO {
	if len(installs) == 0 {
		return nil
	}
	out := make([]dto.ScopeInstallDTO, len(installs))
	for i, s := range installs {
		out[i] = dto.ScopeInstallDTO{Scope: s.Scope, Version: s.Version, State: s.State}
	}
	return out
}

func catalogItemsToDTO(items []CatalogItem) []dto.CatalogItemDTO {
	out := make([]dto.CatalogItemDTO, 0, len(items))
	for _, it := range items {
		out = append(out, dto.CatalogItemDTO{
			Name:               it.Name,
			Description:        it.Description,
			Version:            it.Version,
			State:              it.State,
			InstalledVersion:   it.InstalledVersion,
			Global:             it.Global,
			Scopes:             scopeInstallsToDTO(it.Scopes),
			Type:               it.Type,
			Fqcn:               it.Fqcn,
			RequiredBy:         it.RequiredBy,
			VersionByBlueprint: it.VersionByBlueprint,
		})
	}
	return out
}

func undeclaredDepsToDTO(deps []UndeclaredDependency) []dto.UndeclaredDependencyDTO {
	if len(deps) == 0 {
		return nil
	}
	out := make([]dto.UndeclaredDependencyDTO, 0, len(deps))
	for _, d := range deps {
		out = append(out, dto.UndeclaredDependencyDTO{
			BlueprintID:      d.BlueprintID,
			Role:             d.Role,
			Kind:             d.Kind,
			ParentCollection: d.ParentCollection,
		})
	}
	return out
}
