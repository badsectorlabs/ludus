package ludus_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	ludus "gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/ludustest"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func errorMessage(t *testing.T, response protocol.Response) string {
	t.Helper()
	var body map[string]string
	if err := json.Unmarshal(response.Body, &body); err != nil {
		t.Fatalf("response body %q is not a JSON error: %v", response.Body, err)
	}
	return body["error"]
}

func TestHandleDispatchesByRouteName(t *testing.T) {
	var got struct {
		call   *ludus.Call
		method string
		uri    string
		header string
		body   string
		remote string
	}
	p := &ludus.Plugin{
		ID: "dispatch-test", Name: "Dispatch", Version: "1.0.0",
		Routes: []ludus.Route{
			{Name: "first", Method: http.MethodPost, Pattern: "/same", Handler: func(w http.ResponseWriter, r *http.Request) {
				ludus.Error(w, http.StatusTeapot, "wrong route")
			}},
			{Name: "second", Method: http.MethodPost, Pattern: "/other", Handler: func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				got.call, got.method, got.uri = ludus.CallFrom(r), r.Method, r.URL.RequestURI()
				got.header, got.body, got.remote = r.Header.Get("X-Test"), string(body), r.RemoteAddr
				w.Header().Set("X-Plugin", "second")
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte("created"))
			}},
			{Name: "silent", Method: http.MethodGet, Pattern: "/silent", Handler: func(http.ResponseWriter, *http.Request) {}},
		},
	}
	impl := ludus.NewImplementation(p)
	request := protocol.Request{
		Call:       "call-1",
		Route:      "second",
		Method:     http.MethodPost,
		Path:       "/api/v2/plugins/resource/rpc/other",
		RawQuery:   "a=1&b=2",
		Header:     map[string][]string{"X-Test": {"value"}},
		Body:       []byte("payload"),
		RemoteAddr: "10.0.0.5:4444",
		Auth:       protocol.Principal{UserID: "alice", IsAdmin: true},
		User:       &protocol.User{UserID: "bob"},
		Range:      &protocol.Range{RangeID: "bob-range", Number: 7},
	}
	response, err := impl.Handle(request)
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if response.Status != http.StatusCreated || string(response.Body) != "created" || response.Header["X-Plugin"][0] != "second" {
		t.Fatalf("response = %d %v %q, want the second route's response", response.Status, response.Header, response.Body)
	}
	c := got.call
	if c == nil || c.ID != "call-1" || c.Auth.UserID != "alice" || !c.IsAdmin() || c.User.UserID != "bob" || c.Range.Number != 7 || c.Host == nil || c.Env == nil {
		t.Fatalf("CallFrom() = %+v", c)
	}
	if got.method != http.MethodPost || got.uri != "/api/v2/plugins/resource/rpc/other?a=1&b=2" || got.header != "value" || got.body != "payload" || got.remote != "10.0.0.5:4444" {
		t.Fatalf("reconstructed request = %+v", got)
	}

	response, err = impl.Handle(protocol.Request{Route: "silent", Method: http.MethodGet, Path: "/silent"})
	if err != nil || response.Status != http.StatusOK {
		t.Fatalf("silent handler response = %d, %v; want 200", response.Status, err)
	}

	response, err = impl.Handle(protocol.Request{Route: "missing", Method: http.MethodGet, Path: "/missing"})
	if err != nil {
		t.Fatalf("Handle(unknown) error = %v", err)
	}
	if response.Status != http.StatusNotFound || !strings.Contains(errorMessage(t, response), "missing") {
		t.Fatalf("unknown route response = %d %q, want 404 JSON error", response.Status, response.Body)
	}
}

func TestHandlerPanicBecomes500(t *testing.T) {
	p := &ludus.Plugin{
		ID: "panic-test", Name: "Panic", Version: "1.0.0",
		Routes: []ludus.Route{{Name: "boom", Method: http.MethodGet, Pattern: "/boom", Handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("partial"))
			panic("boom")
		}}},
	}
	impl := ludus.NewImplementation(p)
	for range 2 { // the plugin keeps serving after a panic
		response, err := impl.Handle(protocol.Request{Route: "boom", Method: http.MethodGet, Path: "/boom"})
		if err != nil {
			t.Fatalf("Handle() error = %v", err)
		}
		if response.Status != http.StatusInternalServerError || strings.Contains(string(response.Body), "partial") {
			t.Fatalf("response = %d %q, want 500 without the partial body", response.Status, response.Body)
		}
	}
}

