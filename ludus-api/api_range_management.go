package ludusapi

import (
	"database/sql"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/goforj/godump"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var ansibleTags = []string{"all", "additional-tools", "allow-share-access", "assign-ip", "custom-choco", "custom-groups", "dcs", "debug", "dns-rewrites",
	"domain-join", "install-office", "install-visual-studio", "network", "nexus", "share", "sysprep", "user-defined-roles", "vm-deploy", "windows"}

// DeployRange - deploys the range according to the range config
func DeployRange(e *core.RequestEvent) error {

	var deployBody dto.DeployRangeRequest
	e.BindBody(&deployBody)

	var tags string
	if deployBody.Tags == "" {
		// By default run "all" as the ansible tag
		tags = "all"
	} else {
		// If the user specified a tag or list of tags, make sure they exist
		tagsArray := strings.Split(deployBody.Tags, ",")
		for _, tag := range tagsArray {
			if !slices.Contains(ansibleTags, tag) {
				return JSONError(e, http.StatusBadRequest, fmt.Sprintf("The tag '%s' does not exist on the Ludus server", tag))
			}
		}
		tags = deployBody.Tags
	}

	usersRange := e.Get("range").(*models.Range)
	user := e.Get("user").(*models.User)

	// Make sure we aren't already in a "DEPLOYING" state
	if usersRange.RangeState() == "DEPLOYING" && !deployBody.Force {
		return JSONError(e, http.StatusConflict, "The range has an active deployment running. Try `range abort` to stop the deployment or run with --force if you really want to try a deploy")
	}

	if usersRange.TestingEnabled() && !deployBody.Force {
		return JSONError(e, http.StatusConflict, "Testing enabled; deploy requires internet access to succeed; run with --force if you really want to try a deploy with testing enabled")
	}

	// If the user specified roles, make sure they exist on the server before trying to use them
	if len(deployBody.OnlyRoles) > 0 {
		for _, role := range deployBody.OnlyRoles {
			if role != "" { // Ignore empty strings
				exists, err := checkRoleExists(e, role)
				if err != nil {
					return JSONError(e, http.StatusInternalServerError, err.Error())
				}
				if !exists {
					return JSONError(e, http.StatusBadRequest, fmt.Sprintf("The role '%s' does not exist on the Ludus server for user %s", role, user.UserId()))
				}
			}
		}
	}

	// Set range state to "DEPLOYING"
	usersRange.SetRangeState("DEPLOYING")
	if err := e.App.Save(usersRange); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving range: %v", err))
	}

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go RunRangeManagementAnsibleWithTag(e, tags, deployBody.Force, deployBody.OnlyRoles, deployBody.Limit)

	return JSONResult(e, http.StatusOK, "Range deploy started")
}

// DeleteRange - deletes a range object from the database and proxmox host
func DeleteRange(e *core.RequestEvent) error {

	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus range rm command with --url https://127.0.0.1:8081")
	}

	// Check if force parameter is provided
	forceStr := e.Request.URL.Query().Get("force")
	force := false
	var err error
	if forceStr != "" {
		force, err = strconv.ParseBool(forceStr)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid force parameter")
		}
	}

	// Update the range object to get the latest data
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	err = updateRangeVMData(e, e.Get("range").(*models.Range), proxmoxClient)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	// Get the updated range
	targetRange := e.Get("range").(*models.Range)

	// Check if range has VMs
	if targetRange.NumberOfVms() > 0 && !force {
		return JSONError(e, http.StatusConflict, "Range has VMs. Use --force to delete anyway")
	}

	// If range has VMs and force is true, destroy VMs first
	if targetRange.NumberOfVms() > 0 && force {
		_, err = RunPlaybookWithTag(e, "power.yml", "destroy-range", false)
		if err != nil {
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "Error with destroy-range\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), fmt.Sprintf("%s\n", err.Error()))
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			targetRange.SetRangeState("ERROR")
			err = e.App.Save(targetRange)
			if err != nil {
				return JSONError(e, http.StatusInternalServerError, err.Error())
			}
			return err // Don't remove the pool or range object if destroy fails
		}
	}

	// Remove the Resource Pool from Proxmox (also removes all ACLs for the pool)
	err = removePool(targetRange.RangeId())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	err = manageVmbrInterfaceLocally(targetRange.RangeNumber(), false)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	err = os.RemoveAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, targetRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	// Remove the range directory
	err = os.RemoveAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, targetRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	// Delete the range object from the database
	err = e.App.Delete(targetRange)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Range %s deleted", targetRange.RangeId()))
}

