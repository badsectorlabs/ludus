package ludusapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/pocketbase/pocketbase/core"
	yaml "sigs.k8s.io/yaml"
)

// Ansible Item represents an Ansible role or collection with its version and type
type AnsibleItem struct {
	Name    string
	Version string
	Type    string
	Global  bool
}

var coreAnsibleRoles = []string{"lae.proxmox", "geerlingguy.packer", "ansible-thoteam.nexus3-oss"}

// GetRolesAndCollections - retrieves the available Ansible roles and collections for the user
func GetRolesAndCollections(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	cmd := exec.Command("ansible-galaxy", "role", "list") // no --format json for roles...
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_ROLES_PATH=%s/users/%s/.ansible/roles:%s/resources/global-roles", ludusInstallPath, user.ProxmoxUsername(), ludusInstallPath))

	roleOutput, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible roles: "+err.Error()+"; Output was: "+string(roleOutput))
	}

	// Create a scanner to read the input
	scanner := bufio.NewScanner(bytes.NewReader(roleOutput))

	// Slice to store the roles
	var ansibleItems []AnsibleItem
	// bool to store if we are a user or global role
	isGlobalRole := false

	// Process each line
	for scanner.Scan() {
		line := scanner.Text()

		// Skip non-role lines
		if !strings.HasPrefix(line, "- ") {
			if strings.Contains(line, fmt.Sprintf("# %s/resources/global-roles", ludusInstallPath)) {
				isGlobalRole = true
			}
			continue
		}

		// Split the line into role name and version
		parts := strings.SplitN(line[2:], ", ", 2)
		if len(parts) != 2 {
			logger.Error("Invalid line format: " + line)
			continue
		}

		roleName := strings.TrimSpace(parts[0])
		roleVersion := strings.TrimSpace(parts[1])

		// Append to slice
		ansibleItems = append(ansibleItems, AnsibleItem{
			Name:    roleName,
			Version: roleVersion,
			Type:    "role",
			Global:  isGlobalRole,
		})
	}

	// Check for errors during scanning
	if err := scanner.Err(); err != nil {
		logger.Error("Error reading input: " + err.Error())
	}

	// Collections
	collectionCmd := exec.Command("ansible-galaxy", "collection", "list", "--format", "json")
	collectionCmd.Env = os.Environ()
	collectionCmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	collectionOutput, err := collectionCmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get the ansible collections: "+err.Error())
	}

	// Unmarshal the JSON into a suitable Go data structure
	var data map[string]map[string]map[string]string
	err = json.Unmarshal(collectionOutput, &data)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get parse ansible collections JSON: "+err.Error())
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

	var ansibleResponseItems []dto.GetRolesAndCollectionsResponseItem
	for _, ansibleItem := range ansibleItems {
		ansibleResponseItems = append(ansibleResponseItems, dto.GetRolesAndCollectionsResponseItem{
			Global:  ansibleItem.Global,
			Name:    ansibleItem.Name,
			Version: ansibleItem.Version,
			Type:    ansibleItem.Type,
		})
	}

	return e.JSON(http.StatusOK, ansibleResponseItems)
}

