package ludusapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/apenella/go-ansible/pkg/execute"
	"github.com/apenella/go-ansible/pkg/options"
	"github.com/apenella/go-ansible/pkg/playbook"
	"github.com/gin-gonic/gin"
	"golang.org/x/exp/maps"
	"gorm.io/gorm"
)

// Runs an ansible playbook with an arbitrary amount of extraVars
// Returns a tuple of the playbook output and an error
func (s *Server) RunAnsiblePlaybookWithVariables(c *gin.Context, playbookPathArray []string, extraVarsFiles []string, extraVars map[string]interface{}, tags string, verbose bool, limit string) (string, error) {

	buff := new(bytes.Buffer)

	// Default to using the range management ludus.yml
	if playbookPathArray == nil {
		playbookPathArray = []string{fmt.Sprintf("%s/ansible/range-management/ludus.yml", ludusInstallPath)}
	}

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}
	user, err := GetUserObject(c)
	if err != nil {
		return "Could not get user", err // JSON set in GetUserObject
	}
	usersRange, err := GetRangeObject(c)
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
		// Tell ansible if we have an enterprise license
		"ludus_enterprise_license": server.LicenseType == "enterprise" && server.LicenseValid,
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

	// Check if the user specified a limit, and if so, make sure it has 'localhost' in it
	if limit != "" {
		if !strings.Contains(limit, "localhost") {
			limit = fmt.Sprintf("%s,localhost", limit)
		}
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
	// If we are running as root, chown this log file to ludus:ludus to prevent potential issues when running future commands as a regular user
	defer func() {
		if os.Geteuid() != 0 {
			changeFileOwner(fmt.Sprintf("%s/users/%s/ansible.log", ludusInstallPath, user.ProxmoxUsername), "ludus")
		}
	}()
	// defer is last in, first out, so this will close the file and then chown it
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
		execute.WithEnvVar("ANSIBLE_ROLES_PATH", fmt.Sprintf("%s/users/%s/.ansible/roles:%s/resources/global-roles", ludusInstallPath, user.ProxmoxUsername, ludusInstallPath)),
		// Inject vars for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_NODE", ServerConfiguration.ProxmoxNode),
		execute.WithEnvVar("PROXMOX_INVALID_CERT", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)),
		execute.WithEnvVar("PROXMOX_URL", ServerConfiguration.ProxmoxURL),
		execute.WithEnvVar("PROXMOX_HOSTNAME", ServerConfiguration.ProxmoxHostname),
		// Inject creds for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_USERNAME", user.ProxmoxUsername+"@pam"),
		execute.WithEnvVar("PROXMOX_PASSWORD", proxmoxPassword),
		execute.WithEnvVar("LUDUS_RANGE_CONFIG", fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, user.ProxmoxUsername)),
		execute.WithEnvVar("LUDUS_RANGE_NUMBER", strconv.Itoa(int(usersRange.RangeNumber))),
		execute.WithEnvVar("LUDUS_RANGE_ID", usersRange.UserID),
	)

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         playbookPathArray,
		Exec:              execute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
		StdoutCallback:    "default",
	}

	// Check for a user-defined-roles playbook (included in ludus) and create a placeholder if it doesn't exist
	userDefinedRolePath := fmt.Sprintf("%s/users/%s/.ansible/user-defined-roles.yml", ludusInstallPath, user.ProxmoxUsername)
	if !FileExists(userDefinedRolePath) {
		logToFile(userDefinedRolePath,
			`- name: Run debug task on localhost
  tags: [user-defined-roles]
  hosts: localhost
  gather_facts: false
  tasks:
    - name: No user-defined roles to run
      ansible.builtin.debug:
        msg: "No user-defined roles to run"`,
			false)
		// If we are running as root, chown this file to ludus:ludus to prevent potential issues when deploying as a regular user
		if os.Geteuid() == 0 {
			changeFileOwner(userDefinedRolePath, "ludus")
		}
	}

	// Only notify if this is a range deployment
	if slices.Contains(playbookPathArray, fmt.Sprintf("%s/ansible/range-management/ludus.yml", ludusInstallPath)) {
		// Run and time the playbook
		startTime := time.Now()
		err = playbook.Run(context.TODO())
		duration := time.Since(startTime)
		// Notify the user of the result of the playbook
		payload := NewPayload(err == nil, usersRange.UserID, buff.String(), false, duration)
		notifier := Notify{
			ConfigFilePath: fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, user.ProxmoxUsername),
			Payload:        payload,
		}
		notifier.Send()
	} else {
		// Just run the playbook
		err = playbook.Run(context.TODO())
	}

	// Return the output of the playbook
	if err != nil {
		return buff.String(), err
	}
	return buff.String(), nil

}