// DeleteRangeVMs - stops and deletes all range VMs (keeps range object in database)
func DeleteRangeVMs(e *core.RequestEvent) error {
	usersRange := e.Get("range").(*models.Range)

	logger.Debug("DeleteRangeVMs for range ID: " + usersRange.RangeId())

	// Set range state to "DESTROYING"
	usersRange.SetRangeState("DESTROYING")
	err := e.App.Save(usersRange)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go func(e *core.RequestEvent) {
		_, err := RunPlaybookWithTag(e, "power.yml", "destroy-range", false)
		if err != nil {
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "Error with destroy-range\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), fmt.Sprintf("%s\n", err.Error()))
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			usersRange.SetRangeState("ERROR")
			err = e.App.Save(usersRange)
			if err != nil {
				logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
			}
			return // Don't reset testing if destroy fails
		}
		// The user is rm-ing their range with testing enabled, so after all VMs are destroyed exit testing
		if usersRange.TestingEnabled() {
			// Update the testing state in the DB as well as allowed domains and ips
			usersRange.SetTestingEnabled(false)
			usersRange.SetAllowedDomains([]string{})
			usersRange.SetAllowedIps([]string{})
			err = e.App.Save(usersRange)
			if err != nil {
				logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
			}
		}
		// Set range state to "DESTROYED"
		usersRange.SetRangeState("DESTROYED")
		err = e.App.Save(usersRange)
		if err != nil {
			logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
		}
	}(e)

	return JSONResult(e, http.StatusOK, "Range VM destroy in progress")
}

// GetConfig - retrieves the current configuration of the range
func GetConfig(e *core.RequestEvent) error {
	usersRange := e.Get("range").(*models.Range)
	rangeConfig, err := GetFileContents(fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, usersRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	return JSONResult(e, http.StatusOK, rangeConfig)
}

// GetConfigExample - retrieves an example range configuration
func GetConfigExample(e *core.RequestEvent) error {
	rangeConfig, err := GetFileContents(ludusInstallPath + "/ansible/user-files/range-config.example.yml")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	return JSONResult(e, http.StatusOK, rangeConfig)
}

// GetEtcHosts - retrieves an /etc/hosts file for the range
func GetEtcHosts(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	etcHosts, err := GetFileContents(fmt.Sprintf("%s/users/%s/etc-hosts", ludusInstallPath, user.ProxmoxUsername()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	return JSONResult(e, http.StatusOK, etcHosts)
}

// GetRDP - retrieves RDP files as a zip for the range
func GetRDP(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	playbook := []string{ludusInstallPath + "/ansible/range-management/ludus.yml"}
	extraVars := map[string]interface{}{
		"username": user.ProxmoxUsername(),
	}
	output, err := server.RunAnsiblePlaybookWithVariables(e, playbook, []string{}, extraVars, "generate-rdp", false, "")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, output)
	}

	filePath := fmt.Sprintf("%s/users/%s/rdp.zip", ludusInstallPath, user.ProxmoxUsername())
	fileContents, err := os.ReadFile(filePath)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	e.Response.Header().Set("Content-Disposition", "attachment; filename=rdp.zip")
	e.Response.Header().Set("Content-Type", "application/zip")
	e.Response.Write(fileContents)
	return nil
}

// GetLogs - retrieves the latest range logs
func GetLogs(e *core.RequestEvent) error {
	targetRange := e.Get("range").(*models.Range)
	ansibleLogPath := fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId())
	GetLogsFromFile(e, ansibleLogPath)
	return nil
}

