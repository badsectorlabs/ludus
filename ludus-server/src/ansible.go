package ludusapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/apenella/go-ansible/pkg/execute"
	"github.com/apenella/go-ansible/pkg/options"
	"github.com/apenella/go-ansible/pkg/playbook"
	"github.com/gin-gonic/gin"
	"golang.org/x/exp/maps"
	"gorm.io/gorm"
)

// Runs an ansible playbook with an arbitrary amount of extraVars
// Returns a tuple of the playbook output and an error
func RunAnsiblePlaybookWithVariables(c *gin.Context, playbookPathArray []string, extraVarsFiles []string, extraVars map[string]interface{}, tags string, verbose bool, limit string) (string, error) {

	var err error

	buff := new(bytes.Buffer)

	// Default to using the range management ludus.yml
	if playbookPathArray == nil {
		playbookPathArray = []string{fmt.Sprintf("%s/ansible/range-management/ludus.yml", ludusInstallPath)}
	}

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}
	user, err := getUserObject(c)
	if err != nil {
		return "Could not get user", err // JSON set in getUserObject
	}
	usersRange, err := getRangeObject(c)
	if err != nil {
		return "Could not get range", errors.New("could not get range") // JSON set in getRangeObject
	}

	accessGrantsArray := getAccessGrantsForUser(user.UserID)
	userVars := map[string]interface{}{
		"username":           user.ProxmoxUsername,
		"range_id":           user.UserID,
		"range_second_octet": usersRange.RangeNumber,
		// We have to send this in the event this deploy is a fresh deploy AFTER a user has been granted access to this
		// range, which means there is a fresh router deployed with no knowledge of the access grants
		"access_grants_array":   accessGrantsArray,
		"ludus_testing_enabled": usersRange.TestingEnabled,
	}

	// Merge userVars with any extraVars provided
	maps.Copy(userVars, extraVars)

	// Always include the ludus, server, and user configs
	userDir := fmt.Sprintf("@%s/users/%s/", ludusInstallPath, user.ProxmoxUsername)
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), userDir + "range-config.yml"}
	// root has no range config and cannot use the dynamic inventory
	var inventory string
	if user.UserID == "ROOT" {
		serverAndUserConfigs = []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath)}
		inventory = "127.0.0.1"
	} else {
		// For regular Ludus users, provide the dynamic inventory
		inventory = ludusInstallPath + "/ansible/range-management/proxmox.py"
	}

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Inventory:     inventory,
		ExtraVarsFile: append(serverAndUserConfigs, extraVarsFiles...),
		ExtraVars:     userVars,
		Limit:         limit,
		Tags:          tags,
		Verbose:       verbose,
	}

	// For add-user and del-user the first user will be created as root, so no need to bail if no prox password found
	proxmoxPassword := ""
	if user.UserID != "ROOT" {
		proxmoxPassword = getProxmoxPasswordForUser(user, c)
		if proxmoxPassword == "" {
			return "Could not get proxmox password for user", errors.New("could not get proxmox password for user") // JSON set in getProxmoxPasswordForUser
		}
	}
	// Open a file for saving the ansible log, TRUNC will overwrite
	// TODO, figure out a way to keep the last 10(?) logs?
	ansibleLogFile, err := os.OpenFile(fmt.Sprintf("%s/users/%s/ansible.log", ludusInstallPath, user.ProxmoxUsername), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return "Failed to open ansible log file", errors.New("failed to open ansible log file")
	}
	defer ansibleLogFile.Close()

	execute := execute.NewDefaultExecute(
		// Use a multiwrtier that saves the output to a buffer and a file
		execute.WithWrite(io.MultiWriter(buff, ansibleLogFile)),
		// Also log stderr to the log file and the buff vs stderr (journalctl logs)
		execute.WithWriteError(io.MultiWriter(buff, ansibleLogFile)),
		// Disable color
		execute.WithEnvVar("ANSIBLE_NOCOLOR", "true"),
		// Set the ansible home to the user's ansible directory
		execute.WithEnvVar("ANSIBLE_HOME", fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername)),
		execute.WithEnvVar("ANSIBLE_SSH_CONTROL_PATH_DIR", fmt.Sprintf("%s/users/%s/.ansible/cp", ludusInstallPath, user.ProxmoxUsername)),
		// Inject vars for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_NODE", ServerConfiguration.ProxmoxNode),
		execute.WithEnvVar("PROXMOX_INVALID_CERT", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)),
		execute.WithEnvVar("PROXMOX_URL", ServerConfiguration.ProxmoxURL),
		execute.WithEnvVar("PROXMOX_HOSTNAME", ServerConfiguration.ProxmoxHostname),
		// Inject creds for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_USERNAME", user.ProxmoxUsername+"@pam"),
		execute.WithEnvVar("PROXMOX_PASSWORD", proxmoxPassword),
	)

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         playbookPathArray,
		Exec:              execute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
		StdoutCallback:    "default",
	}

	err = playbook.Run(context.TODO())
	if err != nil {
		return buff.String(), err
	}

	return buff.String(), nil

}

