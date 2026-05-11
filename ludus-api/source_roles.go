package ludusapi

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

type RequirementsDoc struct {
	Roles []RequirementsRole `yaml:"roles,omitempty"`
}

// UnionRoles dedupes per-blueprint requirements.yml entries by (name, src,
// version) and unions them with inferred role names (one slice per blueprint,
// matching bps order). Returns the unique role-name set and a merged
// requirements.yml suitable for `ansible-galaxy install -r`. Malformed
// requirements.yml entries are tolerated so a typo doesn't lose role-name
// discovery from `inferred`.
func UnionRoles(bps []WalkedBlueprint, inferred [][]string) (roleSet []string, mergedRequirementsYAML []byte, err error) {
	nameSet := map[string]struct{}{}
	type key struct{ name, src, version string }
	reqSeen := map[key]RequirementsRole{}
	var reqOrdered []RequirementsRole

	for i, bp := range bps {
		if i < len(inferred) {
			for _, n := range inferred[i] {
				if n != "" {
					nameSet[n] = struct{}{}
				}
			}
		}
		if len(bp.RequirementsYAML) == 0 {
			continue
		}
		var doc RequirementsDoc
		if err := yaml.Unmarshal(bp.RequirementsYAML, &doc); err != nil {
			continue
		}
		for _, r := range doc.Roles {
			if r.Name == "" {
				continue
			}
			nameSet[r.Name] = struct{}{}
			k := key{r.Name, r.Src, r.Version}
			if _, ok := reqSeen[k]; !ok {
				reqSeen[k] = r
				reqOrdered = append(reqOrdered, r)
			}
		}
	}

	for n := range nameSet {
		roleSet = append(roleSet, n)
	}
	sort.Strings(roleSet)

	mergedDoc := RequirementsDoc{Roles: reqOrdered}
	mergedRequirementsYAML, err = yaml.Marshal(mergedDoc)
	return roleSet, mergedRequirementsYAML, err
}

// SplitSubscriptionRoles partitions roleSet by membership in the catalog. With
// an empty catalog (no license) everything is treated as public.
func SplitSubscriptionRoles(roleSet, subscriptionCatalog []string) (subscription, public []string) {
	cat := map[string]struct{}{}
	for _, n := range subscriptionCatalog {
		cat[n] = struct{}{}
	}
	for _, n := range roleSet {
		if _, ok := cat[n]; ok {
			subscription = append(subscription, n)
		} else {
			public = append(public, n)
		}
	}
	sort.Strings(subscription)
	sort.Strings(public)
	return
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