// GetSSHConfig - retrieves a ssh config file for the range
func GetSSHConfig(e *core.RequestEvent) error {
	return JSONError(e, http.StatusNotImplemented, "Not implemented")
}

// ListRange - lists range VMs, their power state, and their testing state
func ListRange(e *core.RequestEvent) error {
	usersRange := e.Get("range").(*models.Range)

	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	err = updateRangeVMData(e, usersRange, proxmoxClient)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	logger.Debug(fmt.Sprintf("ListRange: Listing range %s with range number %d", usersRange.RangeId(), usersRange.RangeNumber()))

	rangeVMs, err := app.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": usersRange.Id}))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	response := dto.ListRangeResponse{
		RangeState:     usersRange.RangeState(),
		RangeNumber:    int32(usersRange.RangeNumber()),
		Description:    usersRange.Description(),
		Purpose:        usersRange.Purpose(),
		LastDeployment: usersRange.LastDeployment().Time(),
		TestingEnabled: usersRange.TestingEnabled(),
		VMs:            make([]dto.ListRangeResponseVMsItem, 0),
		RangeID:        usersRange.RangeId(),
		Name:           usersRange.Name(),
		NumberOfVMs:    int32(usersRange.NumberOfVms()),
		AllowedIPs:     usersRange.AllowedIps(),
		AllowedDomains: usersRange.AllowedDomains(),
	}

	for _, vm := range rangeVMs {
		vmRecord := &models.VMs{}
		vmRecord.SetProxyRecord(vm)
		response.VMs = append(response.VMs, dto.ListRangeResponseVMsItem{
			ProxmoxID:   int32(vmRecord.ProxmoxId()),
			RangeNumber: int32(usersRange.RangeNumber()),
			Name:        vmRecord.Name(),
			PoweredOn:   vmRecord.PoweredOn(),
			Ip:          vmRecord.Ip(),
			IsRouter:    vmRecord.IsRouter(),
		})
	}

	return e.JSON(http.StatusOK, response)
}

func ListAllRanges(e *core.RequestEvent) error {

	var ranges []*models.Range

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() {
		// Get range details for each accessible range
		ranges = user.Ranges()
	} else {
		// Admin gets all ranges
		rangeRecords, err := app.FindAllRecords("ranges")
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
		for _, rangeRecord := range rangeRecords {
			rangeObj := &models.Range{}
			rangeObj.SetProxyRecord(rangeRecord)
			ranges = append(ranges, rangeObj)
		}
	}

	// Get Proxmox client and VM data for updating counts
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	response := make([]dto.ListAllRangeResponseItem, 0)

	// Sort the ranges by range number
	slices.SortFunc(ranges, func(a, b *models.Range) int {
		return int(a.RangeNumber() - b.RangeNumber())
	})

	// Update VM data for all ranges
	for _, rangeRecord := range ranges {
		err = updateRangeVMData(e, rangeRecord, proxmoxClient)
		if err != nil {
			logger.Error(fmt.Sprintf("Error updating VM data for range %s: %s", rangeRecord.RangeId(), err.Error()))
			continue
		}
		responseItem := dto.ListAllRangeResponseItem{
			VMs:            make([]dto.ListAllRangeResponseItemVMsItem, 0),
			RangeID:        rangeRecord.RangeId(),
			Name:           rangeRecord.Name(),
			NumberOfVMs:    int32(rangeRecord.NumberOfVms()),
			AllowedIPs:     rangeRecord.AllowedIps(),
			AllowedDomains: rangeRecord.AllowedDomains(),
			LastDeployment: rangeRecord.LastDeployment().Time(),
			RangeState:     rangeRecord.RangeState(),
			RangeNumber:    int32(rangeRecord.RangeNumber()),
			Description:    rangeRecord.Description(),
			Purpose:        rangeRecord.Purpose(),
			TestingEnabled: rangeRecord.TestingEnabled(),
		}
		vmRecords, err := app.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": rangeRecord.RangeId()}))
		if err != nil {
			logger.Error(fmt.Sprintf("Error finding VMs for range %s: %s", rangeRecord.RangeId(), err.Error()))
			continue
		}
		for _, vmRecord := range vmRecords {
			vmRecordObj := &models.VM{}
			vmRecordObj.SetProxyRecord(vmRecord)
			responseItem.VMs = append(responseItem.VMs, dto.ListAllRangeResponseItemVMsItem{
				Ip:          vmRecordObj.Ip(),
				IsRouter:    vmRecordObj.IsRouter(),
				ProxmoxID:   int32(vmRecordObj.ProxmoxId()),
				RangeNumber: int32(vmRecordObj.Range().RangeNumber()),
				Name:        vmRecordObj.Name(),
				PoweredOn:   vmRecordObj.PoweredOn(),
			})
		}
		response = append(response, responseItem)
	}

	return e.JSON(http.StatusOK, response)
}

