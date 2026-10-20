package ludusapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"ludusapi/models"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// PluginResources manages plugin_resources records: inventory, sharing,
// approval, and the uploaded-package store. Plugin processes live in the
// Server registry. Resource operations are serialized; each request re-reads
// its access record. Host-service handlers never take mu.
type PluginResources struct {
	mu              sync.Mutex
	app             core.App
	server          *Server
	root            string // <data_directory>/plugin-packages
	githubTransport http.RoundTripper
}

func newPluginResources(app core.App, server *Server) *PluginResources {
	return &PluginResources{app: app, server: server, root: filepath.Join(app.DataDir(), "plugin-packages")}
}

func recordSource(r *core.Record) protocol.Source {
	return protocol.Source(r.GetString("source"))
}

func pluginAdmin(auth *core.Record) bool {
	return auth != nil && (auth.IsSuperuser() || auth.GetBool("isAdmin"))
}

func pluginVisible(record, auth *core.Record) bool {
	if auth == nil {
		return false
	}
	if pluginAdmin(auth) {
		return true
	}
	if slices.Contains(record.GetStringSlice("excludedUsers"), auth.Id) {
		return false
	}
	return record.GetBool("allUsers") || record.GetString("owner") == auth.Id || slices.Contains(record.GetStringSlice("allowedUsers"), auth.Id)
}

func pluginAvailable(record, auth *core.Record) bool {
	if auth == nil {
		return false
	}
	return pluginAdmin(auth) || record.GetBool("allUsers") || record.GetString("owner") == auth.Id || slices.Contains(record.GetStringSlice("allowedUsers"), auth.Id)
}

func pluginManage(record, auth *core.Record) bool {
	return auth != nil && (pluginAdmin(auth) || (record.GetString("owner") == auth.Id && !record.GetBool("allUsers") && recordSource(record) == protocol.SourceUploaded))
}

func resourceManifest(r *core.Record) protocol.Manifest {
	var m protocol.Manifest
	_ = r.UnmarshalJSONField("manifest", &m)
	m.Normalize()
	return m
}

// manifestRunsAsRoot reports whether the package has a root-run process, which
// only the admin service hosts.
func manifestRunsAsRoot(m protocol.Manifest) bool {
	return m.Executable != "" && m.RunAs == protocol.RunAsRoot
}

// sameManifest compares manifests as stored, where empty and missing lists
// are the same.
func sameManifest(a, b protocol.Manifest) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(encodedA) == string(encodedB)
}

func vmHookNames(hooks protocol.VMHookCapabilities) []string {
	names := []string{}
	for _, hook := range []struct {
		enabled bool
		name    string
	}{{hooks.Select, "select"}, {hooks.Start, "start"}, {hooks.Stop, "stop"}, {hooks.Status, "status"}, {hooks.BeforeDelete, "beforeDelete"}, {hooks.Address, "address"}} {
		if hook.enabled {
			names = append(names, hook.name)
		}
	}
	return names
}

// pluginForRecord returns the running registry entry for a record, if this
// service hosts it.
func (p *PluginResources) pluginForRecord(r *core.Record) *managedPlugin {
	return p.server.pluginByKey(pluginKey(recordSource(r), r.GetString("pluginID"), r.Id))
}

// runtimeState reports an approved record's state. Plugins hosted by the other
// service are reported from their package files, which the admin service
// writes only after approval.
func (p *PluginResources) runtimeState(r *core.Record, m protocol.Manifest) string {
	if m.Executable == "" {
		return "running"
	}
	if m.RunAs == pluginServiceRunAs() {
		if plugin := p.pluginForRecord(r); plugin != nil && !plugin.exited() {
			return "running"
		}
		return "unavailable"
	}
	marker := filepath.Join(p.server.pluginsRoot(), r.GetString("pluginID"), protocol.ManifestFile)
	if recordSource(r) == protocol.SourceUploaded {
		marker = filepath.Join(p.server.uploadedRootPackageDir(r.Id), pluginApprovalFile)
	}
	if FileExists(marker) {
		return "running"
	}
	return "unavailable"
}