// A helper to keep function calls clean
func RunRangeManagementAnsibleWithTag(c *gin.Context, tag string, verbose bool, onlyRoles []string, limit string) (string, error) {
	usersRange, err := GetRangeObject(c)
	if err != nil {
		return "", errors.New("unable to get users range") // JSON error is set in getRangeObject
	}

	onlyRolesArray := removeEmptyStrings(onlyRoles)
	extraVars := map[string]interface{}{"only_roles": onlyRolesArray}

	// Run the deploy
	output, err := server.RunAnsiblePlaybookWithVariables(c, nil, nil, extraVars, tag, verbose, limit)

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
	return server.RunAnsiblePlaybookWithVariables(c, playbookPathArray, nil, nil, tag, verbose, "")
}

// A helper to expose RunAnsiblePlaybookWithVariables to plugins
func RunAnsiblePlaybookWithVariables(c *gin.Context, playbook string, extraVarsFiles []string, extraVars map[string]interface{}, tags string, verbose bool, limit string) (string, error) {
	playbookPathArray := []string{fmt.Sprintf("%s/ansible/range-management/%s", ludusInstallPath, playbook)}
	return server.RunAnsiblePlaybookWithVariables(c, playbookPathArray, extraVarsFiles, extraVars, tags, verbose, limit)
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

	// If there are two `.` characters in the role name, it is part of a collection, so check that the first two segments of the role name
	// exist in the collection listing, and if so, assume the role exists
	//
	// This is a workaround for the fact that ansible-galaxy doesn't support listing roles in collections
	// We could walk the filesystem and pull out every role in every collection, but that would be slow
	if strings.Count(roleName, ".") == 2 {
		roleParts := strings.Split(roleName, ".")
		collectionName := strings.Join(roleParts[:2], ".")

		// Check if collections are already cached in the context
		if cachedCollections, exists := c.Get("ansible_collections"); exists {
			return slices.Contains(cachedCollections.([]string), collectionName), nil
		}

		user, err := GetUserObject(c)
		if err != nil {
			return false, err
		}

		// Collections
		collectionCmd := exec.Command("ansible-galaxy", "collection", "list", "--format", "json")
		collectionCmd.Env = os.Environ()
		collectionCmd.Env = append(collectionCmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))

		collectionOutput, err := collectionCmd.CombinedOutput()
		if err != nil {
			return false, errors.New("Unable to get the ansible collections: " + err.Error())
		}

		// Unmarshal the JSON into a suitable Go data structure
		var data map[string]map[string]map[string]string
		err = json.Unmarshal(collectionOutput, &data)
		if err != nil {
			return false, errors.New("Unable to get parse ansible collections JSON: " + err.Error())
		}

		// Iterate through the data
		var collections []string
		for path, modules := range data {
			if strings.Contains(path, ".ansible") {
				for name := range modules {
					collections = append(collections, name)
				}
			}
		}

		// Cache the collections in the context
		c.Set("ansible_collections", collections)

		return slices.Contains(collections, collectionName), nil

	}

	// Check if roles are already cached in the context
	if roles, exists := c.Get("ansible_roles"); exists {
		return slices.Contains(roles.([]string), roleName), nil
	}

	user, err := GetUserObject(c)
	if err != nil {
		return false, err
	}
	cmd := exec.Command("ansible-galaxy", "role", "list") // no --format json for roles...
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_ROLES_PATH=%s/users/%s/.ansible/roles:%s/resources/global-roles", ludusInstallPath, user.ProxmoxUsername, ludusInstallPath))
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

	// Cache the roles in the context
	c.Set("ansible_roles", availableAnsibleRoles)

	return slices.Contains(availableAnsibleRoles, roleName), nil

}

