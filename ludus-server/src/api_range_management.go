package ludusapi

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// DeployRange - deploys the range according to the range config
func DeployRange(c *gin.Context) {

	type DeployBody struct {
		Tags      string   `json:"tags"`
		Force     bool     `json:"force"`
		Verbose   bool     `json:"verbose"`
		OnlyRoles []string `json:"only_roles"`
		Limit     string   `json:"limit"`
	}
	var deployBody DeployBody
	c.Bind(&deployBody)

	var tags string
	if deployBody.Tags == "" {
		// By default run "all" as the ansible tag
		tags = "all"
	} else {
		tags = deployBody.Tags
	}

	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	// Make sure we aren't already in a "DEPLOYING" state
	if usersRange.RangeState == "DEPLOYING" && !deployBody.Force {
		c.JSON(http.StatusConflict, gin.H{"error": "The range has an active deployment running. Try `range abort` to stop the deployment or run with --force if you really want to try a deploy"})
		return
	}

	if usersRange.TestingEnabled && !deployBody.Force {
		c.JSON(http.StatusConflict, gin.H{"error": "Testing enabled; deploy requires internet access to succeed; run with --force if you really want to try a deploy with testing enabled"})
		return
	}

	// If the user specified roles, make sure they exist on the server before trying to use them
	if len(deployBody.OnlyRoles) > 0 {
		for _, role := range deployBody.OnlyRoles {
			if role != "" { // Ignore empty strings
				exists, err := checkRoleExists(c, role)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				if !exists {
					c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("The role '%s' does not exist on the Ludus server for user %s", role, usersRange.UserID)})
					return
				}
			}
		}
	}

	// Set range state to "DEPLOYING"
	db.Model(&usersRange).Update("range_state", "DEPLOYING")

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go RunRangeManagementAnsibleWithTag(c, tags, deployBody.Verbose, deployBody.OnlyRoles, deployBody.Limit)

	// Update the deployment time in the DB
	db.Model(&usersRange).Update("last_deployment", time.Now())

	c.JSON(http.StatusOK, gin.H{"result": "Range deploy started"})
}

// DeleteRange - stops and deletes all range VMs
func DeleteRange(c *gin.Context) {
	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	// Set range state to "DESTROYING"
	db.Model(&usersRange).Update("range_state", "DESTROYING")

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go func(c *gin.Context) {
		_, err = RunPlaybookWithTag(c, "power.yml", "destroy-range", false)
		if err != nil {
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "Error with destroy-range\n")
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), fmt.Sprintf("%v\n", c))
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), fmt.Sprintf("%s\n", err.Error()))
			writeStringToFile(fmt.Sprintf("%s/users/ansible-debug.log", ludusInstallPath), "==================\n")
			db.Model(&usersRange).Update("range_state", "ERROR")
			return // Don't reset testing if destroy fails
		}
		// The user is rm-ing their range with testing enabled, so after all VMs are destroyed exit testing
		if usersRange.TestingEnabled {
			// Update the testing state in the DB as well as allowed domains and ips
			usersRange.TestingEnabled = false
			usersRange.AllowedDomains = []string{}
			usersRange.AllowedIPs = []string{}
			db.Save(&usersRange)
		}
		// Set range state to "DESTROYED"
		db.Model(&usersRange).Update("range_state", "DESTROYED")
	}(c)

	c.JSON(http.StatusOK, gin.H{"result": "Range destroy in progress"})
}

// GetConfig - retrieves the current configuration of the range
func GetConfig(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	rangeConfig, err := GetFileContents(fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, user.ProxmoxUsername))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": rangeConfig})
}

// GetConfigExample - retrieves an example range configuration
func GetConfigExample(c *gin.Context) {
	rangeConfig, err := GetFileContents(ludusInstallPath + "/ansible/user-files/range-config.example.yml")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": rangeConfig})
}

// GetEtcHosts - retrieves an /etc/hosts file for the range
func GetEtcHosts(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	etcHosts, err := GetFileContents(fmt.Sprintf("%s/users/%s/etc-hosts", ludusInstallPath, user.ProxmoxUsername))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": etcHosts})
}

