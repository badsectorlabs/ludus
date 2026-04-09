package ludusapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"ludusapi/commandmanager"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	LudusRangeStateDeploying     = "DEPLOYING"
	LudusRangeStateError         = "ERROR"
	LudusRangeStateAborted       = "ABORTED"
	LudusRangeStateDestroying    = "DESTROYING"
	LudusRangeStateDestroyed     = "DESTROYED"
	LudusRangeStateNeverDeployed = "NEVER DEPLOYED"
	LudusRangeStateSuccess       = "SUCCESS"
	ProxmoxPoolNameRegexString   = `^[A-Za-z][A-Za-z0-9_\-]*(\/[A-Za-z0-9_\-]+){0,2}$`
)

var ansibleTags = []string{"all", "access-control", "additional-tools", "allow-share-access", "assign-ip", "custom-choco", "custom-groups", "dcs", "debug", "dns-rewrites",
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

	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}
	user := e.Get("user").(*models.User)

	// Make sure we aren't already in a "DEPLOYING" state
	if usersRange.RangeState() == LudusRangeStateDeploying && !deployBody.Force {
		return JSONError(e, http.StatusConflict, "The range has an active deployment running. Try `range abort` to stop the deployment or run with --force if you really want to try a deploy")
	}

	if usersRange.RangeState() == LudusRangeStateDestroying && !deployBody.Force {
		return JSONError(e, http.StatusConflict, "The range is currently being destroyed. Wait for it to finish or run with --force if you really want to try a deploy")
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

	// Check quota before deploying
	e.App.ExpandRecord(user.Record, []string{"ranges", "groups"}, nil)
	if server.HasEntitlement("ENTERPRISE_PLUGIN") {
		violations, err := CheckDeployQuota(e.App, user, usersRange.RangeId())
		if err != nil {
			logger.Error(fmt.Sprintf("Error checking quota: %s", err.Error()))
			// Log error but don't block — quota check is best-effort if there's an internal error
		} else if len(violations) > 0 {
			return JSONError(e, http.StatusForbidden, FormatQuotaViolations(violations))
		}
	}

	// Set range state to "DEPLOYING"
	usersRange.SetRangeState(LudusRangeStateDeploying)
	usersRange.SetLastDeployment(types.NowDateTime())
	if err := e.App.Save(usersRange); err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving range: %v", err))
	}

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go RunRangeManagementAnsibleWithTag(e, tags, deployBody.Force, deployBody.OnlyRoles, deployBody.Limit)

	return JSONResult(e, http.StatusOK, "Range deploy started")
}