func (p *PluginResources) response(r, auth *core.Record) map[string]any {
	m := resourceManifest(r)
	source := recordSource(r)
	state := r.GetString("state")
	if state == "approved" {
		state = p.runtimeState(r, m)
	}
	apiRoutes := []map[string]string{}
	for _, route := range m.Routes {
		if route.EffectiveScope() == protocol.ScopeAPI {
			apiRoutes = append(apiRoutes, map[string]string{"method": route.Method, "pattern": route.Pattern})
		}
	}
	result := map[string]any{"id": m.ID, "name": m.Name, "version": m.Version, "description": m.Description, "author": m.Author,
		"resourceID": r.Id, "isOwner": auth != nil && r.GetString("owner") == auth.Id,
		"owner": r.GetString("owner"), "allUsers": r.GetBool("allUsers"), "state": state, "hasUI": m.UI != "", "rangeScoped": m.RangeScoped,
		"source": source, "level": m.Level, "runAs": m.RunAs, "forwardCallerCredentials": m.ForwardCallerCredentials,
		"apiRoutes": apiRoutes, "vmHooks": vmHookNames(m.VMHooks),
		"canManage": pluginManage(r, auth), "canActivate": pluginAdmin(auth) && source == protocol.SourceUploaded,
		"canUninstall": pluginManage(r, auth) && source == protocol.SourceUploaded, "sha256": r.GetString("sha256"), "error": r.GetString("error")}
	result["added"] = pluginVisible(r, auth)
	if owner, err := p.app.FindRecordById("users", r.GetString("owner")); err == nil {
		result["ownerUserID"] = owner.GetString("userID")
	}
	if pluginManage(r, auth) {
		userIDs := []string{}
		for _, id := range r.GetStringSlice("allowedUsers") {
			if user, err := p.app.FindRecordById("users", id); err == nil {
				userIDs = append(userIDs, user.GetString("userID"))
			}
		}
		result["allowedUsers"] = userIDs
	}
	return result
}

func (p *PluginResources) record(e *core.RequestEvent) (*core.Record, error) {
	return p.resolve(e.Auth, e.Request.PathValue("pluginID"), false)
}

// Clients address a specific installation by resourceID. Slug URLs prefer the
// caller's personal copy, then a global copy; ambiguity fails closed.
func (p *PluginResources) resolve(auth *core.Record, id string, available bool) (*core.Record, error) {
	if auth == nil {
		return nil, fmt.Errorf("authentication required")
	}
	visible := func(r *core.Record) bool { return pluginVisible(r, auth) || (available && pluginAvailable(r, auth)) }
	if r, err := p.app.FindRecordById("plugin_resources", id); err == nil {
		if visible(r) {
			return r, nil
		}
		return nil, fmt.Errorf("plugin not found")
	}
	if !protocol.ValidPluginID(id) {
		return nil, fmt.Errorf("plugin not found")
	}
	records, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"pluginID": id})
	if err != nil {
		return nil, fmt.Errorf("plugin not found")
	}
	var matches []*core.Record
	var global *core.Record
	for _, r := range records {
		if !visible(r) {
			continue
		}
		uploaded := recordSource(r) == protocol.SourceUploaded
		if r.GetString("owner") == auth.Id && !r.GetBool("allUsers") && uploaded {
			return r, nil
		}
		if r.GetBool("allUsers") && uploaded {
			global = r
		}
		matches = append(matches, r)
	}
	if global != nil {
		return global, nil
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return nil, fmt.Errorf("plugin not found or ambiguous; select an installation from Plugins")
}

// rootPluginRecord resolves the record a /plugins/{pluginID}/... request
// addresses and reports whether its process runs as root.
func (p *PluginResources) rootPluginRecord(auth *core.Record, pluginID string) bool {
	if p == nil || auth == nil {
		return false
	}
	r, err := p.resolve(auth, pluginID, false)
	return err == nil && manifestRunsAsRoot(resourceManifest(r))
}

// installedRecordID returns the record mirroring a server or licensed package.
func (p *PluginResources) installedRecordID(pluginID string) string {
	records, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"pluginID": pluginID}, dbx.Not(dbx.HashExp{"source": string(protocol.SourceUploaded)}))
	if err != nil || len(records) == 0 {
		return ""
	}
	return records[0].Id
}

// approvedUploads lists approved uploaded records with an executable. It does
// not take p.mu.
func (p *PluginResources) approvedUploads() []*core.Record {
	records, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"state": "approved", "source": string(protocol.SourceUploaded)})
	if err != nil {
		p.server.logger().Error("load approved plugin resources", "error", err)
		return nil
	}
	return slices.DeleteFunc(records, func(r *core.Record) bool { return resourceManifest(r).Executable == "" })
}

