// Package ludus is the Ludus plugin SDK. A plugin executable declares a
// *Plugin and passes it to Serve:
//
//	func main() {
//		ludus.Serve(&ludus.Plugin{
//			ID:      "traffic-observer",
//			Name:    "Ludus Traffic Observer",
//			Version: "1.3.0",
//			Routes: []ludus.Route{
//				{Name: "vms", Method: http.MethodGet, Pattern: "/traffic-observer/vms", Handler: listVMs},
//			},
//		})
//	}
//
// Handlers are standard net/http handlers. CallFrom returns the caller, the
// resolved user and range, and a Host client bound to the request.
package ludus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"time"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// ManifestFlag makes Serve print the plugin's plugin.json and exit.
const ManifestFlag = "--ludus-manifest"

const (
	Standard = protocol.LevelStandard
	System   = protocol.LevelSystem

	RunAsLudus = protocol.RunAsLudus
	RunAsRoot  = protocol.RunAsRoot

	// PluginScope mounts a route at /api/v2/plugins/{resourceID}/rpc/<Pattern>.
	PluginScope = protocol.ScopePlugin
	// APIScope mounts a route at /api/v2/<Pattern>. System level only.
	APIScope = protocol.ScopeAPI
)

// Plugin declares a plugin. Serve derives both the runtime Metadata and
// plugin.json from it, so the two always agree.
type Plugin struct {
	ID, Name, Version, Description, Author string

	Level protocol.Level // Standard (default) or System
	RunAs protocol.RunAs // RunAsLudus (default) or RunAsRoot
	// ForwardCallerCredentials keeps the caller's Authorization, X-API-KEY,
	// and Cookie headers on requests routed to this plugin.
	ForwardCallerCredentials bool
	// UI is non-nil when the package ships ui/index.html.
	UI     *UI
	Routes []Route

	VMHooks protocol.VMHookCapabilities
	// VMHook handles advertised VM hook operations. Nil answers VMHookContinue.
	VMHook func(c *Call, r protocol.VMHookRequest) (protocol.VMHookResponse, error)

	// Init runs when the host initializes the plugin. c.Host acts as the plugin principal.
	Init func(c *Call, env Env) error
	// WebApplication returns Guacamole's private endpoint after Init succeeds.
	// Only the administrator-approved ludus-guacamole plugin may provide it.
	WebApplication func() *protocol.WebApplication
	// Jobs are run by the host scheduler at their Interval.
	Jobs []Job
	// Shutdown runs before the host stops the plugin process.
	Shutdown func() error
}

// UI describes the package's ui/index.html.
type UI struct {
	// RangeScoped shows the UI in the context of the selected range.
	RangeScoped bool
}

// Route is an HTTP route served by the plugin. Pattern is an exact path.
type Route struct {
	Name, Method, Pattern string
	Scope                 protocol.RouteScope // PluginScope (default) or APIScope
	Handler               http.HandlerFunc
}

// Job is a scheduled task.
type Job struct {
	Name     string
	Interval time.Duration
	Run      func(c *Call) error
}

// Env is the host context passed to Init.
type Env = protocol.Environment

// Call is the context of one host → plugin call.
type Call struct {
	GuacAppBasePath string
	ID              protocol.CallID
	Auth            protocol.Principal // zero outside request handlers
	User            *protocol.User     // effective user; nil for superusers and outside request handlers
	Range           *protocol.Range    // range resolved by the host; nil when none
	// Host is the host service bound to this call. It is valid until the
	// handler, job, or hook returns.
	Host *Host
	// Env is the environment received at Initialize. Treat it as read-only.
	Env *Env
}

// IsAdmin reports whether the caller is a Ludus admin or a PocketBase superuser.
func (c *Call) IsAdmin() bool {
	return c.Auth.IsAdmin || c.Auth.IsSuperuser
}

type callContextKey struct{}

// CallFrom returns the Call of a request dispatched by the SDK, or nil when r
// did not come through the SDK.
func CallFrom(r *http.Request) *Call {
	c, _ := r.Context().Value(callContextKey{}).(*Call)
	return c
}

func withCall(ctx context.Context, c *Call) context.Context {
	return context.WithValue(ctx, callContextKey{}, c)
}

// Serve runs p as a plugin process. With ManifestFlag among the arguments it
// prints plugin.json to stdout and exits instead. Serve exits with status 1
// when p is not a valid declaration.
func Serve(p *Plugin) {
	manifest, err := ManifestJSON(p)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ludus plugin:", err)
		os.Exit(1)
	}
	if slices.Contains(os.Args[1:], ManifestFlag) {
		if _, err := os.Stdout.Write(manifest); err != nil {
			fmt.Fprintln(os.Stderr, "ludus plugin:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	protocol.Serve(NewImplementation(p))
}

// ManifestJSON returns the plugin.json generated from p's declaration.
func ManifestJSON(p *Plugin) ([]byte, error) {
	if err := validate(p); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(manifest(p), "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func manifest(p *Plugin) protocol.Manifest {
	return protocol.ManifestFromMetadata(metadata(p), p.UI != nil, p.UI != nil && p.UI.RangeScoped)
}

// metadata builds normalized Metadata from the declaration.
func metadata(p *Plugin) protocol.Metadata {
	md := protocol.Metadata{
		ID:                       p.ID,
		Name:                     p.Name,
		Version:                  p.Version,
		Description:              p.Description,
		Author:                   p.Author,
		Level:                    p.Level,
		RunAs:                    p.RunAs,
		VMHooks:                  p.VMHooks,
		ForwardCallerCredentials: p.ForwardCallerCredentials,
	}
	if md.Level == "" {
		md.Level = Standard
	}
	if md.RunAs == "" {
		md.RunAs = RunAsLudus
	}
	for _, route := range p.Routes {
		r := protocol.Route{Name: route.Name, Method: route.Method, Pattern: route.Pattern, Scope: route.Scope}
		r.Scope = r.EffectiveScope()
		md.Routes = append(md.Routes, r)
	}
	return md
}

// validate checks the declaration against the manifest rules plus the parts
// the manifest does not carry (handlers and jobs).
func validate(p *Plugin) error {
	if p == nil {
		return errors.New("plugin declaration is nil")
	}
	if err := manifest(p).Validate(); err != nil {
		return err
	}
	for _, route := range p.Routes {
		if route.Handler == nil {
			return fmt.Errorf("route %q has no handler", route.Name)
		}
	}
	jobs := map[string]bool{}
	for _, job := range p.Jobs {
		if job.Name == "" || jobs[job.Name] {
			return errors.New("jobs need unique names")
		}
		jobs[job.Name] = true
		if job.Interval < protocol.MinJobInterval || job.Run == nil {
			return fmt.Errorf("job %q needs an interval of at least %s and a Run function", job.Name, protocol.MinJobInterval)
		}
	}
	return nil
}

// Logger returns a text logger on stderr, which the host collects.
func Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
