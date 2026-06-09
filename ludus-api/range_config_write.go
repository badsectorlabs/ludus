package ludusapi

import (
	"errors"
	"fmt"
	"ludusapi/models"
	"net/http"
	"os"
	"regexp"

	"github.com/pocketbase/pocketbase/core"
	yaml "gopkg.in/yaml.v3"
)

// writeRangeConfig writes configBytes to <range>/range-config.yml after
// the same schema, structural, and dep-existence checks `range config set`
// runs.
//
// Blueprint apply and `range config set` are substitutes — both lead to
// deploy — so deployment-blocking problems surface at write time on either
// path. After validation, prepareUserDefinedRolesPlaybook keeps the range's
// user-defined-roles.yml in sync with what the new config declares.
//
// Returns (0, nil) on success; non-zero status + error on failure. Used by
// the apply-blueprint handler and the create-range-from-blueprint flow.
func writeRangeConfig(e *core.RequestEvent, targetRange *models.Range, configBytes []byte, force bool) (int, error) {
	if targetRange.TestingEnabled() && !force {
		return http.StatusConflict, errors.New("Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled. Use --force to override.")
	}

	originalRange := e.Get("range")
	e.Set("range", targetRange)
	defer func() {
		if originalRange != nil {
			e.Set("range", originalRange)
		}
	}()

	schemaBytes, err := loadYaml(ludusInstallPath + "/ansible/user-files/range-config.jsonschema")
	if err != nil {
		return http.StatusInternalServerError, fmt.Errorf("can't parse schema: %w", err)
	}
	if err := validateBytes(configBytes, schemaBytes); err != nil {
		return http.StatusBadRequest, fmt.Errorf("Configuration error: %w", err)
	}
	if err := validateRangeYAML(e, configBytes); err != nil {
		return http.StatusBadRequest, fmt.Errorf("Configuration error: %w", err)
	}

	filePath := fmt.Sprintf("%s/ranges/%s/.tmp-range-config.yml", ludusInstallPath, targetRange.RangeId())
	if err := os.WriteFile(filePath, configBytes, 0644); err != nil {
		return http.StatusInternalServerError, fmt.Errorf("Unable to save temporary range config: %w", err)
	}

	if status, err := prepareUserDefinedRolesPlaybook(e, targetRange, configBytes); err != nil {
		return status, err
	}

	if err := os.Rename(filePath, fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, targetRange.RangeId())); err != nil {
		return http.StatusInternalServerError, errors.New("Unable to save the range config")
	}
	return 0, nil
}

// hasUserDefinedRoles returns true when the range config declares at least
// one role on any VM. Existence of the role on disk is not checked.
func hasUserDefinedRoles(configBytes []byte) bool {
	var doc struct {
		Ludus []struct {
			Roles []any `yaml:"roles"`
		} `yaml:"ludus"`
	}
	if err := yaml.Unmarshal(configBytes, &doc); err != nil {
		return false
	}
	for _, vm := range doc.Ludus {
		if len(vm.Roles) > 0 {
			return true
		}
	}
	return false
}

// prepareUserDefinedRolesPlaybook keeps the range's user-defined-roles.yml
// in sync with the just-written range config. When the new config declares
// at least one role, the resolver playbook runs to produce an ordered
// playbook for deploy; resolver failures (circular deps, depends_on misuse)
// are real config bugs and surface as hard errors. When the new config
// declares no roles, any stale playbook from a previous config is removed
// so deploy doesn't run roles that aren't in the current config.
//
// Callers should invoke this any time they write a new range-config.yml so
// the playbook can't drift from the config. Expects e.Get("range") set to
// targetRange. Returns (0, nil) on success.
func prepareUserDefinedRolesPlaybook(e *core.RequestEvent, targetRange *models.Range, configBytes []byte) (int, error) {
	rangeDir := fmt.Sprintf("%s/ranges/%s", ludusInstallPath, targetRange.RangeId())
	playbookPath := fmt.Sprintf("%s/user-defined-roles.yml", rangeDir)

	if !hasUserDefinedRoles(configBytes) {
		// Drop any stale playbook from a previous config so deploy doesn't
		// run roles the current config no longer declares.
		if err := os.Remove(playbookPath); err != nil && !os.IsNotExist(err) {
			return http.StatusInternalServerError, fmt.Errorf("failed to remove stale user-defined-roles.yml: %w", err)
		}
		return 0, nil
	}

	logToFile(rangeDir+"/ansible.log", "Resolving dependencies for user-defined roles..\n", false)
	rolesOutput, err := RunLocalAnsiblePlaybookOnTmpRangeConfig(e, []string{fmt.Sprintf("%s/ansible/range-management/user-defined-roles.yml", ludusInstallPath)})
	logToFile(rangeDir+"/ansible.log", rolesOutput, true)
	if err != nil {
		targetRange.SetRangeState(LudusRangeStateError)
		if saveErr := e.App.Save(targetRange); saveErr != nil {
			logger.Error(fmt.Sprintf("Error saving range: %s", saveErr.Error()))
		}
		if m := regexp.MustCompile(`ERROR[^"]*`).FindString(rolesOutput); m != "" {
			return http.StatusBadRequest, fmt.Errorf("Configuration error: %s", m)
		}
		return http.StatusBadRequest, fmt.Errorf("Error generating ordered roles: %s %v", rolesOutput, err)
	}
	return 0, nil
}