// approvedRootUpload reports whether a root-owned uploaded copy still has an
// approved, root-run record. Only a missing record or one in another state
// withdraws the approval; any other lookup failure is returned.
func (p *PluginResources) approvedRootUpload(resourceID string) (bool, error) {
	r, err := p.app.FindRecordById("plugin_resources", resourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return recordSource(r) == protocol.SourceUploaded && r.GetString("state") == "approved" && manifestRunsAsRoot(resourceManifest(r)), nil
}

func (p *PluginResources) List(e *core.RequestEvent) error {
	if e.Auth == nil {
		return JSONError(e, 401, "authentication required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	records, err := p.app.FindAllRecords("plugin_resources")
	if err != nil {
		return JSONError(e, 500, err.Error())
	}
	items := make([]map[string]any, 0)
	for _, r := range records {
		if pluginVisible(r, e.Auth) || (e.Request.URL.Query().Get("available") == "true" && pluginAvailable(r, e.Auth)) {
			items = append(items, p.response(r, e.Auth))
		}
	}
	return e.JSON(200, map[string]any{"plugins": items, "isAdmin": pluginAdmin(e.Auth)})
}

func (p *PluginResources) Add(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.resolve(e.Auth, e.Request.PathValue("pluginID"), true)
	if err != nil || !pluginAvailable(r, e.Auth) {
		return JSONError(e, 404, "plugin not found")
	}
	excluded := slices.DeleteFunc(r.GetStringSlice("excludedUsers"), func(id string) bool { return id == e.Auth.Id })
	r.Set("excludedUsers", excluded)
	if err := p.app.Save(r); err != nil {
		return JSONError(e, 500, err.Error())
	}
	return e.JSON(200, p.response(r, e.Auth))
}

func (p *PluginResources) Upload(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.IsSuperuser() {
		return JSONError(e, 403, "upload using a Ludus user account")
	}
	allUsers := false
	if value := e.Request.URL.Query().Get("allUsers"); value != "" {
		var err error
		allUsers, err = strconv.ParseBool(value)
		if err != nil {
			return JSONError(e, 400, "invalid allUsers value")
		}
	}
	if allUsers && !pluginAdmin(e.Auth) {
		return JSONError(e, 403, "only administrators can install global plugins")
	}
	var data []byte
	var err error
	mediaType, _, _ := mime.ParseMediaType(e.Request.Header.Get("Content-Type"))
	if mediaType == "application/json" {
		e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, 4096)
		var body struct {
			GitHubURL string `json:"githubURL"`
		}
		decoder := json.NewDecoder(e.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			return JSONError(e, 400, "invalid GitHub import request")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return JSONError(e, 400, "invalid GitHub import request")
		}
		var status int
		data, status, err = importGitHubPlugin(e.Request.Context(), body.GitHubURL, p.githubTransport)
		if err != nil {
			return JSONError(e, status, err.Error())
		}
	} else {
		e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, pluginUploadLimit)
		data, err = io.ReadAll(e.Request.Body)
		if err != nil {
			return JSONError(e, 413, "plugin package exceeds 128 MiB")
		}
	}
	pkg, err := readPluginPackage(data)
	if err != nil {
		return JSONError(e, 400, err.Error())
	}
	if pkg.manifest.ID == "ludus-guacamole" && !pluginAdmin(e.Auth) {
		return JSONError(e, 403, "only administrators can install the Guacamole plugin")
	}
	// Runs after the lock is released: a superseded approved root-run package
	// is withdrawn from the admin service right away.
	var superseded *core.Record
	defer func() {
		if superseded != nil {
			p.withdrawSupersededRootUpload(e, superseded)
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	filter := dbx.HashExp{"pluginID": pkg.manifest.ID, "allUsers": allUsers, "source": string(protocol.SourceUploaded)}
	if !allUsers {
		filter["owner"] = e.Auth.Id
	}
	var existing *core.Record
	var lookupErr error
	if target := e.Request.URL.Query().Get("resourceID"); target != "" {
		if e.Request.URL.Query().Get("replace") != "true" {
			return JSONError(e, 400, "resourceID requires explicit replacement")
		}
		existing, lookupErr = p.app.FindRecordById("plugin_resources", target)
		if lookupErr != nil || !pluginVisible(existing, e.Auth) {
			return JSONError(e, 404, "plugin not found")
		}
		if existing.GetString("pluginID") != pkg.manifest.ID {
			return JSONError(e, 400, "package ID does not match the selected plugin")
		}
	} else {
		var records []*core.Record
		records, lookupErr = p.app.FindAllRecords("plugin_resources", filter)
		if len(records) > 0 {
			existing = records[0]
		}
	}
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return JSONError(e, 500, "could not look up plugin")
	}
	if existing != nil {
		if !pluginManage(existing, e.Auth) || recordSource(existing) != protocol.SourceUploaded {
			return JSONError(e, 403, "only the manager of an uploaded plugin can replace it")
		}
		if e.Request.URL.Query().Get("replace") != "true" {
			return JSONError(e, 409, "plugin ID already installed; select Replace existing version to update it")
		}
		if allUsers && !existing.GetBool("allUsers") {
			return JSONError(e, 400, "replacement preserves access; change global scope using Access")
		}
	}
	ownerID := e.Auth.Id
	if existing != nil {
		ownerID = existing.GetString("owner")
	}
	// Bound the number of packages owned by one user.
	owned, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"owner": ownerID})
	if err != nil {
		return JSONError(e, 500, err.Error())
	}
	if existing == nil && len(owned) >= 20 {
		return JSONError(e, 409, "twenty stored packages per user; ask an administrator to archive unused packages")
	}
	// Bound pending submissions per user as well as each upload size.
	pending, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"owner": ownerID, "state": "pending"})
	if err != nil {
		return JSONError(e, 500, err.Error())
	}
	if len(pending) >= 5 && (existing == nil || existing.GetString("state") != "pending") {
		return JSONError(e, 409, "at most five uploads may await activation")
	}
	c, err := p.app.FindCollectionByNameOrId("plugin_resources")
	if err != nil {
		return JSONError(e, 500, err.Error())
	}
	r := core.NewRecord(c)
	if existing != nil {
		r = existing.Fresh()
	}
	r.Set("pluginID", pkg.manifest.ID)
	r.Set("source", string(protocol.SourceUploaded))
	r.Set("owner", e.Auth.Id)
	r.Set("allowedUsers", []string{e.Auth.Id})
	r.Set("excludedUsers", []string{})
	r.Set("allUsers", allUsers)
	if existing != nil {
		for _, field := range []string{"owner", "allowedUsers", "excludedUsers", "allUsers", "created"} {
			r.Set(field, existing.Get(field))
		}
	}
	r.Set("manifest", pkg.manifest)
	r.Set("sha256", pkg.digest)
	// Every upload, including a replacement, waits for an administrator to
	// review its declarations.
	r.Set("state", "pending")
	r.Set("error", "")
	if existing != nil {
		err = p.replacePackage(r, pkg)
	} else {
		err = p.app.RunInTransaction(func(tx core.App) error {
			if err := tx.Save(r); err != nil {
				return err
			}
			return storePluginPackage(p.root, r.Id, pkg)
		})
		if err != nil && len(r.Id) == 15 {
			_ = os.RemoveAll(filepath.Join(p.root, r.Id))
		}
	}
	if err != nil {
		return JSONError(e, 500, "could not store plugin package: "+err.Error())
	}
	if existing != nil {
		if existing.GetString("state") == "approved" && manifestRunsAsRoot(resourceManifest(existing)) {
			superseded = existing
		}
		p.server.stopPluginByKey(pluginKey(protocol.SourceUploaded, "", existing.Id))
	}
	return e.JSON(201, p.response(r, e.Auth))
}