// deleteRangeResources - performs the actual deletion of range resources (VMs, pool, vmbr, directory, database record)
// This function can be reused in other places that need to delete a range.
// It assumes the range object has already been updated with the latest VM data via updateRangeVMData.
func deleteRangeResources(targetRange *models.Range, force bool, e *core.RequestEvent) error {
	var err error

	// Get the proxmox client
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// If range has VMs and force is true, destroy VMs first
	if targetRange.NumberOfVms() > 0 && force {
		destroyedVMs := false
		// Get the VMs for the range
		vms, err := getVMsForPool(e, ctx, targetRange.RangeId(), proxmoxClient)
		if err != nil {
			return fmt.Errorf("failed to get VMs for range: %w", err)
		}
		for _, vm := range vms {
			if err := destroyVM(ctx, proxmoxClient, int(vm.VMID)); err != nil {
				return fmt.Errorf("failed to destroy VM %d: %w", int(vm.VMID), err)
			}
			destroyedVMs = true
		}

		// If we destroyed any VMs, wait for the pool to be empty before removing it.
		if destroyedVMs {
			if err := waitForPoolEmpty(ctx, proxmoxClient, targetRange.RangeId()); err != nil {
				return fmt.Errorf("failed waiting for pool %s to become empty: %w", targetRange.RangeId(), err)
			}
		}
	}

	// Remove the Resource Pool from Proxmox (also removes all ACLs for the pool)
	err = removePool(targetRange.RangeId())
	if err != nil {
		return fmt.Errorf("failed to remove pool: %w", err)
	}

	err = manageRangeNetwork(targetRange.RangeId(), targetRange.RangeNumber(), false)
	if err != nil {
		return fmt.Errorf("failed to manage range network: %w", err)
	}

	// Remove the range directory
	err = os.RemoveAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, targetRange.RangeId()))
	if err != nil {
		return fmt.Errorf("failed to remove range directory: %w", err)
	}

	// Delete all VM records referencing this range before deleting the range itself,
	// otherwise PocketBase will reject the deletion due to the required foreign key.
	_, err = e.App.DB().NewQuery("DELETE FROM vms WHERE range = {:range_id}").Bind(dbx.Params{"range_id": targetRange.Id}).Execute()
	if err != nil {
		return fmt.Errorf("failed to delete VM records for range: %w", err)
	}

	// Delete the range object from the database
	err = e.App.Delete(targetRange)
	if err != nil {
		return fmt.Errorf("failed to delete range from database: %w", err)
	}

	return nil
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

	// Get proxmox client and range object
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}

	// Update VM data to get accurate count. If the pool is already gone
	// (e.g. from a partially completed previous deletion), treat the range as
	// having zero VMs and proceed with cleanup.
	poolGone := false
	err = updateRangeVMData(e, targetRange, proxmoxClient)
	if err != nil {
		if strings.Contains(err.Error(), fmt.Sprintf("unable to get pool by ID: 500 pool '%s' does not exist", targetRange.RangeId())) {
			poolGone = true
		} else {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
	}

	if !poolGone && targetRange.NumberOfVms() > 0 && !force {
		return JSONError(e, http.StatusConflict, "Range has VMs. Use --force to delete anyway")
	}

	// Perform the actual deletion
	err = deleteRangeResources(targetRange, force, e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Range %s deleted", targetRange.RangeId()))
}

// DeleteRangeVMs - stops and deletes all range VMs (keeps range object in database)
func DeleteRangeVMs(e *core.RequestEvent) error {
	rangeIDToDelete := e.Request.PathValue("rangeID")
	rangeRecordRaw, err := app.FindFirstRecordByData("ranges", "rangeID", rangeIDToDelete)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	rangeRecord := &models.Range{}
	rangeRecord.SetProxyRecord(rangeRecordRaw)

	logger.Debug("DeleteRangeVMs for range ID: " + rangeRecord.RangeId())

	// Set range state to "DESTROYING"
	rangeRecord.SetRangeState(LudusRangeStateDestroying)
	err = e.App.Save(rangeRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Get the proxmox client
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	// Destroy the VMs for the range
	vms, err := getVMsForPool(e, ctx, rangeRecord.RangeId(), proxmoxClient)
	if err != nil {
		return fmt.Errorf("failed to get VMs for range: %w", err)
	}
	logger.Debug(fmt.Sprintf("Destroying %d VMs for range %s", len(vms), rangeRecord.RangeId()))
	for _, vm := range vms {
		logger.Debug(fmt.Sprintf("Destroying VM %d", int(vm.VMID)))
		err = destroyVM(ctx, proxmoxClient, int(vm.VMID))
		if err != nil {
			logger.Error(fmt.Sprintf("Error destroying VM %d: %s", int(vm.VMID), err.Error()))
		}
	}

	// The user is rm-ing their range with testing enabled, so after all VMs are destroyed exit testing
	if rangeRecord.TestingEnabled() {
		// Update the testing state in the DB as well as allowed domains and ips
		rangeRecord.SetTestingEnabled(false)
		rangeRecord.SetAllowedDomains([]string{})
		rangeRecord.SetAllowedIps([]string{})
		err = e.App.Save(rangeRecord)
		if err != nil {
			logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
		}
	}
	// Set range state to "DESTROYED"
	rangeRecord.SetRangeState(LudusRangeStateDestroyed)
	err = e.App.Save(rangeRecord)
	if err != nil {
		logger.Error(fmt.Sprintf("Error saving range: %s", err.Error()))
	}

	return JSONResult(e, http.StatusOK, "Range VMs Destroyed")
}

// GetConfig - retrieves the current configuration of the range
func GetConfig(e *core.RequestEvent) error {
	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}
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
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}
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
	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

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
		ThumbnailUrl:   rangeThumbnailURL(usersRange),
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
			CPU:         int32(vmRecord.Cpu()),
			RAM:         int32(vmRecord.Ram()),
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
			ThumbnailUrl:   rangeThumbnailURL(rangeRecord),
			TestingEnabled: rangeRecord.TestingEnabled(),
		}
		vmRecords, err := app.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": rangeRecord.Id}))
		if err != nil {
			logger.Error(fmt.Sprintf("Error finding VMs for range %s: %s", rangeRecord.RangeId(), err.Error()))
			continue
		}
		for _, vmRecord := range vmRecords {
			vmRecordObj := &models.VM{}
			vmRecordObj.SetProxyRecord(vmRecord)

			// Use rangeRecord from outer loop rather than expanding VM's range relation
			// for efficiency. We already queried VMs by range ID, so they must belong to this range.
			responseItem.VMs = append(responseItem.VMs, dto.ListAllRangeResponseItemVMsItem{
				Ip:          vmRecordObj.Ip(),
				IsRouter:    vmRecordObj.IsRouter(),
				ProxmoxID:   int32(vmRecordObj.ProxmoxId()),
				RangeNumber: int32(rangeRecord.RangeNumber()),
				Name:        vmRecordObj.Name(),
				PoweredOn:   vmRecordObj.PoweredOn(),
				CPU:         int32(vmRecordObj.Cpu()),
				RAM:         int32(vmRecordObj.Ram()),
			})
		}
		response = append(response, responseItem)
	}

	return e.JSON(http.StatusOK, response)
}

