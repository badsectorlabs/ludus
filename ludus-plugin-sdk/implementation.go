package ludus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"sync"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// NewImplementation returns the protocol.Implementation that Serve runs for p.
// It does not validate p; Serve and ManifestJSON do.
func NewImplementation(p *Plugin) protocol.Implementation {
	impl := &implementation{
		plugin:  p,
		routes:  make(map[string]Route, len(p.Routes)),
		jobs:    make(map[string]Job, len(p.Jobs)),
		maxBody: protocol.MaxBodyBytes,
		logger:  Logger(),
		env:     &Env{},
	}
	for _, route := range p.Routes {
		impl.routes[route.Name] = route
	}
	for _, job := range p.Jobs {
		impl.jobs[job.Name] = job
	}
	return impl
}

type implementation struct {
	plugin  *Plugin
	routes  map[string]Route
	jobs    map[string]Job
	maxBody int
	logger  *slog.Logger

	mu   sync.RWMutex
	env  *Env
	host *protocol.HostClient
}

var _ protocol.Implementation = (*implementation)(nil)

func (i *implementation) Metadata() (protocol.Metadata, error) {
	return metadata(i.plugin), nil
}

func (i *implementation) Initialize(request protocol.InitializeRequest, host *protocol.HostClient) (protocol.InitializeResponse, error) {
	env := request.Env
	i.mu.Lock()
	i.env = &env
	i.host = host
	i.mu.Unlock()

	if i.plugin.Init != nil {
		c := i.call(request.Call)
		if err := i.protect("Init", func() error { return i.plugin.Init(c, env) }); err != nil {
			return protocol.InitializeResponse{}, err
		}
	}
	response := protocol.InitializeResponse{Jobs: make([]protocol.ScheduledJob, 0, len(i.plugin.Jobs))}
	for _, job := range i.plugin.Jobs {
		response.Jobs = append(response.Jobs, protocol.ScheduledJob{Name: job.Name, Interval: job.Interval})
	}
	if i.plugin.WebApplication != nil {
		response.WebApplication = i.plugin.WebApplication()
	}
	return response, nil
}

func (i *implementation) Handle(request protocol.Request) (protocol.Response, error) {
	route, ok := i.routes[request.Route]
	if !ok {
		return errorResponse(http.StatusNotFound, fmt.Sprintf("unknown plugin route %q", request.Route)), nil
	}

	c := i.call(request.Call)
	c.GuacAppBasePath = request.GuacAppBasePath
	c.Auth = request.Auth
	c.User = request.User
	c.Range = request.Range

	target := request.Path
	if request.RawQuery != "" {
		target += "?" + request.RawQuery
	}
	ctx, cancel := context.WithCancel(withCall(context.Background(), c))
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, request.Method, target, bytes.NewReader(request.Body))
	if err != nil {
		return errorResponse(http.StatusBadRequest, "invalid plugin request: "+err.Error()), nil
	}
	r.Header = http.Header(request.Header)
	if r.Header == nil {
		r.Header = http.Header{}
	}
	r.RemoteAddr = request.RemoteAddr
	r.RequestURI = target

	w := &responseWriter{header: http.Header{}, limit: i.maxBody}
	if err := i.protect("route "+route.Name, func() error {
		route.Handler(w, r)
		return nil
	}); err != nil {
		return errorResponse(http.StatusInternalServerError, "plugin handler failed"), nil
	}
	return w.response(), nil
}

func (i *implementation) RunJob(request protocol.JobRequest) error {
	job, ok := i.jobs[request.Name]
	if !ok {
		return fmt.Errorf("unknown job %q", request.Name)
	}
	c := i.call(request.Call)
	return i.protect("job "+job.Name, func() error { return job.Run(c) })
}

func (i *implementation) VMHook(request protocol.VMHookRequest) (protocol.VMHookResponse, error) {
	if i.plugin.VMHook == nil {
		return protocol.VMHookResponse{Decision: protocol.VMHookContinue}, nil
	}
	var response protocol.VMHookResponse
	c := i.call(request.Call)
	err := i.protect("VMHook", func() error {
		var err error
		response, err = i.plugin.VMHook(c, request)
		return err
	})
	return response, err
}

func (i *implementation) Shutdown() error {
	if i.plugin.Shutdown == nil {
		return nil
	}
	return i.protect("Shutdown", i.plugin.Shutdown)
}

// call returns a Call bound to id with the current environment and host client.
func (i *implementation) call(id protocol.CallID) *Call {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return &Call{ID: id, Host: &Host{client: i.host, call: id}, Env: i.env}
}

// protect runs fn and turns a panic into an error so that one failing
// callback does not take down the plugin process.
func (i *implementation) protect(what string, fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			i.logger.Error("plugin panic", "in", what, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
			err = fmt.Errorf("%s panicked: %v", what, recovered)
		}
	}()
	return fn()
}

func errorResponse(status int, message string) protocol.Response {
	body, _ := json.Marshal(map[string]string{"error": message})
	return protocol.Response{
		Status: status,
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   body,
	}
}

// responseWriter buffers a handler's response up to limit bytes. Writes past
// the limit fail and discard the body, and the response becomes a 500.
type responseWriter struct {
	header   http.Header
	written  http.Header // header snapshot taken at WriteHeader
	status   int
	body     []byte
	limit    int
	overflow bool
}

func (w *responseWriter) Header() http.Header { return w.header }

func (w *responseWriter) WriteHeader(status int) {
	if status < 100 || status > 999 {
		panic(fmt.Sprintf("invalid WriteHeader code %v", status))
	}
	// Informational responses cannot be relayed; the final status follows.
	if w.status != 0 || status < 200 {
		return
	}
	w.status = status
	w.written = w.header.Clone()
}

func (w *responseWriter) Write(data []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if w.overflow {
		return 0, w.overflowError()
	}
	if len(w.body)+len(data) > w.limit {
		w.overflow = true
		w.body = nil
		return 0, w.overflowError()
	}
	w.body = append(w.body, data...)
	return len(data), nil
}

func (w *responseWriter) overflowError() error {
	return fmt.Errorf("plugin response exceeds %d bytes", w.limit)
}

func (w *responseWriter) response() protocol.Response {
	if w.overflow {
		return errorResponse(http.StatusInternalServerError, w.overflowError().Error())
	}
	w.WriteHeader(http.StatusOK)
	return protocol.Response{Status: w.status, Header: w.written, Body: w.body}
}
