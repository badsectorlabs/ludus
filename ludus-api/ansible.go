package ludusapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ludusapi/models"
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
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/exp/maps"
)

// Runs an ansible playbook with an arbitrary amount of extraVars
// Returns a tuple of the playbook output and an error
func (s *Server) RunAnsiblePlaybookWithVariables(e *core.RequestEvent, playbookPathArray []string, extraVarsFiles []string, extraVars map[string]interface{}, tags string, verbose bool, limit string) (string, error) {

	buff := new(bytes.Buffer)

	// Default to using the range management ludus.yml
	if playbookPathArray == nil {
		playbookPathArray = []string{fmt.Sprintf("%s/ansible/range-management/ludus.yml", ludusInstallPath)}
	}

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}

	user := e.Get("user").(*models.User)
	usersRange := e.Get("range").(*models.Range)

	accessGrantsArray := getAccessGrantsForUser(e, user.UserId())
	userVars := map[string]interface{}{
		"username":           user.ProxmoxUsername(),
		"range_id":           usersRange.RangeId(),
		"range_second_octet": usersRange.RangeNumber(),
		// We have to send this in the event this deploy is a fresh deploy AFTER a user has been granted access to this
		// range, which means there is a fresh router deployed with no knowledge of the access grants
		"access_grants_array":   accessGrantsArray,
		"ludus_testing_enabled": usersRange.TestingEnabled(),
		// Tell ansible if we have an enterprise license
		"ludus_enterprise_license": server.LicenseType == "enterprise" && server.LicenseValid,
	}

	// Merge userVars with any extraVars provided
	maps.Copy(userVars, extraVars)

	// Always include the ludus, server, and user configs
	rangeDir := fmt.Sprintf("@%s/ranges/%s/", ludusInstallPath, usersRange.RangeId())
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), rangeDir + "range-config.yml"}
	// root has no range config and cannot use the dynamic inventory
	var inventory string
	if user.UserId() == "ROOT" {
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

	// Open a file for saving the ansible log, TRUNC will overwrite
	// TODO, figure out a way to keep the last 10(?) logs?
	var ansibleLogFilePath string
	if user.UserId() != "ROOT" {
		ansibleLogFilePath = fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, usersRange.RangeId())
	} else {
		ansibleLogFilePath = fmt.Sprintf("%s/install/ansible.log", ludusInstallPath)
	}
	logger.Debug("Opening ansible log file: " + ansibleLogFilePath)
	ansibleLogFile, err := os.OpenFile(ansibleLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return "Failed to open ansible log file", errors.New("failed to open ansible log file")
	}
	// If we are running as root, chown this log file to ludus:ludus to prevent potential issues when running future commands as a regular user
	defer func() {
		if os.Geteuid() == 0 {
			changeFileOwner(ansibleLogFilePath, "ludus")
		}
	}()
	// defer is last in, first out, so this will close the file and then chown it
	defer ansibleLogFile.Close()

	proxmoxTokenSecret, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
	if err != nil {
		return "", fmt.Errorf("could not decrypt proxmox token secret: %w", err)
	}

	// If this is an access grant or revoke, we need to set the LUDUS_RETURN_ALL_RANGES environment variable to true so ansible can modify the target router
	returnAllRanges := false
	if slices.Contains(playbookPathArray, fmt.Sprintf("%s/ansible/range-management/range-access.yml", ludusInstallPath)) {
		returnAllRanges = true
	}

	ansibleExecute := execute.NewDefaultExecute(
		// Use a multiwrtier that saves the output to a buffer and a file
		execute.WithWrite(io.MultiWriter(buff, ansibleLogFile)),
		// Also log stderr to the log file and the buff vs stderr (journalctl logs)
		execute.WithWriteError(io.MultiWriter(buff, ansibleLogFile)),
		// Disable color
		execute.WithEnvVar("ANSIBLE_NOCOLOR", "true"),
		// Set the ansible home to the user's ansible directory
		execute.WithEnvVar("ANSIBLE_HOME", fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername())),
		execute.WithEnvVar("ANSIBLE_SSH_CONTROL_PATH_DIR", fmt.Sprintf("%s/users/%s/.ansible/cp", ludusInstallPath, user.ProxmoxUsername())),
		execute.WithEnvVar("ANSIBLE_ROLES_PATH", fmt.Sprintf("%s/users/%s/.ansible/roles:%s/resources/global-roles", ludusInstallPath, user.ProxmoxUsername(), ludusInstallPath)),
		// Inject vars for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_NODE", ServerConfiguration.ProxmoxNode),
		execute.WithEnvVar("PROXMOX_INVALID_CERT", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)),
		execute.WithEnvVar("PROXMOX_URL", ServerConfiguration.ProxmoxURL),
		execute.WithEnvVar("PROXMOX_HOSTNAME", ServerConfiguration.ProxmoxHostname),
		// Inject creds for the proxmox.py dynamic inventory script
		execute.WithEnvVar("PROXMOX_USERNAME", user.ProxmoxUsername()+"@"+user.ProxmoxRealm()),
		execute.WithEnvVar("PROXMOX_TOKEN", user.ProxmoxTokenId()),
		execute.WithEnvVar("PROXMOX_SECRET", proxmoxTokenSecret),
		execute.WithEnvVar("LUDUS_RANGE_CONFIG", fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, usersRange.RangeId())),
		execute.WithEnvVar("LUDUS_RANGE_NUMBER", strconv.Itoa(int(usersRange.RangeNumber()))),
		execute.WithEnvVar("LUDUS_RANGE_ID", usersRange.RangeId()),
		execute.WithEnvVar("LUDUS_USER_IS_ADMIN", strconv.FormatBool(user.IsAdmin())),
		execute.WithEnvVar("LUDUS_RETURN_ALL_RANGES", strconv.FormatBool(returnAllRanges)),
	)

	// Loop over the environment and add any that start with LUDUS_SECRET_ to the execute object
	for _, envVar := range os.Environ() {
		envVarParts := strings.SplitN(envVar, "=", 2)
		if len(envVarParts) != 2 {
			continue
		}
		envVarKey := envVarParts[0]
		envVarValue := envVarParts[1]
		if strings.HasPrefix(envVarKey, "LUDUS_SECRET_") {
			ansibleExecute.EnvVars[envVarKey] = envVarValue
		}
	}

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         playbookPathArray,
		Exec:              ansibleExecute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
		StdoutCallback:    "default",
	}

	// Set the ansible binary from the environment if it exists
	if ansibleBinary, ok := os.LookupEnv("LUDUS_ANSIBLE_BINARY"); ok {
		playbook.Binary = ansibleBinary
	}

	// Check for a user-defined-roles playbook (included in ludus) and create a placeholder if it doesn't exist
	userDefinedRolePath := fmt.Sprintf("%s/ranges/%s/user-defined-roles.yml", ludusInstallPath, usersRange.RangeId())
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
		payload := NewPayload(err == nil, usersRange.RangeId(), buff.String(), false, duration)
		notifier := Notify{
			ConfigFilePath: fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, usersRange.RangeId()),
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
func RunRangeManagementAnsibleWithTag(e *core.RequestEvent, tag string, verbose bool, onlyRoles []string, limit string) (string, error) {
	usersRange := e.Get("range").(*models.Range)

	onlyRolesArray := removeEmptyStrings(onlyRoles)
	extraVars := map[string]interface{}{"only_roles": onlyRolesArray}

	// Run the deploy
	output, err := server.RunAnsiblePlaybookWithVariables(e, nil, nil, extraVars, tag, verbose, limit)

	if err != nil {
		usersRange.SetRangeState("ERROR")
		if saveErr := e.App.Save(usersRange); saveErr != nil {
			return "", fmt.Errorf("error saving range: %w", saveErr)
		}
	} else {
		usersRange.SetRangeState("SUCCESS")
		if saveErr := e.App.Save(usersRange); saveErr != nil {
			return "", fmt.Errorf("error saving range: %w", saveErr)
		}
	}
	return output, err
}

