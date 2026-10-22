package ludusapi

import (
	"context"
	"errors"
	"sync/atomic"

	"crypto/tls"
	"encoding/json"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type appTestLaunch struct {
	Launch protocol.AppLaunch
	Error  string
}

type appTestClosed struct {
	Closed []string
	Calls  map[string]string
}

// A real plugin process serves an app over its broker stream. The host
// redeems a one-time ticket into a path-scoped cookie, proxies HTTP and
// WebSocket traffic without Ludus credentials, keeps the app inside its
// session prefix, and on expiry closes open connections, expires the
// session's CallID, and tells the plugin.
func TestPluginAppSessionLifecycle(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("app-plugin")
	m.App = true
	m.Routes = []protocol.Route{{Name: "open", Method: "POST", Pattern: "/open"}, {Name: "closed", Method: "GET", Pattern: "/closed"}}
	id := env.upload(env.ownerToken, "", testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}))
	env.approve(id)

	open := func(query url.Values) appTestLaunch {
		t.Helper()
		var result appTestLaunch
		body := env.call(env.ownerToken, "POST", APIBasePath+"/plugins/"+id+"/rpc/open?"+query.Encode(), nil, 200).Body.Bytes()
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if bad := open(url.Values{"path": {"//evil.example/"}}); bad.Error != string(protocol.CodeInvalid) {
		t.Fatalf("protocol-relative launch path = %+v, want invalid", bad)
	}
	first := open(url.Values{"path": {"/landing#/client/x"}})
	launch := first.Launch
	if first.Error != "" || launch.BasePath != protocol.AppSessionPrefix+id+"/"+launch.Session+"/" || !strings.HasPrefix(launch.URL, launch.BasePath+appLaunchPath+"?ticket=") {
		t.Fatalf("OpenApp = %+v", first)
	}
	if launch.Expires.After(time.Now().Add(appSessionLease + time.Minute)) {
		t.Fatalf("session expiry %s exceeds one lease", launch.Expires)
	}

	server := httptest.NewTLSServer(env.mux)
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	do := func(method, path string, header http.Header) (*http.Response, string) {
		t.Helper()
		request, _ := http.NewRequest(method, server.URL+path, nil)
		for key, values := range header {
			request.Header[key] = values
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(response.Body)
		return response, string(body)
	}

	redeemed, _ := do("GET", launch.URL, nil)
	cookies := redeemed.Cookies()
	if redeemed.StatusCode != http.StatusSeeOther || redeemed.Header.Get("Location") != launch.BasePath+"landing#/client/x" || len(cookies) != 1 {
		t.Fatalf("redeem = %d %q %v", redeemed.StatusCode, redeemed.Header.Get("Location"), cookies)
	}
	cookie := cookies[0]
	if cookie.Name != appSessionCookie || cookie.Path != launch.BasePath || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}
	if replay, _ := do("GET", launch.URL, nil); replay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replayed ticket = %d, want 401", replay.StatusCode)
	}

	withCookie := http.Header{"Cookie": {cookie.Name + "=" + cookie.Value + "; other=kept"}}
	if response, _ := do("GET", launch.BasePath+"whoami", nil); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("request without cookie = %d, want 401", response.StatusCode)
	}
	crossOrigin := withCookie.Clone()
	crossOrigin.Set("Origin", "https://attacker.example")
	if response, _ := do("GET", launch.BasePath+"whoami", crossOrigin); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-origin request = %d, want 401", response.StatusCode)
	}
	credentials := withCookie.Clone()
	credentials.Set("Authorization", "Bearer "+env.ownerToken)
	credentials.Set("X-API-KEY", "secret")
	credentials.Set(protocol.AppContextHeader, "spoofed")
	response, body := do("GET", launch.BasePath+"whoami", credentials)
	var seen map[string]string
	if response.StatusCode != http.StatusOK || json.Unmarshal([]byte(body), &seen) != nil {
		t.Fatalf("app request = %d %s", response.StatusCode, body)
	}
	if seen["path"] != "/whoami" || seen["session"] != launch.Session || seen["base"] != launch.BasePath || seen["user"] != "OWNER" || seen["call"] != "" {
		t.Fatalf("app saw %v", seen)
	}
	if seen["authorization"] != "" || seen["apiKey"] != "" || seen["cookie"] != "other=kept" {
		t.Fatalf("Ludus credentials or the session cookie reached the app: %v", seen)
	}
	appCookies := response.Cookies()
	if len(appCookies) != 1 || appCookies[0].Path != launch.BasePath || response.Header.Values("Referrer-Policy")[0] != "no-referrer" || len(response.Header.Values("Referrer-Policy")) != 1 {
		t.Fatalf("app response not scoped: cookies %v, Referrer-Policy %v", appCookies, response.Header.Values("Referrer-Policy"))
	}
	if redirect, _ := do("GET", launch.BasePath+"redirect", withCookie); redirect.Header.Get("Location") != launch.BasePath+"landing" {
		t.Fatalf("app redirect Location = %q", redirect.Header.Get("Location"))
	}
	if reserved, _ := do("GET", launch.BasePath+".ludus/other", withCookie); reserved.StatusCode != http.StatusNotFound {
		t.Fatalf("reserved host path = %d, want 404", reserved.StatusCode)
	}

	// Reopening the session for the same caller issues another ticket for it.
	again := open(url.Values{"path": {"/"}, "session": {launch.Session}})
	if again.Error != "" || again.Launch.Session != launch.Session || again.Launch.URL == launch.URL {
		t.Fatalf("reopen = %+v", again)
	}
	// Pending launches are capped, but unredeemed tickets expire and free the slot.
	for range appTicketsPerSession - 1 {
		open(url.Values{"path": {"/"}, "session": {launch.Session}})
	}
	if full := open(url.Values{"path": {"/"}, "session": {launch.Session}}); full.Error != string(protocol.CodeUnavailable) {
		t.Fatalf("launch past the pending cap = %+v", full)
	}
	registry := env.server.pluginApps()
	registry.mu.Lock()
	for key, pending := range registry.sessions[launch.Session].tickets {
		pending.expires = time.Now().Add(-time.Second)
		registry.sessions[launch.Session].tickets[key] = pending
	}
	registry.mu.Unlock()
	if freed := open(url.Values{"path": {"/"}, "session": {launch.Session}}); freed.Error != "" {
		t.Fatalf("launch after pending tickets expired = %+v", freed)
	}
	var renewal struct {
		Renewed int
		Expires int64
	}
	if err := json.Unmarshal(env.call(env.ownerToken, "POST", APIBasePath+"/plugins/"+id+"/app/renew", nil, 200).Body.Bytes(), &renewal); err != nil || renewal.Renewed != 1 || renewal.Expires <= time.Now().UnixMilli() {
		t.Fatalf("renew = %+v, %v", renewal, err)
	}
	if err := json.Unmarshal(env.call(env.peerToken, "POST", APIBasePath+"/plugins/"+id+"/app/renew", nil, 404).Body.Bytes(), &renewal); err != nil {
		t.Fatal(err)
	}

	dialer := websocket.Dialer{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	wsHeader := withCookie.Clone()
	wsHeader.Set("Origin", server.URL)
	conn, _, err := dialer.Dial("wss"+strings.TrimPrefix(server.URL, "https")+launch.BasePath+"echo", wsHeader)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, data, err := conn.ReadMessage(); err != nil || string(data) != "ping" {
		t.Fatalf("WebSocket echo = %q, %v", data, err)
	}

	// Expiry ends the session: the WebSocket closes, the CallID expires, the
	// plugin is told, and the cookie no longer opens the app.
	registry.mu.Lock()
	registry.sessions[launch.Session].expires = time.Now().Add(-time.Second)
	registry.mu.Unlock()
	registry.check()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("WebSocket stayed open after the session ended")
	}
	var closed appTestClosed
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := json.Unmarshal(env.call(env.ownerToken, "GET", APIBasePath+"/plugins/"+id+"/rpc/closed", nil, 200).Body.Bytes(), &closed); err != nil {
			t.Fatal(err)
		}
		if len(closed.Closed) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(closed.Closed) != 1 || closed.Closed[0] != launch.Session || closed.Calls[launch.Session] != string(protocol.CodeExpired) {
		t.Fatalf("after expiry: %+v", closed)
	}
	if response, _ := do("GET", launch.BasePath+"whoami", withCookie); response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ended session still served: %d", response.StatusCode)
	}
	if ended := open(url.Values{"path": {"/"}, "session": {launch.Session}}); ended.Error != string(protocol.CodeNotFound) {
		t.Fatalf("reopening an ended session = %+v", ended)
	}

	// The plugin can end a session itself; uninstalling ends the rest.
	second := open(url.Values{"path": {"/"}}).Launch
	redeemed, _ = do("GET", second.URL, nil)
	secondCookie := http.Header{"Cookie": {redeemed.Cookies()[0].Name + "=" + redeemed.Cookies()[0].Value}}
	if end, _ := do("GET", second.BasePath+"end", secondCookie); end.Header.Get("X-End") != "" {
		t.Fatalf("EndAppSession = %q", end.Header.Get("X-End"))
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		response, _ := do("GET", second.BasePath+"whoami", secondCookie)
		if response.StatusCode == http.StatusUnauthorized {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("EndAppSession did not end the session")
		}
		time.Sleep(50 * time.Millisecond)
	}
	third := open(url.Values{"path": {"/"}}).Launch
	env.call(env.ownerToken, "DELETE", APIBasePath+"/plugins/"+id+"?uninstall=true", nil, 200)
	registry.mu.Lock()
	_, live := registry.sessions[third.Session]
	registry.mu.Unlock()
	if live {
		t.Fatal("stopping the plugin left its app session live")
	}
}

