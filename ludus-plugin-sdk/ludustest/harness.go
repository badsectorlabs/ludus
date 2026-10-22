package ludustest

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"net/url"
	"sync/atomic"
	"testing"

	ludus "gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// Harness runs a plugin in-process. The plugin reaches its host service over
// a real net/rpc connection, so c.Host behaves as it does under Ludus.
type Harness struct {
	t      testing.TB
	plugin *ludus.Plugin
	impl   protocol.Implementation
	env    protocol.Environment
	jobs   []protocol.ScheduledJob
	calls  atomic.Uint64
}

// Start validates p, connects it to host, and initializes it with env. A nil
// host means no host service: every Host method fails with ErrUnavailable.
// Empty env.Level and env.RunAs default to the plugin's declaration. The
// plugin is shut down when the test ends.
func Start(t testing.TB, p *ludus.Plugin, host protocol.HostAPI, env protocol.Environment) *Harness {
	t.Helper()
	if _, err := ludus.ManifestJSON(p); err != nil {
		t.Fatalf("invalid plugin declaration: %v", err)
	}
	h := &Harness{t: t, plugin: p, impl: ludus.NewImplementation(p)}
	metadata, err := h.impl.Metadata()
	if err != nil {
		t.Fatalf("plugin Metadata: %v", err)
	}
	if env.Level == "" {
		env.Level = metadata.Level
	}
	if env.RunAs == "" {
		env.RunAs = metadata.RunAs
	}
	if env.ResourceID == "" {
		env.ResourceID = "ludustest"
	}
	h.env = env

	var client *protocol.HostClient
	if host != nil {
		server := rpc.NewServer()
		if err := server.RegisterName(protocol.HostServiceName, host); err != nil {
			t.Fatalf("register host service: %v", err)
		}
		hostConn, pluginConn := net.Pipe()
		served := make(chan struct{})
		go func() {
			defer close(served)
			server.ServeConn(hostConn)
		}()
		client = protocol.NewHostClient(rpc.NewClient(pluginConn))
		t.Cleanup(func() {
			_ = client.Close()
			_ = hostConn.Close()
			<-served
		})
	}

	response, err := h.impl.Initialize(protocol.InitializeRequest{Call: h.newCall(), HostService: 1, Env: env}, client)
	if err != nil {
		t.Fatalf("plugin Initialize: %v", err)
	}
	h.jobs = response.Jobs
	t.Cleanup(func() {
		if err := h.impl.Shutdown(); err != nil {
			t.Errorf("plugin Shutdown: %v", err)
		}
	})
	return h
}

// Env returns the environment the plugin was initialized with.
func (h *Harness) Env() protocol.Environment { return h.env }

// Jobs returns the scheduled jobs the plugin reported at Initialize.
func (h *Harness) Jobs() []protocol.ScheduledJob { return h.jobs }

func (h *Harness) newCall() protocol.CallID {
	return protocol.CallID(fmt.Sprintf("ludustest-call-%d", h.calls.Add(1)))
}

// RequestOption customizes a request sent by Harness.Do.
type RequestOption func(*protocol.Request) error

// WithAuth sets the authenticated principal.
func WithAuth(auth protocol.Principal) RequestOption {
	return func(r *protocol.Request) error { r.Auth = auth; return nil }
}

// WithUser sets the effective user.
func WithUser(user *protocol.User) RequestOption {
	return func(r *protocol.Request) error { r.User = user; return nil }
}

// WithRange sets the resolved range.
func WithRange(rng *protocol.Range) RequestOption {
	return func(r *protocol.Request) error { r.Range = rng; return nil }
}

// WithBody sets the raw request body.
func WithBody(body []byte) RequestOption {
	return func(r *protocol.Request) error { r.Body = body; return nil }
}

// WithJSON sets the body to v encoded as JSON, with a JSON Content-Type.
func WithJSON(v any) RequestOption {
	return func(r *protocol.Request) error {
		body, err := json.Marshal(v)
		if err != nil {
			return err
		}
		r.Body = body
		http.Header(r.Header).Set("Content-Type", "application/json")
		return nil
	}
}

// WithHeader adds a request header.
func WithHeader(key, value string) RequestOption {
	return func(r *protocol.Request) error { http.Header(r.Header).Add(key, value); return nil }
}

// WithQuery sets the query string.
func WithQuery(query url.Values) RequestOption {
	return func(r *protocol.Request) error { r.RawQuery = query.Encode(); return nil }
}

// Do sends a request to the route declared with method and pattern, as the
// host would after authenticating the caller. It fails the test when no such
// route exists or the call fails.
func (h *Harness) Do(method, pattern string, opts ...RequestOption) protocol.Response {
	h.t.Helper()
	metadata, err := h.impl.Metadata()
	if err != nil {
		h.t.Fatalf("plugin Metadata: %v", err)
	}
	// Metadata lists every served route, including the SDK's VMActions routes.
	var route *protocol.Route
	for i := range metadata.Routes {
		if metadata.Routes[i].Method == method && metadata.Routes[i].Pattern == pattern {
			route = &metadata.Routes[i]
			break
		}
	}
	if route == nil {
		h.t.Fatalf("plugin declares no route %s %s", method, pattern)
	}
	path := "/api/v2/plugins/" + h.env.ResourceID + "/rpc" + pattern
	if route.EffectiveScope() == protocol.ScopeAPI {
		path = "/api/v2" + pattern
	}
	request := protocol.Request{
		Call:       h.newCall(),
		Route:      route.Name,
		Method:     method,
		Path:       path,
		Header:     map[string][]string{},
		RemoteAddr: "192.0.2.1:1234",
	}
	for _, opt := range opts {
		if err := opt(&request); err != nil {
			h.t.Fatalf("request option: %v", err)
		}
	}
	response, err := h.impl.Handle(request)
	if err != nil {
		h.t.Fatalf("plugin Handle %s %s: %v", method, pattern, err)
	}
	return response
}

// RunJob runs the named job as the host scheduler would.
func (h *Harness) RunJob(name string) error {
	return h.impl.RunJob(protocol.JobRequest{Call: h.newCall(), Name: name})
}

// VMHook sends a VM hook request; its Call is replaced with a fresh CallID.
func (h *Harness) VMHook(request protocol.VMHookRequest) (protocol.VMHookResponse, error) {
	request.Call = h.newCall()
	return h.impl.VMHook(request)
}

// App returns the plugin's App as the host proxies it: each request carries
// session's AppContext, with paths relative to the app root. Wrap it in
// httptest.NewServer to exercise streaming and WebSocket upgrades. It fails
// the test when the plugin has no App.
func (h *Harness) App(session protocol.AppContext) http.Handler {
	h.t.Helper()
	handler := h.impl.App()
	if handler == nil {
		h.t.Fatal("plugin declares no App")
	}
	encoded, err := protocol.EncodeAppContext(session)
	if err != nil {
		h.t.Fatalf("encode app context: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.Clone(r.Context())
		r.Header.Set(protocol.AppContextHeader, encoded)
		handler.ServeHTTP(w, r)
	})
}

// CloseAppSession tells the plugin that the host ended an app session.
func (h *Harness) CloseAppSession(session string) error {
	return h.impl.AppSessionClosed(protocol.AppSessionRequest{Session: session})
}