// A helper to keep function calls clean
func RunPlaybookWithTag(e *core.RequestEvent, playbook string, tag string, verbose bool) (string, error) {
	playbookPathArray := []string{fmt.Sprintf("%s/ansible/range-management/%s", ludusInstallPath, playbook)}
	return server.RunAnsiblePlaybookWithVariables(e, playbookPathArray, nil, nil, tag, verbose, "")
}

// A helper to expose RunAnsiblePlaybookWithVariables to plugins
func RunAnsiblePlaybookWithVariables(e *core.RequestEvent, playbook string, extraVarsFiles []string, extraVars map[string]interface{}, tags string, verbose bool, limit string) (string, error) {
	playbookPathArray := []string{fmt.Sprintf("%s/ansible/range-management/%s", ludusInstallPath, playbook)}
	return server.RunAnsiblePlaybookWithVariables(e, playbookPathArray, extraVarsFiles, extraVars, tags, verbose, limit)
}

type AccessGrantStruct struct {
	SecondOctet int    `json:"second_octet"`
	Username    string `json:"username"`
}

// Get the access grants for the provided user ID and return an array of {second_octet, username} objects
func getAccessGrantsForUser(e *core.RequestEvent, targetUserId string) []AccessGrantStruct {
	var returnArray []AccessGrantStruct

	// Get direct user-to-range assignments for the target user
	user := e.Get("user").(*models.User)
	ranges := user.Ranges()
	for _, rangeRecord := range ranges {
		returnArray = append(returnArray, AccessGrantStruct{rangeRecord.RangeNumber(), user.ProxmoxUsername()})
	}

	// Get group-based access
	groups := user.Groups()

	// For every group the user is a member of, get the ranges that group has access to
	for _, groupRecord := range groups {
		expandedGroupRecord := groupRecord.ExpandedOne("ranges")
		expandedGroupModel := &models.Group{}
		expandedGroupModel.SetProxyRecord(expandedGroupRecord)
		groupRanges := expandedGroupModel.Ranges()

		for _, groupAccess := range groupRanges {
			// Only add the access grant if it is not already in the returnArray
			if slices.ContainsFunc(returnArray, func(entry AccessGrantStruct) bool {
				return entry.SecondOctet == groupAccess.RangeNumber()
			}) {
				continue
			}
			returnArray = append(returnArray, AccessGrantStruct{groupAccess.RangeNumber(), user.ProxmoxUsername()})
		}
	}

	// logger.Debug("Access grants for user " + targetUserId + ": " + godump.DumpStr(returnArray))

	return returnArray
}

