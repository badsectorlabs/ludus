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
	"maps"
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
	yaml "sigs.k8s.io/yaml"
)

func getMergedDefaults(rangeConfigPath string) map[string]interface{} {
	mergedDefaults := map[string]interface{}{}

	serverConfigPath := fmt.Sprintf("%s/ansible/server-config.yml", ludusInstallPath)
	serverConfigBytes, err := os.ReadFile(serverConfigPath)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to read server config for defaults merge: %v", err))
		return mergedDefaults
	}

	var serverConfig map[string]interface{}
	if err := yaml.Unmarshal(serverConfigBytes, &serverConfig); err != nil {
		logger.Debug(fmt.Sprintf("Failed to parse server config for defaults merge: %v", err))
		return mergedDefaults
	}

	if serverDefaults, ok := serverConfig["defaults"].(map[string]interface{}); ok {
		maps.Copy(mergedDefaults, serverDefaults)
	}

	if rangeConfigPath == "" || !FileExists(rangeConfigPath) {
		return mergedDefaults
	}

	rangeConfigBytes, err := os.ReadFile(rangeConfigPath)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to read range config for defaults merge: %v", err))
		return mergedDefaults
	}

	var rangeConfig map[string]interface{}
	if err := yaml.Unmarshal(rangeConfigBytes, &rangeConfig); err != nil {
		logger.Debug(fmt.Sprintf("Failed to parse range config for defaults merge: %v", err))
		return mergedDefaults
	}

	if rangeDefaults, ok := rangeConfig["defaults"].(map[string]interface{}); ok {
		maps.Copy(mergedDefaults, rangeDefaults)
	}

	return mergedDefaults
}

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
	usersRange, err := GetRange(e)
	if err != nil {
		return "", err
	}

	accessGrantsArray := GetRangeAccessibleUsers(usersRange.RangeNumber())
	rangeConfigPath := fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, usersRange.RangeId())

	// Compute target nodes for cluster deployments
	rangeDefaultTargetNode, vmTargetNodes := computeTargetNodes(e, usersRange.RangeId())

	userVars := map[string]interface{}{
		"username":           user.ProxmoxUsername(),
		"range_id":           usersRange.RangeId(),
		"range_second_octet": usersRange.RangeNumber(),
		// We have to send this in the event this deploy is a fresh deploy AFTER a user has been granted access to this
		// range, which means there is a fresh router deployed with no knowledge of the access grants
		"access_grants_array":   accessGrantsArray,
		"ludus_testing_enabled": usersRange.TestingEnabled(),
		// Pass license entitlements to ansible
		"ludus_entitlements": server.Entitlements,
		"wireguard_port":     ServerConfiguration.WireguardPort,
		// Cluster mode target node settings
		"range_default_target_node": rangeDefaultTargetNode,
		"vm_target_nodes":           vmTargetNodes,
		"ludus_cluster_mode":        UseSDN,
	}

	// Extra vars files are merged at top-level only; without this, a user-provided
	// partial defaults object replaces all server defaults.
	userVars["defaults"] = getMergedDefaults(rangeConfigPath)

	// Merge userVars with any extraVars provided
	maps.Copy(userVars, extraVars)

	// Always include the ludus, server, and user configs
	var serverAndUserConfigs []string
	if FileExists(rangeConfigPath) {
		// The @ prefix is used to tell ansible to use the file as a local file
		serverAndUserConfigs = []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), "@" + rangeConfigPath}
	} else {
		serverAndUserConfigs = []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath)}
	}
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
		return "Failed to open ansible log file", errors.New("failed to open ansible log file: " + err.Error())
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

