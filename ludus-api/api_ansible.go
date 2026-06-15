package ludusapi

import (
	"fmt"
	"io"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"path/filepath"
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

// coreAnsibleCollections is the collection analogue of coreAnsibleRoles:
// collections Ludus relies on and refuses to remove. Empty today (no shipped
// collection is load-bearing yet) but enforced by ActionCollectionFromInternet
// so a future core collection is protected the moment it is added here.
var coreAnsibleCollections = []string{}

// isCoreCollection reports whether the FQCN names a protected core collection.
func isCoreCollection(fqcn string) bool {
	return slices.Contains(coreAnsibleCollections, fqcn)
}

// validateCollectionAction accepts the empty string (defaults to install),
// "install", or "remove" — mirroring the role endpoint's action vocabulary.
func validateCollectionAction(action string) error {
	switch action {
	case "", "install", "remove":
		return nil
	default:
		return fmt.Errorf("action must be one of 'install' or 'remove'")
	}
}

// GetRolesAndCollections - retrieves the installed Ansible roles and
// collections for the user.
func GetRolesAndCollections(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var ansibleItems []AnsibleItem
	roles := scanInstalledRoles(userRolesPath(user.ProxmoxUsername()), globalRolesPath())
	collections := scanInstalledCollections(candidateAnsibleHomes(user.ProxmoxUsername()), globalCollectionsPath())
	// User scope first, then global — the order ansible-galaxy's path
	// sections presented before this read from the shared scan.
	for _, scope := range []string{"user", "global"} {
		for _, r := range roles {
			if r.Scope != scope {
				continue
			}
			version := r.Version
			if version == "" {
				version = "(unknown version)" // what ansible-galaxy prints for a receipt-less role
			}
			ansibleItems = append(ansibleItems, AnsibleItem{
				Name:    r.Name,
				Version: version,
				Type:    "role",
				Global:  scope == "global",
			})
		}
		for _, c := range collections {
			if c.Scope != scope {
				continue
			}
			ansibleItems = append(ansibleItems, AnsibleItem{
				Name:    c.FQCN,
				Version: c.Version,
				Type:    "collection",
				Global:  scope == "global",
			})
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

	scopePath := ""
	if roleBody.Global {
		scopePath = globalRolesPath()
	}
	cmd := galaxyInstallCmd(galaxyRole, []string{roleBody.Action}, []string{roleString}, roleBody.Force, scopePath, userAnsibleHome(user.ProxmoxUsername()))
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
		releaseSourceClaims(e.App, []string{"local_role", "galaxy_role"}, roleBody.Role)
		return JSONResult(e, http.StatusCreated, "Successfully removed: "+roleString)
	} else {
		return JSONResult(e, http.StatusCreated, "Successfully installed: "+roleString)
	}

}

// ansibleRoleArchiveSuffixes are stripped from the end of an uploaded filename when deriving
// the role name for ansible-galaxy. Longer entries must precede shorter suffixes they contain
// (e.g. ".tar.gz" before ".gz").
var ansibleRoleArchiveSuffixes = []string{
	".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst",
	".tgz", ".tbz2", ".txz",
	".tar", ".zip",
	".gz", ".bz2", ".xz", ".zst",
}

// roleNameFromUploadedArchiveBasename returns the role name for ansible-galaxy from the uploaded
// file basename. Only known archive suffixes are removed so names like mynamespace.my_role stay intact.
func roleNameFromUploadedArchiveBasename(basename string) string {
	name := basename
	for {
		stripped := false
		for _, suf := range ansibleRoleArchiveSuffixes {
			if len(name) < len(suf) {
				continue
			}
			tail := name[len(name)-len(suf):]
			if strings.EqualFold(tail, suf) {
				name = name[:len(name)-len(suf)]
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}
	return name
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
	ansibleTmpPath := fmt.Sprintf("%s/users/%s/.ansible/tmp", ludusInstallPath, user.ProxmoxUsername())

	// Make sure the file name is escaped
	fileHeader.Filename = shellescape.Quote(fileHeader.Filename)

	roleTarPath := fmt.Sprintf("%s/%s", ansibleTmpPath, fileHeader.Filename)

	// Go strips all directory information from the file name, no issue with path traversal here. See: https://go-review.googlesource.com/c/go/+/313809 and https://github.com/golang/go/issues/45789

	fileContents, err := io.ReadAll(file)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to read file contents: "+err.Error())
	}
	err = os.WriteFile(roleTarPath, fileContents, 0644)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to save the file: "+err.Error())
	}

	// Strip known archive suffixes only; do not use filepath.Ext in a loop (that treats
	// mynamespace.my_role as name + extension and truncates to mynamespace).
	roleName := roleNameFromUploadedArchiveBasename(filepath.Base(roleTarPath))
	newPath := fmt.Sprintf("%s/%s", ansibleTmpPath, roleName)
	os.Rename(roleTarPath, newPath)
	defer os.Remove(newPath)

	scopePath := ""
	if global {
		scopePath = globalRolesPath()
	}
	cmd := galaxyInstallCmd(galaxyRole, []string{"role", "install"}, []string{roleName}, force, scopePath, userAnsibleHome(user.ProxmoxUsername()))
	cmd.Dir = fmt.Sprintf("%s/users/%s/.ansible/tmp", ludusInstallPath, user.ProxmoxUsername()) // If you try to install a tar'd role with the full path, it will fail to extract. Bug in ansible-galaxy?
	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to install the ansible role %s: %s; Output was: %s", roleTarPath, err.Error(), string(cmdOutput)))
	}
	if strings.Contains(string(cmdOutput), "[WARNING]") && !strings.Contains(string(cmdOutput), fileHeader.Filename+" was installed successfully") {
		return JSONError(e, http.StatusInternalServerError, string(cmdOutput))
	}
	_, err = parseRoleVersion(roleName, user, global)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusCreated, "Successfully installed role")

}