// GetRDP - retrieves RDP files as a zip for the range
func GetRDP(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	playbook := []string{ludusInstallPath + "/ansible/range-management/ludus.yml"}
	extraVars := map[string]interface{}{
		"username": user.ProxmoxUsername,
	}
	output, err := RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "generate-rdp", false, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}

	filePath := fmt.Sprintf("%s/users/%s/rdp.zip", ludusInstallPath, user.ProxmoxUsername)
	c.Header("Content-Disposition", "attachment; filename=rdp.zip")
	c.Header("Content-Type", "application/zip")
	// Serve the file
	c.File(filePath)
}

// GetLogs - retrieves the latest range logs
func GetLogs(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	ansibleLogPath := fmt.Sprintf("%s/users/%s/ansible.log", ludusInstallPath, user.ProxmoxUsername)
	GetLogsFromFile(c, ansibleLogPath)
}

// GetSSHConfig - retrieves a ssh config file for the range
func GetSSHConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"result": "Not implemented"})
}

// ListRange - lists range VMs, their power state, and their testing state
func ListRange(c *gin.Context) {
	err := updateUsersRangeVMData(c)
	if err != nil {
		return // JSON error set in updateUsersRangeVMData
	}
	// Get the updated range
	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON error is set in getRangeObject
	}
	var allVMs []VmObject
	db.Where("range_number = ?", usersRange.RangeNumber).Find(&allVMs)
	// the range we got back from getRangeObject is a cached object from the first lookup
	// so update the values we care about for this ListRange call and return the updated
	// object to the user
	usersRange.VMs = allVMs
	usersRange.NumberOfVMs = int32(len(allVMs))
	c.JSON(http.StatusOK, usersRange)
}

func ListAllRanges(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	// Make sure the range table is up to date by looping over all ranges and updating them
	var usersRanges []RangeObject
	result := db.Find(&usersRanges)
	if result.Error != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, result.Error)
		return
	}

	// The calling user is an admin can can see all VMs
	proxmoxClient, err := getProxmoxClientForUser(c)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Unable to get proxmox client for user: %s", err.Error())})
		return
	}

	rawVMs, err := proxmoxClient.GetVmList()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Unable to list VMs: %s", err.Error())})
		return
	}

	// Loop over all users and update their range data
	for _, rangeObject := range usersRanges {
		// Update the VM data for this range
		var rangeVMCount = 0
		vms := rawVMs["data"].([]interface{})
		for vmCounter := range vms {
			vm := vms[vmCounter].(map[string]interface{})
			// Skip shared templates
			if vm["pool"] == nil || vm["name"] == nil || vm["template"] == nil {
				continue // A vm with these values as nil will cause the conversions to panic
			}
			if vm["pool"].(string) != rangeObject.UserID ||
				strings.HasSuffix(vm["name"].(string), "-template") ||
				int(vm["template"].(float64)) == 1 {
				continue
			}
			rangeVMCount += 1
		}
		db.Model(&rangeObject).Update("number_of_vms", rangeVMCount)
	}

	var ranges []RangeObject
	result = db.Find(&ranges)
	if result.Error != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, result.Error)
	}
	c.JSON(http.StatusOK, ranges)
}

// PutConfig - updates the range config
func PutConfig(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	// Retrieve the 'force' field and convert it to boolean
	var force = false
	forceStr := c.Request.FormValue("force")
	force, err = strconv.ParseBool(forceStr)
	if forceStr != "" && err != nil { // Empty string (unset) => force is false
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid boolean value"})
		return
	}

	if usersRange.TestingEnabled && !force {
		c.JSON(http.StatusConflict, gin.H{"error": "Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled. Use --force to override."})
		return
	}

	file, err := c.FormFile("file")

	// The file cannot be received.
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "No file is received"})
		return
	}

	// The file is received, so let's save it
	filePath := fmt.Sprintf("%s/users/%s/.tmp-range-config.yml", ludusInstallPath, user.ProxmoxUsername)
	err = c.SaveUploadedFile(file, filePath)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to save the range config"})
		return
	}

	// Validate the uploaded range-config - return an error if it isn't valid
	err = validateFile(c, filePath, ludusInstallPath+"/ansible/user-files/range-config.jsonschema")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Configuration error: " + err.Error()})
		return
	}

	// The file is valid, so let's move it to the range-config
	err = os.Rename(filePath, fmt.Sprintf("%s/users/%s/range-config.yml", ludusInstallPath, user.ProxmoxUsername))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to save the range config"})
		return
	}

	// File saved successfully. Return proper result
	c.JSON(http.StatusOK, gin.H{"result": "Your range config has been successfully updated."})
}

