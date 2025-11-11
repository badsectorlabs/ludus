package ludusapi

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"

	"ludusapi/dto"
	"ludusapi/models"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var UserIDRegex = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

// AddUser - adds a user to the system
func AddUser(e *core.RequestEvent) error {

	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusUnauthorized, "You are not an admin and cannot add users")
	}

	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users add command with --url https://127.0.0.1:8081")
	}

	var addUserJSON dto.AddUserRequest
	e.BindBody(&addUserJSON)

	if addUserJSON.Name == "" || addUserJSON.UserID == "" {
		return JSONError(e, http.StatusBadRequest, "Name and userID are required")
	}

	if !UserIDRegex.MatchString(addUserJSON.UserID) {
		return JSONError(e, http.StatusBadRequest, "provided userID does not match ^[A-Za-z0-9]{1,20}$")
	}

	// ADMIN is the pool used for generally available VMs (Nexus cache)
	// ROOT is the ID of the root ludus user
	// CICD is the ID of the CI/CD user
	// SHARED is the pool used for shared VMs (templates)
	// 0 is a bug in proxmox where creating a resource pool with ID 0 succeeds but doesn't actually create the pool
	reservedUserIDs := []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}

	// Do not allow users to be created with the reserved user IDs
	if slices.Contains(reservedUserIDs, addUserJSON.UserID) {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("%s is a reserved user ID", addUserJSON.UserID))
	}

	// Validate there is an email and password
	if addUserJSON.Email == "" || addUserJSON.Password == "" {
		return JSONError(e, http.StatusBadRequest, "Email and password are required")
	}

	// Check that the password is at least 8 characters long
	if len(addUserJSON.Password) < 8 {
		return JSONError(e, http.StatusBadRequest, "Password must be at least 8 characters long")
	}

	// Check if the user already exists
	matchingUsers, err := app.CountRecords("users", dbx.HashExp{"userID": addUserJSON.UserID})
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if user already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(e, http.StatusBadRequest, "User with that ID already exists")
	}

	// Convert to lower-case, and replace spaces with "-"
	var user models.Users
	user.SetName(addUserJSON.Name)
	user.SetUserId(addUserJSON.UserID)
	user.SetEmail(addUserJSON.Email)
	user.SetPassword(addUserJSON.Password)
	user.SetProxmoxUsername(strings.ReplaceAll(strings.ToLower(addUserJSON.Name), " ", "-"))

	matchingUsers, err = app.CountRecords("users", dbx.HashExp{"proxmoxUsername": user.ProxmoxUsername()})
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if username already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(e, http.StatusBadRequest, "User with that name already exists")
	}

	// Check if the username already exists on the host system
	if userExistsOnHostSystem(user.ProxmoxUsername()) {
		return JSONError(e, http.StatusBadRequest, "User with that name already exists on the host system. Ludus uses the PAM for user authentication, so you must use a unique username for each Ludus user.")
	}

	if poolExists(user.UserId()) {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Pool with the name %s already exists", user.UserId()))
	}

	// Start database transaction and setup the error handling to clean up if anything fails during user creation
	app.RunInTransaction(func(txApp core.App) error {

		wasError := false
		defer func() {
			if wasError {
				// Remove the user from the host system - we validated the user did not exist on the host system before running the playbook, so this is safe to do
				removeUserFromHostSystem(user.ProxmoxUsername())
				removeUserFromProxmox(user.ProxmoxUsername(), "pam")
				removeUserFromPocketBaseByID(user.Id)
				removePool(user.UserId())
			}
		}()

		// Create a default range for the user using the new utility function, also creates a UserRangeAccess record
		err := CreateDefaultUserRange(txApp, user.UserId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating user's default range: %v", err))
		}

		// Get the next available user number
		user.SetUserNumber(findNextAvailableUserNumber(txApp))

		// Refuse to create more than 150 users
		if user.UserNumber() > 150 {
			return JSONError(e, http.StatusBadRequest, "Cannot create more than 150 users per Ludus due to networking constraints")
		}

		playbook := []string{ludusInstallPath + "/ansible/user-management/add-user.yml"}
		extraVars := map[string]interface{}{
			"username":          user.ProxmoxUsername(),
			"user_id":           user.UserId(),
			"user_number":       user.UserNumber(),
			"proxmox_public_ip": ServerConfiguration.ProxmoxPublicIP,
			"user_is_admin":     user.IsAdmin(),
			"proxmox_password":  addUserJSON.Password,
		}
		output, err := server.RunAnsiblePlaybookWithVariables(e, playbook, []string{}, extraVars, "", false, "")
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error running ansible playbook: %v", output))
		}

		apiKey := GenerateAPIKey(user.UserId())
		hashedAPIKey, err := HashString(apiKey)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error hashing API key: %v", err))
		}
		user.SetHashedApikey(hashedAPIKey)

		// Create a Proxmox API Token for the user
		tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user.ProxmoxUsername())
		if err != nil {
			wasError = true
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating Proxmox API token: %v", err))
		}

		encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
		if err != nil {
			wasError = true
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error encrypting Proxmox API token secret: %v", err))
		}

		user.SetProxmoxTokenId(tokenID)
		user.SetProxmoxTokenSecret(encryptedTokenSecret)

		// Add the plaintext API Key to this response only

		response := dto.AddUserResponse{
			Name:            user.Name(),
			UserID:          user.UserId(),
			DateCreated:     user.Created().Time(),
			DateLastActive:  user.Updated().Time(),
			IsAdmin:         user.IsAdmin(),
			ProxmoxUsername: user.ProxmoxUsername(),
			ApiKey:          apiKey,
		}
		return e.JSON(http.StatusCreated, response)
	})
	return nil
}