func TestHostErrorStatus(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		message string
	}{
		{protocol.HostErrorf(protocol.CodeForbidden, "not your range"), http.StatusForbidden, "not your range"},
		{protocol.HostErrorf(protocol.CodeNotFound, "no such VM"), http.StatusNotFound, "no such VM"},
		{protocol.HostErrorf(protocol.CodeInvalid, "bad timeout"), http.StatusBadRequest, "bad timeout"},
		{protocol.HostErrorf(protocol.CodeExpired, "call ended"), http.StatusServiceUnavailable, "call ended"},
		{&protocol.HostError{Code: protocol.CodeUnavailable}, http.StatusServiceUnavailable, "unavailable"},
		{fmt.Errorf("list VMs: %w", protocol.HostErrorf(protocol.CodeForbidden, "denied")), http.StatusForbidden, "denied"},
		{errors.New("host RangeVMs: connection is shut down"), http.StatusInternalServerError, "host RangeVMs: connection is shut down"},
	} {
		recorder := httptest.NewRecorder()
		ludus.HostError(recorder, test.err)
		var body map[string]string
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		if recorder.Code != test.status || body["error"] != test.message {
			t.Errorf("HostError(%v) = %d %q, want %d %q", test.err, recorder.Code, body["error"], test.status, test.message)
		}
	}
}

// TestHostCallsCarryTheirCallID runs a plugin against a fake host over a real
// net/rpc connection and checks that every host call is made on behalf of the
// call that triggered it.
func TestHostCallsCarryTheirCallID(t *testing.T) {
	var mu sync.Mutex
	pluginCalls := map[string]protocol.CallID{}
	hostCalls := map[protocol.CallID]bool{}
	record := func(stage string, c *ludus.Call) {
		mu.Lock()
		defer mu.Unlock()
		pluginCalls[stage] = c.ID
	}
	host := &ludustest.Host{
		LicenseFunc: func(args protocol.CallArgs) (protocol.License, error) {
			mu.Lock()
			defer mu.Unlock()
			hostCalls[args.Call] = true
			return protocol.License{Valid: true}, nil
		},
		RangeVMsFunc: func(args protocol.RangeArgs) ([]protocol.VM, error) {
			mu.Lock()
			hostCalls[args.Call] = true
			mu.Unlock()
			switch args.Number {
			case 2:
				return nil, protocol.HostErrorf(protocol.CodeForbidden, "range 2 is not yours")
			case 3:
				return nil, errors.New("range 3 is malformed")
			}
			return []protocol.VM{{Name: "dc01", RangeNumber: args.Number}}, nil
		},
	}
	p := &ludus.Plugin{
		ID: "host-test", Name: "Host test", Version: "1.0.0",
		Init: func(c *ludus.Call, env ludus.Env) error {
			record("init", c)
			if c.Env.StateDir != "/state" {
				return fmt.Errorf("Env.StateDir = %q", c.Env.StateDir)
			}
			_, err := c.Host.License()
			return err
		},
		Jobs: []ludus.Job{{Name: "sync", Interval: time.Minute, Run: func(c *ludus.Call) error {
			record("job", c)
			_, err := c.Host.License()
			return err
		}}},
		Routes: []ludus.Route{
			{Name: "vms", Method: http.MethodGet, Pattern: "/vms", Handler: func(w http.ResponseWriter, r *http.Request) {
				c := ludus.CallFrom(r)
				record(fmt.Sprintf("handle-%d", c.Range.Number), c)
				vms, err := c.Host.RangeVMs(c.Range.Number)
				if err != nil {
					ludus.HostError(w, err)
					return
				}
				ludus.JSON(w, http.StatusOK, vms)
			}},
			{Name: "ranges", Method: http.MethodGet, Pattern: "/ranges", Handler: func(w http.ResponseWriter, r *http.Request) {
				_, err := ludus.CallFrom(r).Host.ListRanges()
				ludus.HostError(w, err)
			}},
		},
	}
	h := ludustest.Start(t, p, host, protocol.Environment{StateDir: "/state"})
	if jobs := h.Jobs(); len(jobs) != 1 || jobs[0].Name != "sync" || jobs[0].Interval != time.Minute {
		t.Fatalf("Jobs() = %+v", jobs)
	}

	response := h.Do(http.MethodGet, "/vms", ludustest.WithRange(&protocol.Range{Number: 1}))
	var vms []protocol.VM
	if err := json.Unmarshal(response.Body, &vms); response.Status != http.StatusOK || err != nil || len(vms) != 1 || vms[0].RangeNumber != 1 {
		t.Fatalf("GET /vms = %d %q", response.Status, response.Body)
	}
	if response := h.Do(http.MethodGet, "/vms", ludustest.WithRange(&protocol.Range{Number: 2})); response.Status != http.StatusForbidden || errorMessage(t, response) != "range 2 is not yours" {
		t.Fatalf("forbidden range = %d %q, want 403", response.Status, response.Body)
	}
	if response := h.Do(http.MethodGet, "/vms", ludustest.WithRange(&protocol.Range{Number: 3})); response.Status != http.StatusBadRequest {
		t.Fatalf("plain fake-host error = %d %q, want 400 (CodeInvalid)", response.Status, response.Body)
	}
	if response := h.Do(http.MethodGet, "/ranges"); response.Status != http.StatusServiceUnavailable {
		t.Fatalf("unset fake-host method = %d %q, want 503", response.Status, response.Body)
	}
	if err := h.RunJob("sync"); err != nil {
		t.Fatalf("RunJob(sync) error = %v", err)
	}
	if err := h.RunJob("missing"); err == nil {
		t.Fatal("RunJob(missing) succeeded")
	}

	mu.Lock()
	defer mu.Unlock()
	seen := map[protocol.CallID]string{}
	for stage, id := range pluginCalls {
		if id == "" || !hostCalls[id] {
			t.Errorf("%s call %q never reached the host (host saw %v)", stage, id, hostCalls)
		}
		if other, ok := seen[id]; ok {
			t.Errorf("%s and %s share CallID %q", stage, other, id)
		}
		seen[id] = stage
	}
	if len(pluginCalls) != 5 {
		t.Errorf("plugin calls = %v, want init, job, and three handles", pluginCalls)
	}
}