// PutConfig - updates the range config
func PutConfig(e *core.RequestEvent) error {
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}

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
	logger.Debug(fmt.Sprintf("Range has roles: %v", rangeHasRoles))
	if rangeHasRoles != nil && rangeHasRoles.(bool) {
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), "Resolving dependencies for user-defined roles..\n", false)
		rolesOutput, err := RunLocalAnsiblePlaybookOnTmpRangeConfig(e, []string{fmt.Sprintf("%s/ansible/range-management/user-defined-roles.yml", ludusInstallPath)})
		logToFile(fmt.Sprintf("%s/ranges/%s/ansible.log", ludusInstallPath, targetRange.RangeId()), rolesOutput, true)
		if err != nil {
			targetRange.SetRangeState(LudusRangeStateError)
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

	// Check if the new config would exceed quotas (warning only, don't block)
	if server.HasEntitlement("ENTERPRISE_PLUGIN") {
		user := e.Get("user").(*models.User)
		e.App.ExpandRecord(user.Record, []string{"ranges", "groups"}, nil)
		violations, quotaErr := CheckDeployQuota(e.App, user, targetRange.RangeId())
		if quotaErr != nil {
			logger.Debug(fmt.Sprintf("Quota check warning failed: %s", quotaErr.Error()))
		} else if len(violations) > 0 {
			return JSONResult(e, http.StatusOK, fmt.Sprintf("Your range config has been successfully updated.\nWarning: deploying this config would exceed your quota: %s\nYou may need to free resources from other ranges before deploying.", FormatQuotaViolations(violations)))
		}
	}

	// File saved successfully. Return proper result
	return JSONResult(e, http.StatusOK, "Your range config has been successfully updated.")
}

func GetAnsibleInventoryForRange(e *core.RequestEvent) error {
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}
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
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RANGE_ID=%s", targetRange.RangeId()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_RETURN_ALL_RANGES=%s", strconv.FormatBool(allRanges)))
	cmd.Env = append(cmd.Env, fmt.Sprintf("LUDUS_USER_IS_ADMIN=%s", strconv.FormatBool(targetUser.IsAdmin())))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible inventory: "+string(out))
	}
	return JSONResult(e, http.StatusOK, string(out))
}