// replacePackage stages bytes before touching the current package. Resource
// operations hold p.mu, so no request can observe the filesystem swap before
// the corresponding record update commits. Failed updates restore the old files.
func (p *PluginResources) replacePackage(r *core.Record, pkg *pluginPackage) error {
	stage, err := os.MkdirTemp(p.root, ".replacement-")
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := storePluginPackage(stage, r.Id, pkg); err != nil {
		return err
	}
	current, backup := filepath.Join(p.root, r.Id), filepath.Join(stage, "previous")
	movedOld, installed := false, false
	err = p.app.RunInTransaction(func(tx core.App) error {
		if err := tx.Save(r); err != nil {
			return err
		}
		if err := os.Rename(current, backup); err != nil {
			return err
		}
		movedOld = true
		if err := os.Rename(filepath.Join(stage, r.Id), current); err != nil {
			return err
		}
		installed = true
		return nil
	})
	if err != nil && movedOld {
		if installed {
			if removeErr := os.RemoveAll(current); removeErr != nil {
				cleanup = false
				return fmt.Errorf("%w; previous package retained at %s: %v", err, backup, removeErr)
			}
		}
		if restoreErr := os.Rename(backup, current); restoreErr != nil {
			cleanup = false
			return fmt.Errorf("%w; previous package retained at %s: %v", err, backup, restoreErr)
		}
	}
	return err
}

// withdrawSupersededRootUpload asks the admin service to stop the root-run
// plugin of a replaced record and delete its root-owned copy. The admin
// service's periodic cleanup is the backstop if the call fails.
func (p *PluginResources) withdrawSupersededRootUpload(e *core.RequestEvent, r *core.Record) {
	if err := withdrawRootUpload(e, r.Id); err != nil {
		p.server.logger().Error("could not withdraw the superseded root-run plugin; the admin service removes it within a minute", "plugin", r.GetString("pluginID"), "resourceID", r.Id, "error", err)
	}
	p.server.refreshPluginDeclarations(p.server.installedPackages())
}

