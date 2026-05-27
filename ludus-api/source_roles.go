package ludusapi

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// RequirementsRole mirrors one entry of an ansible-galaxy requirements.yml roles list.
type RequirementsRole struct {
	Name    string `yaml:"name"`
	Src     string `yaml:"src,omitempty"`
	Version string `yaml:"version,omitempty"`
	Scm     string `yaml:"scm,omitempty"`
}

// RequirementsCollection mirrors one entry of an ansible-galaxy requirements.yml
// collections list. Source/Type carry the install hint when the collection isn't
// coming from the default galaxy server (e.g. type: git, source: <git URL>).
type RequirementsCollection struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version,omitempty"`
	Source  string `yaml:"source,omitempty"`
	Type    string `yaml:"type,omitempty"`
}

// SubscriptionRoleRef is one entry under `subscription_roles:` in
// requirements.yml. Both bare scalar (`- ludus_ghosts_client`) and structured
// (`- name: ludus_ghosts_client`) shapes are accepted.
type SubscriptionRoleRef struct {
	Name string
}

func (s *SubscriptionRoleRef) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		s.Name = node.Value
		return nil
	case yaml.MappingNode:
		var m struct {
			Name string `yaml:"name"`
		}
		if err := node.Decode(&m); err != nil {
			return err
		}
		s.Name = m.Name
		return nil
	default:
		return fmt.Errorf("subscription_roles entry must be a scalar name or a mapping with `name:`")
	}
}

type RequirementsDoc struct {
	Roles       []RequirementsRole       `yaml:"roles,omitempty"`
	Collections []RequirementsCollection `yaml:"collections,omitempty"`
	// SubscriptionRoles is a Ludus-specific extension. It MUST be stripped
	// before handing the file to ansible-galaxy, which hard-errors on unknown
	// top-level keys.
	SubscriptionRoles []SubscriptionRoleRef `yaml:"subscription_roles,omitempty"`
}

// subscriptionRolesFromRequirements extracts the names declared under
// `subscription_roles:` in a blueprint's requirements.yml.
func subscriptionRolesFromRequirements(requirementsYAML []byte) []string {
	_, _, sub := declaredFromRequirements(requirementsYAML)
	return sub
}

// declaredFromRequirements returns the declared names from each section of a
// blueprint's requirements.yml. Empty slices when the file is absent or the
// section is empty. Malformed YAML returns nil/nil/nil rather than erroring;
// callers are walking many sources and don't want one bad file to abort.
func declaredFromRequirements(requirementsYAML []byte) (roles, collections, subscriptionRoles []string) {
	if len(requirementsYAML) == 0 {
		return nil, nil, nil
	}
	var doc RequirementsDoc
	if err := yaml.Unmarshal(requirementsYAML, &doc); err != nil {
		return nil, nil, nil
	}
	for _, r := range doc.Roles {
		if r.Name != "" {
			roles = append(roles, r.Name)
		}
	}
	for _, c := range doc.Collections {
		if c.Name != "" {
			collections = append(collections, c.Name)
		}
	}
	for _, s := range doc.SubscriptionRoles {
		if s.Name != "" {
			subscriptionRoles = append(subscriptionRoles, s.Name)
		}
	}
	return roles, collections, subscriptionRoles
}

type RoleInstallResult struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

