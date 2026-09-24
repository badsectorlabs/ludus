package ludusapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"ludusapi/models"
	"ludusapi/pluginrpc"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type resourceRuntime struct {
	plugin *managedPlugin
	cancel context.CancelFunc
	system bool
}

// Resource operations are serialized; each request re-reads its access record.
// These ACLs govern use of trusted plugins, not isolation of server executables.
type PluginResources struct {
	mu       sync.Mutex
	app      core.App
	server   *Server
	root     string
	runtimes map[string]*resourceRuntime
}

func newPluginResources(app core.App, server *Server) *PluginResources {
	return &PluginResources{app: app, server: server, root: filepath.Join(app.DataDir(), "plugin-packages"), runtimes: map[string]*resourceRuntime{}}
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
	if record.GetString("state") == "removed" {
		return record.GetString("owner") == auth.Id && !record.GetBool("allUsers")
	}
	if slices.Contains(record.GetStringSlice("excludedUsers"), auth.Id) {
		return false
	}
	return record.GetBool("allUsers") || record.GetString("owner") == auth.Id || slices.Contains(record.GetStringSlice("allowedUsers"), auth.Id)
}

func pluginAvailable(record, auth *core.Record) bool {
	if auth == nil || record.GetString("state") == "removed" {
		return false
	}
	return pluginAdmin(auth) || record.GetBool("allUsers") || record.GetString("owner") == auth.Id || slices.Contains(record.GetStringSlice("allowedUsers"), auth.Id)
}

func pluginManage(record, auth *core.Record) bool {
	return auth != nil && (pluginAdmin(auth) || (record.GetString("owner") == auth.Id && !record.GetBool("allUsers") && !record.GetBool("system")))
}

func resourceManifest(r *core.Record) PluginManifest {
	var m PluginManifest
	_ = r.UnmarshalJSONField("manifest", &m)
	return m
}