// RunAddUserPlaybookStandalone runs the add-user playbook without a request event.
// Used when creating the initial admin user during InitDb. extraVars must include:
// username, user_id, user_number, proxmox_public_ip, user_is_admin, proxmox_password.
func RunAddUserPlaybookStandalone(extraVars map[string]interface{}) (string, error) {
	buff := new(bytes.Buffer)
	playbookPath := ludusInstallPath + "/ansible/user-management/add-user.yml"
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath)}

	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Inventory:     "127.0.0.1",
		ExtraVarsFile: serverAndUserConfigs,
		ExtraVars:     extraVars,
		Tags:          "",
		Verbose:       false,
	}

	ansibleLogFilePath := fmt.Sprintf("%s/install/ansible.log", ludusInstallPath)
	ansibleLogFile, err := os.OpenFile(ansibleLogFilePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0660)
	if err != nil {
		return "", fmt.Errorf("failed to open ansible log file: %w", err)
	}
	defer func() {
		ansibleLogFile.Close()
		if os.Geteuid() == 0 {
			changeFileOwner(ansibleLogFilePath, "ludus")
		}
	}()

	ansibleExecute := execute.NewDefaultExecute(
		execute.WithWrite(io.MultiWriter(buff, ansibleLogFile)),
		execute.WithWriteError(io.MultiWriter(buff, ansibleLogFile)),
		execute.WithEnvVar("ANSIBLE_NOCOLOR", "true"),
		execute.WithEnvVar("ANSIBLE_HOME", fmt.Sprintf("%s/install", ludusInstallPath)),
		execute.WithEnvVar("PROXMOX_NODE", ServerConfiguration.ProxmoxNode),
		execute.WithEnvVar("PROXMOX_INVALID_CERT", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)),
		execute.WithEnvVar("PROXMOX_URL", ServerConfiguration.ProxmoxURL),
		execute.WithEnvVar("PROXMOX_HOSTNAME", ServerConfiguration.ProxmoxHostname),
	)

	pb := &playbook.AnsiblePlaybookCmd{
		Playbooks:         []string{playbookPath},
		Exec:              ansibleExecute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
		StdoutCallback:    "default",
	}
	if ansibleBinary, ok := os.LookupEnv("LUDUS_ANSIBLE_BINARY"); ok {
		pb.Binary = ansibleBinary
	}

	err = pb.Run(context.TODO())
	if err != nil {
		return buff.String(), err
	}
	return buff.String(), nil
}

