package ludusapi

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// pluginAPIRoute is an API-scoped route (/api/v2/<Pattern>) declared by a
// system-level plugin.
type pluginAPIRoute struct {
	key      string // pluginKey of the declaring plugin
	pluginID string
	route    protocol.Route
	// plugin is the local registry entry; nil for a root-run plugin hosted by
	// the admin service, which the regular service proxies to.
	plugin *managedPlugin
}

func manifestAPIRoutes(manifest protocol.Manifest, key string, plugin *managedPlugin) []pluginAPIRoute {
	var routes []pluginAPIRoute
	for _, route := range manifest.Routes {
		if route.EffectiveScope() == protocol.ScopeAPI {
			routes = append(routes, pluginAPIRoute{key: key, pluginID: manifest.ID, route: route, plugin: plugin})
		}
	}
	return routes
}

var (
	coreAPIRouteMuxOnce sync.Once
	coreAPIRouteMuxes   *http.ServeMux
)

// coreAPIRouteMux matches the core routes registered under APIBasePath. Core
// routes take precedence over the plugin catch-all, so a plugin route one of
// them matches would never be reached.
func coreAPIRouteMux() *http.ServeMux {
	coreAPIRouteMuxOnce.Do(func() {
		mux := http.NewServeMux()
		for _, route := range slices.Concat(routes, pluginResourceRoutes(nil)) {
			mux.Handle(route.Method+" "+APIBasePath+route.Pattern, http.NotFoundHandler())
		}
		coreAPIRouteMuxes = mux
	})
	return coreAPIRouteMuxes
}

// apiRouteConflict rejects an API-scoped route that a core route matches for
// the same method, or that another plugin already declared.
func apiRouteConflict(manifest protocol.Manifest, key string, others []pluginAPIRoute) error {
	for _, route := range manifest.Routes {
		if route.EffectiveScope() != protocol.ScopeAPI {
			continue
		}
		request := &http.Request{Method: route.Method, URL: &url.URL{Path: APIBasePath + route.Pattern}}
		if _, pattern := coreAPIRouteMux().Handler(request); pattern != "" {
			return fmt.Errorf("plugin %s route %s %s conflicts with core route %s", manifest.ID, route.Method, route.Pattern, pattern)
		}
		for _, other := range others {
			if other.key != key && other.route.Method == route.Method && other.route.Pattern == route.Pattern {
				return fmt.Errorf("plugin %s route %s %s is already declared by plugin %s", manifest.ID, route.Method, route.Pattern, other.pluginID)
			}
		}
	}
	return nil
}

// runningAPIRoutesLocked lists the API routes this service dispatches or
// proxies. The caller holds s.pluginMu.
func (s *Server) runningAPIRoutesLocked() []pluginAPIRoute {
	routes := slices.Clone(s.remotePluginRoutes)
	for _, plugin := range s.plugins {
		routes = append(routes, manifestAPIRoutes(plugin.manifest, plugin.key(), plugin)...)
	}
	return routes
}

// declaredAPIRoutes lists the API routes of every active plugin in either
// service: installed packages and approved uploads. Activation and license
// installs check against it.
func (s *Server) declaredAPIRoutes() []pluginAPIRoute {
	var routes []pluginAPIRoute
	for _, plugin := range s.activePluginDeclarations(s.installedPackages()) {
		routes = append(routes, manifestAPIRoutes(plugin.manifest, plugin.key, nil)...)
	}
	return routes
}

// activePluginDeclaration is an active plugin of either service, read from
// its approved declaration without running anything.
type activePluginDeclaration struct {
	key      string
	manifest protocol.Manifest
}

// activePluginDeclarations lists approved uploads, oldest first, then
// installed packages by ID. Uploads come first because activation already
// checked them against every active plugin; an installed package that
// collides with one arrived later.
func (s *Server) activePluginDeclarations(packages []installedPackage) []activePluginDeclaration {
	var active []activePluginDeclaration
	if s.PluginResources != nil {
		records := s.PluginResources.approvedUploads()
		slices.SortFunc(records, func(a, b *core.Record) int {
			if c := strings.Compare(a.GetString("created"), b.GetString("created")); c != 0 {
				return c
			}
			return strings.Compare(a.Id, b.Id)
		})
		for _, record := range records {
			active = append(active, activePluginDeclaration{key: pluginKey(protocol.SourceUploaded, "", record.Id), manifest: resourceManifest(record)})
		}
	}
	for _, pkg := range packages {
		if pkg.manifest.Executable != "" {
			active = append(active, activePluginDeclaration{key: pluginKey(pkg.source, pkg.manifest.ID, ""), manifest: pkg.manifest})
		}
	}
	return active
}

