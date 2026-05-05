package ludusapi

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// SupportedManifestVersion is the highest blueprint.yml / source.yml
// manifest_version this Ludus understands. Bump on breaking schema changes.
const SupportedManifestVersion = 1

// blueprintManifestIDRegex permits up to two slashes so authors can scope IDs
// into folders (e.g. "windows/dc"). The slug-prefixed display id stays
// unambiguous because sourceIDRegex disallows slashes.
var blueprintManifestIDRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`)

var sourceIDRegex = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_\-]*$`)

// BlueprintManifest is the parsed shape of a blueprint.yml. Authors, license,
// and homepage live on the source manifest and are inherited at read time.
type BlueprintManifest struct {
	ManifestVersion int      `yaml:"manifest_version"`
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	Description     string   `yaml:"description"`
	Version         string   `yaml:"version"`
	Tags            []string `yaml:"tags,omitempty"`
	Thumbnail       string   `yaml:"thumbnail,omitempty"`
	Config          string   `yaml:"config"`
	MinLudusVersion string   `yaml:"min_ludus_version,omitempty"`
}

func ParseBlueprintManifest(data []byte) (*BlueprintManifest, error) {
	var m BlueprintManifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("blueprint.yml is not valid YAML: %w", err)
	}
	if m.ManifestVersion == 0 {
		return nil, fmt.Errorf("manifest_version is required in blueprint.yml")
	}
	if m.ManifestVersion > SupportedManifestVersion {
		return nil, fmt.Errorf("manifest_version %d is not supported by this Ludus (supports up to %d)", m.ManifestVersion, SupportedManifestVersion)
	}
	if m.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if !blueprintManifestIDRegex.MatchString(m.ID) {
		return nil, fmt.Errorf("id must match %s", blueprintManifestIDRegex.String())
	}
	if m.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	// description is optional — blueprints created from scratch may have none.
	if m.Version == "" {
		return nil, fmt.Errorf("version is required")
	}
	semverV := m.Version
	if !strings.HasPrefix(semverV, "v") {
		semverV = "v" + semverV
	}
	if !semver.IsValid(semverV) {
		return nil, fmt.Errorf("version must be valid semver, got %q", m.Version)
	}
	if m.Config == "" {
		return nil, fmt.Errorf("config is required (path to range-config.yml)")
	}
	if err := validateRelativePath("config", m.Config); err != nil {
		return nil, err
	}
	if m.Thumbnail != "" {
		if err := validateRelativePath("thumbnail", m.Thumbnail); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func validateRelativePath(label, p string) error {
	if filepath.IsAbs(p) {
		return fmt.Errorf("%s path must be relative, got %q", label, p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.Contains(cleaned, "/../") || cleaned == "." || strings.HasPrefix(cleaned, "/") {
		return fmt.Errorf("%s path must resolve inside blueprint dir, got %q", label, p)
	}
	return nil
}

// SourceManifest is the parsed shape of a source.yml at a source repo's root.
// All fields except ManifestVersion are optional. License, homepage, and
// authors apply to every blueprint published in the source.
type SourceManifest struct {
	ManifestVersion int      `yaml:"manifest_version"`
	Name            string   `yaml:"name,omitempty"`
	Description     string   `yaml:"description,omitempty"`
	Authors         []string `yaml:"authors,omitempty"`
	Homepage        string   `yaml:"homepage,omitempty"`
	License         string   `yaml:"license,omitempty"`
}

func ParseSourceManifest(data []byte) (*SourceManifest, error) {
	var s SourceManifest
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("source.yml is not valid YAML: %w", err)
	}
	if s.ManifestVersion == 0 {
		return nil, fmt.Errorf("manifest_version is required in source.yml")
	}
	if s.ManifestVersion > SupportedManifestVersion {
		return nil, fmt.Errorf("manifest_version %d is not supported by this Ludus (supports up to %d)", s.ManifestVersion, SupportedManifestVersion)
	}
	return &s, nil
}

// rangeConfigVM is a partial type matching only the fields InferFromRangeConfig
// needs. Roles entries can be bare scalars (`- my_role`) or mappings with a
// `name` key, so we accept raw yaml.Node and resolve via roleNameFromNode.
type rangeConfigVM struct {
	Template string      `yaml:"template"`
	Roles    []yaml.Node `yaml:"roles"`
}

type rangeConfigDoc struct {
	Ludus []rangeConfigVM `yaml:"ludus"`
}

func roleNameFromNode(n yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return n.Value
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == "name" && n.Content[i+1].Kind == yaml.ScalarNode {
				return n.Content[i+1].Value
			}
		}
	}
	return ""
}

// InferFromRangeConfig returns deduped, sorted unique template and role names
// referenced by any VM in a Ludus range config. Used by source-add /
// source-sync to populate blueprint rows.
func InferFromRangeConfig(data []byte) (templates, roles []string, err error) {
	var doc rangeConfigDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("config.yml is not valid YAML: %w", err)
	}
	tSet := map[string]struct{}{}
	rSet := map[string]struct{}{}
	for _, vm := range doc.Ludus {
		if vm.Template != "" {
			tSet[vm.Template] = struct{}{}
		}
		for _, r := range vm.Roles {
			if name := roleNameFromNode(r); name != "" {
				rSet[name] = struct{}{}
			}
		}
	}
	for k := range tSet {
		templates = append(templates, k)
	}
	for k := range rSet {
		roles = append(roles, k)
	}
	sort.Strings(templates)
	sort.Strings(roles)
	return templates, roles, nil
}