func GetAnsibleTagsForDeployment(e *core.RequestEvent) error {
	// returns a comma-separated string of tags
	cmd := fmt.Sprintf("grep 'tags:' %s/ansible/range-management/ludus.yml | cut -d ':' -f 2 | tr -d '[]' | tr ',' '\\n' | egrep -v 'always|never' | sort -u | paste -sd ',' -", ludusInstallPath)
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible tags: "+err.Error())
	}

	// Parse comma-separated string into array
	tagsString := strings.TrimSpace(string(out))
	var tags []string
	if tagsString != "" {
		tagList := strings.Split(tagsString, ",")
		for _, tag := range tagList {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}

	// Return structured JSON response
	response := dto.ListRangeTagsResponse{Tags: tags}
	return e.JSON(http.StatusOK, response)
}

// Find the ansible process for this user and kill it
func AbortAnsible(e *core.RequestEvent) error {
	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}
	targetUser := e.Get("user").(*models.User)

	ansiblePidString, err := findAnsiblePidForUser(targetUser.ProxmoxUsername())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	ansiblePid, err := strconv.Atoi(ansiblePidString)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	commandmanager.KillProcessAndChildren(ansiblePid)

	// Set range state to "ABORTED"
	targetRange.SetRangeState(LudusRangeStateAborted)
	err = e.App.Save(targetRange)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, "Ansible process aborted")
}

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

	// If UserID array is provided, verify the users exist
	for _, userID := range payload.UserID {
		_, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil {
			return e.JSON(http.StatusInternalServerError, dto.CreateRangeResponseError{Errors: []dto.CreateRangeResponseErrorItem{{UserID: userID, Error: fmt.Sprintf("Error finding user: %v", err)}}})
		}
	}

	// Check range quota for each user being assigned
	if server.HasEntitlement("ENTERPRISE_PLUGIN") {
		for _, uid := range payload.UserID {
			userRecord, err := e.App.FindFirstRecordByData("users", "userID", uid)
			if err != nil {
				continue // Already validated above
			}
			userObj := &models.User{}
			userObj.SetProxyRecord(userRecord)
			e.App.ExpandRecord(userObj.Record, []string{"ranges", "groups"}, nil)

			rangeViolations, quotaErr := CheckRangeQuota(e.App, userObj)
			if quotaErr != nil {
				logger.Error(fmt.Sprintf("Error checking range quota for user %s: %s", uid, quotaErr.Error()))
			} else if len(rangeViolations) > 0 {
				return JSONError(e, http.StatusForbidden, fmt.Sprintf("User %s: %s", uid, FormatQuotaViolations(rangeViolations)))
			}
		}
	}

	if payload.RangeID == "" {
		return JSONError(e, http.StatusBadRequest, "Range ID is required")
	}

	// Validate the range ID is a valid proxmox pool name using regex
	proxmoxPoolNameRegex := regexp.MustCompile(ProxmoxPoolNameRegexString)
	if !proxmoxPoolNameRegex.MatchString(payload.RangeID) {
		return JSONError(e, http.StatusConflict, "Range ID name must be a valid proxmox pool name (e.g. 'DEMO', 'my-range' or 'New_Range'). Use only letters, numbers, hyphens, and underscores. Must start with a letter.")
	}

	// Check if range ID is already in use in the database as early as possible.
	existingRange, err := e.App.FindFirstRecordByData("ranges", "rangeID", payload.RangeID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Error checking if range ID %s is already in use: %v", payload.RangeID, err))
	}
	if existingRange != nil {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Range ID %s already in use", payload.RangeID))
	}

	// Determine range number and validate collisions early.
	var rangeNumber int
	if payload.RangeNumber > 0 {
		// Make sure the range number is not a reserved range number
		if slices.Contains(ServerConfiguration.ReservedRangeNumbers, int32(payload.RangeNumber)) {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Range number %d is a reserved range number. Edit the reserved_range_numbers array in /opt/ludus/config.yml to use this range number.", payload.RangeNumber))
		}

		// Check if range number is already in use
		rangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeNumber", payload.RangeNumber)
		if err != nil && err != sql.ErrNoRows {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Error checking if range number %d is already in use: %v", payload.RangeNumber, err))
		}
		if rangeRecord != nil {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Range number %d already in use", payload.RangeNumber))
		}
		rangeNumber = int(payload.RangeNumber)
	} else {
		// Find next available range number
		rangeNumber = findNextAvailableRangeNumber(app)
	}

	if poolExists(payload.RangeID) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("Pool with the name %s already exists", payload.RangeID))
	}

	// Create a new resource pool for the range
	err = createPool(payload.RangeID)
	if err != nil {
		return JSONError(e, http.StatusConflict, "Unable to create resource pool: "+err.Error())
	}

	// Create the network interface for the range (SDN VNet or legacy vmbr)
	err = manageRangeNetwork(payload.RangeID, rangeNumber, true)
	if err != nil {
		removePool(payload.RangeID)
		return JSONError(e, http.StatusConflict, "Unable to create range network: "+err.Error())
	}

	// Create the range config file
	os.MkdirAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, payload.RangeID), 0755)
	copyFileContents(fmt.Sprintf("%s/ansible/user-files/range-config.example.yml", ludusInstallPath), fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, payload.RangeID))
	chownDirToUsernameRecursive(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, payload.RangeID), "ludus")

	// Create the range
	rangeCollection, err := e.App.FindCollectionByNameOrId("ranges")
	if err != nil {
		return JSONError(e, http.StatusConflict, "Unable to find ranges collection: "+err.Error())
	}
	rawRangeRecord := core.NewRecord(rangeCollection)
	rangeRecord := &models.Ranges{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	rangeRecord.SetName(payload.Name)
	rangeRecord.SetRangeId(payload.RangeID)
	rangeRecord.SetDescription(payload.Description)
	rangeRecord.SetPurpose(payload.Purpose)
	rangeRecord.SetRangeNumber(rangeNumber)
	rangeRecord.SetNumberOfVms(0)
	rangeRecord.SetRangeState(LudusRangeStateNeverDeployed)

	err = e.App.Save(rangeRecord)
	if err != nil {
		removePool(payload.RangeID)
		manageRangeNetwork(payload.RangeID, rangeNumber, false)
		os.RemoveAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, payload.RangeID))
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return JSONError(e, http.StatusConflict, fmt.Sprintf("Range ID %s or range number %d is already in use", payload.RangeID, rangeNumber))
		}
		return JSONError(e, http.StatusConflict, "Unable to save range: "+err.Error())
	}

	// Always give access to the ludus_admins group
	err = grantGroupAccessToRangeInProxmox("ludus_admins", payload.RangeID, rangeNumber)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to give group access to pool: "+err.Error())
	}

	// If UserID was provided, create direct access record
	errorArray := []dto.CreateRangeResponseErrorItem{}
	for _, userID := range payload.UserID {
		userRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
		if err != nil {
			errorArray = append(errorArray, dto.CreateRangeResponseErrorItem{UserID: userID, Error: fmt.Sprintf("Error finding user: %v", err)})
		}
		userRecord.Set("ranges+", rangeRecord.Id)
		err = e.App.Save(userRecord)
		if err != nil {
			errorArray = append(errorArray, dto.CreateRangeResponseErrorItem{UserID: userID, Error: fmt.Sprintf("Unable to save user: %v", err)})
		}
		// Give the user in proxmox permissions to the pool
		err = giveUserAccessToRange(userRecord.GetString("proxmoxUsername"), userRecord.GetString("proxmoxRealm"), payload.RangeID, rangeNumber)
		if err != nil {
			errorArray = append(errorArray, dto.CreateRangeResponseErrorItem{UserID: userID, Error: fmt.Sprintf("Unable to give user access to pool: %v", err)})
		}
	}

	if len(errorArray) > 0 {
		return e.JSON(http.StatusInternalServerError, dto.CreateRangeResponseError{Errors: errorArray})
	}

	return JSONResult(e, http.StatusCreated, fmt.Sprintf("Range %s created successfully", payload.RangeID))
}