// refreshPluginDeclarations resolves the API routes of every active plugin in
// both services: in the order of activePluginDeclarations, a plugin whose
// route an earlier one claims is refused. Both services compute the same
// result from the same disk and database. It records what this service needs
// of the other service's accepted plugins (proxied routes, VM hooks) and
// returns the refused plugins by key.
func (s *Server) refreshPluginDeclarations(packages []installedPackage) map[string]error {
	runAs := pluginServiceRunAs()
	var claimed, remote []pluginAPIRoute
	var hooks protocol.VMHookCapabilities
	rejected := map[string]error{}
	for _, plugin := range s.activePluginDeclarations(packages) {
		routes := manifestAPIRoutes(plugin.manifest, plugin.key, nil)
		if err := apiRouteConflict(plugin.manifest, plugin.key, claimed); err != nil {
			rejected[plugin.key] = err
			s.logger().Error("Refusing plugin with a conflicting API route", "plugin", plugin.manifest.ID, "error", err)
			continue
		}
		claimed = append(claimed, routes...)
		if plugin.manifest.RunAs != runAs {
			remote = append(remote, routes...)
			hooks = unionVMHooks(hooks, plugin.manifest.VMHooks)
		}
	}
	s.pluginMu.Lock()
	s.remotePluginRoutes = remote
	s.remoteVMHooks = hooks
	s.rejectedPlugins = rejected
	s.pluginMu.Unlock()
	return rejected
}

// pluginAPIRouteFor finds the plugin route for a path relative to APIBasePath.
// Local registry entries win over proxied ones.
func (s *Server) pluginAPIRouteFor(method, path string) (pluginAPIRoute, bool) {
	if s == nil {
		return pluginAPIRoute{}, false
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	for _, plugin := range s.plugins {
		for _, route := range manifestAPIRoutes(plugin.manifest, plugin.key(), plugin) {
			if route.route.Method == method && route.route.Pattern == path {
				return route, true
			}
		}
	}
	for _, route := range s.remotePluginRoutes {
		if route.route.Method == method && route.route.Pattern == path {
			return route, true
		}
	}
	return pluginAPIRoute{}, false
}

// servePluginAPIRoute is the catch-all for /api/v2/{path...}. Core routes are
// more specific, so only unmatched paths reach it. API-scoped routes extend
// Ludus itself, like core routes: requireAuth already ran, and the plugin
// authorizes each caller (host-service methods enforce the caller's access).
// The resource record's sharing settings govern plugin-scoped routes and the
// plugin UI only.
func (s *Server) servePluginAPIRoute(e *core.RequestEvent) error {
	target, ok := s.pluginAPIRouteFor(e.Request.Method, strings.TrimPrefix(e.Request.URL.Path, APIBasePath))
	if !ok || target.plugin == nil {
		return JSONError(e, http.StatusNotFound, "not found")
	}
	return s.handlePluginRoute(e, target.plugin, target.route, false)
}

// handlePluginRoute sends one request to a plugin under a CallID that expires
// when the plugin returns.
func (s *Server) handlePluginRoute(e *core.RequestEvent, plugin *managedPlugin, route protocol.Route, pluginScope bool) error {
	if plugin.manifest.ID == antisandboxPluginID {
		license := s.licenseSnapshot()
		if !license.Valid || !slices.Contains(license.Entitlements, antisandboxEntitlement) {
			return JSONError(e, http.StatusForbidden, "Anti-Sandbox requires a valid entitled license")
		}
	}
	call, done := plugin.host.beginRequest(e)
	defer done()
	request, err := newPluginRequest(e, call, route, plugin.manifest.ForwardCallerCredentials)
	if errors.Is(err, errPluginBodyTooLarge) {
		return JSONError(e, http.StatusRequestEntityTooLarge, err.Error())
	}
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "invalid plugin request: "+err.Error())
	}
	response, err := plugin.rpc.Handle(request)
	if err != nil {
		return JSONError(e, http.StatusBadGateway, fmt.Sprintf("plugin %s request failed: %v", plugin.manifest.ID, err))
	}
	if pluginScope {
		// Plugin-scope responses must not set host cookies or inject active
		// HTML into the authenticated Ludus origin through direct navigation.
		response.Header = filterPluginResponseHeaders(response.Header)
	}
	return writePluginResponse(e, response)
}

func registerPluginAPIRoutes(se *core.ServeEvent, s *Server) {
	se.Router.Any(APIBasePath+"/{path...}", s.servePluginAPIRoute)
}