func GetAnsibleInventoryForRange(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return // JSON set in getProxmoxPasswordForUser
	}

	cmd := exec.Command("ansible-inventory", "-i", ludusInstallPath+"/ansible/range-management/proxmox.py", "--list", "-y")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_NODE=%s", ServerConfiguration.ProxmoxNode))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_INVALID_CERT=%s", strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert)))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_URL=%s", ServerConfiguration.ProxmoxURL))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_HOSTNAME=%s", ServerConfiguration.ProxmoxHostname))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_USERNAME=%s", user.ProxmoxUsername+"@pam"))
	cmd.Env = append(cmd.Env, fmt.Sprintf("PROXMOX_PASSWORD=%s", proxmoxPassword))
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to get the ansible inventory: " + string(out)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": string(out)})
}

func GetAnsibleTagsForDeployment(c *gin.Context) {
	cmd := fmt.Sprintf("grep 'tags:' %s/ansible/range-management/ludus.yml | cut -d ':' -f 2 | tr -d '[]' | tr ',' '\\n' | egrep -v 'always|never' | sort -u | paste -sd ',' -", ludusInstallPath)
	out, err := exec.Command("bash", "-c", cmd).Output()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to get the ansible tags: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": string(out)})
}

// Find the ansible process for this user and kill it
func AbortAnsible(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	ansiblePid, err := findAnsiblePidForUser(user.ProxmoxUsername)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	killProcessAndChildren(ansiblePid)

	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	// Set range state to "ABORTED"
	db.Model(&usersRange).Update("range_state", "ABORTED")

	c.JSON(http.StatusOK, gin.H{"result": "Ansible process aborted"})
}