func AssignOrRevokeRangeAccess(e *core.RequestEvent, actionVerb string, force bool) error {
	actingUser := e.Get("user").(*models.User)
	if !actingUser.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "You must be an admin to use this endpoint")
	}

	rangeID := e.Request.PathValue("rangeID")
	rangeNumber, err := GetRangeNumberFromRangeID(rangeID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found: %v", rangeID, err))
	}

	userID := e.Request.PathValue("userID")
	if userID == "" {
		return JSONError(e, http.StatusBadRequest, "User ID is required")
	}

	// Check if user already has access to this range
	if actionVerb == "grant" && HasRangeAccess(e, userID, rangeNumber) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("User %s already has access to range %s", userID, rangeID))
	}

	if actionVerb == "revoke" && !HasRangeAccess(e, userID, rangeNumber) {
		return JSONError(e, http.StatusConflict, fmt.Sprintf("User %s does not have access to range %s", userID, rangeID))
	}

	// Get the target range object
	targetRange, err := GetRangeObjectByNumber(rangeNumber)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting range object: %v", err))
	}

	// Get the source user object by looking up the user ID in the database
	sourceUserRecord, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil && err == sql.ErrNoRows {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User %s not found", userID))
	}
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting user object: %v", err))
	}
	sourceUserObject := &models.User{}
	sourceUserObject.SetProxyRecord(sourceUserRecord)

	if actionVerb == "grant" || actionVerb == "assign" {

		sourceUserObject.Set("ranges+", targetRange.Id)
		err = e.App.Save(sourceUserObject)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+err.Error())
		}

		err := RunAccessControlPlaybook(e, targetRange)
		if err != nil {
			sourceUserObject.Set("ranges-", targetRange.Id)
			e.App.Save(sourceUserObject)
			if errors.Is(err, ErrRangeRouterPoweredOff) {
				return JSONError(e, http.StatusConflict, err.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Error running access control playbook: "+err.Error())
		}

		// Give the user access to the proxmox pool for the range
		err = giveUserAccessToRange(sourceUserObject.ProxmoxUsername(), sourceUserObject.ProxmoxRealm(), targetRange.RangeId(), rangeNumber)
		if err != nil {
			sourceUserObject.Set("ranges-", targetRange.Id)
			e.App.Save(sourceUserObject)
			return JSONError(e, http.StatusInternalServerError, "Unable to give user access to pool: "+err.Error())
		}

		return JSONResult(e, http.StatusCreated, fmt.Sprintf("Range %s assigned to user %s successfully", rangeID, userID))

	} else if actionVerb == "revoke" {

		sourceUserObject.Set("ranges-", targetRange.Id)
		err = e.App.Save(sourceUserObject)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to save user: "+err.Error())
		}

		err := RunAccessControlPlaybook(e, targetRange)
		if err != nil {
			sourceUserObject.Set("ranges+", targetRange.Id)
			e.App.Save(sourceUserObject)
			if errors.Is(err, ErrRangeRouterPoweredOff) {
				return JSONError(e, http.StatusConflict, err.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Error running access control playbook: "+err.Error())
		}

		err = removeUserAccessFromRange(sourceUserObject.ProxmoxUsername(), sourceUserObject.ProxmoxRealm(), targetRange.RangeId(), rangeNumber)
		if err != nil {
			sourceUserObject.Set("ranges+", targetRange.Id)
			e.App.Save(sourceUserObject)
			return JSONError(e, http.StatusInternalServerError, "Unable to remove user access from pool: "+err.Error())
		}

		return JSONResult(e, http.StatusOK, fmt.Sprintf("Range %s access revoked from user %s successfully", rangeID, userID))
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
	userHasAccess := HasRangeAccess(e, user.UserId(), rangeNumber)
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

	result, err := GetAccessibleRangesForUser(user)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting accessible ranges: %v", err))
	}

	return e.JSON(http.StatusOK, result)
}
