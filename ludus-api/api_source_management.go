package ludusapi

import (
	"context"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

var sourceSlugRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*$`)

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
// upload-type sources require an `archive` file field. Runs sync inline and
// rolls the source row back if the first sync fails.
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
		req.GlobalRoles = e.Request.FormValue("globalRoles") == "true"
		req.Force = e.Request.FormValue("force") == "true"
		req.DryRun = e.Request.FormValue("dryRun") == "true"
	} else {
		if err := e.BindBody(&req); err != nil {
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("invalid request: %v", err))
		}
	}

	if req.GlobalRoles && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "globalRoles requires admin caller")
	}

	if req.Type != "git" && req.Type != "upload" {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("type must be 'git' or 'upload', got %q", req.Type))
	}

	sourceID := strings.TrimSpace(req.ID)
	var err error
	if sourceID == "" {
		sourceID, err = deriveSourceIDFromRequest(&req, e)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, err.Error())
		}
	}
	if !sourceSlugRegex.MatchString(sourceID) {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("sourceID %q does not match %s", sourceID, sourceSlugRegex.String()))
	}
	if reservedSourceIDs[sourceID] {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("sourceID %q is reserved; pick another", sourceID))
	}

	existing, _ := e.App.FindRecordsByFilter("sources",
		"owner = {:o} && sourceID = {:s}", "", 1, 0,
		map[string]any{"o": user.Id, "s": sourceID})
	if len(existing) > 0 {
		return JSONError(e, http.StatusConflict,
			fmt.Sprintf("source %q already exists; pick a different source ID", sourceID))
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
		GlobalRoles:     req.GlobalRoles,
		Force:           req.Force,
		DryRun:          req.DryRun,
		Archive:         uploadBytes,
		ArchiveFilename: uploadFilename,
	}

	// Dry-run: sync, return the plan, roll the row + on-disk checkout back.
	if req.DryRun {
		syncResult, syncErr := runSourceSync(context.Background(), e, e.App, src, opts)
		_ = os.RemoveAll(SourceCheckoutDir(src.Id))
		_ = e.App.Delete(src)
		if syncErr != nil {
			return JSONError(e, http.StatusBadRequest, syncErr.Error())
		}
		if syncResult == nil || syncResult.DryRun == nil {
			return JSONError(e, http.StatusInternalServerError, "no dry-run plan produced")
		}
		return e.JSON(http.StatusOK, syncResult.DryRun)
	}

	// First-sync failure rolls the row back so the caller can retry without rm.
	syncResult, syncErr := runSourceSync(context.Background(), e, e.App, src, opts)
	if syncErr != nil {
		_ = os.RemoveAll(SourceCheckoutDir(src.Id))
		_ = e.App.Delete(src)
		return JSONError(e, http.StatusBadRequest, syncErr.Error())
	}
	return e.JSON(http.StatusOK, sourceSyncResponse(sourceID, syncResult))
}

// sourceSyncResponse wraps a SyncResult for the wire and tags the source
// the result belongs to. Used by both CreateSource and SyncSource.
func sourceSyncResponse(sourceID string, res *SyncResult) map[string]any {
	out := map[string]any{
		"sourceID": sourceID,
	}
	if res != nil {
		embedArtifactResults(out, res.TemplateResults, res.LocalRoleResults, res.RoleResults)
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
	if basename == "" || !sourceSlugRegex.MatchString(basename) || reservedSourceIDs[basename] {
		return "", fmt.Errorf("could not auto-derive sourceID; pass --id explicitly")
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
		req.GlobalRoles = e.Request.FormValue("globalRoles") == "true"
		req.Force = e.Request.FormValue("force") == "true"
		uploadBytes, uploadFilename, _ = readMultipartArchive(e, "archive")
	} else if e.Request.ContentLength > 0 {
		if err := e.BindBody(&req); err != nil {
			return JSONError(e, http.StatusBadRequest, err.Error())
		}
	}

	if req.GlobalRoles && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "globalRoles requires admin caller")
	}
	if uploadBytes != nil && src.GetString("type") != "upload" {
		return JSONError(e, http.StatusBadRequest,
			"archive uploads are only valid for upload-type sources; this source is git-backed")
	}
	if req.Ref != "" && src.GetString("type") != "git" {
		return JSONError(e, http.StatusBadRequest,
			"ref is only meaningful for git-type sources")
	}

	refChanged := false
	if req.Ref != "" && req.Ref != src.GetString("ref") {
		src.Set("ref", req.Ref)
		refChanged = true
	}
	if refChanged {
		if err := e.App.Save(src); err != nil {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
	}

	// New archive bytes on an upload source: re-extract + re-register inline.
	if uploadBytes != nil {
		opts := SyncOptions{
			GlobalRoles:     req.GlobalRoles,
			Force:           req.Force,
			Archive:         uploadBytes,
			ArchiveFilename: uploadFilename,
		}
		syncResult, syncErr := runSourceSync(context.Background(), e, e.App, src, opts)
		if syncErr != nil {
			return JSONError(e, http.StatusBadRequest, syncErr.Error())
		}
		return e.JSON(http.StatusOK, sourceSyncResponse(src.GetString("sourceID"), syncResult))
	}

	return e.JSON(http.StatusOK, sourceRecordToResponseWithKind(e.App, src))
}

// DeleteSource handles DELETE /sources/{sourceID}.
// With purge=true: cascade-removes every template/local-role/galaxy-role
// this source's source_artifacts rows claim, regardless of co-claims from
// other sources. The response carries the sourceIDs of any other sources
// that also claimed those names — those sources are now missing files and
// can re-sync to restore them.
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

	var req dto.DeleteSourceRequest
	_ = e.BindBody(&req) // body is optional

	var purge PurgeResult
	if req.Purge {
		var pErr error
		purge, pErr = purgeSourceArtifacts(e.App, src)
		if pErr != nil {
			return JSONError(e, http.StatusInternalServerError, pErr.Error())
		}
	}

	_ = os.RemoveAll(SourceCheckoutDir(src.Id))

	if err := e.App.Delete(src); err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	resp := dto.DeleteSourceResponse{Status: "deleted"}
	if len(purge.Failures) > 0 {
		resp.Status = "deleted_with_errors"
		resp.PurgeErrors = purge.Failures
	}
	resp.AffectedSources = purge.AffectedSources
	return e.JSON(http.StatusOK, resp)
}

// PurgeResult is what purgeSourceArtifacts hands back to the delete handler.
// Failures are per-artifact errors during removal. AffectedSources are the
// visible sourceIDs of OTHER sources that also claim any of the artifacts we
// just removed — those sources are now missing on-disk files and will need
// to re-sync. We deliberately do NOT skip purging on their behalf: the user
// asked to purge, and we honor that.
type PurgeResult struct {
	Failures        []string
	AffectedSources []string
}

// purgeSourceArtifacts removes every template/role this source's
// source_artifacts rows claim — regardless of whether other sources also
// claim the same name. Per-artifact failures are collected and returned so
// the caller can include them in the response. The function only returns a
// hard error when the artifact lookup itself fails.
func purgeSourceArtifacts(app core.App, src *core.Record) (PurgeResult, error) {
	var res PurgeResult
	affected := map[string]bool{}

	artifacts, err := app.FindRecordsByFilter("source_artifacts",
		"source = {:s}", "", 0, 0, map[string]any{"s": src.Id})
	if err != nil {
		return res, err
	}
	for _, art := range artifacts {
		kind := art.GetString("kind")
		name := art.GetString("name")

		// Collect co-claimants for reporting — they keep their source_artifacts
		// rows, but their on-disk files are about to go away.
		others, _ := app.FindRecordsByFilter("source_artifacts",
			"source != {:s} && kind = {:k} && name = {:n}", "", 0, 0,
			map[string]any{"s": src.Id, "k": kind, "n": name})
		for _, row := range others {
			otherRecID := row.GetString("source")
			if otherRecID == "" || affected[otherRecID] {
				continue
			}
			affected[otherRecID] = true
		}

		var rmErr error
		switch kind {
		case "template":
			rmErr = removeTemplateByName(app, name)
			if rmErr == nil {
				if rec, _ := app.FindFirstRecordByData("templates", "name", name); rec != nil {
					_ = app.Delete(rec)
				}
			}
		case "local_role":
			rmErr = removeLocalRoleByName(app, name, src)
		case "galaxy_role":
			rmErr = removeGalaxyRoleByName(app, name, src)
		}
		if rmErr != nil {
			res.Failures = append(res.Failures, fmt.Sprintf("%s %q: %v", kind, name, rmErr))
		}
	}

	// Resolve affected source record ids to visible sourceIDs for the response.
	for recID := range affected {
		other, lookupErr := app.FindRecordById("sources", recID)
		if lookupErr != nil {
			continue
		}
		visible := other.GetString("sourceID")
		if visible == "" {
			visible = recID
		}
		res.AffectedSources = append(res.AffectedSources, visible)
	}
	sort.Strings(res.AffectedSources)
	return res, nil
}

func removeTemplateByName(_ core.App, name string) error {
	dir := filepath.Join(ludusInstallPath, "packer", name)
	return os.RemoveAll(dir)
}

// removeLocalRoleByName removes a role from the global-roles dir or the
// source owner's per-user roles dir. Idempotent: missing dirs are ignored.
func removeLocalRoleByName(app core.App, name string, src *core.Record) error {
	candidates := []string{
		filepath.Join(ludusInstallPath, "resources", "global-roles", name),
	}
	if owner, err := app.FindRecordById("users", src.GetString("owner")); err == nil {
		if home := userRolesPath(owner.GetString("proxmoxUsername")); home != "" {
			candidates = append(candidates, filepath.Join(home, name))
		}
	}
	for _, base := range candidates {
		_ = os.RemoveAll(base)
	}
	return nil
}

// removeGalaxyRoleByName removes a galaxy-installed role registered by the given
// source. Source-add can install roles either to the global-roles dir (with
// --global-roles) or to the source owner's per-user roles dir, so purge has to
// check both. Errors from individual removals are aggregated; missing dirs are
// not treated as an error.
func removeGalaxyRoleByName(app core.App, name string, src *core.Record) error {
	candidates := []string{
		filepath.Join(ludusInstallPath, "resources", "global-roles", name),
	}
	if owner, err := app.FindRecordById("users", src.GetString("owner")); err == nil {
		if home := userRolesPath(owner.GetString("proxmoxUsername")); home != "" {
			candidates = append(candidates, filepath.Join(home, name))
		}
	}
	for _, dir := range candidates {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			continue
		}
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			return rmErr
		}
	}
	return nil
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
// source: any nonempty subset of {"templates", "roles", "blueprints"}.
// Returns "(empty)" when the source has none.
func computeSourceKind(app core.App, srcID string) string {
	hasBP, _ := app.FindRecordsByFilter("blueprints", "source = {:s}", "", 1, 0,
		map[string]any{"s": srcID})
	hasTpl, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && kind = 'template'", "", 1, 0,
		map[string]any{"s": srcID})
	hasRole, _ := app.FindRecordsByFilter("source_artifacts",
		"source = {:s} && (kind = 'local_role' || kind = 'galaxy_role')", "", 1, 0,
		map[string]any{"s": srcID})

	parts := []string{}
	if len(hasTpl) > 0 {
		parts = append(parts, "templates")
	}
	if len(hasRole) > 0 {
		parts = append(parts, "roles")
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

// findSourceByVisibleID looks up a source by its user-facing sourceID, scoped to
// what the caller can see (owner / shared / admin). Returns a JSONError-wrapped
// error suitable for direct return from handlers.
func findSourceByVisibleID(e *core.RequestEvent, sourceID string) (*core.Record, error) {
	user, ok := e.Get("user").(*models.User)
	if !ok || user == nil {
		return nil, JSONError(e, http.StatusUnauthorized, "unauthenticated")
	}
	// PocketBase enforces uniqueness on (owner, sourceID), not sourceID alone,
	// so two users may legitimately have a source with the same id. Pull up to
	// 2 rows so we can detect collisions and force the caller to disambiguate
	// rather than silently picking one — that path was a real authorization
	// hazard for admin queries.
	var records []*core.Record
	var err error
	if user.IsAdmin() {
		records, err = e.App.FindRecordsByFilter("sources",
			"sourceID = {:s}", "+owner", 2, 0,
			map[string]any{"s": sourceID})
	} else {
		records, err = e.App.FindRecordsByFilter("sources",
			"sourceID = {:s} && owner = {:u}", "+owner", 2, 0,
			map[string]any{"s": sourceID, "u": user.Id})
	}
	if err != nil || len(records) == 0 {
		return nil, JSONError(e, http.StatusNotFound, fmt.Sprintf("source %q not found", sourceID))
	}
	if len(records) > 1 {
		owners := make([]string, 0, len(records))
		for _, r := range records {
			owners = append(owners, resolveOwnerUserID(e, r.GetString("owner")))
		}
		return nil, JSONError(e, http.StatusConflict,
			fmt.Sprintf("multiple sources match %q (owners: %s); re-run with -u <ownerID> to disambiguate",
				sourceID, strings.Join(owners, ", ")))
	}
	// For non-admins, prefer the owned row when both an owned and a shared
	// copy match (impossible in current schema but the +owner sort is stable).
	return records[0], nil
}

// SyncSource handles POST /sources/{sourceID}/sync. Owner-or-admin
// only. For git sources, re-clones at the source's ref and reinstalls. For
// upload sources, reinstalls from the existing on-disk checkout — the same
// archive bytes that were stored when the source was created. To push a new
// archive version for an upload source, PATCH the source with the archive
// instead. dryRun returns the plan without touching state.
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

	var req dto.SyncSourceRequest
	_ = e.BindBody(&req)
	if req.GlobalRoles && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "globalRoles requires admin caller")
	}

	opts := SyncOptions{
		GlobalRoles: req.GlobalRoles,
		Force:       req.Force,
		DryRun:      req.DryRun,
	}

	// Dry-run: run sync synchronously and return the plan; no persisted state changes.
	if req.DryRun {
		result, syncErr := runSourceSync(context.Background(), e, e.App, src, opts)
		if syncErr != nil {
			return JSONError(e, http.StatusInternalServerError, syncErr.Error())
		}
		if result == nil || result.DryRun == nil {
			return JSONError(e, http.StatusInternalServerError, "no dry-run plan produced")
		}
		return e.JSON(http.StatusOK, result.DryRun)
	}

	syncResult, syncErr := runSourceSync(context.Background(), e, e.App, src, opts)
	if syncErr != nil {
		return JSONError(e, http.StatusInternalServerError, syncErr.Error())
	}
	return e.JSON(http.StatusOK, sourceSyncResponse(src.GetString("sourceID"), syncResult))
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
		"inferred_templates": anySliceToStrings(bp.Get("inferred_templates")),
		"inferred_roles":     anySliceToStrings(bp.Get("inferred_roles")),
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