// Access is rechecked while a session is live: unsharing the plugin ends the
// sessions of users who lost it.
func TestPluginAppSessionEndsWhenAccessIsRevoked(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("shared-app")
	m.App = true
	m.Routes = []protocol.Route{{Name: "open", Method: "POST", Pattern: "/open"}}
	id := env.upload(env.adminToken, "", testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}))
	env.approve(id)
	env.call(env.adminToken, "PUT", APIBasePath+"/plugins/"+id+"/access", []byte(`{"allowedUsers":["OWNER"]}`), 200)

	var result appTestLaunch
	if err := json.Unmarshal(env.call(env.ownerToken, "POST", APIBasePath+"/plugins/"+id+"/rpc/open?path=/", nil, 200).Body.Bytes(), &result); err != nil || result.Error != "" {
		t.Fatalf("open = %+v, %v", result, err)
	}
	registry := env.server.pluginApps()
	registry.check()
	registry.mu.Lock()
	_, live := registry.sessions[result.Launch.Session]
	registry.mu.Unlock()
	if !live {
		t.Fatal("a session with access was ended")
	}
	env.call(env.adminToken, "PUT", APIBasePath+"/plugins/"+id+"/access", []byte(`{"allowedUsers":[]}`), 200)
	registry.check()
	registry.mu.Lock()
	_, live = registry.sessions[result.Launch.Session]
	registry.mu.Unlock()
	if live {
		t.Fatal("a session outlived the caller's access to the plugin")
	}
}

