package ludusapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Ansible Item represents an Ansible role or collection with its version and type
type AnsibleItem struct {
	Name    string
	Version string
	Type    string
}

// GetRolesAndCollections - retrieves the available Ansible roles and collections for the user
func GetRolesAndCollections(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	cmd := exec.Command("ansible-galaxy", "role", "list") // no --format json for roles...
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	roleOutput, err := cmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to get the ansible roles: " + err.Error()})
		return
	}

	// Create a scanner to read the input
	scanner := bufio.NewScanner(bytes.NewReader(roleOutput))

	// Slice to store the roles
	var ansibleItems []AnsibleItem

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
		roleVersion := strings.TrimSpace(parts[1])

		// Append to slice
		ansibleItems = append(ansibleItems, AnsibleItem{
			Name:    roleName,
			Version: roleVersion,
			Type:    "role",
		})
	}

	// Check for errors during scanning
	if err := scanner.Err(); err != nil {
		fmt.Println("Error reading input:", err)
	}

	// Collections
	collectionCmd := exec.Command("ansible-galaxy", "collection", "list", "--format", "json")
	collectionCmd.Env = os.Environ()
	collectionCmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	collectionOutput, err := collectionCmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to get the ansible collections: " + err.Error()})
		return
	}

	// Unmarshal the JSON into a suitable Go data structure
	var data map[string]map[string]map[string]string
	err = json.Unmarshal(collectionOutput, &data)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to get parse ansible collections JSON: " + err.Error()})
		return
	}

	// Iterate through the data
	for path, modules := range data {
		if strings.Contains(path, ".ansible") {
			for name, module := range modules {
				ansibleModule := AnsibleItem{
					Name:    name,
					Version: module["version"],
					Type:    "collection",
				}
				ansibleItems = append(ansibleItems, ansibleModule)
			}
		}
	}

	c.JSON(http.StatusOK, ansibleItems)
}

// ActionRoleFromInternet - installs an ansible role from ansible galaxy or publicly available source control
func ActionRoleFromInternet(c *gin.Context) {
	type RoleBody struct {
		Role    string `json:"role"`
		Version string `json:"version"`
		Force   bool   `json:"force"`
		Action  string `json:"action"`
	}
	var roleBody RoleBody
	c.Bind(&roleBody)

	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	var roleString = roleBody.Role
	if roleBody.Version != "" {
		roleString = fmt.Sprintf("%s,%s", roleBody.Role, roleBody.Version)
	}

	if roleBody.Action != "install" && roleBody.Action != "remove" {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "action must be one of 'install' or 'remove'"})
		return
	}

	var cmd *exec.Cmd
	if roleBody.Force {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Unable to %s the ansible role %s: %s; Output was: %s", roleBody.Action, roleString, err.Error(), string(cmdOutput))})
		return
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": string(cmdOutput)})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"result": "Successfully installed: " + roleString})

}

// InstallRoleFromTar - installs an ansible role from a user uploaded tar file
func InstallRoleFromTar(c *gin.Context) {
	// Parse the multipart form
	if err := c.Request.ParseMultipartForm(1073741824); err != nil { // allow 1GB
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Retrieve the 'force' field and convert it to boolean
	forceStr := c.Request.FormValue("force")
	force, err := strconv.ParseBool(forceStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid boolean value"})
		return
	}

	// Retrieve the file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File retrieval failed"})
		return
	}

	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	// Save the file to the server
	roleTarPath := fmt.Sprintf("%s/users/%s/.ansible/tmp/%s", ludusInstallPath, user.ProxmoxUsername, file.Filename)
	err = c.SaveUploadedFile(file, roleTarPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Saving file failed"})
		return
	}
	defer os.Remove(roleTarPath)

	var cmd *exec.Cmd
	if force {
		cmd = exec.Command("ansible-galaxy", "role", "install", file.Filename, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", "role", "install", file.Filename)
	}
	cmd.Dir = fmt.Sprintf("%s/users/%s/.ansible/tmp", ludusInstallPath, user.ProxmoxUsername) // If you try to install a tar'd role with the full path, it will fail to extract. Bug in ansible-galaxy?
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Unable to install the ansible role %s: %s; Output was: %s", roleTarPath, err.Error(), string(cmdOutput))})
		return
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": string(cmdOutput)})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"result": "Successfully installed role"})

}

// ActionCollectionFromInternet - installs an ansible collection from ansible galaxy or publicly available source control
func ActionCollectionFromInternet(c *gin.Context) {
	type CollectionBody struct {
		Collection string `json:"collection"`
		Version    string `json:"version"`
		Force      bool   `json:"force"`
		Action     string `json:"action"`
	}
	var collectionBody CollectionBody
	c.Bind(&collectionBody)

	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	var collectionString = collectionBody.Collection
	if collectionBody.Version != "" {
		collectionString = fmt.Sprintf("%s,%s", collectionBody.Collection, collectionBody.Version)
	}

	if collectionBody.Action != "install" && collectionBody.Action != "remove" {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "action must be one of 'install' or 'remove'"})
		return
	}

	var cmd *exec.Cmd
	if collectionBody.Force {
		cmd = exec.Command("ansible-galaxy", "collection", collectionBody.Action, collectionString, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", "collection", collectionBody.Action, collectionString)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Unable to %s the ansible collection %s: %s; Output was: %s", collectionBody.Action, collectionString, err.Error(), string(cmdOutput))})
		return
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": string(cmdOutput)})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"result": "Successfully installed: " + collectionString})

}