func parseRoleVersion(roleName string, user *models.User, global bool) (string, error) {
	roleDir := filepath.Join(ludusInstallPath, "users", user.ProxmoxUsername(), ".ansible", "roles", roleName)
	if global {
		roleDir = filepath.Join(ludusInstallPath, "resources", "global-roles", roleName)
	}
	return reflectRoleVersionToGalaxyInfo(roleDir)
}

// reflectRoleVersionToGalaxyInfo upserts version.yml's version into
// .galaxy_install_info so the role shows up in `ansible-galaxy role list`.
// Preserves install_date when the file exists (galaxy install) and
// synthesizes a fresh one when it doesn't (manual role copy).
func reflectRoleVersionToGalaxyInfo(roleDir string) (string, error) {
	metaDir := filepath.Join(roleDir, "meta")
	versionYmlPath := filepath.Join(metaDir, "version.yml")
	if _, err := os.Stat(versionYmlPath); err != nil {
		return "", nil
	}
	versionYmlContents, err := os.ReadFile(versionYmlPath)
	if err != nil {
		return "", fmt.Errorf("read version.yml in %s: %w", metaDir, err)
	}
	var versionYml map[string]string
	if err := yaml.Unmarshal(versionYmlContents, &versionYml); err != nil {
		return "", fmt.Errorf("parse version.yml in %s: %w", metaDir, err)
	}
	version := strings.TrimSpace(versionYml["version"])
	if version == "" {
		return "", nil
	}

	infoPath := filepath.Join(metaDir, ".galaxy_install_info")
	existing, readErr := os.ReadFile(infoPath)
	if readErr != nil {
		content := fmt.Sprintf("install_date: \"\"\nversion: %s\n", version)
		if err := os.WriteFile(infoPath, []byte(content), 0660); err != nil {
			return "", fmt.Errorf("write .galaxy_install_info in %s: %w", metaDir, err)
		}
		return version, nil
	}

	lines := strings.Split(string(existing), "\n")
	versionExists := false
	for i, line := range lines {
		if strings.HasPrefix(line, "version:") {
			lines[i] = fmt.Sprintf("version: %s", version)
			versionExists = true
			break
		}
	}
	if !versionExists {
		lines = append(lines, fmt.Sprintf("version: %s", version))
	}
	if err := os.WriteFile(infoPath, []byte(strings.Join(lines, "\n")), 0660); err != nil {
		return "", fmt.Errorf("write .galaxy_install_info in %s: %w", metaDir, err)
	}
	return version, nil
}

