package ludusapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"ludusapi/models"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// Plugin apps are browser applications served by plugins that declare
// "app": true. A plugin opens a session for the caller of one of its
// requests (Host.OpenApp); the host serves the session at
// /plugin-apps/{resourceID}/{sessionID}/ on the Ludus origin, authenticates
// every request with a session cookie set by a one-time launch ticket, and
// proxies it to the plugin over the plugin's private broker stream. The host
// owns session lifetime: it expires sessions, ends them when the caller loses
// access, and closes their open connections.
const (
	appSessionLease        = time.Hour
	appTicketLifetime      = 30 * time.Second
	appCheckInterval       = 15 * time.Second
	appSessionCookie       = "ludus_app"
	appLaunchPath          = ".ludus/launch"
	appTicketsPerSession   = 8
	appMaxLaunchPathLength = 2048
)

// pluginAppTransport is the HTTP client side of a plugin's app stream.
type pluginAppTransport struct {
	transport *http.Transport
	close     func()
}

func newPluginAppTransport(dial func() (net.Conn, error), closeFn func()) *pluginAppTransport {
	transport := &http.Transport{
		DialContext:         func(context.Context, string, string) (net.Conn, error) { return dial() },
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	}
	return &pluginAppTransport{transport: transport, close: func() {
		transport.CloseIdleConnections()
		closeFn()
	}}
}

// connectPluginApp dials the app stream the plugin reported at Initialize.
// The stream must match the approved "app" declaration.
func connectPluginApp(plugin *managedPlugin, stream uint32) error {
	if stream == 0 {
		if plugin.manifest.App {
			return fmt.Errorf("plugin %s declares an app but did not serve one", plugin.manifest.ID)
		}
		return nil
	}
	if !plugin.manifest.App {
		return fmt.Errorf("plugin %s serves an app it did not declare", plugin.manifest.ID)
	}
	dialer, ok := plugin.rpc.(interface {
		DialApp(uint32) (*yamux.Session, error)
	})
	if !ok {
		return fmt.Errorf("plugin %s app stream is unavailable", plugin.manifest.ID)
	}
	session, err := dialer.DialApp(stream)
	if err != nil {
		return fmt.Errorf("connect to plugin %s app: %w", plugin.manifest.ID, err)
	}
	plugin.app = newPluginAppTransport(func() (net.Conn, error) { return session.Open() }, func() { _ = session.Close() })
	return nil
}

type appTicket struct {
	path    string
	expires time.Time
}

// appSession is one browser session of a plugin app. Fields after the
// identity are guarded by pluginAppRegistry.mu.
type appSession struct {
	id, resourceID, base, cookie string
	plugin                       *managedPlugin
	authID, userID               string // users record IDs: principal and effective user
	call                         protocol.CallID
	callDone                     func()
	ctx                          context.Context // canceled when the session ends
	cancel                       context.CancelFunc

	expires time.Time
	tickets map[string]appTicket
	auth    protocol.Principal
	user    *protocol.User
	ended   bool
	// unverifiedSince is when checks started failing to read the session's
	// records; zero while verified.
	unverifiedSince time.Time
}

type pluginAppRegistry struct {
	server   *Server
	mu       sync.Mutex
	sessions map[string]*appSession
	limits   map[*managedPlugin]appSessionLimits // unset: protocol defaults
	reaper   sync.Once
	now      func() time.Time
}

type appSessionLimits struct{ perUser, total int }

func (s *Server) pluginApps() *pluginAppRegistry {
	s.appsOnce.Do(func() {
		s.apps = &pluginAppRegistry{server: s, sessions: map[string]*appSession{}, limits: map[*managedPlugin]appSessionLimits{}, now: time.Now}
	})
	return s.apps
}