// PutConfig - updates the range config
func PutConfig(e *core.RequestEvent) error {
	targetRange := e.Get("range").(*models.Range)

	// Retrieve the 'force' field and convert it to boolean
	forceStr := e.Request.FormValue("force")
	force, err := strconv.ParseBool(forceStr)
	if forceStr != "" && err != nil { // Empty string (unset) => force is false
		return JSONError(e, http.StatusBadRequest, "Invalid boolean value")
	}

	if targetRange.TestingEnabled() && !force {
		return JSONError(e, http.StatusConflict, "Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled. Use --force to override.")
	}

	file, _, err := e.Request.FormFile("file")

	// The file cannot be received.
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Error retrieving file from request: "+err.Error())
	}

	// The file is received, so let's save it
	filePath := fmt.Sprintf("%s/ranges/%s/.tmp-range-config.yml", ludusInstallPath, targetRange.RangeId())
	fileContents, err := io.ReadAll(file)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to read file contents: "+err.Error())
	}
	err = os.WriteFile(filePath, fileContents, 0644)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save the range config: "+err.Error())
	}
	// Validate the uploaded range-config - return an error if it isn't valid
	err = validateFile(e, filePath, ludusInstallPath+"/ansible/user-files/range-config.jsonschema")
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Configuration error: "+err.Error())
	}

	// Check the roles and dependencies
	rangeHasRoles := e.Get("rangeHasRoles")
	if rangeHasRoles != nil && rangeHasRoles.(bool) {
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), "Resolving dependencies for user-defined roles..\n", false)
		rolesOutput, err := RunLocalAnsiblePlaybookOnTmpRangeConfig(e, []string{fmt.Sprintf("%s/ansible/range-management/user-defined-roles.yml", ludusInstallPath)})
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), rolesOutput, true)
		if err != nil {
			targetRange.SetRangeState("ERROR")
			err = e.App.Save(targetRange)
			if err != nil {
				logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
			}
			// Find the 'ERROR' line in the output and return it to the user
			errorLine := regexp.MustCompile(`ERROR[^"]*`)
			errorMatch := errorLine.FindString(rolesOutput)
			if errorMatch != "" {
				return JSONError(e, http.StatusBadRequest, "Configuration error: "+errorMatch)
			} else {
				return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Error generating ordered roles: %s %s", rolesOutput, err))

			}
		}
	}

	// The file is valid, so let's move it to the range-config
	err = os.Rename(filePath, fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, targetRange.RangeId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save the range config")
	}

	// File saved successfully. Return proper result
	return JSONResult(e, http.StatusOK, "Your range config has been successfully updated.")
}