// isGitCollectionSource reports whether a collection identifier names a git
// source rather than a galaxy FQCN — true for any URL (has "://"), an
// scp-style ssh ref (git@…), or an explicit git+ prefix.
func isGitCollectionSource(c string) bool {
	return strings.Contains(c, "://") || strings.HasPrefix(c, "git@") || strings.HasPrefix(c, "git+")
}

// buildCollectionInstallArg renders the positional argument for
// `ansible-galaxy collection install`. Galaxy collections use the pin form
// (name:==version); git sources use git+<url>,<ref>. Bare https URLs get a
// git+ prefix since ansible-galaxy rejects them otherwise; git@ / already-
// git+ strings are passed through unchanged.
func buildCollectionInstallArg(collection, version string) string {
	if isGitCollectionSource(collection) {
		src := collection
		if strings.Contains(src, "://") && !strings.HasPrefix(src, "git+") {
			src = "git+" + src
		}
		if version != "" {
			return src + "," + version
		}
		return src
	}
	if version != "" {
		return fmt.Sprintf("%s:==%s", collection, version)
	}
	return collection
}

// ActionCollectionFromInternet - installs an ansible collection from ansible
// galaxy / source control, or removes one from disk. ansible-galaxy has no
// `collection remove` subcommand, so a remove deletes the on-disk collection
// directory directly (ansible/ansible#67759). Mirrors ActionRoleFromInternet's
// install/remove dispatch, core-item guard, and global admin gate.
func ActionCollectionFromInternet(e *core.RequestEvent) error {
	var collectionBody dto.InstallCollectionRequest
	e.BindBody(&collectionBody)

	user := e.Get("user").(*models.User)
	if user.ProxmoxUsername() == "root" {
		return JSONError(e, http.StatusForbidden, "Don't use the ROOT API key for ansible actions, use a user API key instead.")
	}
	if !user.IsAdmin() && ServerConfiguration.PreventUserAnsibleAdd {
		return JSONError(e, http.StatusForbidden, "You are not authorized to perform this ansible action")
	}

	if err := validateCollectionAction(collectionBody.Action); err != nil {
		return JSONError(e, http.StatusBadRequest, err.Error())
	}

	if collectionBody.Action == "remove" {
		if collectionBody.Global && !user.IsAdmin() {
			return JSONError(e, http.StatusForbidden, "Only administrators can remove globally-installed collections")
		}
		if isCoreCollection(collectionBody.Collection) {
			return JSONError(e, http.StatusBadRequest, "You cannot remove this core Ludus collection as it is required for Ludus to function")
		}
		owner := ""
		if !collectionBody.Global {
			owner = user.ProxmoxUsername()
		}
		if err := removeLocalCollectionByName(collectionBody.Collection, owner, collectionBody.Global); err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to remove the ansible collection %s: %s", collectionBody.Collection, err.Error()))
		}
		releaseSourceClaims(e.App, []string{"local_collection", "collection"}, collectionBody.Collection)
		return JSONResult(e, http.StatusCreated, "Successfully removed: "+collectionBody.Collection)
	}

	// action == "" or "install": pass the source string straight to
	// ansible-galaxy, the same way the role endpoint does — a git URL installs
	// from git, an FQCN installs from galaxy. No separate type flag: the
	// string's shape is the signal.
	collectionString := buildCollectionInstallArg(collectionBody.Collection, collectionBody.Version)

	// Make sure the collection string is escaped
	collectionString = shellescape.Quote(collectionString)

	scopePath := ""
	if collectionBody.Global {
		scopePath = globalCollectionsPath()
	}
	cmd := galaxyInstallCmd(galaxyCollection, []string{"collection", "install"}, []string{collectionString}, collectionBody.Force, scopePath, userAnsibleHome(user.ProxmoxUsername()))
	cmdOutput, err := cmd.CombinedOutput()
	output := string(cmdOutput)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to install the ansible collection %s: %s; Output was: %s", collectionString, err.Error(), output))
	}
	// Judge the outcome by galaxy's result markers, not by scanning for
	// [WARNING]: a successful install can still print advisory warnings (e.g. a
	// version-compat note about an unrelated collection already in the search
	// path), and those must not be treated as failures. Output with neither the
	// no-op nor the success marker falls through to the error below, so genuine
	// problems are still surfaced.
	if strings.Contains(output, "Nothing to do. All requested collections are already installed") {
		return JSONError(e, http.StatusConflict, "Collection already installed. Collections from https://docs.ansible.com/ansible/latest/collections/index.html are installed globally. If you want to reinstall it, consider using `--force`.")
	}
	if strings.Contains(output, "was installed successfully") {
		return JSONResult(e, http.StatusCreated, "Successfully installed: "+collectionString)
	}
	return JSONError(e, http.StatusInternalServerError, output)
}