func (p *PluginResources) response(r, auth *core.Record) map[string]any {
	m := resourceManifest(r)
	state := r.GetString("state")
	if state == "approved" {
		if runtime := p.runtimes[r.Id]; runtime != nil && (runtime.plugin == nil || runtime.plugin.client == nil || !runtime.plugin.client.Exited()) {
			state = "running"
		} else {
			state = "unavailable"
		}
	}
	result := map[string]any{"id": m.ID, "name": m.Name, "version": m.Version, "description": m.Description, "author": m.Author,
		"resourceID": r.Id, "isOwner": auth != nil && r.GetString("owner") == auth.Id,
		"owner": r.GetString("owner"), "allUsers": r.GetBool("allUsers"), "state": state, "hasUI": m.UI != "", "rangeScoped": m.RangeScoped,
		"system": r.GetBool("system"), "canManage": pluginManage(r, auth), "canActivate": pluginManage(r, auth) && !r.GetBool("system"),
		"canUninstall": pluginManage(r, auth) && !r.GetBool("system"), "sha256": r.GetString("sha256"), "error": r.GetString("error")}
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

// New clients address a specific installation by resourceID. Legacy slug URLs
// prefer the caller's personal copy, then a global copy; ambiguity fails closed.
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
	if !pluginIDPattern.MatchString(id) {
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
		if r.GetString("owner") == auth.Id && !r.GetBool("allUsers") && !r.GetBool("system") {
			return r, nil
		}
		if r.GetBool("allUsers") && !r.GetBool("system") {
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
	e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, pluginUploadLimit)
	data, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return JSONError(e, 413, "plugin package exceeds 128 MiB")
	}
	pkg, err := readPluginPackage(data)
	if err != nil {
		return JSONError(e, 400, err.Error())
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	filter := dbx.HashExp{"pluginID": pkg.manifest.ID, "allUsers": allUsers, "system": false}
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
		if !pluginManage(existing, e.Auth) || existing.GetBool("system") {
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
	r.Set("pluginID", pkg.manifest.ID)
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
	r.Set("state", "pending")
	// Stage into a fresh generated record directory. A rejected or failed upload
	// leaves both the previous database record and running process intact.
	if err := p.app.RunInTransaction(func(tx core.App) error {
		if existing != nil {
			if err := tx.Delete(existing); err != nil {
				return err
			}
		}
		if err := tx.Save(r); err != nil {
			return err
		}
		return storePluginPackage(p.root, r.Id, pkg)
	}); err != nil {
		if len(r.Id) == 15 {
			_ = os.RemoveAll(filepath.Join(p.root, r.Id))
		}
		return JSONError(e, 500, "could not store plugin package: "+err.Error())
	}
	if existing != nil {
		p.stop(existing.Id)
		// This directory belongs only to the superseded generated package record;
		// plugin-created data is separate and remains available to the new version.
		if err := os.RemoveAll(filepath.Join(p.root, existing.Id)); err != nil {
			p.server.Logger.Error("could not remove superseded plugin package", "plugin", pkg.manifest.ID, "error", err)
		}
	}
	return e.JSON(201, p.response(r, e.Auth))
}

func validateResourceMetadata(m PluginManifest, metadata pluginrpc.Metadata) error {
	if metadata.ID != m.ID || metadata.Name != m.Name || metadata.Version != m.Version {
		return fmt.Errorf("executable identity does not match plugin.json")
	}
	// Lifecycle hooks affect VM operations outside a user request. Until those
	// hooks carry resource scope, use the existing administrator-installed path.
	if metadata.VMHooks != (pluginrpc.VMHookCapabilities{}) {
		return fmt.Errorf("VM lifecycle plugins require server-side installation; resource access cannot scope lifecycle hooks yet")
	}
	seen := map[string]bool{}
	for _, route := range metadata.Routes {
		if route.Name == "" || route.Pattern == "" || !strings.HasPrefix(route.Pattern, "/") || strings.ContainsAny(route.Pattern, "?#{}\\") || strings.Contains(route.Pattern, "..") {
			return fmt.Errorf("resource plugin routes must be exact absolute paths")
		}
		if !slices.Contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, route.Method) {
			return fmt.Errorf("unsupported plugin route method")
		}
		key := route.Method + " " + route.Pattern
		if seen[key] {
			return fmt.Errorf("duplicate plugin route")
		}
		seen[key] = true
	}
	return nil
}

func (p *PluginResources) start(r *core.Record) error {
	m := resourceManifest(r)
	if runtime, exists := p.runtimes[r.Id]; exists {
		if runtime.plugin == nil || runtime.plugin.client == nil || !runtime.plugin.client.Exited() {
			return nil
		}
		p.stop(r.Id)
	}
	if m.Executable == "" {
		p.runtimes[r.Id] = &resourceRuntime{}
		return nil
	}
	path := filepath.Join(p.root, r.Id, "plugin")
	if err := os.Chmod(path, 0700); err != nil {
		return err
	}
	runtime, err := startManagedPlugin(path, p.server.Logger)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			runtime.client.Kill()
		}
	}()
	if err := validateResourceMetadata(m, runtime.metadata); err != nil {
		return err
	}
	ConfigMu.RLock()
	configuration, err := json.Marshal(ServerConfiguration)
	ConfigMu.RUnlock()
	if err != nil {
		return err
	}
	connection, err := p.server.pluginPocketBaseConnection()
	if err != nil {
		return err
	}
	response, err := runtime.rpc.Initialize(pluginrpc.InitializeRequest{Configuration: configuration, Server: p.server.pluginState(), UseSDN: true, PocketBase: connection})
	if err != nil {
		return err
	}
	for _, job := range response.Jobs {
		if job.Name == "" || job.Interval < time.Second {
			return fmt.Errorf("invalid scheduled job")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.runtimes[r.Id] = &resourceRuntime{plugin: runtime, cancel: cancel}
	runtime.initialized = true
	// Resource plugins never replace host license state or register unscoped
	// legacy routes. Jobs terminate with their resource runtime.
	for _, declared := range response.Jobs {
		job := declared
		go func() {
			ticker := time.NewTicker(job.Interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if _, err := runtime.rpc.RunJob(job.Name); err != nil {
						p.server.Logger.Error("plugin job failed", "plugin", m.ID, "job", job.Name, "error", err)
					}
				}
			}
		}()
	}
	keep = true
	return nil
}