// Withdraw stops and deletes the root-owned copy of an uploaded package whose
// record is gone or no longer approved. It can only take an approval away.
func (p *PluginResources) Withdraw(e *core.RequestEvent) error {
	if e.Auth == nil {
		return JSONError(e, 401, "authentication required")
	}
	resourceID := e.Request.PathValue("pluginID")
	if !pluginRecordIDPattern.MatchString(resourceID) {
		return JSONError(e, 404, "plugin not found")
	}
	if pluginServiceRunAs() != protocol.RunAsRoot {
		return JSONError(e, 409, "root-owned plugin copies are managed by the admin service")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	approved, err := p.approvedRootUpload(resourceID)
	if err != nil {
		p.server.logger().Error("could not check plugin approval", "resourceID", resourceID, "error", err)
		return JSONError(e, 503, "could not check the plugin approval")
	}
	if approved {
		return JSONError(e, 409, "the plugin is still approved")
	}
	p.server.removeUploadedRootPackage(resourceID)
	return e.JSON(200, map[string]bool{"withdrawn": true})
}

// activationRequest echoes what the administrator reviewed.
type activationRequest struct {
	SHA256 string         `json:"sha256"`
	Level  protocol.Level `json:"level"`
	RunAs  protocol.RunAs `json:"runAs"`
}

// Activate approves an uploaded package. The regular service proxies root-run
// packages to the admin service, which copies them into root-owned storage.
func (p *PluginResources) Activate(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.record(e)
	if err != nil {
		return JSONError(e, 404, err.Error())
	}
	if !pluginAdmin(e.Auth) {
		return JSONError(e, 403, "only administrators can activate plugins")
	}
	if recordSource(r) != protocol.SourceUploaded {
		return JSONError(e, 409, "installed plugins are approved by their installation")
	}
	var body activationRequest
	if err := e.BindBody(&body); err != nil {
		return JSONError(e, 400, "invalid activation request")
	}
	m := resourceManifest(r)
	if body.SHA256 != r.GetString("sha256") || body.Level != m.Level || body.RunAs != m.RunAs {
		return JSONError(e, 409, "the package changed after it was reviewed; reload the plugin and review it again")
	}
	// 409 is reserved for "the package changed after review", which the GUI
	// answers by reloading. A route conflict needs a different package.
	if err := apiRouteConflict(m, pluginKey(protocol.SourceUploaded, "", r.Id), p.server.declaredAPIRoutes()); err != nil {
		return JSONError(e, http.StatusUnprocessableEntity, err.Error())
	}
	if err := p.start(r, m); err != nil {
		r.Set("error", err.Error())
		r.Set("state", "error")
		_ = p.app.Save(r)
		return JSONError(e, 400, err.Error())
	}
	r.Set("state", "approved")
	r.Set("error", "")
	if err := p.app.Save(r); err != nil {
		p.stop(r)
		return JSONError(e, 500, err.Error())
	}
	return e.JSON(200, p.response(r, e.Auth))
}

// start runs an uploaded package in this service. A root-run package first
// gets a root-owned copy built from the stored ZIP whose SHA-256 the
// administrator approved; the uploaded files themselves are never trusted.
func (p *PluginResources) start(r *core.Record, m protocol.Manifest) error {
	if m.ID == "ludus-guacamole" && !trustedGuacamoleResource(p.app, r) {
		return fmt.Errorf("only administrator-owned Guacamole plugins may be activated")
	}
	if m.Executable == "" {
		return nil
	}
	if m.RunAs != pluginServiceRunAs() {
		return fmt.Errorf("plugin %s runs as %s and is activated through the %s service", m.ID, m.RunAs, m.RunAs)
	}
	key := pluginKey(protocol.SourceUploaded, "", r.Id)
	if plugin := p.server.pluginByKey(key); plugin != nil {
		if !plugin.exited() {
			return nil
		}
		p.server.stopPlugin(plugin, false)
	}
	if m.RunAs == protocol.RunAsLudus {
		dir := filepath.Join(p.root, r.Id)
		if err := os.Chmod(filepath.Join(dir, protocol.ExecutableFile), 0700); err != nil {
			return err
		}
		_, err := p.server.startPlugin(dir, m, protocol.SourceUploaded, r.Id)
		return err
	}
	data, err := readStoredPluginArchive(p.root, r.Id)
	if err != nil {
		return fmt.Errorf("read stored package: %w", err)
	}
	if sha256Hex(data) != r.GetString("sha256") {
		return errors.New("stored package does not match the reviewed SHA-256")
	}
	pkg, err := readPluginPackage(data)
	if err != nil {
		return err
	}
	if !sameManifest(pkg.manifest, m) {
		return errors.New("stored package declarations differ from the reviewed plugin.json")
	}
	if err := writeRootPackage(p.server.uploadedRootPackageDir(r.Id), pkg, pluginApproval{Source: protocol.SourceUploaded, ResourceID: r.Id}); err != nil {
		return fmt.Errorf("install root-owned package: %w", err)
	}
	if _, err := p.server.startUploadedRootPackage(r.Id); err != nil {
		p.server.removeUploadedRootPackage(r.Id)
		return err
	}
	return nil
}

// stop ends an uploaded plugin hosted by this service; the admin service also
// removes the root-owned copy.
func (p *PluginResources) stop(r *core.Record) {
	if pluginServiceRunAs() == protocol.RunAsRoot {
		p.server.removeUploadedRootPackage(r.Id)
		return
	}
	p.server.stopPluginByKey(pluginKey(protocol.SourceUploaded, "", r.Id))
}

func (p *PluginResources) Access(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.record(e)
	if err != nil {
		return JSONError(e, 404, err.Error())
	}
	if !pluginManage(r, e.Auth) {
		return JSONError(e, 403, "only the owner or an administrator can change access")
	}
	var body struct {
		AllUsers     bool     `json:"allUsers"`
		AllowedUsers []string `json:"allowedUsers"`
	}
	if err := e.BindBody(&body); err != nil {
		return JSONError(e, 400, "invalid access request")
	}
	if body.AllUsers != r.GetBool("allUsers") && !pluginAdmin(e.Auth) {
		return JSONError(e, 403, "only administrators can change all-users access")
	}
	if body.AllUsers != r.GetBool("allUsers") && recordSource(r) == protocol.SourceUploaded {
		filter := dbx.HashExp{"pluginID": r.GetString("pluginID"), "allUsers": body.AllUsers, "source": string(protocol.SourceUploaded)}
		if !body.AllUsers {
			filter["owner"] = r.GetString("owner")
		}
		records, err := p.app.FindAllRecords("plugin_resources", filter)
		if err != nil {
			return JSONError(e, 500, "could not check plugin scope")
		}
		if len(records) > 0 {
			return JSONError(e, 409, "a copy already exists in the requested scope")
		}
	}
	if len(body.AllowedUsers) > 100 {
		return JSONError(e, 400, "at most 100 users may be granted access")
	}
	allowed := []string{}
	for _, id := range append(body.AllowedUsers, r.GetString("owner")) {
		if id == "" || slices.Contains(allowed, id) {
			continue
		}
		user, err := p.app.FindFirstRecordByData("users", "userID", id)
		if err != nil {
			user, err = p.app.FindRecordById("users", id)
		}
		if err != nil {
			return JSONError(e, 400, "unknown Ludus user ID: "+id)
		}
		if !slices.Contains(allowed, user.Id) {
			allowed = append(allowed, user.Id)
		}
	}
	r.Set("allowedUsers", allowed)
	r.Set("allUsers", body.AllUsers)
	// Explicitly granting an individual access restores an earlier opt-out.
	excluded := r.GetStringSlice("excludedUsers")
	excluded = slices.DeleteFunc(excluded, func(id string) bool { return slices.Contains(allowed, id) })
	r.Set("excludedUsers", excluded)
	if err := p.app.Save(r); err != nil {
		return JSONError(e, 500, err.Error())
	}
	return e.JSON(200, p.response(r, e.Auth))
}

func (p *PluginResources) Remove(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.record(e)
	if err != nil {
		return JSONError(e, 404, err.Error())
	}
	uploaded := recordSource(r) == protocol.SourceUploaded
	if e.Request.URL.Query().Get("uninstall") == "true" || (e.Auth != nil && r.GetString("owner") == e.Auth.Id && !r.GetBool("allUsers") && uploaded) {
		if !pluginManage(r, e.Auth) {
			return JSONError(e, 403, "only the owner of a personal plugin or an administrator can delete it")
		}
		if !uploaded {
			return JSONError(e, 409, "remove this installed plugin using its server installer")
		}
		if manifestRunsAsRoot(resourceManifest(r)) != (pluginServiceRunAs() == protocol.RunAsRoot) {
			return JSONError(e, 500, "this plugin is managed by the other Ludus service")
		}
		// Stage the package so a database failure can put it back. Plugin-created
		// data and changes inside range machines are outside package ownership.
		staging, err := os.MkdirTemp(p.root, ".deleting-"+r.Id+"-")
		if err != nil {
			return JSONError(e, 500, "could not stage plugin deletion: "+err.Error())
		}
		packagePath := filepath.Join(p.root, r.Id)
		stagedPackage := filepath.Join(staging, "package")
		moved := false
		if err := os.Rename(packagePath, stagedPackage); err == nil {
			moved = true
		} else if !os.IsNotExist(err) {
			_ = os.Remove(staging)
			return JSONError(e, 500, "could not stage plugin package: "+err.Error())
		}
		if err := p.app.Delete(r); err != nil {
			if moved {
				if restoreErr := os.Rename(stagedPackage, packagePath); restoreErr != nil {
					p.server.logger().Error("could not restore plugin package after deletion failure", "plugin", r.GetString("pluginID"), "error", restoreErr)
				}
			}
			_ = os.Remove(staging)
			return JSONError(e, 500, "could not delete plugin record: "+err.Error())
		}
		p.stop(r)
		if err := os.RemoveAll(staging); err != nil {
			p.server.logger().Error("could not remove deleted plugin package", "plugin", r.GetString("pluginID"), "error", err)
			return JSONError(e, 500, "plugin record deleted but package cleanup failed: "+err.Error())
		}
	} else {
		if e.Auth == nil {
			return JSONError(e, 401, "authentication required")
		}
		if pluginAdmin(e.Auth) {
			return JSONError(e, 409, "administrators retain management visibility; use uninstall to remove the plugin")
		}
		excluded := r.GetStringSlice("excludedUsers")
		if !slices.Contains(excluded, e.Auth.Id) {
			excluded = append(excluded, e.Auth.Id)
		}
		r.Set("excludedUsers", excluded)
		if err := p.app.Save(r); err != nil {
			return JSONError(e, 500, err.Error())
		}
	}
	return e.JSON(200, map[string]bool{"removed": true})
}

// UI returns ui/index.html from the package directory.
func (p *PluginResources) UI(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.record(e)
	if err != nil {
		return JSONError(e, 404, err.Error())
	}
	m := resourceManifest(r)
	if r.GetString("state") != "approved" || m.UI == "" {
		return JSONError(e, 409, "plugin frontend is not active")
	}
	dir := filepath.Join(p.server.pluginsRoot(), r.GetString("pluginID"))
	if recordSource(r) == protocol.SourceUploaded {
		dir = filepath.Join(p.root, r.Id)
	}
	html, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(protocol.UIFile)))
	if err != nil || len(html) == 0 || len(html) > pluginUILimit {
		return JSONError(e, 500, "plugin frontend unavailable")
	}
	e.Response.Header().Set("Cache-Control", "no-store")
	// Return data, not executable HTML on the Ludus origin. The GUI places it
	// in an opaque-origin iframe with a restricted host message bridge.
	return e.JSON(200, map[string]any{"html": string(html), "bridgeVersion": 1})
}