func GetSubscriptionRoles(e *core.RequestEvent) error {
	subscriptionRoles, err := GetSubscriptionRolesMetadata(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get subscription roles: "+err.Error())
	}
	return e.JSON(http.StatusOK, subscriptionRoles)
}

// InstallSubscriptionRoles - installs one or more subscription roles using the license key
func InstallSubscriptionRoles(e *core.RequestEvent) error {
	var requestBody dto.InstallSubscriptionRolesRequest
	e.BindBody(&requestBody)

	// Check if license is valid
	if !server.LicenseValid || server.LicenseKey == "" {
		return JSONError(e, http.StatusForbidden, "A valid Ludus license key is required to install subscription roles")
	}

	user := e.Get("user").(*models.User)
	if user.ProxmoxUsername() == "root" {
		return JSONError(e, http.StatusForbidden, "Don't use the ROOT API key for ansible actions, use a user API key instead.")
	}

	if !user.IsAdmin() && ServerConfiguration.PreventUserAnsibleAdd {
		return JSONError(e, http.StatusForbidden, "You are not authorized to perform this ansible action")
	}

	var success []string
	var errors []dto.InstallSubscriptionRolesResponseErrorsItem

	// Create temp directory for role downloads
	tempDir := fmt.Sprintf("%s/users/%s/.ansible/tmp", ludusInstallPath, user.ProxmoxUsername())
	err := os.MkdirAll(tempDir, 0755)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to create temp directory: "+err.Error())
	}

	// Process each role
	for _, roleName := range requestBody.Roles {

		// Make sure the file name is escaped
		escapedRoleName := shellescape.Quote(roleName)

		// Download the role tar file from the license server
		roleFileName, err := DownloadRoleUsingLicenseKey(e, escapedRoleName, tempDir)
		if err != nil {
			if strings.Contains(err.Error(), "resource was not found") {
				errors = append(errors, dto.InstallSubscriptionRolesResponseErrorsItem{
					Role:   roleName,
					Reason: "Role not found, it may not be available to your license level",
				})
				continue
			}
			errors = append(errors, dto.InstallSubscriptionRolesResponseErrorsItem{
				Role:   roleName,
				Reason: fmt.Sprintf("Failed to download role from server: %s", err.Error()),
			})
			continue
		}

		roleTarPath := fmt.Sprintf("%s/%s", tempDir, roleFileName)

		os.Rename(roleTarPath, fmt.Sprintf("%s/%s", tempDir, escapedRoleName))
		defer os.Remove(fmt.Sprintf("%s/%s", tempDir, escapedRoleName))

		// Install the role using ansible-galaxy (globally or per-user based on request)
		scopePath := ""
		if requestBody.Global {
			scopePath = globalRolesPath()
		}
		cmd := galaxyInstallCmd(galaxyRole, []string{"role", "install"}, []string{escapedRoleName}, requestBody.Force, scopePath, userAnsibleHome(user.ProxmoxUsername()))
		cmd.Dir = tempDir // ansible-galaxy needs to be run from the directory containing the tar file
		cmdOutput, err := cmd.CombinedOutput()
		if err != nil && !strings.Contains(string(cmdOutput), "was installed successfully") {
			errors = append(errors, dto.InstallSubscriptionRolesResponseErrorsItem{
				Role:   roleName,
				Reason: fmt.Sprintf("Failed to install role: %s; Output was: %s", err.Error(), string(cmdOutput)),
			})
			continue
		}

		// Check for warnings that indicate failure
		if strings.Contains(string(cmdOutput), "[WARNING]") && !strings.Contains(string(cmdOutput), roleFileName+" was installed successfully") {
			logger.Warn("Installation warning for role %s: %s", roleName, string(cmdOutput))
		}

		_, err = parseRoleVersion(roleName, user, requestBody.Global)
		if err != nil {
			errors = append(errors, dto.InstallSubscriptionRolesResponseErrorsItem{
				Role:   roleName,
				Reason: fmt.Sprintf("Failed to parse role version: %s", err.Error()),
			})
		}

		// Success - add to success list
		success = append(success, roleName)
	}

	response := dto.InstallSubscriptionRolesResponse{
		Success: success,
		Errors:  errors,
	}

	return e.JSON(http.StatusOK, response)
}

