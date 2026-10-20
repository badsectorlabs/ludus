package protocol_test

import (
	"strings"
	"testing"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestParseManifestAppliesDefaults(t *testing.T) {
	m, err := protocol.ParseManifest([]byte(`{
		"schemaVersion": 1,
		"protocolVersion": 1,
		"id": "traffic-observer",
		"name": "Ludus Traffic Observer",
		"version": "1.3.0",
		"description": "",
		"author": "",
		"executable": "plugin",
		"routes": [{"name": "vms", "method": "GET", "pattern": "/traffic-observer/vms"}],
		"vmHooks": {}
	}`))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if m.Level != protocol.LevelStandard || m.RunAs != protocol.RunAsLudus || m.Routes[0].Scope != protocol.ScopePlugin {
		t.Fatalf("defaults = level %q, runAs %q, scope %q", m.Level, m.RunAs, m.Routes[0].Scope)
	}
}

func TestParseManifestIsStrict(t *testing.T) {
	const base = `{"schemaVersion":1,"protocolVersion":1,"id":"ok-plugin","name":"OK","version":"1","description":"","author":"","executable":"plugin","vmHooks":{}`
	for name, data := range map[string]string{
		"unknown field": base + `,"system":true}`,
		"trailing data": base + `}{}`,
		"invalid":       `{"schemaVersion":2}`,
	} {
		if _, err := protocol.ParseManifest([]byte(data)); err == nil {
			t.Errorf("%s: ParseManifest() succeeded", name)
		}
	}
	if _, err := protocol.ParseManifest([]byte(base + `}`)); err != nil {
		t.Fatalf("base manifest: %v", err)
	}
}

func validManifest() protocol.Manifest {
	return protocol.Manifest{
		SchemaVersion:   protocol.ManifestSchemaVersion,
		ProtocolVersion: protocol.ProtocolVersion,
		ID:              "test-plugin",
		Name:            "Test plugin",
		Version:         "1.0.0",
		Level:           protocol.LevelSystem,
		RunAs:           protocol.RunAsLudus,
		Executable:      protocol.ExecutableFile,
	}
}

func TestManifestValidate(t *testing.T) {
	route := func(name, method, pattern string, scope protocol.RouteScope) protocol.Route {
		return protocol.Route{Name: name, Method: method, Pattern: pattern, Scope: scope}
	}
	for _, test := range []struct {
		name   string
		modify func(*protocol.Manifest)
		valid  bool
	}{
		{"system executable", func(m *protocol.Manifest) {}, true},
		{"UI only", func(m *protocol.Manifest) { m.Executable, m.ProtocolVersion, m.UI = "", 0, protocol.UIFile }, true},
		{"neither executable nor UI", func(m *protocol.Manifest) { m.Executable = "" }, false},
		{"bad id", func(m *protocol.Manifest) { m.ID = "Bad_ID" }, false},
		{"schema version", func(m *protocol.Manifest) { m.SchemaVersion = 2 }, false},
		{"unknown level", func(m *protocol.Manifest) { m.Level = "admin" }, false},
		{"unknown runAs", func(m *protocol.Manifest) { m.RunAs = "nobody" }, false},
		{"executable path", func(m *protocol.Manifest) { m.Executable = "bin/plugin" }, false},
		{"executable without protocol version", func(m *protocol.Manifest) { m.ProtocolVersion = 0 }, false},
		{"executable with other protocol version", func(m *protocol.Manifest) { m.ProtocolVersion = 2 }, false},
		{"UI path", func(m *protocol.Manifest) { m.UI = "index.html" }, false},
		{"UI-only routes", func(m *protocol.Manifest) {
			m.Executable, m.ProtocolVersion, m.UI = "", 0, protocol.UIFile
			m.Routes = []protocol.Route{route("a", "GET", "/a", protocol.ScopePlugin)}
		}, false},
		{"UI-only credentials", func(m *protocol.Manifest) {
			m.Executable, m.ProtocolVersion, m.UI = "", 0, protocol.UIFile
			m.ForwardCallerCredentials = true
		}, false},
		{"VM hooks on system", func(m *protocol.Manifest) { m.VMHooks.Start = true }, true},
		{"VM hooks on standard", func(m *protocol.Manifest) { m.Level = protocol.LevelStandard; m.VMHooks.Start = true }, false},
		{"api scope on system", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("q", "GET", "/user/quotas", protocol.ScopeAPI)}
		}, true},
		{"api scope on standard", func(m *protocol.Manifest) {
			m.Level = protocol.LevelStandard
			m.Routes = []protocol.Route{route("q", "GET", "/user/quotas", protocol.ScopeAPI)}
		}, false},
		{"api scope /plugins", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("p", "GET", "/plugins", protocol.ScopeAPI)}
		}, false},
		{"api scope /plugins/", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("p", "GET", "/plugins/other/rpc/x", protocol.ScopeAPI)}
		}, false},
		{"api scope /pluginsx", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("p", "GET", "/pluginsx", protocol.ScopeAPI)}
		}, true},
		{"plugin scope /plugins", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("p", "GET", "/plugins", protocol.ScopePlugin)}
		}, true},
		{"unknown scope", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("p", "GET", "/x", "global")}
		}, false},
		{"duplicate route names", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/a", ""), route("a", "GET", "/b", "")}
		}, false},
		{"empty route name", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("", "GET", "/a", "")}
		}, false},
		{"duplicate method and pattern", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/a", ""), route("b", "GET", "/a", protocol.ScopePlugin)}
		}, false},
		{"same pattern, other method", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/a", ""), route("b", "POST", "/a", "")}
		}, true},
		{"same pattern, other scope", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/a", ""), route("b", "GET", "/a", protocol.ScopeAPI)}
		}, true},
		{"unsupported method", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "HEAD", "/a", "")}
		}, false},
		{"relative pattern", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "a", "")}
		}, false},
		{"wildcard pattern", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/vms/{id}", "")}
		}, false},
		{"dot-dot pattern", func(m *protocol.Manifest) {
			m.Routes = []protocol.Route{route("a", "GET", "/a/../b", "")}
		}, false},
	} {
		m := validManifest()
		test.modify(&m)
		err := m.Validate()
		if (err == nil) != test.valid {
			t.Errorf("%s: Validate() error = %v, want valid=%v", test.name, err, test.valid)
		}
	}
}