// RPC dispatches plugin-scope routes (/plugins/{pluginID}/rpc/<Pattern>).
func (p *PluginResources) RPC(e *core.RequestEvent) error {
	p.mu.Lock()
	r, err := p.record(e)
	if err != nil {
		p.mu.Unlock()
		return JSONError(e, 404, err.Error())
	}
	m := resourceManifest(r)
	var plugin *managedPlugin
	if r.GetString("state") == "approved" {
		plugin = p.pluginForRecord(r)
	}
	p.mu.Unlock()
	if plugin == nil {
		return JSONError(e, 503, "plugin is unavailable")
	}
	if m.RangeScoped {
		if e.Request.URL.Query().Get("rangeID") == "" {
			return JSONError(e, 400, "select a range")
		}
		if rng, ok := e.Get("range").(*models.Range); !ok || rng == nil {
			return JSONError(e, 403, "range access required")
		}
	}
	path := "/" + e.Request.PathValue("pluginPath")
	for _, route := range plugin.manifest.Routes {
		if route.EffectiveScope() == protocol.ScopePlugin && route.Pattern == path && route.Method == e.Request.Method {
			return p.server.handlePluginRoute(e, plugin, route, true)
		}
	}
	return JSONError(e, 404, "plugin route not declared")
}

func filterPluginResponseHeaders(headers map[string][]string) map[string][]string {
	filtered := map[string][]string{"X-Content-Type-Options": {"nosniff"}, "Content-Security-Policy": {"sandbox; default-src 'none'"}, "Cache-Control": {"no-store"}}
	for key, value := range headers {
		if slices.Contains([]string{"content-type", "content-disposition"}, strings.ToLower(key)) {
			filtered[key] = value
		}
	}
	return filtered
}