// Grant or revoke access to a range - admin only endpoint
func RangeAccessAction(c *gin.Context) {

	type RangeAccessActionPayload struct {
		AccessActionVerb string `json:"action"`
		TargetUserID     string `json:"targetUserID"`
		SourceUserID     string `json:"sourceUserID"`
		Force            bool   `json:"force"`
	}
	var thisRangeAccessActionPayload RangeAccessActionPayload

	err := c.BindJSON(&thisRangeAccessActionPayload)
	if err != nil {
		c.JSON(http.StatusNoContent, gin.H{"error": "Improperly formatted range access payload"})
		return
	}

	if !isAdmin(c, true) {
		return
	}

	if thisRangeAccessActionPayload.AccessActionVerb != "grant" && thisRangeAccessActionPayload.AccessActionVerb != "revoke" {
		c.JSON(http.StatusNoContent, gin.H{"error": "Only 'grant' and 'revoke' are supported as actions"})
		return
	}

	var targetUserObject UserObject
	targetResult := db.First(&targetUserObject, "user_id = ?", thisRangeAccessActionPayload.TargetUserID)
	if errors.Is(targetResult.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s user not found", thisRangeAccessActionPayload.TargetUserID)})
		return
	}

	var sourceUserObject UserObject
	sourceResult := db.First(&sourceUserObject, "user_id = ?", thisRangeAccessActionPayload.SourceUserID)
	if errors.Is(sourceResult.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s user not found", thisRangeAccessActionPayload.SourceUserID)})
		return
	}

	// Check if this is a revoke for access that doesn't exist
	var rangeAccessObject RangeAccessObject
	noRangeAccessResultFound := false
	rangeAccessResult := db.First(&rangeAccessObject, "target_user_id = ?", thisRangeAccessActionPayload.TargetUserID)
	if errors.Is(rangeAccessResult.Error, gorm.ErrRecordNotFound) {
		noRangeAccessResultFound = true
	}
	if noRangeAccessResultFound && thisRangeAccessActionPayload.AccessActionVerb == "revoke" {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s has no existing access grants", thisRangeAccessActionPayload.TargetUserID)})
		return
	}
	if thisRangeAccessActionPayload.AccessActionVerb == "revoke" && !slices.Contains(rangeAccessObject.SourceUserIDs, thisRangeAccessActionPayload.SourceUserID) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s does not have access to %s", thisRangeAccessActionPayload.SourceUserID, thisRangeAccessActionPayload.TargetUserID)})
		return
	}
	// Check if the user already has access
	if thisRangeAccessActionPayload.AccessActionVerb == "grant" && slices.Contains(rangeAccessObject.SourceUserIDs, thisRangeAccessActionPayload.SourceUserID) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("%s already has access to %s", thisRangeAccessActionPayload.SourceUserID, thisRangeAccessActionPayload.TargetUserID)})
		return
	}

	var targetUserRangeObject RangeObject
	db.First(&targetUserRangeObject, "user_id = ?", thisRangeAccessActionPayload.TargetUserID)

	var sourceUserRangeObject RangeObject
	db.First(&sourceUserRangeObject, "user_id = ?", thisRangeAccessActionPayload.SourceUserID)

	extraVars := map[string]interface{}{
		"target_username":           targetUserObject.ProxmoxUsername,
		"target_range_id":           targetUserObject.UserID,
		"target_range_second_octet": targetUserRangeObject.RangeNumber,
		"source_username":           sourceUserObject.ProxmoxUsername,
		"source_range_id":           sourceUserObject.UserID,
		"source_range_second_octet": sourceUserRangeObject.RangeNumber,
	}
	output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/range-access.yml"}, nil, extraVars, thisRangeAccessActionPayload.AccessActionVerb, false, "")
	if err != nil {
		routerWANFatalRegex := regexp.MustCompile(`fatal:.*?192\.0\.2\\"`)
		if strings.Contains(output, "Target router is not up") && thisRangeAccessActionPayload.AccessActionVerb == "grant" {
			c.JSON(http.StatusOK, gin.H{"result": `WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!
The target range router VM is inaccessible!
If the target range router is deployed, no firewall changes have taken place.
If the VM is not deployed yet, the rule will be added when it is deployed and you can ignore this.
WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING! WARNING!`})
		} else if routerWANFatalRegex.MatchString(output) && !thisRangeAccessActionPayload.Force {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "The target range router is inaccessible. To revoke access, the target range router must be up and accessible. Use --force to override this protection."})
			return
		} else if routerWANFatalRegex.MatchString(output) && thisRangeAccessActionPayload.Force {
			// pass
		} else { // Some other error we want to fail on
			c.JSON(http.StatusInternalServerError, gin.H{"error": output})
			return
		}
	}

	if thisRangeAccessActionPayload.AccessActionVerb == "grant" {
		// If this is the first grant, create a record
		if noRangeAccessResultFound {
			rangeAccessObject.TargetUserID = targetUserObject.UserID
			rangeAccessObject.SourceUserIDs = []string{sourceUserObject.UserID}
			db.Create(&rangeAccessObject)
		} else {
			// Not the first grant for this range, update the record
			rangeAccessObject.SourceUserIDs = append(rangeAccessObject.SourceUserIDs, sourceUserObject.UserID)
			db.Save(&rangeAccessObject)
		}
		// Response may have been set by 'target router not up' warning
		if !c.Writer.Written() {
			c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Range access to %s's range granted to %s. Have %s pull an updated wireguard config.",
				targetUserObject.ProxmoxUsername,
				sourceUserObject.ProxmoxUsername,
				sourceUserObject.ProxmoxUsername)})
		}
	} else {
		rangeAccessObject.SourceUserIDs = removeStringExact(rangeAccessObject.SourceUserIDs, sourceUserObject.UserID)
		db.Save(&rangeAccessObject)

		// If we have removed the last access, leaving the entry empty, remove the record from the DB to prevent confusion
		// with empty records
		if len(rangeAccessObject.SourceUserIDs) == 0 {
			db.Delete(&rangeAccessObject)
		}
		c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Range access to %s's range revoked from %s.",
			targetUserObject.ProxmoxUsername,
			sourceUserObject.ProxmoxUsername)})
	}
}

// Return the current state of range access grants - admin only endpoint
func RangeAccessList(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}
	var rangeAccessObjects []RangeAccessObject
	result := db.Find(&rangeAccessObjects)
	if result.Error != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, result.Error)
	}
	c.JSON(http.StatusOK, rangeAccessObjects)
}