// ActionRoleFromInternet - installs an ansible role from ansible galaxy or publicly available source control
func ActionRoleFromInternet(e *core.RequestEvent) error {
	var roleBody dto.InstallRoleRequest
	e.BindBody(&roleBody)

	user := e.Get("user").(*models.User)
	if user.ProxmoxUsername() == "root" {
		return JSONError(e, http.StatusForbidden, "Don't use the ROOT API key for ansible actions, use a user API key instead.")
	}

	if !user.IsAdmin() && ServerConfiguration.PreventUserAnsibleAdd {
		return JSONError(e, http.StatusForbidden, "You are not authorized to perform this ansible action")
	}

	var roleString = roleBody.Role
	if roleBody.Version != "" {
		roleString = fmt.Sprintf("%s,%s", roleBody.Role, roleBody.Version)
	}

	if roleBody.Action != "install" && roleBody.Action != "remove" {
		return JSONError(e, http.StatusInternalServerError, "action must be one of 'install' or 'remove'")
	}

	if roleBody.Action == "remove" && slices.Contains(coreAnsibleRoles, roleBody.Role) {
		return JSONError(e, http.StatusBadRequest, "You cannot remove this core Ludus role as it is required for Ludus to function")
	}

	// Make sure the role string is escaped
	roleString = shellescape.Quote(roleString)

	var cmd *exec.Cmd
	if roleBody.Global && roleBody.Force {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString, "-f", "--roles-path", fmt.Sprintf("%s/resources/global-roles", ludusInstallPath))
	} else if roleBody.Global {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString, "--roles-path", fmt.Sprintf("%s/resources/global-roles", ludusInstallPath))
	} else if roleBody.Force {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", roleBody.Action, roleString)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to %s the ansible role %s: %s; Output was: %s", roleBody.Action, roleString, err.Error(), string(cmdOutput)))
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") && !strings.Contains(string(cmdOutput), roleBody.Role+" was installed successfully") {
		return JSONError(e, http.StatusInternalServerError, string(cmdOutput))
	}
	if strings.Contains(string(cmdOutput), "is not installed, skipping.") {
		return JSONError(e, http.StatusConflict, string(cmdOutput))
	}
	if roleBody.Action != "install" {
		return JSONResult(e, http.StatusCreated, "Successfully removed: "+roleString)
	} else {
		return JSONResult(e, http.StatusCreated, "Successfully installed: "+roleString)
	}

}

// InstallRoleFromTar - installs an ansible role from a user uploaded tar file
func InstallRoleFromTar(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && ServerConfiguration.PreventUserAnsibleAdd {
		return JSONError(e, http.StatusForbidden, "You are not authorized to perform this ansible action")
	}

	// Parse the multipart form
	if err := e.Request.ParseMultipartForm(1073741824); err != nil { // allow 1GB
		return JSONError(e, http.StatusBadRequest, err.Error())
	}

	// Retrieve the 'force' field and convert it to boolean
	forceStr := e.Request.FormValue("force")
	if forceStr == "" {
		forceStr = "false"
	}
	force, err := strconv.ParseBool(forceStr)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid boolean value for 'force': "+err.Error())
	}

	// Retrieve the 'global' field and convert it to boolean
	globalStr := e.Request.FormValue("global")
	if globalStr == "" {
		globalStr = "false"
	}
	global, err := strconv.ParseBool(globalStr)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid boolean value for 'global': "+err.Error())
	}

	// Retrieve the file
	file, fileHeader, err := e.Request.FormFile("file")
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "File retrieval failed")
	}

	// Save the file to the server
	roleTarPath := fmt.Sprintf("%s/users/%s/.ansible/tmp/%s", ludusInstallPath, user.ProxmoxUsername(), fileHeader.Filename)

	// Go strips all directory information from the file name, no issue with path traversal here. See: https://go-review.googlesource.com/c/go/+/313809 and https://github.com/golang/go/issues/45789

	fileContents, err := io.ReadAll(file)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to read file contents: "+err.Error())
	}
	err = os.WriteFile(roleTarPath, fileContents, 0644)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save the file: "+err.Error())
	}
	defer os.Remove(roleTarPath)

	// Make sure the file name is escaped
	fileHeader.Filename = shellescape.Quote(fileHeader.Filename)

	var cmd *exec.Cmd
	if global && force {
		cmd = exec.Command("ansible-galaxy", "role", "install", fileHeader.Filename, "-f", "--roles-path", fmt.Sprintf("%s/resources/global-roles", ludusInstallPath))
	} else if global {
		cmd = exec.Command("ansible-galaxy", "role", "install", fileHeader.Filename, "--roles-path", fmt.Sprintf("%s/resources/global-roles", ludusInstallPath))
	} else if force {
		cmd = exec.Command("ansible-galaxy", "role", "install", fileHeader.Filename, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", "role", "install", fileHeader.Filename)
	}
	cmd.Dir = fmt.Sprintf("%s/users/%s/.ansible/tmp", ludusInstallPath, user.ProxmoxUsername()) // If you try to install a tar'd role with the full path, it will fail to extract. Bug in ansible-galaxy?
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to install the ansible role %s: %s; Output was: %s", roleTarPath, err.Error(), string(cmdOutput)))
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") && !strings.Contains(string(cmdOutput), fileHeader.Filename+" was installed successfully") {
		return JSONError(e, http.StatusInternalServerError, string(cmdOutput))
	}
	// Parse the version.yml file in the meta directory for this role, and set the value in meta/.galaxy_install_info
	roleName := strings.TrimSuffix(fileHeader.Filename, ".tar.gz")
	var roleMetaPath string
	var galaxyInstallInfoPath string
	if !global {
		roleMetaPath = fmt.Sprintf("%s/users/%s/.ansible/roles/%s/meta", ludusInstallPath, user.ProxmoxUsername(), roleName)
		galaxyInstallInfoPath = fmt.Sprintf("%s/users/%s/.ansible/roles/%s/meta/.galaxy_install_info", ludusInstallPath, user.ProxmoxUsername(), roleName)
	} else {
		roleMetaPath = fmt.Sprintf("%s/resources/global-roles/%s/meta", ludusInstallPath, roleName)
		galaxyInstallInfoPath = fmt.Sprintf("%s/resources/global-roles/%s/meta/.galaxy_install_info", ludusInstallPath, roleName)
	}
	versionYmlPath := fmt.Sprintf("%s/version.yml", roleMetaPath)
	if _, err := os.Stat(versionYmlPath); err == nil {
		versionYmlContents, err := os.ReadFile(versionYmlPath)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to read version.yml in the role meta directory: %s", roleMetaPath))
		}
		// Parse the version.yml file
		var versionYml map[string]string
		err = yaml.Unmarshal(versionYmlContents, &versionYml)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to parse version.yml in the role meta directory: %s", roleMetaPath))
		}
		// Write the version to the .galaxy_install_info file
		fileContents, err := os.ReadFile(galaxyInstallInfoPath)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to read .galaxy_install_info: %s", err))
		}

		// Convert the contents to a string and split into lines
		contents := string(fileContents)
		lines := strings.Split(contents, "\n")

		// Flag to check if version line exists
		versionExists := false
		for i, line := range lines {
			if strings.HasPrefix(line, "version:") {
				// Update the version line
				lines[i] = fmt.Sprintf("version: %s", versionYml["version"])
				versionExists = true
				break
			}
		}

		// If version line does not exist, append it
		if !versionExists {
			lines = append(lines, fmt.Sprintf("version: %s", versionYml["version"]))
		}

		// Join the lines back together
		updatedContents := strings.Join(lines, "\n")

		// Write the updated contents back to the file
		err = os.WriteFile(galaxyInstallInfoPath, []byte(updatedContents), 0660)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to write to .galaxy_install_info in the role meta directory: %s", roleMetaPath))
		}

	}

	return JSONResult(e, http.StatusCreated, "Successfully installed role")

}

