package ludusapi

import (
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func configurePluginWebApplication(plugin *managedPlugin, app *protocol.WebApplication) error {
	if app == nil {
		return nil
	}
	if plugin.manifest.ID != "ludus-guacamole" {
		return errors.New("web application is only supported for Guacamole")
	}
	target, err := url.Parse(app.URL)
	if err != nil || target.Scheme != "http" || target.User != nil || target.Path != "" || target.RawQuery != "" || target.Fragment != "" {
		return errors.New("invalid plugin web application endpoint")
	}
	ip := net.ParseIP(target.Hostname())
	port, err := strconv.Atoi(target.Port())
	if ip == nil || !ip.IsLoopback() || err != nil || port < 1 || port > 65535 || len(app.Token) < 32 || strings.ContainsAny(app.Token, "\r\n") {
		return errors.New("invalid private plugin endpoint")
	}
	plugin.webApplication = app
	return nil
}

// The package chooses its own ID, so the ID alone cannot authorize use of
// the Guacamole proxy.
func trustedGuacamoleResource(app core.App, record *core.Record) bool {
	if record == nil || record.GetString("pluginID") != "ludus-guacamole" {
		return false
	}
	switch recordSource(record) {
	case protocol.SourceServer, protocol.SourceLicensed:
		return true
	case protocol.SourceUploaded:
	default:
		return false
	}
	ownerID := record.GetString("owner")
	if ownerID == "" {
		return false
	}
	owner, err := app.FindRecordById("users", ownerID)
	return err == nil && pluginAdmin(owner)
}

// The private plugin endpoint authenticates a one-time launch ticket and then
// an HttpOnly session cookie. It owns session expiry, revocation and authorization.
// This route is outside the JSON RPC bridge so HTTP streaming/WebSocket upgrades
// and the unmodified upstream application can pass through without buffering.
func (p *PluginResources) GuacamoleWebApplication(e *core.RequestEvent) error {
	p.mu.Lock()
	id := e.Request.PathValue("pluginID")
	record, err := p.app.FindRecordById("plugin_resources", id)
	if err != nil || !trustedGuacamoleResource(p.app, record) || record.GetString("state") != "approved" {
		p.mu.Unlock()
		return e.NotFoundError("Guacamole is unavailable", nil)
	}
	if manifestRunsAsRoot(resourceManifest(record)) && pluginServiceRunAs() != protocol.RunAsRoot {
		p.mu.Unlock()
		return proxyToAdmin(e)
	}
	plugin := p.pluginForRecord(record)
	if plugin == nil || plugin.exited() || plugin.webApplication == nil {
		p.mu.Unlock()
		return e.NotFoundError("Guacamole is unavailable", nil)
	}
	app := *plugin.webApplication
	p.mu.Unlock()
	target, _ := url.Parse(app.URL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	original := proxy.Director
	proxy.Director = func(r *http.Request) {
		original(r)
		r.Header.Del("Authorization")
		r.Header.Del("X-API-KEY")
		r.Header.Set("X-Ludus-App-Token", app.Token)
		r.Header.Set("X-Ludus-App-Base", "/guacamole/"+id+"/")
		r.Header.Set("X-Forwarded-Proto", "https")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, _ error) {
		http.Error(w, "Guacamole is unavailable", http.StatusBadGateway)
	}
	e.Response.Header().Set("Referrer-Policy", "no-referrer")
	e.Response.Header().Set("Cache-Control", "no-store")
	proxy.ServeHTTP(e.Response, e.Request)
	return nil
}

func registerPluginWebApplicationRoutes(se *core.ServeEvent, p *PluginResources) {
	se.Router.Any("/guacamole/{pluginID}/{appPath...}", p.GuacamoleWebApplication)
}