// Return true if the role exists for the user, or false if it doesn't
func checkRoleExists(e *core.RequestEvent, roleName string) (bool, error) {

	// If there are two `.` characters in the role name, it is part of a collection, so check that the first two segments of the role name
	// exist in the collection listing, and if so, assume the role exists
	//
	// This is a workaround for the fact that ansible-galaxy doesn't support listing roles in collections
	// We could walk the filesystem and pull out every role in every collection, but that would be slow
	if strings.Count(roleName, ".") == 2 {
		roleParts := strings.Split(roleName, ".")
		collectionName := strings.Join(roleParts[:2], ".")

		// Check if collections are already cached in the context
		cachedCollections := e.Get("ansible_collections")
		if cachedCollections != nil {
			return slices.Contains(cachedCollections.([]string), collectionName), nil
		}

		user := e.Get("user").(*models.User)

		// Collections
		collectionCmd := exec.Command("ansible-galaxy", "collection", "list", "--format", "json")
		collectionCmd.Env = os.Environ()
		collectionCmd.Env = append(collectionCmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))

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
		e.Set("ansible_collections", collections)

		return slices.Contains(collections, collectionName), nil

	}

	// Check if roles are already cached in the context
	roles := e.Get("ansible_roles")
	if roles != nil {
		return slices.Contains(roles.([]string), roleName), nil
	}

	user := e.Get("user").(*models.User)

	cmd := exec.Command("ansible-galaxy", "role", "list") // no --format json for roles...
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_ROLES_PATH=%s/users/%s/.ansible/roles:%s/resources/global-roles", ludusInstallPath, user.ProxmoxUsername(), ludusInstallPath))
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
			logger.Error("Invalid line format: " + line)
			continue
		}

		roleName := strings.TrimSpace(parts[0])
		availableAnsibleRoles = append(availableAnsibleRoles, roleName)
	}

	// Cache the roles in the context
	e.Set("ansible_roles", availableAnsibleRoles)

	return slices.Contains(availableAnsibleRoles, roleName), nil

}