func (p *PluginResources) Activate(e *core.RequestEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, err := p.record(e)
	if err != nil {
		return JSONError(e, 404, err.Error())
	}
	if !pluginManage(r, e.Auth) {
		return JSONError(e, 403, "only the owner of a personal plugin or an administrator can activate it")
	}
	if r.GetBool("system") {
		return JSONError(e, 409, "system plugins are activated by the server installer")
	}
	if err := p.start(r); err != nil {
		r.Set("error", err.Error())
		r.Set("state", "error")
		_ = p.app.Save(r)
		return JSONError(e, 400, err.Error())
	}
	r.Set("state", "approved")
	r.Set("error", "")
	if err := p.app.Save(r); err != nil {
		p.stop(r.Id)
		return JSONError(e, 500, err.Error())
	}
	return e.JSON(200, p.response(r, e.Auth))
}

func (p *PluginResources) stop(id string) {
	if runtime := p.runtimes[id]; runtime != nil {
		if runtime.cancel != nil {
			runtime.cancel()
		}
		// Killing the process also interrupts RPC requests/jobs that are blocked.
		if runtime.plugin != nil && !runtime.system {
			runtime.plugin.client.Kill()
		}
		delete(p.runtimes, id)
	}
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
	if body.AllUsers != r.GetBool("allUsers") && !r.GetBool("system") {
		filter := dbx.HashExp{"pluginID": r.GetString("pluginID"), "allUsers": body.AllUsers, "system": false}
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
	if e.Request.URL.Query().Get("uninstall") == "true" || (e.Auth != nil && r.GetString("owner") == e.Auth.Id && !r.GetBool("allUsers") && !r.GetBool("system")) {
		if !pluginManage(r, e.Auth) {
			return JSONError(e, 403, "only the owner of a personal plugin or an administrator can delete it")
		}
		if r.GetBool("system") {
			return JSONError(e, 409, "remove this system plugin using its server installer")
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
					p.server.Logger.Error("could not restore plugin package after deletion failure", "plugin", r.GetString("pluginID"), "error", restoreErr)
				}
			}
			_ = os.Remove(staging)
			return JSONError(e, 500, "could not delete plugin record: "+err.Error())
		}
		p.stop(r.Id)
		if err := os.RemoveAll(staging); err != nil {
			p.server.Logger.Error("could not remove deleted plugin package", "plugin", r.GetString("pluginID"), "error", err)
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
	runtime := p.runtimes[r.Id]
	if runtime == nil {
		return JSONError(e, 503, "plugin is unavailable")
	}
	var html []byte
	if runtime.system {
		if runtime.plugin.metadata.UI != nil {
			html = []byte(runtime.plugin.metadata.UI.HTML)
		}
	} else {
		html, err = os.ReadFile(filepath.Join(p.root, r.Id, "ui", "index.html"))
	}
	if err != nil || len(html) == 0 || len(html) > pluginUILimit {
		return JSONError(e, 500, "plugin frontend unavailable")
	}
	e.Response.Header().Set("Cache-Control", "no-store")
	// Return data, not executable HTML on the Ludus origin. The GUI places it
	// in an opaque-origin iframe with a restricted host message bridge.
	return e.JSON(200, map[string]any{"html": string(html), "bridgeVersion": 1})
}

func (p *PluginResources) RPC(e *core.RequestEvent) error {
	p.mu.Lock()
	r, err := p.record(e)
	if err != nil {
		p.mu.Unlock()
		return JSONError(e, 404, err.Error())
	}
	runtime := p.runtimes[r.Id]
	if r.GetString("state") != "approved" || runtime == nil || runtime.plugin == nil {
		p.mu.Unlock()
		return JSONError(e, 503, "plugin is unavailable")
	}
	m := resourceManifest(r)
	plugin := runtime.plugin
	p.mu.Unlock()
	if m.RangeScoped {
		if e.Request.URL.Query().Get("rangeID") == "" {
			return JSONError(e, 400, "select a range")
		}
		if rng, ok := e.Get("range").(*models.Range); !ok || rng == nil {
			return JSONError(e, 403, "range access required")
		}
	}
	path := "/" + e.Request.PathValue("pluginPath")
	for _, route := range plugin.metadata.Routes {
		if route.Pattern != path || route.Method != e.Request.Method {
			continue
		}
		e.Request.Body = http.MaxBytesReader(e.Response, e.Request.Body, 8<<20)
		request, err := PluginRequestFromEvent(e)
		if err != nil {
			return JSONError(e, 400, "invalid plugin request")
		}
		request.Route = route.Name
		request.Path = APIBasePath + route.Pattern
		response, err := plugin.rpc.Handle(request)
		if err != nil {
			return JSONError(e, 502, "plugin request failed: "+err.Error())
		}
		// Do not allow plugin responses to set host cookies or inject active HTML
		// into the authenticated Ludus origin through a direct navigation.
		response.Header = filterPluginResponseHeaders(response.Header)
		return WritePluginResponse(e, response)
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

func (p *PluginResources) startup() {
	p.mu.Lock()
	defer p.mu.Unlock()
	// System-installed plugins are administrator-visible until deliberately
	// shared. Never infer an owner or silently publish them to all users.
	for _, plugin := range p.server.pluginSnapshot() {
		meta := plugin.metadata
		if !pluginIDPattern.MatchString(meta.ID) {
			continue
		}
		records, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"pluginID": meta.ID, "system": true})
		if err != nil {
			p.server.Logger.Error("load system plugin resource", "error", err)
			continue
		}
		var r *core.Record
		if len(records) > 0 {
			r = records[0]
		}
		if r == nil {
			c, err := p.app.FindCollectionByNameOrId("plugin_resources")
			if err != nil {
				continue
			}
			r = core.NewRecord(c)
			r.Set("pluginID", meta.ID)
			r.Set("system", true)
			r.Set("allUsers", false)
		}
		m := PluginManifest{SchemaVersion: 1, ID: meta.ID, Name: meta.Name, Version: meta.Version, Description: meta.Description, Author: meta.Author, Protocol: pluginrpc.ProtocolVersion}
		if meta.UI != nil && meta.UI.HTML != "" {
			m.UI = "ui/index.html"
			m.RangeScoped = meta.UI.RangeScoped
		}
		r.Set("manifest", m)
		r.Set("state", "approved")
		if err := p.app.Save(r); err == nil {
			p.runtimes[r.Id] = &resourceRuntime{plugin: plugin, system: true}
		}
	}
	records, err := p.app.FindAllRecords("plugin_resources", dbx.HashExp{"state": "approved", "system": false})
	if err != nil {
		p.server.Logger.Error("load plugin resources", "error", err)
		return
	}
	for _, r := range records {
		if err := p.start(r); err != nil {
			r.Set("state", "error")
			r.Set("error", err.Error())
			_ = p.app.Save(r)
		}
	}
}

func (p *PluginResources) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id := range p.runtimes {
		p.stop(id)
	}
}

func registerPluginResourceRoutes(se *core.ServeEvent, p *PluginResources) {
	base := APIBasePath + "/plugins"
	se.Router.GET(base, p.List)
	se.Router.POST(base+"/install", p.Upload)
	se.Router.POST(base+"/{pluginID}/add", p.Add)
	se.Router.POST(base+"/{pluginID}/activate", p.Activate)
	se.Router.PUT(base+"/{pluginID}/access", p.Access)
	se.Router.DELETE(base+"/{pluginID}", p.Remove)
	se.Router.GET(base+"/{pluginID}/ui", p.UI)
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		se.Router.Route(method, base+"/{pluginID}/rpc/{pluginPath...}", p.RPC)
	}
}