func TestPluginAppDeclarationMustMatchStream(t *testing.T) {
	plugin := &managedPlugin{manifest: protocol.Manifest{ID: "declared", App: true}}
	if err := connectPluginApp(plugin, 0); err == nil {
		t.Fatal("a declared app without a stream was accepted")
	}
	plugin.manifest.App = false
	if err := connectPluginApp(plugin, 7); err == nil {
		t.Fatal("an undeclared app stream was accepted")
	}
	if err := connectPluginApp(plugin, 0); err != nil || plugin.app != nil {
		t.Fatalf("plugin without an app: %v", err)
	}
}

// A plugin sets its own session limits; the host enforces them in OpenApp.
func TestPluginAppSessionLimits(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("limited-app")
	m.App = true
	m.Routes = []protocol.Route{{Name: "open", Method: "POST", Pattern: "/open"}}
	id := env.upload(env.ownerToken, "", testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}))
	env.approve(id)
	plugin := env.server.pluginByKey(pluginKey(protocol.SourceUploaded, "", id))
	setLimits := func(perUser, total int) protocol.ErrorCode {
		call, done := plugin.host.beginPluginCall(context.Background())
		defer done()
		var reply protocol.EmptyReply
		plugin.host.SetAppSessionLimits(protocol.AppSessionLimitsArgs{Call: call, PerUser: perUser, Total: total}, &reply)
		return hostCode(reply.Err)
	}
	for _, invalid := range [][2]int{{0, 1}, {3, 2}, {1, protocol.MaxAppSessions + 1}} {
		if code := setLimits(invalid[0], invalid[1]); code != protocol.CodeInvalid {
			t.Fatalf("limits %v = %q, want invalid", invalid, code)
		}
	}
	if code := setLimits(1, 2); code != "" {
		t.Fatalf("valid limits = %q", code)
	}
	open := func(token string) string {
		t.Helper()
		var result appTestLaunch
		if err := json.Unmarshal(env.call(token, "POST", APIBasePath+"/plugins/"+id+"/rpc/open?path=/", nil, 200).Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Error
	}
	if code := open(env.ownerToken); code != "" {
		t.Fatalf("first session = %q", code)
	}
	if code := open(env.ownerToken); code != string(protocol.CodeUnavailable) {
		t.Fatalf("second session per user = %q, want unavailable", code)
	}
	if code := open(env.adminToken); code != "" {
		t.Fatalf("another user's session = %q", code)
	}
	if code := setLimits(1, 3); code != "" {
		t.Fatal(code)
	}
	if code := setLimits(2, 2); code != "" {
		t.Fatal(code)
	}
	if code := open(env.ownerToken); code != string(protocol.CodeUnavailable) {
		t.Fatalf("session past the total = %q, want unavailable", code)
	}
}