// InstallRolesFromRequirementsWithHome shells out to `ansible-galaxy role
// install -r`. ansibleHome is exported as ANSIBLE_HOME to keep galaxy off the
// systemd-protected /home default. Callers MUST inspect each result's OK
// field; ansible-galaxy may exit non-zero while still installing some roles,
// and the mixed state is surfaced via per-role results, not a wrapped error.
func InstallRolesFromRequirementsWithHome(requirementsYAML []byte, rolesPath, ansibleHome string, force bool) ([]RoleInstallResult, error) {
	if len(requirementsYAML) == 0 || strings.TrimSpace(string(requirementsYAML)) == "{}" {
		return nil, nil
	}
	tmp, err := os.CreateTemp("", "ludus-requirements-*.yml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(requirementsYAML); err != nil {
		return nil, err
	}
	tmp.Close()

	args := []string{"role", "install", "-r", tmp.Name()}
	if force {
		args = append(args, "-f")
	}
	if rolesPath != "" {
		args = append(args, "--roles-path", rolesPath)
	}
	cmd := exec.Command("ansible-galaxy", args...)
	cmd.Dir = filepath.Dir(tmp.Name())
	if ansibleHome != "" {
		cmd.Env = append(os.Environ(), "ANSIBLE_HOME="+ansibleHome)
	}
	out, err := cmd.CombinedOutput()

	results := parseGalaxyInstallOutput(string(out))
	if err != nil && len(results) == 0 {
		return nil, fmt.Errorf("ansible-galaxy install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return results, nil
}

// InstallCollectionsFromRequirementsWithHome shells out to `ansible-galaxy
// collection install -r`. Same shape as InstallRolesFromRequirementsWithHome
// but for the collections list inside requirements.yml. ANSIBLE_HOME pins the
// install location so collections land alongside roles instead of in the
// systemd-protected /home default. Returns one result per collection touched;
// callers should treat the result list the same way they treat role results.
func InstallCollectionsFromRequirementsWithHome(requirementsYAML []byte, ansibleHome string, force bool) ([]RoleInstallResult, error) {
	if !hasRequirementsCollections(requirementsYAML) {
		return nil, nil
	}
	tmp, err := os.CreateTemp("", "ludus-requirements-*.yml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(requirementsYAML); err != nil {
		return nil, err
	}
	tmp.Close()

	args := []string{"collection", "install", "-r", tmp.Name()}
	if force {
		args = append(args, "-f")
	}
	cmd := exec.Command("ansible-galaxy", args...)
	cmd.Dir = filepath.Dir(tmp.Name())
	if ansibleHome != "" {
		cmd.Env = append(os.Environ(), "ANSIBLE_HOME="+ansibleHome)
	}
	out, err := cmd.CombinedOutput()

	results := parseGalaxyCollectionInstallOutput(string(out))
	if err != nil && len(results) == 0 {
		return nil, fmt.Errorf("ansible-galaxy collection install failed: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Backfill: ansible-galaxy emits "Nothing to do. All requested collections
	// are already installed." with NO per-collection lines when everything is
	// already on disk. Without this, the parser returns zero results and the
	// caller never records source_artifacts rows for already-installed
	// collections — they vanish from the source view even though the bytes
	// are right there. When the command succeeded, treat every declared
	// collection as OK so the artifact registry stays accurate.
	if err == nil {
		seen := map[string]bool{}
		for _, r := range results {
			seen[r.Name] = true
		}
		for _, c := range declaredCollectionEntries(requirementsYAML) {
			if c.Name == "" || seen[c.Name] {
				continue
			}
			// Prefer the on-disk version from MANIFEST.json over the declared
			// pin: requirements.yml often omits the version (or uses a range
			// like ">=1.2.0" that isn't a real version), but ansible-galaxy
			// has already resolved it to a concrete release on disk.
			version := readGalaxyInstalledCollectionVersion(ansibleHome, c.Name)
			if version == "" {
				version = c.Version
			}
			results = append(results, RoleInstallResult{Name: c.Name, Version: version, OK: true})
		}
	}
	return results, nil
}

// declaredCollectionEntries returns the `collections:` entries from a
// requirements.yml verbatim — name and version preserved. Used by the
// install path to backfill already-installed collections that
// ansible-galaxy skipped without printing per-collection lines.
func declaredCollectionEntries(requirementsYAML []byte) []RequirementsCollection {
	if len(requirementsYAML) == 0 {
		return nil
	}
	var doc RequirementsDoc
	if err := yaml.Unmarshal(requirementsYAML, &doc); err != nil {
		return nil
	}
	return doc.Collections
}

// hasRequirementsCollections is true when the requirements.yml contains at
// least one entry under the `collections:` key. Tolerates malformed YAML by
// returning false rather than erroring — the role install path will still run.
func hasRequirementsCollections(requirementsYAML []byte) bool {
	if len(requirementsYAML) == 0 {
		return false
	}
	var doc RequirementsDoc
	if err := yaml.Unmarshal(requirementsYAML, &doc); err != nil {
		return false
	}
	return len(doc.Collections) > 0
}

// findUndeclaredDependencies returns each config.yml role reference that
// isn't covered by requirements.yml or a local role at the source root.
//
// Per-shape rules:
//   - bare name (`myrole`) or 2-part (`org.repo`): must appear under `roles:`
//     or as a directory under the source's `roles/`.
//   - 3-part FQCN (`namespace.collection.role`): the parent collection
//     (`namespace.collection`) must appear under `collections:`.
func findUndeclaredDependencies(walked *WalkedSource) []UndeclaredDependency {
	localRoles := map[string]bool{}
	for _, dir := range walked.LocalRoles {
		localRoles[filepath.Base(dir)] = true
	}
	var out []UndeclaredDependency
	for _, bp := range walked.Blueprints {
		declaredRoles, declaredCollections := parseDeclaredRequirements(bp.RequirementsYAML)

		declaredSubscription := map[string]bool{}
		for _, name := range subscriptionRolesFromRequirements(bp.RequirementsYAML) {
			declaredSubscription[name] = true
		}

		var configBytes []byte
		if bp.ConfigPath != "" {
			if data, err := os.ReadFile(bp.ConfigPath); err == nil {
				configBytes = data
			}
		}
		if len(configBytes) == 0 {
			continue
		}
		_, refs, err := InferFromRangeConfig(configBytes)
		if err != nil {
			continue
		}
		bpID := ""
		if bp.Manifest != nil {
			bpID = bp.Manifest.ID
		}
		for _, ref := range refs {
			if localRoles[ref] || declaredRoles[ref] || declaredSubscription[ref] {
				continue
			}
			parts := strings.Split(ref, ".")
			if len(parts) >= 3 {
				parent := strings.Join(parts[:2], ".")
				if declaredCollections[parent] {
					continue
				}
				out = append(out, UndeclaredDependency{
					BlueprintID:      bpID,
					Role:             ref,
					Kind:             UndeclaredKindCollection,
					ParentCollection: parent,
				})
				continue
			}
			out = append(out, UndeclaredDependency{
				BlueprintID: bpID,
				Role:        ref,
				Kind:        UndeclaredKindRole,
			})
		}
	}
	return out
}

// parseDeclaredRequirements returns the set of role names and collection
// names a requirements.yml declares. Malformed YAML yields empty sets — the
// caller treats every config.yml reference as undeclared in that case,
// which is the correct nudge for a broken file.
func parseDeclaredRequirements(requirementsYAML []byte) (roles, collections map[string]bool) {
	roles = map[string]bool{}
	collections = map[string]bool{}
	if len(requirementsYAML) == 0 {
		return
	}
	var doc RequirementsDoc
	if err := yaml.Unmarshal(requirementsYAML, &doc); err != nil {
		return
	}
	for _, r := range doc.Roles {
		if r.Name != "" {
			roles[r.Name] = true
		}
	}
	for _, c := range doc.Collections {
		if c.Name != "" {
			collections[c.Name] = true
		}
	}
	return
}

// parseGalaxyCollectionInstallOutput parses per-collection results from
// `ansible-galaxy collection install` stdout. Recognised line shapes:
//
//	"Installing 'community.general:9.4.0' to '/path/to/collections/...'"
//	"'community.general:9.4.0' was installed successfully"
//	"Nothing to do. All requested collections are already installed."
//	"ERROR! Failed to resolve the requested dependencies map. ..."
func parseGalaxyCollectionInstallOutput(out string) []RoleInstallResult {
	var results []RoleInstallResult
	seen := map[string]bool{}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "Installing '"):
			name, version, ok := parseCollectionInstallingLine(line)
			if ok && !seen[name] {
				seen[name] = true
				results = append(results, RoleInstallResult{Name: name, Version: version, OK: true})
			}
		case strings.Contains(line, "was installed successfully"):
			// Format: "'ns.coll:1.2.3' was installed successfully"
			name, version, ok := parseCollectionQuotedSpec(line)
			if ok && !seen[name] {
				seen[name] = true
				results = append(results, RoleInstallResult{Name: name, Version: version, OK: true})
			}
		case strings.HasPrefix(line, "ERROR!"):
			results = append(results, RoleInstallResult{OK: false, Error: strings.TrimPrefix(line, "ERROR! ")})
		}
	}
	return results
}

// parseCollectionInstallingLine extracts (name, version) from
// "Installing 'ns.coll:1.2.3' to '/path/...'". Returns ok=false if the line
// doesn't match the expected shape.
func parseCollectionInstallingLine(line string) (name, version string, ok bool) {
	const prefix = "Installing '"
	rest := strings.TrimPrefix(line, prefix)
	end := strings.Index(rest, "'")
	if end <= 0 {
		return "", "", false
	}
	return splitCollectionSpec(rest[:end])
}

// parseCollectionQuotedSpec extracts (name, version) from a line containing
// "'ns.coll:1.2.3'". Returns ok=false if no quoted spec is present.
func parseCollectionQuotedSpec(line string) (name, version string, ok bool) {
	start := strings.Index(line, "'")
	if start < 0 {
		return "", "", false
	}
	rest := line[start+1:]
	end := strings.Index(rest, "'")
	if end <= 0 {
		return "", "", false
	}
	return splitCollectionSpec(rest[:end])
}

// splitCollectionSpec splits "ns.coll[:version]" into name and version parts.
func splitCollectionSpec(spec string) (name, version string, ok bool) {
	if spec == "" {
		return "", "", false
	}
	if idx := strings.Index(spec, ":"); idx > 0 {
		return spec[:idx], spec[idx+1:], true
	}
	return spec, "", true
}

// parseGalaxyInstallOutput parses per-role results from `ansible-galaxy
// install` stdout. Recognised line shapes:
//
//	"- geerlingguy.docker (3.0.0) was installed successfully"
//	"- the role geerlingguy.docker is already installed, skipping."
//	"[WARNING]: - <name> is already installed, skipping"
//	"[WARNING]: - <name> was NOT installed successfully: <reason>"
func parseGalaxyInstallOutput(out string) []RoleInstallResult {
	var results []RoleInstallResult
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "[WARNING]:") {
			if strings.Contains(line, "is already installed") {
				line = strings.TrimSpace(strings.TrimPrefix(line, "[WARNING]:"))
			} else if strings.Contains(line, "was NOT installed successfully") {
				body := strings.TrimSpace(strings.TrimPrefix(line, "[WARNING]:"))
				body = strings.TrimSpace(strings.TrimPrefix(body, "-"))
				name, reason := body, ""
				if idx := strings.Index(body, " was NOT installed successfully:"); idx >= 0 {
					name = strings.TrimSpace(body[:idx])
					reason = strings.TrimSpace(body[idx+len(" was NOT installed successfully:"):])
				}
				results = append(results, RoleInstallResult{Name: name, OK: false, Error: reason})
				continue
			} else {
				continue
			}
		} else if strings.HasPrefix(line, "[DEPRECATION") || strings.HasPrefix(line, "[NOTICE]") {
			continue
		}

		switch {
		case strings.Contains(line, "was installed successfully"):
			parts := strings.Fields(strings.TrimPrefix(line, "-"))
			if len(parts) >= 1 {
				name := parts[0]
				ver := ""
				if len(parts) >= 2 && strings.HasPrefix(parts[1], "(") {
					ver = strings.Trim(parts[1], "()")
				}
				results = append(results, RoleInstallResult{Name: name, Version: ver, OK: true})
			}
		case strings.Contains(line, "is already installed"):
			parts := strings.Fields(strings.TrimPrefix(line, "-"))
			if len(parts) >= 1 {
				name := parts[0]
				if name == "the" && len(parts) >= 3 && parts[1] == "role" {
					name = parts[2]
				}
				ver := ""
				if len(parts) >= 2 && strings.HasPrefix(parts[1], "(") {
					ver = strings.Trim(parts[1], "()")
				}
				results = append(results, RoleInstallResult{Name: name, Version: ver, OK: true, Error: "already installed (skipped)"})
			}
		case strings.HasPrefix(strings.ToUpper(line), "ERROR! - YOU CAN USE --IGNORE-ERRORS"):
			// Generic trailing line ansible-galaxy emits whenever any role failed.
			// The per-role failure was already captured from the [WARNING]: line.
			continue
		case strings.HasPrefix(strings.ToUpper(line), "ERROR"):
			results = append(results, RoleInstallResult{OK: false, Error: line})
		}
	}
	return results
}