// Run a simple local ansible playbook that doesn't require any extra vars and doesn't log
func RunLocalAnsiblePlaybookOnTmpRangeConfig(e *core.RequestEvent, playbookPathArray []string) (string, error) {

	buff := new(bytes.Buffer)

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}
	user := e.Get("user").(*models.User)
	usersRange := e.Get("range").(*models.Range)

	accessGrantsArray := getAccessGrantsForUser(e, user.UserId())
	userVars := map[string]interface{}{
		"username":           user.ProxmoxUsername(),
		"range_id":           usersRange.RangeId(),
		"range_second_octet": usersRange.RangeNumber(),
		// We have to send this in the event this deploy is a fresh deploy AFTER a user has been granted access to this
		// range, which means there is a fresh router deployed with no knowledge of the access grants
		"access_grants_array":   accessGrantsArray,
		"ludus_testing_enabled": usersRange.TestingEnabled(),
	}

	// Always include the ludus, server, and user configs
	rangeDir := fmt.Sprintf("@%s/ranges/%s/", ludusInstallPath, usersRange.RangeId())
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), rangeDir + "range-config.yml"}
	inventory := "127.0.0.1"

	ansibleExecute := execute.NewDefaultExecute(
		// Use a multiwrtier that saves the output to a buffer and a file
		execute.WithWrite(buff),
		// Also log stderr to the log file and the buff vs stderr (journalctl logs)
		execute.WithWriteError(buff),
		// Disable color
		execute.WithEnvVar("ANSIBLE_NOCOLOR", "true"),
		// Set the ansible home to the user's ansible directory
		execute.WithEnvVar("ANSIBLE_HOME", fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername())),
		execute.WithEnvVar("ANSIBLE_SSH_CONTROL_PATH_DIR", fmt.Sprintf("%s/users/%s/.ansible/cp", ludusInstallPath, user.ProxmoxUsername())),
	)

	// Loop over the environment and add any that start with LUDUS_SECRET_ to the execute object
	for _, envVar := range os.Environ() {
		envVarParts := strings.SplitN(envVar, "=", 2)
		if len(envVarParts) != 2 {
			continue
		}
		envVarKey := envVarParts[0]
		envVarValue := envVarParts[1]
		if strings.HasPrefix(envVarKey, "LUDUS_SECRET_") {
			ansibleExecute.EnvVars[envVarKey] = envVarValue
		}
	}

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Inventory:     inventory,
		ExtraVarsFile: serverAndUserConfigs,
		ExtraVars:     userVars,
	}

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         playbookPathArray,
		Exec:              ansibleExecute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
		StdoutCallback:    "default",
	}

	// Set the ansible binary from the environment if it exists
	if ansibleBinary, ok := os.LookupEnv("LUDUS_ANSIBLE_BINARY"); ok {
		playbook.Binary = ansibleBinary
	}

	err := playbook.Run(context.TODO())
	if err != nil {
		return buff.String(), fmt.Errorf("error running ansible playbook: %w", err)
	}

	return buff.String(), nil

}

func GetErrorsFromAnsiblePlaybook(rangeID string) []string {
	ansibleLogPath := fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, rangeID)
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