// DeleteUser - removes a user to the system
func DeleteUser(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot delete users")
	}

	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users rm command with --url https://127.0.0.1:8081")
	}

	userID := e.Request.URL.Query().Get("userID")
	if len(userID) == 0 {
		return JSONError(e, http.StatusBadRequest, "userID not provided")
	}

	// Deleting yourself fails as it removes the ansible home directory before the play ends and thus modules are not available to finish the play
	if userID == e.Request.URL.Query().Get("userID") {
		return JSONError(e, http.StatusBadRequest, "You cannot remove yourself")
	}

	var user models.User
	userRecord, err := app.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
	}
	if userRecord == nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User %s not found", userID))
	}
	user.SetProxyRecord(userRecord)

	playbook := []string{ludusInstallPath + "/ansible/user-management/del-user.yml"}
	extraVars := map[string]interface{}{
		"username":      user.ProxmoxUsername(),
		"user_id":       user.UserId(),
		"user_number":   user.UserNumber(),
		"user_is_admin": user.IsAdmin(),
	}
	output, err := server.RunAnsiblePlaybookWithVariables(e, playbook, []string{}, extraVars, "", false, "")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error running ansible playbook: %v", output))
	}

	err = app.Delete(userRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting user: %v", err))
	}
	err = removeUserFromProxmox(user.ProxmoxUsername(), "pam")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing user from Proxmox: %v", err))
	}
	err = removeUserFromPocketBaseByID(userRecord.Id)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing user from PocketBase: %v", err))
	}

	return JSONResult(e, http.StatusOK, "User deleted")
}

// GetAPIKey - reset and retrieve the Ludus API key for the user
func GetAPIKey(e *core.RequestEvent) error {

	user := e.Get("user").(*models.User)

	// Generate a new API and update the database
	apiKey := GenerateAPIKey(user.UserId())
	hashedAPIKey, _ := HashString(apiKey)
	user.SetHashedApikey(hashedAPIKey)
	err := app.Save(user)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving user: %v", err))
	}

	result := dto.GetAPIKeyResponseResult{
		ApiKey: apiKey,
	}
	response := dto.GetAPIKeyResponse{
		Result: &result,
	}

	return e.JSON(http.StatusCreated, response)
}