func TestHostWithoutHostServiceIsUnavailable(t *testing.T) {
	var hostErr error
	p := &ludus.Plugin{
		ID: "no-host", Name: "No host", Version: "1.0.0",
		Init: func(c *ludus.Call, env ludus.Env) error {
			_, hostErr = c.Host.License()
			return nil
		},
	}
	ludustest.Start(t, p, nil, protocol.Environment{})
	if !errors.Is(hostErr, ludus.ErrUnavailable) {
		t.Fatalf("License() without a host service error = %v, want ErrUnavailable", hostErr)
	}
}

func TestVMHookWithoutHandlerContinues(t *testing.T) {
	p := &ludus.Plugin{ID: "hooks", Name: "Hooks", Version: "1.0.0", Level: ludus.System, VMHooks: protocol.VMHookCapabilities{Start: true}}
	response, err := ludustest.Start(t, p, nil, protocol.Environment{}).VMHook(protocol.VMHookRequest{Operation: protocol.VMHookStart})
	if err != nil || response.Decision != protocol.VMHookContinue {
		t.Fatalf("VMHook() = %+v, %v; want VMHookContinue", response, err)
	}
}

func manifestPlugin() *ludus.Plugin {
	handler := func(http.ResponseWriter, *http.Request) {}
	return &ludus.Plugin{
		ID: "manifest-test", Name: "Manifest test", Version: "2.1.0", Description: "Tests manifests", Author: "Bad Sector Labs",
		Level:                    ludus.System,
		RunAs:                    ludus.RunAsRoot,
		ForwardCallerCredentials: true,
		UI:                       &ludus.UI{RangeScoped: true},
		Routes: []ludus.Route{
			{Name: "status", Method: http.MethodGet, Pattern: "/manifest-test/status", Handler: handler},
			{Name: "quota", Method: http.MethodPost, Pattern: "/user/quotas", Scope: ludus.APIScope, Handler: handler},
		},
		VMHooks: protocol.VMHookCapabilities{Start: true, Stop: true},
	}
}