// failingLookups makes record lookups fail as a busy database would.
type failingLookups struct {
	core.App
	fail *atomic.Bool
}

func (f failingLookups) FindRecordById(collection any, id string, optFilters ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	if f.fail.Load() {
		return nil, errors.New("database is locked")
	}
	return f.App.FindRecordById(collection, id, optFilters...)
}

// A check that cannot read the session's records keeps the session until the
// grace period passes; a successful check resets it.
func TestPluginAppSessionSurvivesTemporaryLookupFailures(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("busy-app")
	m.App = true
	m.Routes = []protocol.Route{{Name: "open", Method: "POST", Pattern: "/open"}}
	id := env.upload(env.ownerToken, "", testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}))
	env.approve(id)
	var result appTestLaunch
	if err := json.Unmarshal(env.call(env.ownerToken, "POST", APIBasePath+"/plugins/"+id+"/rpc/open?path=/", nil, 200).Body.Bytes(), &result); err != nil || result.Error != "" {
		t.Fatalf("open = %+v, %v", result, err)
	}
	fail := &atomic.Bool{}
	env.resources.app = failingLookups{App: env.resources.app, fail: fail}
	registry := env.server.pluginApps()
	now := time.Now()
	registry.now = func() time.Time { return now }
	live := func() bool {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		_, ok := registry.sessions[result.Launch.Session]
		return ok
	}

	fail.Store(true)
	registry.check()
	now = now.Add(appUnverifiedGrace / 2)
	registry.check()
	if !live() {
		t.Fatal("a temporary lookup failure ended the session")
	}
	fail.Store(false)
	registry.check() // verified: resets the grace period
	fail.Store(true)
	now = now.Add(appUnverifiedGrace / 2)
	registry.check()
	now = now.Add(appUnverifiedGrace / 2)
	registry.check()
	if !live() {
		t.Fatal("the grace period was not reset by a successful check")
	}
	now = now.Add(appUnverifiedGrace)
	registry.check()
	if live() {
		t.Fatal("a session that stayed unverified past the grace period was kept")
	}
}
