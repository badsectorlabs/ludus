package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
)

const (
	ManifestSchemaVersion = 1
	ManifestFile          = "plugin.json"
	ExecutableFile        = "plugin"
	UIFile                = "ui/index.html"
)

var pluginIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,63}$`)

var routeMethods = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}

// ValidPluginID reports whether id is a valid package ID.
func ValidPluginID(id string) bool { return pluginIDPattern.MatchString(id) }

// Manifest is plugin.json: an inert declaration the host reads without
// running anything. A running plugin's Metadata must match it.
type Manifest struct {
	SchemaVersion            int    `json:"schemaVersion"`
	ProtocolVersion          int    `json:"protocolVersion,omitempty"`
	ID                       string `json:"id"`
	Name                     string `json:"name"`
	Version                  string `json:"version"`
	Description              string `json:"description"`
	Author                   string `json:"author"`
	Level                    Level  `json:"level,omitempty"`
	RunAs                    RunAs  `json:"runAs,omitempty"`
	Executable               string `json:"executable,omitempty"`
	UI                       string `json:"ui,omitempty"`
	RangeScoped              bool   `json:"rangeScoped,omitempty"`
	ForwardCallerCredentials bool   `json:"forwardCallerCredentials,omitempty"`
	// App: the executable serves a browser application on the Ludus origin
	// under /plugin-apps/{resourceID}/ (see Metadata.App).
	App bool `json:"app,omitempty"`
	// VMActions: the GUI range editor offers the plugin's per-VM app launches.
	VMActions bool               `json:"vmActions,omitempty"`
	Routes    []Route            `json:"routes,omitempty"`
	VMHooks   VMHookCapabilities `json:"vmHooks"`
}

// ParseManifest strictly decodes, normalizes, and validates plugin.json.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("invalid %s: %w", ManifestFile, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Manifest{}, fmt.Errorf("unexpected trailing %s data", ManifestFile)
	}
	m.Normalize()
	if err := m.Validate(); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Normalize applies defaults: standard level, ludus run-as user, plugin route scope.
func (m *Manifest) Normalize() {
	if m.Level == "" {
		m.Level = LevelStandard
	}
	if m.RunAs == "" {
		m.RunAs = RunAsLudus
	}
	for i := range m.Routes {
		m.Routes[i].Scope = m.Routes[i].EffectiveScope()
	}
}

// Validate checks a normalized manifest.
func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchemaVersion || !ValidPluginID(m.ID) ||
		strings.TrimSpace(m.Name) == "" || strings.TrimSpace(m.Version) == "" ||
		len(m.Name) > 120 || len(m.Version) > 64 || len(m.Description) > 2000 || len(m.Author) > 200 {
		return errors.New("invalid plugin manifest identity or schema version")
	}
	if m.Level != LevelStandard && m.Level != LevelSystem {
		return fmt.Errorf("level must be %q or %q", LevelStandard, LevelSystem)
	}
	if m.RunAs != RunAsLudus && m.RunAs != RunAsRoot {
		return fmt.Errorf("runAs must be %q or %q", RunAsLudus, RunAsRoot)
	}
	if m.Executable != "" && (m.Executable != ExecutableFile || m.ProtocolVersion != ProtocolVersion) {
		return fmt.Errorf("executable must be %s and protocolVersion must be %d", ExecutableFile, ProtocolVersion)
	}
	if m.UI != "" && m.UI != UIFile {
		return fmt.Errorf("ui must be %s", UIFile)
	}
	if m.UI == "" && m.Executable == "" {
		return errors.New("package needs an executable or UI")
	}
	if m.Executable == "" && (len(m.Routes) > 0 || m.VMHooks.Any() || m.ForwardCallerCredentials || m.App) {
		return errors.New("routes, VM hooks, apps, and forwardCallerCredentials require an executable")
	}
	if m.VMHooks.Any() && m.Level != LevelSystem {
		return errors.New("VM hooks require level system")
	}
	if m.VMActions && (!m.App || !hasPluginRoute(m.Routes, "GET", VMActionsPattern) || !hasPluginRoute(m.Routes, "POST", VMActionLaunchPattern)) {
		return fmt.Errorf("vmActions requires app and the routes GET %s and POST %s", VMActionsPattern, VMActionLaunchPattern)
	}
	return validateRoutes(m.Routes, m.Level)
}

func hasPluginRoute(routes []Route, method, pattern string) bool {
	return slices.ContainsFunc(routes, func(r Route) bool {
		return r.Method == method && r.Pattern == pattern && r.EffectiveScope() == ScopePlugin
	})
}

func validateRoutes(routes []Route, level Level) error {
	names := map[string]bool{}
	paths := map[string]bool{}
	for _, route := range routes {
		scope := route.EffectiveScope()
		if route.Name == "" || names[route.Name] {
			return errors.New("plugin routes need unique names")
		}
		names[route.Name] = true
		if !slices.Contains(routeMethods, route.Method) {
			return fmt.Errorf("unsupported plugin route method %q", route.Method)
		}
		if !strings.HasPrefix(route.Pattern, "/") || strings.ContainsAny(route.Pattern, "?#{}\\ ") ||
			strings.Contains(route.Pattern, "..") || strings.Contains(route.Pattern, "//") {
			return fmt.Errorf("plugin route %q must be an exact absolute path", route.Pattern)
		}
		switch scope {
		case ScopePlugin:
		case ScopeAPI:
			if level != LevelSystem {
				return errors.New("api-scoped routes require level system")
			}
			if route.Pattern == "/plugins" || strings.HasPrefix(route.Pattern, "/plugins/") {
				return errors.New("api-scoped routes must not start with /plugins")
			}
		default:
			return fmt.Errorf("unsupported route scope %q", route.Scope)
		}
		key := string(scope) + " " + route.Method + " " + route.Pattern
		if paths[key] {
			return fmt.Errorf("duplicate plugin route %s %s", route.Method, route.Pattern)
		}
		paths[key] = true
	}
	return nil
}

// CheckMetadata reports how a running plugin's Metadata disagrees with the
// manifest the administrator approved.
func (m Manifest) CheckMetadata(md Metadata) error {
	var mismatches []string
	if md.ID != m.ID || md.Name != m.Name || md.Version != m.Version {
		mismatches = append(mismatches, "identity")
	}
	level, runAs := md.Level, md.RunAs
	if level == "" {
		level = LevelStandard
	}
	if runAs == "" {
		runAs = RunAsLudus
	}
	if level != m.Level {
		mismatches = append(mismatches, "level")
	}
	if runAs != m.RunAs {
		mismatches = append(mismatches, "runAs")
	}
	if md.ForwardCallerCredentials != m.ForwardCallerCredentials {
		mismatches = append(mismatches, "forwardCallerCredentials")
	}
	if md.App != m.App {
		mismatches = append(mismatches, "app")
	}
	if md.VMActions != m.VMActions {
		mismatches = append(mismatches, "vmActions")
	}
	if md.VMHooks != m.VMHooks {
		mismatches = append(mismatches, "vmHooks")
	}
	if !sameRoutes(md.Routes, m.Routes) {
		mismatches = append(mismatches, "routes")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("plugin metadata does not match %s: %s", ManifestFile, strings.Join(mismatches, ", "))
	}
	return nil
}

func sameRoutes(a, b []Route) bool {
	if len(a) != len(b) {
		return false
	}
	key := func(r Route) string {
		return r.Name + "\x00" + r.Method + "\x00" + r.Pattern + "\x00" + string(r.EffectiveScope())
	}
	keys := make([]string, 0, len(a))
	for _, r := range a {
		keys = append(keys, key(r))
	}
	for _, r := range b {
		index := slices.Index(keys, key(r))
		if index < 0 {
			return false
		}
		keys = slices.Delete(keys, index, index+1)
	}
	return true
}

// ManifestFromMetadata builds plugin.json for a package containing an
// executable and, when ui is true, ui/index.html.
func ManifestFromMetadata(md Metadata, ui, rangeScoped bool) Manifest {
	m := Manifest{
		SchemaVersion:            ManifestSchemaVersion,
		ProtocolVersion:          ProtocolVersion,
		ID:                       md.ID,
		Name:                     md.Name,
		Version:                  md.Version,
		Description:              md.Description,
		Author:                   md.Author,
		Level:                    md.Level,
		RunAs:                    md.RunAs,
		Executable:               ExecutableFile,
		ForwardCallerCredentials: md.ForwardCallerCredentials,
		App:                      md.App,
		VMActions:                md.VMActions,
		Routes:                   append([]Route(nil), md.Routes...),
		VMHooks:                  md.VMHooks,
	}
	if ui {
		m.UI = UIFile
		m.RangeScoped = rangeScoped
	}
	m.Normalize()
	return m
}