// GetCredentials - get the proxmox creds for the user
func GetCredentials(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	proxmoxPassword, err := getProxmoxPasswordForUser(user)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting proxmox password for user: %v", err))
	}
	result := dto.GetCredentialsResponseResult{
		ProxmoxUsername: user.ProxmoxUsername(),
		ProxmoxPassword: proxmoxPassword,
	}
	response := dto.GetCredentialsResponse{
		Result: &result,
	}
	return e.JSON(http.StatusOK, response)
}

// GetWireguardConfig - retrieves a WireGuard configuration file for the user
func GetWireguardConfig(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	wireGuardConfig, err := GetFileContents(fmt.Sprintf("%s/users/%s/%s_client.conf", ludusInstallPath, user.ProxmoxUsername(), user.UserId()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting wireguard config: %v", err))
	}
	response := dto.GetWireguardConfigResponse{
		Result: &dto.GetWireguardConfigResponseResult{
			WireGuardConfig: wireGuardConfig,
		},
	}
	return e.JSON(http.StatusOK, response)
}

// ListAllUsers - lists all users
func ListAllUsers(e *core.RequestEvent) error {

	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot list all users")
	}

	usersRecords, err := app.FindAllRecords("users")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error listing users: %v", err))
	}

	users := make([]dto.ListAllUsersResponseItem, len(usersRecords))
	for i, userRecord := range usersRecords {
		userModel := &models.User{}
		userModel.SetProxyRecord(userRecord)
		users[i] = dto.ListAllUsersResponseItem{
			Name:            userModel.Name(),
			UserID:          userModel.UserId(),
			UserNumber:      userModel.UserNumber(),
			DateCreated:     userModel.Created().Time(),
			DateLastActive:  userModel.LastActive().Time(),
			IsAdmin:         userModel.IsAdmin(),
			ProxmoxUsername: userModel.ProxmoxUsername(),
		}
	}
	return e.JSON(http.StatusOK, users)
}

// ListUser - lists user details
func ListUser(e *core.RequestEvent) error {

	userModel := e.Get("user").(*models.User)
	user := dto.ListUserResponseItem{
		Name:            userModel.Name(),
		UserID:          userModel.UserId(),
		DateCreated:     userModel.Created().Time(),
		DateLastActive:  userModel.LastActive().Time(),
		IsAdmin:         userModel.IsAdmin(),
		ProxmoxUsername: userModel.ProxmoxUsername(),
		UserNumber:      userModel.UserNumber(),
	}
	response := []dto.ListUserResponseItem{user}
	return e.JSON(http.StatusOK, response)
}

// PasswordReset - resets a user's proxmox password
func PasswordReset(e *core.RequestEvent) error {
	return JSONError(e, http.StatusInternalServerError, "Not implemented")
}

// PostCredentials - updates the users proxmox password
func PostCredentials(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var credsToUpdate dto.PostCredentialsRequest
	e.BindBody(&credsToUpdate)
	if credsToUpdate.ProxmoxPassword == "" {
		return JSONError(e, http.StatusBadRequest, "Missing proxmoxPassword value")
	}

	filePath := fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, user.ProxmoxUsername())
	err := os.WriteFile(filePath, []byte(credsToUpdate.ProxmoxPassword), 0660)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving proxmox password: %v", err))
	}

	// File saved successfully. Return proper result+
	response := dto.PostCredentialsResponse{
		Result: "Your proxmox password has been successfully updated in Ludus. THIS DOES NOT UPDATE THE PROXMOX PASSWORD ON THE HOST SYSTEM. YOU MUST UPDATE THE PROXMOX PASSWORD ON THE HOST SYSTEM MANUALLY TO MATCH THE PASSWORD YOU SET IN LUDUS.",
	}
	return e.JSON(http.StatusOK, response)
}