// detectGalaxyVersionMismatches downgrades "already installed (skipped)"
// results to OK=false when the on-disk version disagrees with the requested
// pin. Bare-name deps (no pin) are left alone — "already installed" is the
// right answer for them.
func detectGalaxyVersionMismatches(results []RoleInstallResult, requirementsYAML []byte, rolesPath string) []RoleInstallResult {
	requested := parseRequestedVersions(requirementsYAML)
	for i, r := range results {
		if !r.OK || !strings.Contains(r.Error, "already installed") {
			continue
		}
		wantVer, ok := requested[r.Name]
		if !ok || wantVer == "" {
			continue
		}
		haveVer := readGalaxyInstalledVersion(rolesPath, r.Name)
		if haveVer == "" || haveVer == wantVer {
			continue
		}
		results[i].OK = false
		results[i].Error = fmt.Sprintf("version mismatch: requested %s, installed %s; set force=true to overwrite", wantVer, haveVer)
	}
	return results
}

func parseRequestedVersions(data []byte) map[string]string {
	if len(data) == 0 {
		return nil
	}
	var doc RequirementsDoc
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	out := map[string]string{}
	for _, r := range doc.Roles {
		if r.Name != "" && r.Version != "" {
			out[r.Name] = r.Version
		}
	}
	return out
}

// readGalaxyInstalledCollectionVersion returns the version recorded in
// MANIFEST.json for an installed Ansible collection, or "" if the file is
// missing or unreadable.
func readGalaxyInstalledCollectionVersion(ansibleHome, name string) string {
	if ansibleHome == "" {
		return ""
	}
	dot := strings.Index(name, ".")
	if dot <= 0 || dot == len(name)-1 {
		return ""
	}
	manifestPath := filepath.Join(
		ansibleHome, "collections", "ansible_collections",
		name[:dot], name[dot+1:], "MANIFEST.json",
	)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return ""
	}
	var doc struct {
		CollectionInfo struct {
			Version string `json:"version"`
		} `json:"collection_info"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	return doc.CollectionInfo.Version
}

func readGalaxyInstalledVersion(rolesPath, name string) string {
	if rolesPath == "" {
		return ""
	}
	infoPath := filepath.Join(rolesPath, name, "meta", ".galaxy_install_info")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "version:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, "version:"))
		v = strings.Trim(v, `"'`)
		return v
	}
	return ""
}