// mirrorInstalled keeps one record per server or licensed package so they
// share the inventory, sharing, and visibility rules of uploads. Installed
// packages are administrator-visible until deliberately shared; an owner is
// never inferred.
func (p *PluginResources) mirrorInstalled(packages []installedPackage, rejected map[string]error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	collection, err := p.app.FindCollectionByNameOrId("plugin_resources")
	if err != nil {
		p.server.logger().Error("load plugin resources collection", "error", err)
		return
	}
	installed := map[string]bool{}
	for _, pkg := range packages {
		installed[pkg.manifest.ID] = true
		r := core.NewRecord(collection)
		if id := p.installedRecordID(pkg.manifest.ID); id != "" {
			if existing, err := p.app.FindRecordById("plugin_resources", id); err == nil {
				r = existing
			}
		}
		if r.IsNew() {
			r.Set("pluginID", pkg.manifest.ID)
			r.Set("allUsers", false)
			r.Set("allowedUsers", []string{})
			r.Set("excludedUsers", []string{})
		}
		sum := pkg.sha256
		if sum == "" && pkg.manifest.Executable != "" {
			previous := resourceManifest(r)
			sum = r.GetString("sha256")
			if r.IsNew() || sum == "" || previous.Version != pkg.manifest.Version || recordSource(r) != pkg.source {
				if sum, err = fileSHA256(filepath.Join(pkg.dir, protocol.ExecutableFile)); err != nil {
					p.server.logger().Error("hash installed plugin", "plugin", pkg.manifest.ID, "error", err)
				}
			}
		}
		state, message := "approved", ""
		if err := rejected[pluginKey(pkg.source, pkg.manifest.ID, "")]; err != nil {
			state, message = "error", err.Error()
		}
		if !r.IsNew() && sameManifest(resourceManifest(r), pkg.manifest) && recordSource(r) == pkg.source &&
			r.GetString("sha256") == sum && r.GetString("state") == state && r.GetString("error") == message {
			continue
		}
		r.Set("source", string(pkg.source))
		r.Set("manifest", pkg.manifest)
		r.Set("sha256", sum)
		r.Set("state", state)
		r.Set("error", message)
		if err := p.app.Save(r); err != nil {
			p.server.logger().Error("mirror installed plugin", "plugin", pkg.manifest.ID, "error", err)
		}
	}
	records, err := p.app.FindAllRecords("plugin_resources", dbx.Not(dbx.HashExp{"source": string(protocol.SourceUploaded)}))
	if err != nil {
		p.server.logger().Error("load installed plugin resources", "error", err)
		return
	}
	for _, r := range records {
		if installed[r.GetString("pluginID")] || r.GetString("state") == "error" {
			continue
		}
		r.Set("state", "error")
		r.Set("error", "the package is no longer installed")
		if err := p.app.Save(r); err != nil {
			p.server.logger().Error("mark removed plugin", "plugin", r.GetString("pluginID"), "error", err)
		}
	}
}