// setLimits sets a plugin's session limits for later OpenApp calls.
func (a *pluginAppRegistry) setLimits(plugin *managedPlugin, perUser, total int) *protocol.HostError {
	if perUser < 1 || perUser > total || total > protocol.MaxAppSessions {
		return hostErrorf(protocol.CodeInvalid, "app session limits must satisfy 1 <= per user <= total <= %d", protocol.MaxAppSessions)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.limits[plugin] = appSessionLimits{perUser: perUser, total: total}
	return nil
}

func randomHex(bytes int) string {
	raw := make([]byte, bytes)
	if _, err := rand.Read(raw); err != nil {
		panic(fmt.Sprintf("generate random identifier: %v", err))
	}
	return hex.EncodeToString(raw)
}

// requestTokenExpiry is the expiry of the request's JWT, or zero for API keys.
// The authentication middleware has already verified the token.
func requestTokenExpiry(r *http.Request) time.Time {
	token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if token == "" {
		return time.Time{}
	}
	claims, err := security.ParseUnverifiedJWT(token)
	if err != nil {
		return time.Time{}
	}
	expires, err := claims.GetExpirationTime()
	if err != nil || expires == nil {
		return time.Time{}
	}
	return expires.Time
}

// leaseUntil is a renewed session deadline: one lease, capped by the login.
func (a *pluginAppRegistry) leaseUntil(tokenExpires time.Time) time.Time {
	deadline := a.now().Add(appSessionLease)
	if !tokenExpires.IsZero() && tokenExpires.Before(deadline) {
		deadline = tokenExpires
	}
	return deadline
}

// validAppLaunchPath accepts "/" and app-relative paths, optionally with a
// query and fragment; never a scheme, host, or backslash.
func validAppLaunchPath(path string) bool {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || len(path) > appMaxLaunchPathLength ||
		strings.ContainsAny(path, "\\\r\n\t") || strings.HasPrefix(strings.TrimPrefix(path, "/"), ".ludus") {
		return false
	}
	parsed, err := url.Parse(path)
	return err == nil && parsed.Scheme == "" && parsed.Host == "" && parsed.User == nil
}

// open starts or renews a session for the caller of e and issues a ticket.
func (a *pluginAppRegistry) open(plugin *managedPlugin, e *core.RequestEvent, path, sessionID string) (protocol.AppLaunch, *protocol.HostError) {
	if !plugin.manifest.App || plugin.app == nil || plugin.resourceID == "" {
		return protocol.AppLaunch{}, hostErrorf(protocol.CodeForbidden, "plugin %s does not serve an app", plugin.manifest.ID)
	}
	if e.Auth == nil || e.Auth.IsSuperuser() || e.Auth.Collection().Name != "users" {
		return protocol.AppLaunch{}, hostErrorf(protocol.CodeForbidden, "apps require a Ludus user account")
	}
	user, ok := e.Get("user").(*models.User)
	if !ok || user == nil || user.Record == nil {
		return protocol.AppLaunch{}, hostErrorf(protocol.CodeForbidden, "the request has no effective user")
	}
	if !validAppLaunchPath(path) {
		return protocol.AppLaunch{}, hostErrorf(protocol.CodeInvalid, "invalid app path")
	}
	tokenExpires := requestTokenExpiry(e.Request)
	if !tokenExpires.IsZero() && !tokenExpires.After(a.now()) {
		return protocol.AppLaunch{}, hostErrorf(protocol.CodeExpired, "the caller's login has expired")
	}
	a.startReaper()
	ticket := randomHex(32)

	a.mu.Lock()
	defer a.mu.Unlock()
	var session *appSession
	if sessionID != "" {
		session = a.sessions[sessionID]
		if session == nil || session.ended || session.plugin != plugin || session.authID != e.Auth.Id || session.userID != user.Id {
			return protocol.AppLaunch{}, hostErrorf(protocol.CodeNotFound, "app session %q is not live for this caller", sessionID)
		}
		// Unredeemed launches (a blocked pop-up, a closed tab) expire.
		for key, pending := range session.tickets {
			if !pending.expires.After(a.now()) {
				delete(session.tickets, key)
			}
		}
		if len(session.tickets) >= appTicketsPerSession {
			return protocol.AppLaunch{}, hostErrorf(protocol.CodeUnavailable, "too many pending launches for this app session")
		}
	} else {
		limits, ok := a.limits[plugin]
		if !ok {
			limits = appSessionLimits{perUser: protocol.DefaultAppSessionsPerUser, total: protocol.DefaultAppSessions}
		}
		caller, total := 0, 0
		for _, existing := range a.sessions {
			if existing.plugin == plugin {
				total++
				if existing.authID == e.Auth.Id {
					caller++
				}
			}
		}
		if caller >= limits.perUser || total >= limits.total {
			return protocol.AppLaunch{}, hostErrorf(protocol.CodeUnavailable, "app session limit reached; close an existing session first")
		}
		id := randomHex(16)
		ctx, cancel := context.WithCancel(context.Background())
		session = &appSession{
			id: id, resourceID: plugin.resourceID, plugin: plugin,
			base:   protocol.AppSessionPrefix + plugin.resourceID + "/" + id + "/",
			cookie: randomHex(32), authID: e.Auth.Id, userID: user.Id,
			ctx: ctx, cancel: cancel, tickets: map[string]appTicket{},
		}
		session.auth = pluginPrincipal(e.Auth)
		effective := pluginUser(user)
		session.user = &effective
		session.call, session.callDone = plugin.host.registerAppSession(session)
		a.sessions[id] = session
	}
	session.expires = a.leaseUntil(tokenExpires)
	session.tickets[ticket] = appTicket{path: path, expires: a.now().Add(appTicketLifetime)}
	return protocol.AppLaunch{
		URL:      session.base + appLaunchPath + "?ticket=" + ticket,
		Session:  session.id,
		Call:     session.call,
		BasePath: session.base,
		Expires:  session.expires,
	}, nil
}

// renew extends every live session of the principal on a resource.
func (a *pluginAppRegistry) renew(resourceID, authID string, tokenExpires time.Time) (int, time.Time) {
	if !tokenExpires.IsZero() && !tokenExpires.After(a.now()) {
		return 0, time.Time{}
	}
	deadline := a.leaseUntil(tokenExpires)
	a.mu.Lock()
	defer a.mu.Unlock()
	renewed := 0
	for _, session := range a.sessions {
		if session.ended || session.resourceID != resourceID || session.authID != authID || !session.expires.After(a.now()) {
			continue
		}
		if deadline.After(session.expires) {
			session.expires = deadline
		}
		renewed++
	}
	if renewed == 0 {
		return 0, time.Time{}
	}
	return renewed, deadline
}

// end removes a session, closes its connections, and expires its CallID.
// notify tells the plugin through AppSessionClosed.
func (a *pluginAppRegistry) end(session *appSession, notify bool) {
	a.mu.Lock()
	if session.ended {
		a.mu.Unlock()
		return
	}
	session.ended = true
	delete(a.sessions, session.id)
	a.mu.Unlock()
	session.cancel()
	session.callDone()
	if notify {
		go func() {
			if err := session.plugin.rpc.AppSessionClosed(protocol.AppSessionRequest{Session: session.id}); err != nil && !session.plugin.exited() {
				a.server.logger().Info("Plugin did not accept the end of an app session", "plugin", session.plugin.manifest.ID, "error", err)
			}
		}()
	}
}

// endPlugin ends the sessions of a plugin that is stopping. Its process is
// shutting down, so it is not notified.
func (a *pluginAppRegistry) endPlugin(plugin *managedPlugin) {
	for _, session := range a.matching(func(s *appSession) bool { return s.plugin == plugin }) {
		a.end(session, false)
	}
	a.mu.Lock()
	delete(a.limits, plugin)
	a.mu.Unlock()
}

func (a *pluginAppRegistry) matching(match func(*appSession) bool) []*appSession {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result []*appSession
	for _, session := range a.sessions {
		if match(session) {
			result = append(result, session)
		}
	}
	return result
}

func (a *pluginAppRegistry) startReaper() {
	a.reaper.Do(func() {
		go func() {
			ticker := time.NewTicker(appCheckInterval)
			defer ticker.Stop()
			for range ticker.C {
				a.check()
			}
		}()
	})
}

// appUnverifiedGrace is how long a session survives checks that could not
// read its records (for example a busy database). Expiry still applies.
const appUnverifiedGrace = time.Minute

type appAccess int

const (
	appAccessLive appAccess = iota
	appAccessDenied
	appAccessUnknown
)

// check ends expired sessions and sessions whose caller lost access to the
// plugin, and refreshes the identity snapshot sent to the plugin. A session
// whose records cannot be read is kept until appUnverifiedGrace passes.
func (a *pluginAppRegistry) check() {
	for _, session := range a.matching(func(*appSession) bool { return true }) {
		auth, user, access, err := a.access(session)
		switch access {
		case appAccessDenied:
			a.end(session, true)
		case appAccessUnknown:
			a.mu.Lock()
			if session.unverifiedSince.IsZero() {
				session.unverifiedSince = a.now()
			}
			stale := a.now().Sub(session.unverifiedSince) > appUnverifiedGrace
			a.mu.Unlock()
			if stale {
				a.server.logger().Info("Ending an app session that could not be verified", "plugin", session.plugin.manifest.ID, "error", err)
				a.end(session, true)
			}
		default:
			a.mu.Lock()
			session.auth, session.user, session.unverifiedSince = auth, user, time.Time{}
			a.mu.Unlock()
		}
	}
}

// access re-reads the session's records: the plugin resource must be
// approved and visible to the principal, and an impersonating principal must
// still be an admin. Missing records deny; other lookup errors are unknown.
func (a *pluginAppRegistry) access(session *appSession) (protocol.Principal, *protocol.User, appAccess, error) {
	a.mu.Lock()
	expired := !session.expires.After(a.now())
	a.mu.Unlock()
	resources := a.server.PluginResources
	if expired || session.plugin.exited() || a.server.pluginByKey(session.plugin.key()) != session.plugin || resources == nil {
		return protocol.Principal{}, nil, appAccessDenied, nil
	}
	find := func(collection, id string) (*core.Record, appAccess, error) {
		record, err := resources.app.FindRecordById(collection, id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, appAccessDenied, nil
		case err != nil:
			return nil, appAccessUnknown, err
		}
		return record, appAccessLive, nil
	}
	record, access, err := find("plugin_resources", session.resourceID)
	if access != appAccessLive {
		return protocol.Principal{}, nil, access, err
	}
	if record.GetString("state") != "approved" {
		return protocol.Principal{}, nil, appAccessDenied, nil
	}
	auth, access, err := find("users", session.authID)
	if access != appAccessLive {
		return protocol.Principal{}, nil, access, err
	}
	if !pluginVisible(record, auth) {
		return protocol.Principal{}, nil, appAccessDenied, nil
	}
	userRecord := auth
	if session.userID != session.authID {
		if !auth.GetBool("isAdmin") {
			return protocol.Principal{}, nil, appAccessDenied, nil
		}
		if userRecord, access, err = find("users", session.userID); access != appAccessLive {
			return protocol.Principal{}, nil, access, err
		}
	}
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	effective := pluginUser(user)
	return pluginPrincipal(auth), &effective, appAccessLive, nil
}

// requestEvent rebuilds the session caller's request context for a host
// call made with the session's CallID, from the current records.
func (session *appSession) requestEvent(ctx context.Context) (*core.RequestEvent, *protocol.HostError) {
	// A missing record is final; any other lookup failure is temporary.
	lookup := func(id, what string) (*core.Record, *protocol.HostError) {
		record, err := app.FindRecordById("users", id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, hostErrorf(protocol.CodeExpired, "the app session's %s no longer exists", what)
		}
		if err != nil {
			return nil, hostErrorf(protocol.CodeUnavailable, "look up the app session's %s: %v", what, err)
		}
		return record, nil
	}
	authRecord, herr := lookup(session.authID, "user")
	if herr != nil {
		return nil, herr
	}
	userRecord := authRecord
	if session.userID != session.authID {
		if !authRecord.GetBool("isAdmin") {
			return nil, hostErrorf(protocol.CodeExpired, "the app session's user can no longer act for another user")
		}
		if userRecord, herr = lookup(session.userID, "effective user"); herr != nil {
			return nil, herr
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, session.base, nil)
	if err != nil {
		return nil, hostErrorf(protocol.CodeUnavailable, "app session request: %v", err)
	}
	e := &core.RequestEvent{App: app, Auth: authRecord}
	e.Request = request
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	app.ExpandRecord(userRecord, []string{"ranges", "groups"}, nil)
	e.Set("user", user)
	return e, nil
}

func denyPluginApp(e *core.RequestEvent) error {
	e.Response.Header().Set("Cache-Control", "no-store")
	return e.String(http.StatusUnauthorized, "This app session has ended. Open it from Ludus again.\n")
}

// ServePluginApp is /plugin-apps/{resourceID}/{sessionID}/{appPath...}. It is
// outside /api/v2: the browser navigates here directly, authenticated only by
// the session cookie, never by Ludus credentials.
func (s *Server) ServePluginApp(e *core.RequestEvent) error {
	registry := s.pluginApps()
	resourceID, sessionID := e.Request.PathValue("resourceID"), e.Request.PathValue("sessionID")
	if !pluginRecordIDPattern.MatchString(resourceID) {
		return e.NotFoundError("", nil)
	}
	registry.mu.Lock()
	session := registry.sessions[sessionID]
	registry.mu.Unlock()
	if session == nil {
		// Root-run plugins and their sessions live in the admin service.
		if pluginServiceRunAs() != protocol.RunAsRoot && s.PluginResources != nil {
			if record, err := s.PluginResources.app.FindRecordById("plugin_resources", resourceID); err == nil && manifestRunsAsRoot(resourceManifest(record)) {
				return proxyToAdmin(e)
			}
		}
		return denyPluginApp(e)
	}
	if session.resourceID != resourceID {
		return denyPluginApp(e)
	}
	rest := e.Request.PathValue("appPath")
	if rest == appLaunchPath {
		return registry.redeem(e, session)
	}
	cookie, err := e.Request.Cookie(appSessionCookie)
	registry.mu.Lock()
	live := !session.ended && session.expires.After(registry.now())
	auth, user := session.auth, session.user
	registry.mu.Unlock()
	if err != nil || !live || !hmac.Equal([]byte(cookie.Value), []byte(session.cookie)) {
		return denyPluginApp(e)
	}
	if rest == ".ludus" || strings.HasPrefix(rest, ".ludus/") {
		return e.NotFoundError("", nil)
	}
	// Browsers send Origin on cross-origin requests and WebSocket handshakes;
	// an app session only accepts its own origin.
	if origin := e.Request.Header.Get("Origin"); origin != "" {
		if parsed, err := url.Parse(origin); err != nil || parsed.Host != e.Request.Host {
			return denyPluginApp(e)
		}
	}
	appContext, err := protocol.EncodeAppContext(protocol.AppContext{Session: session.id, Call: session.call, BasePath: session.base, Auth: auth, User: user})
	if err != nil {
		return e.InternalServerError("", err)
	}
	escaped := strings.TrimPrefix(e.Request.URL.EscapedPath(), session.base)
	proxy := &httputil.ReverseProxy{
		Transport:     session.plugin.app.transport,
		FlushInterval: -1,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", "plugin-app"
			r.Out.URL.Path, r.Out.URL.RawPath = "/"+rest, "/"+escaped
			r.Out.Host = r.In.Host
			r.SetXForwarded()
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("X-API-KEY")
			removeCookie(r.Out.Header, appSessionCookie)
			r.Out.Header.Set(protocol.AppContextHeader, appContext)
		},
		ModifyResponse: func(response *http.Response) error {
			scopePluginAppResponse(response.Header, session.base)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				http.Error(w, "The app is unavailable.", http.StatusBadGateway)
			}
		},
	}
	// Ending the session cancels the request, which closes upgraded
	// connections (WebSockets) and streaming responses.
	ctx, cancel := context.WithCancel(e.Request.Context())
	stop := context.AfterFunc(session.ctx, cancel)
	defer stop()
	defer cancel()
	proxy.ServeHTTP(e.Response, e.Request.WithContext(ctx))
	return nil
}

