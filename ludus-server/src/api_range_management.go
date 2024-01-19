package ludusapi

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// DeployRange - deploys the range according to the range config
func DeployRange(c *gin.Context) {

	type DeployBody struct {
		Tags    string `json:"tags"`
		Force   bool   `json:"force"`
		Verbose bool   `json:"verbose"`
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
	if usersRange.RangeState == "DEPLOYING" {
		c.JSON(http.StatusConflict, gin.H{"error": "The range has an active deployment running"})
		return
	}

	if usersRange.TestingEnabled && !deployBody.Force {
		c.JSON(http.StatusConflict, gin.H{"error": "Testing enabled; deploy requires internet access to succeed; run with --force if you really want to try a deploy with testing enabled"})
		return
	}

	// Set range state to "DEPLOYING"
	db.Model(&usersRange).Update("range_state", "DEPLOYING")

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go RunRangeManagementAnsibleWithTag(c, tags, deployBody.Verbose)

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

	// This can take a long time, so run as a go routine and have the user check the status via another endpoint
	go func() {
		_, err := RunPlaybookWithTag(c, "power.yml", "stop-range", false)
		if err != nil {
			user, _ := getUserObject(c)
			writeStringToFile(fmt.Sprintf("%s/users/%s/ansible-debug.log", ludusInstallPath, user.ProxmoxUsername), err.Error())
			return // Don't attempt to destroy if there is a power off error
		}
		_, err = RunPlaybookWithTag(c, "power.yml", "destroy-range", false)
		if err != nil {
			user, _ := getUserObject(c)
			writeStringToFile(fmt.Sprintf("%s/users/%s/ansible-debug.log", ludusInstallPath, user.ProxmoxUsername), err.Error())
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

	}()

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
	output, err := RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "generate-rdp", false)
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
	if !isAdmin(c) {
		return
	}

	var ranges []RangeObject
	result := db.Find(&ranges)
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

	if usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "Testing is enabled; to prevent conflicts, the config cannot be updated while testing is enabled"})
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
