package ludusapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// testProcessPlugin is served by the re-executed test binary. It calls the host
// service from Initialize, Handle, and RunJob, and reports whether the CallIDs
// of earlier calls still work.
type testProcessPlugin struct {
	metadata protocol.Metadata

	mu       sync.Mutex
	host     *protocol.HostClient
	initCall protocol.CallID
	jobCall  protocol.CallID
	jobErr   string
}

func hostErrorCode(err error) string {
	var hostErr *protocol.HostError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &hostErr):
		return string(hostErr.Code)
	default:
		return "transport: " + err.Error()
	}
}

func (p *testProcessPlugin) Metadata() (protocol.Metadata, error) { return p.metadata, nil }

func (p *testProcessPlugin) Initialize(request protocol.InitializeRequest, host *protocol.HostClient) (protocol.InitializeResponse, error) {
	if host == nil {
		return protocol.InitializeResponse{}, errors.New("no host service")
	}
	if _, err := host.License(request.Call); err != nil {
		return protocol.InitializeResponse{}, fmt.Errorf("host call during Initialize: %w", err)
	}
	p.mu.Lock()
	p.host, p.initCall = host, request.Call
	p.mu.Unlock()
	return protocol.InitializeResponse{Jobs: []protocol.ScheduledJob{{Name: "probe", Interval: time.Hour}}}, nil
}

func (p *testProcessPlugin) Handle(request protocol.Request) (protocol.Response, error) {
	p.mu.Lock()
	host, initCall, jobCall, jobErr := p.host, p.initCall, p.jobCall, p.jobErr
	p.mu.Unlock()
	report := map[string]string{
		"handle":     hostErrorCode(func() error { _, err := host.License(request.Call); return err }()),
		"initialize": hostErrorCode(func() error { _, err := host.License(initCall); return err }()),
		"job":        "not run",
		"jobErr":     jobErr,
		"route":      request.Route,
		"user":       request.Auth.UserID,
	}
	if jobCall != "" {
		report["job"] = hostErrorCode(func() error { _, err := host.License(jobCall); return err }())
	}
	body, _ := json.Marshal(report)
	return protocol.Response{Status: http.StatusOK, Header: map[string][]string{"Content-Type": {"application/json"}, "Set-Cookie": {"escape=1"}}, Body: body}, nil
}

func (p *testProcessPlugin) RunJob(request protocol.JobRequest) error {
	p.mu.Lock()
	host := p.host
	p.mu.Unlock()
	_, err := host.License(request.Call)
	p.mu.Lock()
	p.jobCall, p.jobErr = request.Call, hostErrorCode(err)
	p.mu.Unlock()
	return err
}

func (p *testProcessPlugin) VMHook(protocol.VMHookRequest) (protocol.VMHookResponse, error) {
	return protocol.VMHookResponse{Decision: protocol.VMHookContinue}, nil
}

func (p *testProcessPlugin) Shutdown() error { return nil }

// TestPluginProcessHelper is the plugin process: the test binary re-executed
// through a package's plugin script, without a compiler or fixture binary.
func TestPluginProcessHelper(t *testing.T) {
	if os.Getenv("LUDUS_PLUGIN_PROCESS_HELPER") != "1" {
		return
	}
	plugin := &testProcessPlugin{}
	if err := json.Unmarshal([]byte(os.Getenv("LUDUS_TEST_PLUGIN_METADATA")), &plugin.metadata); err != nil {
		panic(err)
	}
	protocol.Serve(plugin)
	os.Exit(0)
}

func metadataFor(m protocol.Manifest) protocol.Metadata {
	return protocol.Metadata{ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description, Author: m.Author,
		Level: m.Level, RunAs: m.RunAs, Routes: m.Routes, VMHooks: m.VMHooks, ForwardCallerCredentials: m.ForwardCallerCredentials}
}

// testPluginScript is a plugin executable that serves metadata.
func testPluginScript(t *testing.T, metadata protocol.Metadata) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
	return "#!/bin/sh\nexport LUDUS_PLUGIN_PROCESS_HELPER=1\nexport LUDUS_TEST_PLUGIN_METADATA=" + quote(string(encoded)) +
		"\nexec " + quote(executable) + " -test.run='^TestPluginProcessHelper$'\n"
}

type callReport struct {
	Handle, Initialize, Job, JobErr, Route, User string
}

