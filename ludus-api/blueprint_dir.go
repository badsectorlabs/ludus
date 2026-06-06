package ludusapi

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type BlueprintDirInputs struct {
	Root            string
	DirName         string // dir name under Root; falls back to BlueprintID. Set distinct when staging an atomic-swap rebuild.
	BlueprintID     string
	ConfigBytes     []byte
	RolesPath       string // caller's user roles dir, used to classify referenced roles
	GlobalRolesPath string // global roles dir, used as fallback
	SubCatalog      []string
}

type BlueprintDirResult struct {
	Dir string
}

func BuildBlueprintDir(in BlueprintDirInputs) (BlueprintDirResult, error) {
	res := BlueprintDirResult{}
	dirName := in.DirName
	if dirName == "" {
		dirName = in.BlueprintID
	}
	blueprintDir := filepath.Join(in.Root, dirName)
	if mkErr := os.MkdirAll(blueprintDir, 0755); mkErr != nil {
		return res, fmt.Errorf("create blueprint dir: %w", mkErr)
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(blueprintDir)
		}
	}()

	if writeErr := os.WriteFile(filepath.Join(blueprintDir, "range-config.yml"), in.ConfigBytes, 0644); writeErr != nil {
		return res, fmt.Errorf("write range-config.yml: %w", writeErr)
	}

	_, roleNames, parseErr := InferFromRangeConfig(in.ConfigBytes)
	if parseErr != nil {
		return res, fmt.Errorf("parse config.yml: %w", parseErr)
	}

	subSet := map[string]bool{}
	for _, n := range in.SubCatalog {
		subSet[n] = true
	}
	var requirementsRoles []RequirementsRole
	var subscriptionRefs []string
	var customLocalRoles []string

	for _, r := range roleNames {
		if subSet[r] {
			subscriptionRefs = append(subscriptionRefs, r)
			continue
		}
		src := filepath.Join(in.RolesPath, r)
		if _, statErr := os.Stat(src); statErr != nil {
			if in.GlobalRolesPath != "" {
				if _, gErr := os.Stat(filepath.Join(in.GlobalRolesPath, r)); gErr == nil {
					src = filepath.Join(in.GlobalRolesPath, r)
				} else {
					requirementsRoles = append(requirementsRoles, RequirementsRole{Name: r})
					continue
				}
			} else {
				requirementsRoles = append(requirementsRoles, RequirementsRole{Name: r})
				continue
			}
		}
		if version, ok := readGalaxyInstallVersion(filepath.Join(src, "meta", ".galaxy_install_info")); ok {
			requirementsRoles = append(requirementsRoles, RequirementsRole{Name: r, Version: version})
			continue
		}
		customLocalRoles = append(customLocalRoles, r)
	}

	if len(customLocalRoles) > 0 {
		return res, fmt.Errorf("range-config references custom local roles %v which are not galaxy-installable and not in the subscription catalog; package them as a source (a roles+blueprints source can distribute both together)", customLocalRoles)
	}

	if len(requirementsRoles) > 0 || len(subscriptionRefs) > 0 {
		doc := RequirementsDoc{Roles: requirementsRoles}
		for _, n := range subscriptionRefs {
			doc.SubscriptionRoles = append(doc.SubscriptionRoles, SubscriptionRoleRef{Name: n})
		}
		out, marshalErr := yaml.Marshal(&doc)
		if marshalErr != nil {
			return res, fmt.Errorf("marshal requirements.yml: %w", marshalErr)
		}
		if writeErr := os.WriteFile(filepath.Join(blueprintDir, "requirements.yml"), out, 0644); writeErr != nil {
			return res, fmt.Errorf("write requirements.yml: %w", writeErr)
		}
	}

	committed = true
	res.Dir = blueprintDir
	return res, nil
}

func readGalaxyInstallVersion(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:")), true
		}
	}
	return "", false
}