// GetRoleVars - retrieves the variables for one or more Ansible roles
func GetRoleVars(e *core.RequestEvent) error {
	var requestBody dto.GetRoleVarsRequest
	e.BindBody(&requestBody)

	if len(requestBody.Roles) == 0 {
		return JSONError(e, http.StatusBadRequest, "At least one role name is required")
	}

	user := e.Get("user").(*models.User)
	var response dto.GetRoleVarsResponse

	for _, roleName := range requestBody.Roles {
		roleResponse := dto.GetRoleVarsResponseRole{
			Name: roleName,
			Vars: make(map[string]interface{}),
		}

		// Try user-specific role first
		userRolePath := fmt.Sprintf("%s/users/%s/.ansible/roles/%s", ludusInstallPath, user.ProxmoxUsername(), roleName)
		// Try global role
		globalRolePath := fmt.Sprintf("%s/resources/global-roles/%s", ludusInstallPath, roleName)

		var rolePath string
		var roleFound bool

		// Check if role exists in user-specific location
		if _, err := os.Stat(userRolePath); err == nil {
			rolePath = userRolePath
			roleResponse.Global = false
			roleFound = true
		} else if _, err := os.Stat(globalRolePath); err == nil {
			// Check if role exists in global location
			rolePath = globalRolePath
			roleResponse.Global = true
			roleFound = true
		}

		// If role found, read user-configurable variables from defaults/main.yml
		if roleFound {
			// Read defaults/main.yml (user-configurable variables)
			defaultsPath := fmt.Sprintf("%s/defaults/main.yml", rolePath)
			if _, err := os.Stat(defaultsPath); err == nil {
				defaultsContent, err := os.ReadFile(defaultsPath)
				if err == nil {
					var defaultsVars map[string]interface{}
					if err := yaml.Unmarshal(defaultsContent, &defaultsVars); err == nil {
						// Set defaults as vars (these are what users can configure via role_vars)
						for k, v := range defaultsVars {
							roleResponse.Vars[k] = v
						}
					}
				}
			}
		}

		response.Roles = append(response.Roles, roleResponse)
	}

	return e.JSON(http.StatusOK, response)
}