func TestPluginCallIDsExpireWhenTheirCallReturns(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("process-plugin")
	m.Routes = []protocol.Route{{Name: "calls", Method: "GET", Pattern: "/calls", Scope: protocol.ScopePlugin}}
	id := env.upload(env.ownerToken, "", testPackage(t, m, map[string]string{protocol.ExecutableFile: testPluginScript(t, metadataFor(m))}))

	// Initialize calls the host while Activate holds the resource lock.
	activated := make(chan struct{})
	go func() {
		defer close(activated)
		env.approve(id)
	}()
	select {
	case <-activated:
	case <-time.After(time.Minute):
		t.Fatal("Activate did not complete while Initialize called the host")
	}
	plugin := env.server.pluginByKey(pluginKey(protocol.SourceUploaded, "", id))
	if plugin == nil || plugin.exited() {
		t.Fatal("activated plugin is not running")
	}

	var report callReport
	deadline := time.Now().Add(30 * time.Second)
	for {
		response := env.call(env.ownerToken, "GET", APIBasePath+"/plugins/process-plugin/rpc/calls", nil, 200)
		if response.Header().Get("Set-Cookie") != "" {
			t.Fatal("plugin-scope response set a host cookie")
		}
		if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Job == string(protocol.CodeExpired) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if report.Handle != "" || report.Route != "calls" || report.User != "OWNER" {
		t.Fatalf("host call during Handle failed: %+v", report)
	}
	if report.Initialize != string(protocol.CodeExpired) {
		t.Fatalf("Initialize CallID still valid after Initialize returned: %+v", report)
	}
	if report.JobErr != "" || report.Job != string(protocol.CodeExpired) {
		t.Fatalf("job CallID: %+v", report)
	}

	// Uninstall ends the process and its jobs.
	env.call(env.ownerToken, "DELETE", APIBasePath+"/plugins/"+id+"?uninstall=true", nil, 200)
	if !plugin.exited() || env.server.pluginByKey(plugin.key()) != nil {
		t.Fatal("uninstall did not stop the plugin")
	}
}

func TestPluginMetadataMustMatchManifest(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("declared-plugin")
	for name, mutate := range map[string]func(*protocol.Metadata){
		"level": func(md *protocol.Metadata) { md.Level = protocol.LevelSystem },
		"runAs": func(md *protocol.Metadata) { md.RunAs = protocol.RunAsRoot },
		"routes": func(md *protocol.Metadata) {
			md.Routes = []protocol.Route{{Name: "extra", Method: "GET", Pattern: "/extra"}}
		},
		"forwardCallerCredentials": func(md *protocol.Metadata) { md.ForwardCallerCredentials = true },
		"identity":                 func(md *protocol.Metadata) { md.Version = "2" },
	} {
		t.Run(name, func(t *testing.T) {
			metadata := metadataFor(m)
			mutate(&metadata)
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, protocol.ExecutableFile), []byte(testPluginScript(t, metadata)), 0700); err != nil {
				t.Fatal(err)
			}
			_, err := env.server.startPlugin(dir, m, protocol.SourceServer, "")
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("mismatched %s: err = %v", name, err)
			}
			if len(env.server.pluginSnapshot()) != 0 {
				t.Fatal("a plugin with mismatched metadata was registered")
			}
		})
	}
}

func TestInstalledSystemPluginServesAPIRoutes(t *testing.T) {
	env := newPluginTestEnv(t)
	m := testExecutableManifest("system-plugin")
	m.Level = protocol.LevelSystem
	m.Routes = []protocol.Route{{Name: "calls", Method: "GET", Pattern: "/system-plugin/calls", Scope: protocol.ScopeAPI}}
	writeInstalledPackage(t, env.server, m, testPluginScript(t, metadataFor(m)))
	// A second package claiming the same API route is not started.
	duplicate := m
	duplicate.ID = "zz-duplicate-plugin"
	writeInstalledPackage(t, env.server, duplicate, testPluginScript(t, metadataFor(duplicate)))

	env.server.StartPlugins()
	if len(env.server.pluginSnapshot()) != 1 {
		t.Fatalf("running plugins = %d, want 1", len(env.server.pluginSnapshot()))
	}
	record, err := env.pb.FindFirstRecordByData("plugin_resources", "pluginID", "system-plugin")
	if err != nil || recordSource(record) != protocol.SourceServer || record.GetString("state") != "approved" {
		t.Fatalf("installed package was not mirrored: %v", err)
	}

	path := APIBasePath + "/system-plugin/calls"
	var report callReport
	if err := json.Unmarshal(env.call(env.adminToken, "GET", path, nil, 200).Body.Bytes(), &report); err != nil || report.Handle != "" {
		t.Fatalf("API route report = %+v, %v", report, err)
	}
	// API-scoped routes extend Ludus itself: any authenticated caller reaches
	// them without the record being shared, and the plugin sees who called.
	if err := json.Unmarshal(env.call(env.ownerToken, "GET", path, nil, 200).Body.Bytes(), &report); err != nil || report.User != "OWNER" {
		t.Fatalf("API route report for owner = %+v, %v", report, err)
	}
	env.call(env.ownerToken, "POST", path, nil, 404)
	env.call(env.ownerToken, "GET", APIBasePath+"/system-plugin/undeclared", nil, 404)
}