// Run a simple local ansible playbook that doesn't require any extra vars and doesn't log
func RunLocalAnsiblePlaybookOnTmpRangeConfig(c *gin.Context, playbookPathArray []string) (string, error) {

	buff := new(bytes.Buffer)

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}
	user, err := GetUserObject(c)
	if err != nil {
		return "Could not get user", err // JSON set in GetUserObject
	}
	usersRange, err := GetRangeObject(c)
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

	// Always include the ludus, server, and user configs
	userDir := fmt.Sprintf("@%s/users/%s/", ludusInstallPath, user.ProxmoxUsername)
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), userDir + ".tmp-range-config.yml"}
	inventory := "127.0.0.1"

	execute := execute.NewDefaultExecute(
		// Use a multiwrtier that saves the output to a buffer and a file
		execute.WithWrite(buff),
		// Also log stderr to the log file and the buff vs stderr (journalctl logs)
		execute.WithWriteError(buff),
		// Disable color
		execute.WithEnvVar("ANSIBLE_NOCOLOR", "true"),
		// Set the ansible home to the user's ansible directory
		execute.WithEnvVar("ANSIBLE_HOME", fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername)),
		execute.WithEnvVar("ANSIBLE_SSH_CONTROL_PATH_DIR", fmt.Sprintf("%s/users/%s/.ansible/cp", ludusInstallPath, user.ProxmoxUsername)),
	)

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Inventory:     inventory,
		ExtraVarsFile: serverAndUserConfigs,
		ExtraVars:     userVars,
	}

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

func GetErrorsFromAnsiblePlaybook(user UserObject) []string {
	ansibleLogPath := fmt.Sprintf("%s/users/%s/ansible.log", ludusInstallPath, user.ProxmoxUsername)
	ansibleLogContents, err := os.ReadFile(ansibleLogPath)
	if err != nil {
		return []string{"Could not read ansible log: " + err.Error()}
	}
	return getFatalErrorsFromLog(string(ansibleLogContents))
}

func getFatalErrorsFromLog(input string) []string {
	scanner := bufio.NewScanner(strings.NewReader(input))
	fatalRegex := regexp.MustCompile(`^fatal:.*$|^failed:.*$|^ERROR! .*$`)
	ignoreRegex := regexp.MustCompile(`\.\.\.ignoring$`)
	errorCount := 0
	var fatalErrors []string

	var previousLine string
	for scanner.Scan() {
		currentLine := scanner.Text()
		// Check if the current line is an ignoring line and the previous line was a fatal line
		if ignoreRegex.MatchString(currentLine) && fatalRegex.MatchString(previousLine) {
			// Skip this fatal line because it's followed by ...ignoring
			previousLine = "" // Reset previousLine to avoid false positives
			continue
		}

		if fatalRegex.MatchString(previousLine) {
			errorCount += 1
			fatalErrors = append(fatalErrors, previousLine)
		}

		// Update previous lines for the next iteration
		previousLine = currentLine
	}

	// Check the last line in case the file ends with a fatal line
	if fatalRegex.MatchString(previousLine) {
		errorCount += 1
		fatalErrors = append(fatalErrors, previousLine)
	}
	return fatalErrors
}