func GetAnsibleInventoryForRange(e *core.RequestEvent) error {
	targetRange := e.Get("range").(*models.Range)
	targetUser := e.Get("user").(*models.User)

	proxmoxTokenSecret, err := DecryptStringFromDatabase(targetUser.ProxmoxTokenSecret())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Could not decrypt proxmox token secret: %s", err))
	}

	// Check for allranges parameter
	allRanges := e.Request.URL.Query().Get("allranges") == "true"

	cmd := exec.Command("ansible-inventory", "-i", ludusInstallPath+"/ansible/range-management/proxmox.py", "--list", "-y")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, targetUser.ProxmoxUsername()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_NODE=%s", ServerConfiguration.ProxmoxNode))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_INVALID_CERT=%s", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_URL=%s", ServerConfiguration.ProxmoxURL))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_HOSTNAME=%s", ServerConfiguration.ProxmoxHostname))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_USERNAME=%s", targetUser.ProxmoxUsername()+"@"+targetUser.ProxmoxRealm()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_TOKEN=%s", targetUser.ProxmoxTokenId()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_SECRET=%s", proxmoxTokenSecret))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RANGE_CONFIG=%s/ranges/%s/range-config.yml", ludusInstallPath, targetRange.RangeId()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RANGE_NUMBER=%s", strconv.Itoa(int(targetRange.RangeNumber()))))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RANGE_ID=%s", targetUser.UserId()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RETURN_ALL_RANGES=%s", strconv.FormatBool(allRanges)))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_USER_IS_ADMIN=%s", strconv.FormatBool(targetUser.IsAdmin())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible inventory: "+string(out))
	}
	return JSONResult(e, http.StatusOK, string(out))
}

func GetAnsibleTagsForDeployment(e *core.RequestEvent) error {
	cmd := fmt.Sprintf("grep 'tags:' %s/ansible/range-management/ludus.yml | cut -d ':' -f 2 | tr -d '[]' | tr ',' '\\n' | egrep -v 'always|never' | sort -u | paste -sd ',' -", ludusInstallPath)
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible tags: "+err.Error())
	}
	return JSONResult(e, http.StatusOK, string(out))
}

// Find the ansible process for this user and kill it
func AbortAnsible(e *core.RequestEvent) error {
	targetRange := e.Get("range").(*models.Range)
	targetUser := e.Get("user").(*models.User)

	ansiblePid, err := findAnsiblePidForUser(targetUser.ProxmoxUsername())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	killProcessAndChildren(ansiblePid)

	// Set range state to "ABORTED"
	targetRange.SetRangeState("ABORTED")
	err = e.App.Save(targetRange)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, "Ansible process aborted")
}

// New range management endpoints for group-based access system

