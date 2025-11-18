package ludusapi

import (
	"errors"
	"fmt"
	"ludusapi/models"
	"strings"

	"github.com/goforj/godump"
	"github.com/pocketbase/pocketbase/core"
)

func RunAccessControlPlaybook(e *core.RequestEvent, targetRange *models.Range, sourceUserObject *models.User, actionVerb string, force bool) (success bool, warning string, error error) {

	// Don't run the playbook if the user already has/doesn't have access to the range
	if actionVerb == "grant" {
		if HasRangeAccess(sourceUserObject.UserId(), targetRange.RangeNumber()) {
			return true, "", nil
		}
	} else if actionVerb == "revoke" {
		if !HasRangeAccess(sourceUserObject.UserId(), targetRange.RangeNumber()) {
			return true, "", nil
		}
	} else {
		return false, "", errors.New("invalid action verb: " + actionVerb)
	}

	extraVars := map[string]interface{}{
		"target_range_id":           targetRange.RangeId(),
		"target_range_second_octet": targetRange.RangeNumber(),
		"source_username":           sourceUserObject.ProxmoxUsername(),
		"source_user_id":            sourceUserObject.UserId(),
		"user_number":               sourceUserObject.UserNumber(),
	}
	logger.Debug(godump.DumpStr(extraVars))
	output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/range-access.yml"}, nil, extraVars, actionVerb, false, "")
	if err != nil {
		if strings.Contains(output, "Target router is not up") && actionVerb == "grant" {
			return true, `WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
The target range router VM is inaccessible!
If the target range router is deployed, no firewall changes have taken place.
If the VM is not deployed yet, the rule will be added when it is deployed and you can ignore this.
WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!`, nil
		} else if strings.Contains(output, "Target router is not up") && actionVerb == "revoke" && !force {
			return false, "", fmt.Errorf("The target range router is inaccessible. To revoke access, the target range router must be up and accessible. Use --force to override this protection.")
		} else if strings.Contains(output, "Target router is not up") && actionVerb == "revoke" && force {
			return true, "", nil
		} else { // Some other error we want to fail on
			return false, "", errors.New(output)
		}
	}
	return true, "", nil
}