// A helper to keep function calls clean
func RunRangeManagementAnsibleWithTag(e *core.RequestEvent, tag string, verbose bool, onlyRoles []string, limit string) (string, error) {
	usersRange, err := GetRange(e)
	if err != nil {
		return "", err
	}

	onlyRolesArray := removeEmptyStrings(onlyRoles)
	extraVars := map[string]interface{}{"only_roles": onlyRolesArray}

	// Run the deploy
	output, err := server.RunAnsiblePlaybookWithVariables(e, nil, nil, extraVars, tag, verbose, limit)

	if err != nil {
		usersRange.SetRangeState(LudusRangeStateError)
		if saveErr := e.App.Save(usersRange); saveErr != nil {
			return "", fmt.Errorf("error saving range: %w", saveErr)
		}
	} else {
		usersRange.SetRangeState(LudusRangeStateSuccess)
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

		var stdoutBuf bytes.Buffer
		collectionCmd.Stdout = &stdoutBuf
		collectionCmd.Stderr = io.Discard
		err := collectionCmd.Run()
		collectionOutput := stdoutBuf.Bytes()
		if err != nil {
			return false, errors.New("Unable to get the ansible collections: " + err.Error())
		}

		// Unmarshal the JSON into a suitable Go data structure
		var data map[string]map[string]map[string]string
		err = json.Unmarshal(collectionOutput, &data)
		if err != nil {
			return false, errors.New("Unable to parse ansible collections JSON: " + err.Error())
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
	var stdoutBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	roleOutput := stdoutBuf.Bytes()
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
	usersRange, err := GetRange(e)
	if err != nil {
		return "", err
	}

	accessGrantsArray := GetRangeAccessibleUsers(usersRange.RangeNumber())
	userVars := map[string]interface{}{
		"username":           user.ProxmoxUsername(),
		"range_id":           usersRange.RangeId(),
		"range_second_octet": usersRange.RangeNumber(),
		// We have to send this in the event this deploy is a fresh deploy AFTER a user has been granted access to this
		// range, which means there is a fresh router deployed with no knowledge of the access grants
		"access_grants_array":   accessGrantsArray,
		"ludus_testing_enabled": usersRange.TestingEnabled(),
	}
	userVars["defaults"] = getMergedDefaults(fmt.Sprintf("%s/ranges/%s/.tmp-range-config.yml", ludusInstallPath, usersRange.RangeId()))

	// Always include the ludus, server, and user configs
	// Use .tmp-range-config.yml since this function is called during PutConfig before the file is renamed
	rangeDir := fmt.Sprintf("@%s/ranges/%s/", ludusInstallPath, usersRange.RangeId())
	serverAndUserConfigs := []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath), fmt.Sprintf("@%s/ansible/server-config.yml", ludusInstallPath), rangeDir + ".tmp-range-config.yml"}
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

	err = playbook.Run(context.TODO())
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

// computeTargetNodes computes the target node for each VM in the range config
// Returns: (rangeDefaultTargetNode, vmTargetNodes map[vm_name]node)
// Priority:
// 1. Node the VM is already running on
// 2. VM target_node
// 3. Range target_node
// 4. Auto-select (80% RAM + 20% CPU weighted algorithm)
// 5. Server proxmox_node
func computeTargetNodes(e *core.RequestEvent, rangeID string) (string, map[string]string) {
	vmTargetNodes := make(map[string]string)

	// Read and parse the range config
	rangeConfigPath := fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, rangeID)
	if !FileExists(rangeConfigPath) {
		// No range config, use default node
		return ServerConfiguration.ProxmoxNode, vmTargetNodes
	}

	configBytes, err := os.ReadFile(rangeConfigPath)
	if err != nil {
		logger.Debug(fmt.Sprintf("Failed to read range config for target node computation: %v", err))
		return ServerConfiguration.ProxmoxNode, vmTargetNodes
	}

	var config LudusConfig
	if err := yaml.Unmarshal(configBytes, &config); err != nil {
		logger.Debug(fmt.Sprintf("Failed to parse range config for target node computation: %v", err))
		return ServerConfiguration.ProxmoxNode, vmTargetNodes
	}

	// Determine the default target node
	// Priority: Range target_node > Auto-select
	var defaultTargetNode string
	if config.Defaults != nil {
		defaultTargetNode = config.Defaults.TargetNode
	}
	if defaultTargetNode == "" {
		// Try auto-selection based on resource usage
		client, err := getRootGoProxmoxClient()
		if err == nil {
			selectedNode, err := SelectOptimalNode(client)
			if err == nil {
				defaultTargetNode = selectedNode
			}
		}
	}

	// If still empty, use the configured node
	if defaultTargetNode == "" {
		defaultTargetNode = ServerConfiguration.ProxmoxNode
	}

	// Build per-VM target node map
	// Priority: Existing VM's node >VM target_node > Range default
	for _, vm := range config.Ludus {
		node, err := getNodeForVMByName(e, vm.VMName)
		if err == nil && node != "" {
			logger.Debug(fmt.Sprintf("VM %s is already running on node %s", vm.VMName, node))
			vmTargetNodes[vm.VMName] = node
		} else if vm.TargetNode != "" {
			logger.Debug(fmt.Sprintf("VM %s target node is %s", vm.VMName, vm.TargetNode))
			vmTargetNodes[vm.VMName] = vm.TargetNode
		} else {
			logger.Debug(fmt.Sprintf("VM %s target node is default node %s", vm.VMName, defaultTargetNode))
			vmTargetNodes[vm.VMName] = defaultTargetNode
		}
	}

	// Router: always set target node when we have a router VM name. Priority: existing node > router.target_node > range default
	usersRange, err := GetRange(e)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to get range for target node computation: %v", err))
		return defaultTargetNode, vmTargetNodes
	}
	routerVMName, err := GetRouterVMName(usersRange)
	if err == nil && routerVMName != "" {
		if node, err := getNodeForVMByName(e, routerVMName); err == nil && node != "" {
			logger.Debug(fmt.Sprintf("Router %s is running on node %s", routerVMName, node))
			vmTargetNodes[routerVMName] = node
		} else if config.Router != nil && config.Router.TargetNode != "" {
			logger.Debug(fmt.Sprintf("Router %s target node is %s", routerVMName, config.Router.TargetNode))
			vmTargetNodes[routerVMName] = config.Router.TargetNode
		} else {
			logger.Debug(fmt.Sprintf("Router %s target node is default node %s", routerVMName, defaultTargetNode))
			vmTargetNodes[routerVMName] = defaultTargetNode
		}
	}

	logger.Debug(fmt.Sprintf("Computed target nodes for range %s: default=%s, per-vm=%v", rangeID, defaultTargetNode, vmTargetNodes))
	return defaultTargetNode, vmTargetNodes
}