func TestServeManifestFlag(t *testing.T) {
	switch os.Getenv("LUDUS_SDK_TEST_SERVE") {
	case "valid":
		ludus.Serve(manifestPlugin())
		return
	case "invalid":
		p := manifestPlugin()
		p.Level = ludus.Standard // api-scoped routes and VM hooks need system level
		ludus.Serve(p)
		return
	}

	run := func(mode string) (stdout, stderr []byte, err error) {
		var out, errOut bytes.Buffer
		cmd := exec.Command(os.Args[0], "-test.run=^TestServeManifestFlag$", "--", ludus.ManifestFlag)
		cmd.Env = append(os.Environ(), "LUDUS_SDK_TEST_SERVE="+mode)
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err = cmd.Run()
		return out.Bytes(), errOut.Bytes(), err
	}

	stdout, stderr, err := run("valid")
	if err != nil {
		t.Fatalf("plugin --ludus-manifest: %v\n%s", err, stderr)
	}
	manifest, err := protocol.ParseManifest(stdout)
	if err != nil {
		t.Fatalf("ParseManifest(output) error = %v\n%s", err, stdout)
	}
	metadata, err := ludus.NewImplementation(manifestPlugin()).Metadata()
	if err != nil {
		t.Fatalf("Metadata() error = %v", err)
	}
	if err := manifest.CheckMetadata(metadata); err != nil {
		t.Fatalf("CheckMetadata() error = %v", err)
	}
	if manifest.Executable != protocol.ExecutableFile || manifest.ProtocolVersion != protocol.ProtocolVersion ||
		manifest.UI != protocol.UIFile || !manifest.RangeScoped || manifest.Description != "Tests manifests" || manifest.Author != "Bad Sector Labs" {
		t.Fatalf("manifest = %+v", manifest)
	}
	generated, err := ludus.ManifestJSON(manifestPlugin())
	if err != nil || !bytes.Equal(generated, stdout) {
		t.Fatalf("ManifestJSON() = %q, %v; want the --ludus-manifest output", generated, err)
	}

	stdout, stderr, err = run("invalid")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(stdout) != 0 || !bytes.Contains(stderr, []byte("level system")) {
		t.Fatalf("invalid declaration: err=%v stdout=%q stderr=%q; want exit 1 with the reason on stderr", err, stdout, stderr)
	}
}

// The SDK refuses to send VM actions the GUI would reject, so the plugin
// author sees the declaration error instead of silently missing buttons.
func TestVMActionsRejectInvalidIcons(t *testing.T) {
	var actions []ludus.VMAction
	p := &ludus.Plugin{ID: "vm-actions", Name: "VM actions", Version: "1", App: http.NotFoundHandler(), VMActions: &ludus.VMActions{
		List:   func(*ludus.Call) ([]ludus.VMAction, error) { return actions, nil },
		Launch: func(*ludus.Call, string) (*ludus.AppLaunch, error) { return nil, ludus.ErrNotFound },
	}}
	h := ludustest.Start(t, p, nil, protocol.Environment{})
	list := func() protocol.Response {
		return h.Do(http.MethodGet, protocol.VMActionsPattern, ludustest.WithRange(&protocol.Range{RecordID: "r1", Number: 1}))
	}
	actions = []ludus.VMAction{
		{ID: "ssh", ProxmoxID: 1, Label: "SSH", Icon: "Terminal", Color: "#c6a153"},
		{ID: "rdp", ProxmoxID: 1, Label: "RDP", IconSVG: `<svg viewBox="0 0 1 1"><path d="M0 0h1v1H0z"/></svg>`},
	}
	if response := list(); response.Status != http.StatusOK || !strings.Contains(string(response.Body), `"iconSVG":"\u003csvg`) {
		t.Fatalf("valid actions = %d %s", response.Status, response.Body)
	}
	for _, invalid := range []ludus.VMAction{
		{ID: "a", Icon: "../index"},
		{ID: "b", IconSVG: "<img src=x>"},
		{ID: "c", Color: "red"},
	} {
		actions = []ludus.VMAction{invalid}
		if response := list(); response.Status != http.StatusInternalServerError {
			t.Errorf("invalid action %+v = %d, want 500", invalid, response.Status)
		}
	}
}