// redeem consumes a launch ticket, sets the session cookie, and sends the
// browser to the path the plugin asked for.
func (a *pluginAppRegistry) redeem(e *core.RequestEvent, session *appSession) error {
	if e.Request.Method != http.MethodGet {
		return denyPluginApp(e)
	}
	ticket := e.Request.URL.Query().Get("ticket")
	a.mu.Lock()
	grant, ok := session.tickets[ticket]
	now := a.now()
	for key, pending := range session.tickets {
		if key == ticket || !pending.expires.After(now) {
			delete(session.tickets, key)
		}
	}
	live := !session.ended && session.expires.After(now)
	a.mu.Unlock()
	if ticket == "" || !ok || !live || !grant.expires.After(now) {
		return denyPluginApp(e)
	}
	e.Response.Header().Set("Cache-Control", "no-store")
	e.Response.Header().Set("Referrer-Policy", "no-referrer")
	http.SetCookie(e.Response, &http.Cookie{Name: appSessionCookie, Value: session.cookie, Path: session.base, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	return e.Redirect(http.StatusSeeOther, session.base+strings.TrimPrefix(grant.path, "/"))
}

// removeCookie drops one cookie from a request's Cookie headers.
func removeCookie(header http.Header, name string) {
	cookies := (&http.Request{Header: header}).Cookies()
	header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != name {
			header.Add("Cookie", (&http.Cookie{Name: cookie.Name, Value: cookie.Value}).String())
		}
	}
}

// scopePluginAppResponse keeps an app inside its session prefix: absolute
// redirects and cookie paths are relative to the app root, as the app sees
// it, and cookies never get a Domain. Host-owned headers replace the app's.
func scopePluginAppResponse(header http.Header, base string) {
	if location := header.Get("Location"); strings.HasPrefix(location, "/") && !strings.HasPrefix(location, "//") {
		header.Set("Location", base+strings.TrimPrefix(location, "/"))
	}
	cookies := (&http.Response{Header: header}).Cookies()
	header.Del("Set-Cookie")
	for _, cookie := range cookies {
		if cookie.Name == appSessionCookie {
			continue
		}
		cookie.Domain = ""
		cookie.Path = base + strings.TrimPrefix(cookie.Path, "/")
		header.Add("Set-Cookie", cookie.String())
	}
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
}

// RenewApp is POST /api/v2/plugins/{pluginID}/app/renew. The GUI calls it
// for apps it opened; renewal is capped by the caller's login.
func (p *PluginResources) RenewApp(e *core.RequestEvent) error {
	if e.Auth == nil || e.Auth.IsSuperuser() {
		return JSONError(e, http.StatusForbidden, "apps require a Ludus user account")
	}
	record, err := p.record(e)
	if err != nil {
		return JSONError(e, http.StatusNotFound, err.Error())
	}
	renewed, expires := p.server.pluginApps().renew(record.Id, e.Auth.Id, requestTokenExpiry(e.Request))
	result := map[string]any{"renewed": renewed, "expires": int64(0)}
	if renewed > 0 {
		result["expires"] = expires.UnixMilli()
	}
	return e.JSON(http.StatusOK, result)
}

func registerPluginAppRoutes(se *core.ServeEvent, s *Server) {
	se.Router.Any(protocol.AppSessionPrefix+"{resourceID}/{sessionID}/{appPath...}", s.ServePluginApp)
}

// OpenApp is evaluated as the caller of a request handler.
func (h *HostService) OpenApp(args protocol.OpenAppArgs, reply *protocol.OpenAppReply) error {
	reply.Err = h.run(args.Call, accessCaller, "OpenApp", func(c *hostScope) *protocol.HostError {
		if c.session != nil {
			return hostErrorf(protocol.CodeForbidden, "apps are opened while handling a request, not from an app session")
		}
		launch, herr := h.server.pluginApps().open(h.plugin, c.event, args.Path, args.Session)
		reply.Launch = launch
		return herr
	})
	return nil
}

// EndAppSession ends the app session the CallID belongs to.
func (h *HostService) EndAppSession(args protocol.CallArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessAny, "EndAppSession", func(c *hostScope) *protocol.HostError {
		if c.session == nil {
			return hostErrorf(protocol.CodeInvalid, "the call does not belong to an app session")
		}
		session := c.session
		go h.server.pluginApps().end(session, true)
		return nil
	})
	return nil
}

// SetAppSessionLimits sets the plugin's own app session limits.
func (h *HostService) SetAppSessionLimits(args protocol.AppSessionLimitsArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessAny, "SetAppSessionLimits", func(c *hostScope) *protocol.HostError {
		if !h.plugin.manifest.App {
			return hostErrorf(protocol.CodeForbidden, "plugin %s does not serve an app", h.plugin.manifest.ID)
		}
		return h.server.pluginApps().setLimits(h.plugin, args.PerUser, args.Total)
	})
	return nil
}