// copyDir recursively copies a directory from src to dst. Symlinks are
// rejected outright as a defense-in-depth measure. Git metadata is skipped:
// a submodule checkout carries a .git gitlink file whose target only exists
// inside the source checkout, so copying it would plant a dangling reference
// in the installed copy.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Name() == ".git" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to copy symlink %q: blueprint dirs and role/template dirs must contain only regular files and directories", path)
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to copy non-regular file %q", path)
		}

		srcFile, err := os.Open(path)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.Create(dstPath)
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

// MoveRoleScope - moves one or more roles between local and global installation paths
func MoveRoleScope(e *core.RequestEvent) error {
	var requestBody dto.MoveRoleScopeRequest
	e.BindBody(&requestBody)

	if len(requestBody.Roles) == 0 {
		return JSONError(e, http.StatusBadRequest, "At least one role name is required")
	}

	user := e.Get("user").(*models.User)
	if !user.IsAdmin() {
		return JSONError(e, http.StatusForbidden, "Only administrators can move roles between scopes")
	}

	targetGlobal := requestBody.Global
	var success []string
	var errors []dto.MoveRoleScopeResponseErrorsItem

	// Process each role
	for _, roleName := range requestBody.Roles {
		// Determine source and destination paths
		userRolePath := fmt.Sprintf("%s/users/%s/.ansible/roles/%s", ludusInstallPath, user.ProxmoxUsername(), roleName)
		globalRolePath := fmt.Sprintf("%s/resources/global-roles/%s", ludusInstallPath, roleName)

		isCurrentlyGlobal := false
		var currentPath string

		// Find where role currently exists
		if _, err := os.Stat(globalRolePath); err == nil {
			isCurrentlyGlobal = true
			currentPath = globalRolePath
		} else if _, err := os.Stat(userRolePath); err == nil {
			isCurrentlyGlobal = false
			currentPath = userRolePath
		} else {
			errors = append(errors, dto.MoveRoleScopeResponseErrorsItem{
				Role:  roleName,
				Error: fmt.Sprintf("Role '%s' not found", roleName),
			})
			continue
		}

		// Check if we're already at target
		if isCurrentlyGlobal == targetGlobal {
			scope := "global"
			if !targetGlobal {
				scope = "local"
			}
			errors = append(errors, dto.MoveRoleScopeResponseErrorsItem{
				Role:  roleName,
				Error: fmt.Sprintf("Role is already installed in %s scope", scope),
			})
			continue
		}

		// Set destination path
		var destPath string
		if targetGlobal {
			destPath = globalRolePath
		} else {
			destPath = userRolePath
		}

		// Check destination doesn't exist
		if _, err := os.Stat(destPath); err == nil {
			errors = append(errors, dto.MoveRoleScopeResponseErrorsItem{
				Role:  roleName,
				Error: "Role already exists at destination",
			})
			continue
		}

		// Create parent directory for destination if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			errors = append(errors, dto.MoveRoleScopeResponseErrorsItem{
				Role:  roleName,
				Error: fmt.Sprintf("Failed to create destination directory: %s", err.Error()),
			})
			continue
		}

		// Copy the role directory
		if err := copyDir(currentPath, destPath); err != nil {
			errors = append(errors, dto.MoveRoleScopeResponseErrorsItem{
				Role:  roleName,
				Error: fmt.Sprintf("Failed to copy role: %s", err.Error()),
			})
			continue
		}

		// Remove the source directory only if copy is false (move operation)
		// If copy is true, keep the source (useful for global->local to keep global for others,
		// or local->global to keep local override)
		if !requestBody.Copy {
			if err := os.RemoveAll(currentPath); err != nil {
				// Best effort cleanup - log but don't fail if we can't remove source
				logger.Warn("Failed to remove source role directory %s: %s", currentPath, err.Error())
			}
		}

		// Update the role version in the new location
		_, _ = parseRoleVersion(roleName, user, targetGlobal)

		// Success - add to success list
		success = append(success, roleName)
	}

	response := dto.MoveRoleScopeResponse{
		Success: success,
		Errors:  errors,
	}

	return e.JSON(http.StatusOK, response)
}