func TestCheckMetadata(t *testing.T) {
	manifest := validManifest()
	manifest.ForwardCallerCredentials = true
	manifest.VMHooks.Start = true
	manifest.Routes = []protocol.Route{
		{Name: "a", Method: "GET", Pattern: "/a", Scope: protocol.ScopePlugin},
		{Name: "b", Method: "POST", Pattern: "/b", Scope: protocol.ScopeAPI},
	}
	matching := func() protocol.Metadata {
		return protocol.Metadata{
			ID: manifest.ID, Name: manifest.Name, Version: manifest.Version,
			Description:              "descriptions may differ",
			Level:                    protocol.LevelSystem,
			ForwardCallerCredentials: true,
			VMHooks:                  protocol.VMHookCapabilities{Start: true},
			// Order and the default scope do not matter; RunAs defaults to ludus.
			Routes: []protocol.Route{
				{Name: "b", Method: "POST", Pattern: "/b", Scope: protocol.ScopeAPI},
				{Name: "a", Method: "GET", Pattern: "/a"},
			},
		}
	}
	if err := manifest.CheckMetadata(matching()); err != nil {
		t.Fatalf("CheckMetadata(matching) error = %v", err)
	}

	for _, test := range []struct {
		mismatch string
		modify   func(*protocol.Metadata)
	}{
		{"identity", func(md *protocol.Metadata) { md.Version = "1.0.1" }},
		{"identity", func(md *protocol.Metadata) { md.ID = "other-plugin" }},
		{"level", func(md *protocol.Metadata) { md.Level = "" }},
		{"runAs", func(md *protocol.Metadata) { md.RunAs = protocol.RunAsRoot }},
		{"forwardCallerCredentials", func(md *protocol.Metadata) { md.ForwardCallerCredentials = false }},
		{"vmHooks", func(md *protocol.Metadata) { md.VMHooks.Stop = true }},
		{"routes", func(md *protocol.Metadata) { md.Routes[0].Scope = protocol.ScopePlugin }},
		{"routes", func(md *protocol.Metadata) { md.Routes[1].Pattern = "/c" }},
		{"routes", func(md *protocol.Metadata) { md.Routes = md.Routes[:1] }},
		{"routes", func(md *protocol.Metadata) { md.Routes = append(md.Routes, md.Routes[1]) }},
	} {
		md := matching()
		test.modify(&md)
		err := manifest.CheckMetadata(md)
		if err == nil || !strings.Contains(err.Error(), test.mismatch) {
			t.Errorf("CheckMetadata(%+v) error = %v, want %s mismatch", md, err, test.mismatch)
		}
	}
}

func TestManifestFromMetadataValidates(t *testing.T) {
	md := protocol.Metadata{
		ID: "test-plugin", Name: "Test", Version: "1.0.0",
		Routes: []protocol.Route{{Name: "a", Method: "GET", Pattern: "/a"}},
	}
	m := protocol.ManifestFromMetadata(md, true, true)
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if err := m.CheckMetadata(md); err != nil {
		t.Fatalf("CheckMetadata() error = %v", err)
	}
	if m.Level != protocol.LevelStandard || m.RunAs != protocol.RunAsLudus || m.UI != protocol.UIFile || !m.RangeScoped {
		t.Fatalf("manifest = %+v", m)
	}
	if m := protocol.ManifestFromMetadata(md, false, true); m.UI != "" || m.RangeScoped {
		t.Fatalf("manifest without UI = %+v", m)
	}
}