// flagRejectedUploads records why approved uploads were refused for a
// conflicting API route; they leave the approved state and stop.
func (p *PluginResources) flagRejectedUploads(rejected map[string]error) {
	if len(rejected) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.approvedUploads() {
		err := rejected[pluginKey(protocol.SourceUploaded, "", r.Id)]
		if err == nil {
			continue
		}
		r.Set("state", "error")
		r.Set("error", err.Error())
		if saveErr := p.app.Save(r); saveErr != nil {
			p.server.logger().Error("flag refused plugin", "plugin", r.GetString("pluginID"), "error", saveErr)
			continue
		}
		p.server.stopPluginByKey(pluginKey(protocol.SourceUploaded, "", r.Id))
	}
}

// startUploaded starts approved uploads this (regular) service hosts.
// Root-run uploads start only from the admin service's root-owned copies.
func (p *PluginResources) startUploaded() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range p.approvedUploads() {
		m := resourceManifest(r)
		if m.RunAs != pluginServiceRunAs() {
			continue
		}
		if err := p.start(r, m); err != nil {
			r.Set("state", "error")
			r.Set("error", err.Error())
			_ = p.app.Save(r)
		}
	}
}

func pluginResourceRoutes(p *PluginResources) PocketBaseRoutes {
	routes := PocketBaseRoutes{
		{"ListPlugins", http.MethodGet, "/plugins", p.List},
		{"InstallPlugin", http.MethodPost, "/plugins/install", p.Upload},
		{"AddPlugin", http.MethodPost, "/plugins/{pluginID}/add", p.Add},
		{"ActivatePlugin", http.MethodPost, "/plugins/{pluginID}/activate", p.Activate},
		{"WithdrawRootPlugin", http.MethodPost, "/plugins/{pluginID}/withdraw", p.Withdraw},
		{"SetPluginAccess", http.MethodPut, "/plugins/{pluginID}/access", p.Access},
		{"RemovePlugin", http.MethodDelete, "/plugins/{pluginID}", p.Remove},
		{"GetPluginUI", http.MethodGet, "/plugins/{pluginID}/ui", p.UI},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		routes = append(routes, PocketBaseRoute{"PluginRPC", method, "/plugins/{pluginID}/rpc/{pluginPath...}", p.RPC})
	}
	return routes
}