// A helper to keep function calls clean
func RunRangeManagementAnsibleWithTag(c *gin.Context, tag string, verbose bool, onlyRoles []string, limit string) (string, error) {
	usersRange, err := getRangeObject(c)
	if err != nil {
		return "", errors.New("unable to get users range") // JSON error is set in getRangeObject
	}

	onlyRolesArray := removeEmptyStrings(onlyRoles)
	extraVars := map[string]interface{}{"only_roles": onlyRolesArray}

	// Run the deploy
	output, err := RunAnsiblePlaybookWithVariables(c, nil, nil, extraVars, tag, verbose, limit)

	if err != nil {
		db.Model(&usersRange).Update("range_state", "ERROR")
	} else {
		db.Model(&usersRange).Update("range_state", "SUCCESS")
	}
	return output, err
}

// A helper to keep function calls clean
func RunPlaybookWithTag(c *gin.Context, playbook string, tag string, verbose bool) (string, error) {
	playbookPathArray := []string{fmt.Sprintf("%s/ansible/range-management/%s", ludusInstallPath, playbook)}
	return RunAnsiblePlaybookWithVariables(c, playbookPathArray, nil, nil, tag, verbose, "")
}

type AccessGrantStruct struct {
	SecondOctet int32  `json:"second_octet"`
	Username    string `json:"username"`
}

// Get the access grants for the provided user ID and return an array of {second_octet, username} objects
func getAccessGrantsForUser(targetUserId string) []AccessGrantStruct {
	var accessGrants RangeAccessObject
	result := db.First(&accessGrants, "target_user_id = ?", targetUserId)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil
	}
	var returnArray []AccessGrantStruct
	for _, sourceUserID := range accessGrants.SourceUserIDs {
		var sourceUser UserObject
		db.First(&sourceUser, "user_id = ?", sourceUserID)
		var sourceRange RangeObject
		db.First(&sourceRange, "user_id = ?", sourceUserID)
		returnArray = append(returnArray, AccessGrantStruct{sourceRange.RangeNumber, sourceUser.ProxmoxUsername})
	}
	return returnArray
}

// Return true if the role exists for the user, or false if it doesn't
func checkRoleExists(c *gin.Context, roleName string) (bool, error) {
	user, err := getUserObject(c)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("ansible-galaxy", "role", "list") // no --format json for roles...
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	roleOutput, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("unable to get the ansible roles: %w", err)
	}

	// Create a scanner to read the input
	scanner := bufio.NewScanner(bytes.NewReader(roleOutput))

	// Slice to store the roles
	var availableAnsibleRoles []string

	// Process each line
	for scanner.Scan() {
		line := scanner.Text()

		// Skip non-role lines
		if !strings.HasPrefix(line, "- ") {
			continue
		}

		// Split the line into role name and version
		parts := strings.SplitN(line[2:], ", ", 2)
		if len(parts) != 2 {
			fmt.Println("Invalid line format:", line)
			continue
		}

		roleName := strings.TrimSpace(parts[0])
		availableAnsibleRoles = append(availableAnsibleRoles, roleName)
	}
	return slices.Contains(availableAnsibleRoles, roleName), nil

}