// ActionCollectionFromInternet - installs an ansible collection from ansible galaxy or publicly available source control
func ActionCollectionFromInternet(e *core.RequestEvent) error {
	var collectionBody dto.InstallCollectionRequest
	e.BindBody(&collectionBody)

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() && ServerConfiguration.PreventUserAnsibleAdd {
		return JSONError(e, http.StatusForbidden, "You are not authorized to perform this ansible action")
	}

	var collectionString = collectionBody.Collection
	if collectionBody.Version != "" {
		collectionString = fmt.Sprintf("%s:==%s", collectionBody.Collection, collectionBody.Version)
	}

	// Make sure the collection string is escaped
	collectionString = shellescape.Quote(collectionString)

	var cmd *exec.Cmd
	if collectionBody.Force {
		cmd = exec.Command("ansible-galaxy", "collection", "install", collectionString, "-f")
	} else {
		cmd = exec.Command("ansible-galaxy", "collection", "install", collectionString)
	}
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("ANSIBLE_HOME=%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername()))
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to install the ansible collection %s: %s; Output was: %s", collectionString, err.Error(), string(cmdOutput)))
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") {
		return JSONError(e, http.StatusInternalServerError, string(cmdOutput))
	}
	if strings.Contains(string(cmdOutput), "Nothing to do. All requested collections are already installed. If you want to reinstall them, consider using `--force`.") {
		return JSONError(e, http.StatusConflict, "Collection already installed. Collections from https://docs.ansible.com/ansible/latest/collections/index.html are installed globally. If you want to reinstall it, consider using `--force`.")
	}
	return JSONResult(e, http.StatusCreated, "Successfully installed: "+collectionString)

}