// CreateRange allows users to create ranges not tied to a specific user
func CreateRange(e *core.RequestEvent) error {

	// Make sure this is the admin server (root) as we need to create a vmbr interface for the range
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus range create command with --url https://127.0.0.1:8081")
	}

	var payload dto.CreateRangeRequest
	if err := e.BindBody(&payload); err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	// If UserID is provided, verify the user exists
	if payload.UserID != "" {
		_, err := e.App.FindFirstRecordByData("users", "userID", payload.UserID)
		if err != nil {
			return JSONError(e, http.StatusNotFound, fmt.Sprintf("User not found: %v", err))
		}
	}

	if poolExists(payload.RangeID) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Pool with the name %s already exists", payload.RangeID))
	}

	// Determine range number
	var rangeNumber int
	if payload.RangeNumber > 0 {
		// Check if range number is already in use
		rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeNumber", payload.RangeNumber)
		if err != nil && err != sql.ErrNoRows {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if range number %d is already in use: %v", payload.RangeNumber, err))
		}
		if rangeRecord != nil {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Range number %d already in use", payload.RangeNumber))
		}
		// Make sure the range number is not a reserved range number
		if slices.Contains(ServerConfiguration.ReservedRangeNumbers, int32(payload.RangeNumber)) {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Range number %d is a reserved range number. Edit the reserved_range_numbers array in /opt/ludus/config.yml to use this range number.", payload.RangeNumber))
		}
		rangeNumber = int(payload.RangeNumber)
	} else {
		// Find next available range number
		rangeNumber = findNextAvailableRangeNumber(app)
	}

	// Check if name is already in use
	existingRange, err := e.App.FindFirstRecordByData("ranges", "rangeID", payload.RangeID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if range ID %s is already in use: %v", payload.RangeID, err))
	}
	if existingRange != nil {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Range ID %s already in use", payload.RangeID))
	}

	// Validate the name is a valid proxmox pool name using regex
	proxmoxPoolNameRegex := regexp.MustCompile(`^[A-Za-z0-9_\-]+(\/[A-Za-z0-9_\-]+){0,2}$`)
	if !proxmoxPoolNameRegex.MatchString(payload.RangeID) {
		return JSONError(e, http.StatusBadRequest, "Range ID name must be a valid proxmox pool name (e.g. 'DEMO', 'my-range' or 'New_Range'). Use only letters, numbers, hyphens, and underscores.")
	}

	// Create a new resource pool for the range
	err = createPool(payload.RangeID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to create resource pool: "+err.Error())
	}

	// Create the vmbr interface for the range
	err = manageVmbrInterfaceLocally(rangeNumber, true)
	if err != nil {
		removePool(payload.RangeID)
		return JSONError(e, http.StatusInternalServerError, "Unable to create vmbr interface: "+err.Error())
	}

	// Create the range config file
	os.MkdirAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, payload.RangeID), 0755)
	copyFileContents(fmt.Sprintf("%s/ansible/user-files/range-config.example.yml", ludusInstallPath), fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, payload.RangeID))

	// Create the range
	rangeCollection, err := e.App.FindCollectionByNameOrId("ranges")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to find ranges collection: "+err.Error())
	}
	rawRangeRecord := core.NewRecord(rangeCollection)
	rangeRecord := models.Ranges{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	rangeRecord.SetName(payload.Name)
	rangeRecord.SetRangeId(payload.RangeID)
	rangeRecord.SetDescription(payload.Description)
	rangeRecord.SetPurpose(payload.Purpose)
	rangeRecord.SetRangeNumber(rangeNumber)
	rangeRecord.SetNumberOfVms(0)
	rangeRecord.SetRangeState("NEVER DEPLOYED")

	err = e.App.Save(rangeRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save range: "+err.Error())
	}

	// If UserID was provided, create direct access record
	if payload.UserID != "" {
		userRecord, err := e.App.FindFirstRecordByData("users", "userID", payload.UserID)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
		}
		userRecord.Set("ranges+", rangeRecord.Id)
		err = e.App.Save(userRecord)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+err.Error())
		}
		// Give the user in proxmox permissions to the pool
		err = giveUserAccessToPool(userRecord.GetString("proxmoxUsername"), "pam", payload.RangeID)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to give user access to pool: "+err.Error())
		}
	}

	return JSONResult(e, http.StatusCreated, "Range created successfully")
}

func AssignOrRevokeRangeAccess(e *core.RequestEvent, actionVerb string, force bool) error {
	targetUser := e.Get("user").(*models.User)
	if !targetUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You must be an admin to use this endpoint")
	}

	rangeID := e.Request.URL.Query().Get("rangeID")
	rangeNumber, err := GetRangeNumberFromRangeID(rangeID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found: %v", rangeID, err))
	}

	userID := e.Request.URL.Query().Get("userID")
	if userID == "" {
		return JSONError(e, http.StatusBadRequest, "User ID is required")
	}

	// Check if user already has access to this range
	if actionVerb == "grant" && HasRangeAccess(userID, rangeNumber) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("User %s already has access to range %s", userID, rangeID))
	}

	if actionVerb == "revoke" && !HasRangeAccess(userID, rangeNumber) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("User %s does not have access to range %s", userID, rangeID))
	}

	// Get the target range object
	targetRange, err := GetRangeObjectByNumber(rangeNumber)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting range object: %v", err))
	}

	// Get the source user object by looking up the user ID in the database
	sourceUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting user object: %v", err))
	}
	sourceUserObject := models.User{}
	sourceUserObject.SetProxyRecord(sourceUserRecord)

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
		logger.Debug("actionVerb: " + actionVerb)
		if strings.Contains(output, "Target router is not up") && actionVerb == "grant" {
			return JSONResult(e, http.StatusOK, `WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
The target range router VM is inaccessible!
If the target range router is deployed, no firewall changes have taken place.
If the VM is not deployed yet, the rule will be added when it is deployed and you can ignore this.
WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!`)
		} else if strings.Contains(output, "Target router is not up") && actionVerb == "revoke" && !force {
			return JSONError(e, http.StatusInternalServerError, "The target range router is inaccessible. To revoke access, the target range router must be up and accessible. Use --force to override this protection.")
		} else if strings.Contains(output, "Target router is not up") && actionVerb == "revoke" && force {
			return nil
		} else { // Some other error we want to fail on
			return JSONError(e, http.StatusInternalServerError, output)
		}
	}

	if actionVerb == "grant" {

		sourceUserObject.Set("ranges+", targetRange.Id)
		err = e.App.Save(sourceUserObject)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+err.Error())
		}

		// Give the user access to the proxmox pool for the range
		err = giveUserAccessToPool(sourceUserObject.ProxmoxUsername(), "pam", targetRange.RangeId())
		if err != nil {
			sourceUserObject.Set("ranges-", targetRange.Id)
			saveErr := e.App.Save(sourceUserObject)
			if saveErr != nil {
				return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+saveErr.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Unable to give user access to pool: "+err.Error())
		}

		// Check if c.Writer.Written() is false to prevent double response if we set the warning result above
		return JSONResult(e, http.StatusCreated, "Range assigned to user successfully")

	} else if actionVerb == "revoke" {

		// Check if the user has direct access to the range
		sourceUserObject.Set("ranges-", targetRange.Id)
		err = e.App.Save(sourceUserObject)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+err.Error())
		}

		err := removeUserAccessFromPool(sourceUserObject.ProxmoxUsername(), "pam", targetRange.RangeId())
		if err != nil {
			sourceUserObject.Set("ranges+", targetRange.Id)
			saveErr := e.App.Save(sourceUserObject)
			if saveErr != nil {
				return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+saveErr.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Unable to remove user access from pool: "+err.Error())
		}

		return JSONResult(e, http.StatusOK, "Range access revoked from user successfully")
	} else {
		return JSONError(e, http.StatusBadRequest, "Invalid action verb")
	}
}

// AssignRangeToUser directly assigns a range to a user (admin only)
func AssignRangeToUser(e *core.RequestEvent) error {
	return AssignOrRevokeRangeAccess(e, "grant", false)
}

// RevokeRangeFromUser revokes direct range access from a user (admin only)
func RevokeRangeFromUser(e *core.RequestEvent) error {
	forceStr := e.Request.URL.Query().Get("force")
	force := false
	var err error
	if forceStr != "" {
		force, err = strconv.ParseBool(forceStr)
		if err != nil {
			return JSONError(e, http.StatusBadRequest, "Invalid force parameter")
		}
	}
	return AssignOrRevokeRangeAccess(e, "revoke", force)
}

// ListRangeUsers lists all users with access to a range (admins and users with access to the range only)
func ListRangeUsers(e *core.RequestEvent) error {

	rangeID := e.Request.PathValue("rangeID")
	if rangeID == "" {
		return JSONError(e, http.StatusBadRequest, "Range ID is required")
	}
	rangeNumber, err := GetRangeNumberFromRangeID(rangeID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found: %v", rangeID, err))
	}

	user := e.Get("user").(*models.User)
	userHasAccess := HasRangeAccess(user.UserId(), rangeNumber)
	if !userHasAccess && !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You (%s) do not have access to range %s and cannot list users with access to it", user.UserId(), rangeID))
	}

	// Get all users with access to this range
	result := GetRangeAccessibleUsers(rangeNumber)

	return e.JSON(http.StatusOK, result)
}

// ListUserAccessibleRanges lists all ranges the current user can access
func ListUserAccessibleRanges(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	if user == nil {
		return JSONError(e, http.StatusUnauthorized, "User not found")
	}

	// Get all ranges the user can access
	accessibleRanges := GetUserAccessibleRanges(user.UserId())

	var result dto.ListUserAccessibleRangesResponse
	result.Value = make([]dto.ListUserAccessibleRangesResponseItem, 0)
	for _, accessibleRange := range accessibleRanges {
		result.Value = append(result.Value, dto.ListUserAccessibleRangesResponseItem{
			RangeID:     accessibleRange.RangeID,
			RangeNumber: int64(accessibleRange.RangeNumber),
			AccessType:  accessibleRange.AccessType,
		})
	}

	return e.JSON(http.StatusOK, result)
}
