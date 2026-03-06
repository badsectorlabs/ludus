package ludusapi

import (
	"database/sql"
	"fmt"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ludusapi/dto"
	"ludusapi/models"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

var UserIDRegex = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

// provisionNewUser handles the common provisioning steps for a new Ludus user:
// assigns a user number, creates a default range, runs the add-user ansible playbook,
// generates an API key, creates a Proxmox API token, grants Proxmox access,
// and saves the user record. The caller must set Name, UserId, Email, Password,
// ProxmoxUsername, ProxmoxRealm, and ProxmoxPassword on the user before calling.
// Returns the plaintext API key on success.
func provisionNewUser(txApp core.App, user *models.User, plaintextPassword string) (string, error) {
	user.SetUserNumber(findNextAvailableUserNumber(txApp))
	if user.UserNumber() > 150 {
		return "", fmt.Errorf("cannot create more than 150 users")
	}

	if err := CreateDefaultUserRangeForBootstrap(txApp, user); err != nil {
		return "", fmt.Errorf("creating default range: %w", err)
	}

	extraVars := map[string]interface{}{
		"username":          user.ProxmoxUsername(),
		"user_id":           user.UserId(),
		"user_number":       user.UserNumber(),
		"proxmox_public_ip": ServerConfiguration.ProxmoxPublicIP,
		"user_is_admin":     user.IsAdmin(),
		"proxmox_password":  plaintextPassword,
	}
	output, err := RunAddUserPlaybookStandalone(extraVars)
	if err != nil {
		return "", fmt.Errorf("running add-user playbook: %w (output: %s)", err, output)
	}

	apiKey := GenerateAPIKey(user.UserId())
	hashedAPIKey, err := HashString(apiKey)
	if err != nil {
		return "", fmt.Errorf("hashing API key: %w", err)
	}
	user.SetHashedApikey(hashedAPIKey)

	tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user.ProxmoxUsername(), user.ProxmoxRealm(), plaintextPassword)
	if err != nil {
		return "", fmt.Errorf("creating Proxmox API token: %w", err)
	}
	encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
	if err != nil {
		return "", fmt.Errorf("encrypting Proxmox token secret: %w", err)
	}
	user.SetProxmoxTokenId(tokenID)
	user.SetProxmoxTokenSecret(encryptedTokenSecret)

	if err := GrantUserProxmoxAccessToDefaultRange(txApp, user); err != nil {
		return "", fmt.Errorf("granting Proxmox access to default range: %w", err)
	}

	if err := txApp.Save(user); err != nil {
		return "", fmt.Errorf("saving user: %w", err)
	}

	return apiKey, nil
}

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

	// Validate there is an email
	if addUserJSON.Email == "" {
		return JSONError(e, http.StatusBadRequest, "Email is required")
	}

	if addUserJSON.Password == "" {
		addUserJSON.Password = security.RandomString(15)
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

	// Get the user collection
	userCollection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user collection: %v", err))
	}
	userRecord := core.NewRecord(userCollection)
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	user.SetName(addUserJSON.Name)
	user.SetUserId(addUserJSON.UserID)
	user.SetEmail(addUserJSON.Email)
	user.SetPassword(addUserJSON.Password)
	user.SetIsAdmin(addUserJSON.IsAdmin)
	// Convert to lower-case, and replace spaces with "-"
	user.SetProxmoxUsername(strings.ReplaceAll(strings.ToLower(addUserJSON.Name), " ", "-"))
	user.SetProxmoxRealm("pam") // For now, always use PAM for user authentication
	encryptedPassword, err := EncryptStringForDatabase(addUserJSON.Password)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error encrypting Proxmox password: %v", err))
	}
	user.SetProxmoxPassword(encryptedPassword)

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
				removeUserFromHostSystem(user.ProxmoxUsername())
				removeUserFromProxmox(user.ProxmoxUsername(), "pam")
				removePool(user.UserId())
				defaultRangeRecord, err := txApp.FindFirstRecordByData("ranges", "rangeID", user.DefaultRangeId())
				if err == nil {
					txApp.Delete(defaultRangeRecord)
				}
				txApp.Delete(user)
			}
		}()

		apiKey, err := provisionNewUser(txApp, user, addUserJSON.Password)
		if err != nil {
			wasError = true
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error provisioning user: %v", err))
		}

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

// validateInternalToken decrypts the X-Internal-Token header and verifies that
// the userID and isAdmin fields match the request body, and that the token was
// created within the last 30 seconds. The token payload format is "userID|isAdmin|unixTimestamp".
func validateInternalToken(token string, expectedUserID string, expectedIsAdmin bool) error {
	decrypted, err := DecryptStringFromDatabase(token)
	if err != nil {
		return fmt.Errorf("invalid internal token: %w", err)
	}

	parts := strings.SplitN(decrypted, "|", 3)
	if len(parts) != 3 {
		return fmt.Errorf("malformed internal token")
	}

	tokenUserID := parts[0]
	tokenIsAdmin := parts[1]
	tokenTimestamp := parts[2]

	if tokenUserID != expectedUserID {
		return fmt.Errorf("token userID mismatch")
	}

	if tokenIsAdmin != strconv.FormatBool(expectedIsAdmin) {
		return fmt.Errorf("token isAdmin mismatch")
	}

	ts, err := strconv.ParseInt(tokenTimestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp in token")
	}

	if math.Abs(float64(time.Now().Unix()-ts)) > 30 {
		return fmt.Errorf("token expired")
	}

	return nil
}

// ProvisionOAuth2User is an internal endpoint called by the non-root API (port 8080)
// to delegate user provisioning to the root API (port 8081). It is protected by
// a localhost check, root euid check, and an encrypted internal token.
func ProvisionOAuth2User(e *core.RequestEvent) error {
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "This endpoint is only available on the admin API")
	}

	host, _, err := net.SplitHostPort(e.Request.RemoteAddr)
	if err != nil || (host != "127.0.0.1" && host != "::1") {
		return JSONError(e, http.StatusForbidden, "This endpoint is only available from localhost")
	}

	var req dto.ProvisionOAuth2UserRequest
	if err := e.BindBody(&req); err != nil {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err))
	}

	if req.Name == "" || req.Email == "" || req.UserID == "" || req.Password == "" || req.ProxmoxUsername == "" {
		return JSONError(e, http.StatusBadRequest, "name, email, userID, password, and proxmoxUsername are required")
	}

	token := e.Request.Header.Get("X-Internal-Token")
	if token == "" {
		return JSONError(e, http.StatusForbidden, "Missing internal token")
	}
	if err := validateInternalToken(token, req.UserID, req.IsAdmin); err != nil {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("Token validation failed: %v", err))
	}

	matchingUsers, err := app.CountRecords("users", dbx.HashExp{"userID": req.UserID})
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if user already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(e, http.StatusBadRequest, "User with that ID already exists")
	}

	matchingUsers, err = app.CountRecords("users", dbx.HashExp{"proxmoxUsername": req.ProxmoxUsername})
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error checking if username already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(e, http.StatusBadRequest, "User with that proxmox username already exists")
	}

	if userExistsOnHostSystem(req.ProxmoxUsername) {
		return JSONError(e, http.StatusBadRequest, "User with that name already exists on the host system")
	}

	if poolExists(req.UserID) {
		return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Pool with the name %s already exists", req.UserID))
	}

	userCollection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user collection: %v", err))
	}
	userRecord := core.NewRecord(userCollection)
	user := &models.User{}
	user.SetProxyRecord(userRecord)
	user.SetName(req.Name)
	user.SetUserId(req.UserID)
	user.SetEmail(req.Email)
	user.SetPassword(req.Password)
	user.SetIsAdmin(req.IsAdmin)
	user.SetProxmoxUsername(req.ProxmoxUsername)
	user.SetProxmoxRealm("pam")
	encryptedPassword, err := EncryptStringForDatabase(req.Password)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error encrypting password: %v", err))
	}
	user.SetProxmoxPassword(encryptedPassword)

	app.RunInTransaction(func(txApp core.App) error {
		wasError := false
		defer func() {
			if wasError {
				removeUserFromHostSystem(user.ProxmoxUsername())
				removeUserFromProxmox(user.ProxmoxUsername(), "pam")
				removePool(user.UserId())
				defaultRangeRecord, findErr := txApp.FindFirstRecordByData("ranges", "rangeID", user.DefaultRangeId())
				if findErr == nil && defaultRangeRecord != nil {
					txApp.Delete(defaultRangeRecord)
				}
				txApp.Delete(user)
			}
		}()

		_, err := provisionNewUser(txApp, user, req.Password)
		if err != nil {
			wasError = true
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error provisioning user: %v", err))
		}

		response := dto.ProvisionOAuth2UserResponse{
			RecordID: user.Record.Id,
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

	userID := e.Request.PathValue("userID")
	if len(userID) == 0 {
		return JSONError(e, http.StatusBadRequest, "userID not provided")
	}

	// Deleting yourself fails as it removes the ansible home directory before the play ends and thus modules are not available to finish the play
	if userID == e.Auth.GetString("userID") {
		return JSONError(e, http.StatusBadRequest, "You cannot remove yourself")
	}

	// Don't allow the user to delete the root user
	if userID == "ROOT" {
		return JSONError(e, http.StatusBadRequest, "You cannot delete the root user")
	}

	var user models.User
	userRecord, err := app.FindFirstRecordByData("users", "userID", userID)
	if err != nil && err == sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("User was not found in the Ludus database: %v", err))
	} else if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
	}
	if userRecord == nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User record for %s is nil", userID))
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

	err = removeUserFromProxmox(user.ProxmoxUsername(), "pam")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing user from Proxmox: %v", err))
	}

	if e.Request.URL.Query().Get("deleteDefaultRange") == "true" {
		// Get proxmox client and range object
		proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
		targetRangeRaw, err := app.FindFirstRecordByData("ranges", "rangeID", user.DefaultRangeId())
		if err != nil && err != sql.ErrNoRows {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding range: %v", err))
		} else if err != nil && err == sql.ErrNoRows {
			// Range is already gone
			// pass
		} else if targetRangeRaw != nil {
			targetRange := &models.Range{}
			targetRange.SetProxyRecord(targetRangeRaw)
			err = updateRangeVMData(e, targetRange, proxmoxClient)
			if err != nil {
				return JSONError(e, http.StatusInternalServerError, err.Error())
			}
			err = deleteRangeResources(targetRange, true, e)
			if err != nil {
				return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting range resources: %v", err))
			}
		}
	}
	err = removeUserFromHostSystem(user.ProxmoxUsername())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing user from host system: %v", err))
	}
	err = app.Delete(userRecord)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error deleting user: %v", err))
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
		UserID: user.UserId(),
	}
	response := dto.GetAPIKeyResponse{
		Result: &result,
	}

	return e.JSON(http.StatusCreated, response)
}

// GetCredentials - get the proxmox creds for the user
func GetCredentials(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	proxmoxPassword := user.ProxmoxPassword()
	if proxmoxPassword == "" {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Ludus does not know the Proxmox password for user %s", user.UserId()))
	}
	decryptedPassword, err := DecryptStringFromDatabase(proxmoxPassword)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error decrypting Proxmox password: %v", err))
	}
	result := dto.GetCredentialsResponseResult{
		LudusEmail:      user.Email(),
		ProxmoxUsername: user.ProxmoxUsername(),
		ProxmoxRealm:    user.ProxmoxRealm(),
		ProxmoxPassword: decryptedPassword,
	}
	response := dto.GetCredentialsResponse{
		Result: &result,
	}
	return e.JSON(http.StatusOK, response)
}

// GetWireguardConfig - retrieves a WireGuard configuration file for the user
func GetWireguardConfig(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	wireGuardConfig, err := getWireGuardConfigForUser(user)
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

// GetDefaultRangeID - retrieves the default range ID for the user
func GetDefaultRangeID(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	defaultRangeID := user.DefaultRangeId()
	if defaultRangeID == "" {
		return JSONError(e, http.StatusNotFound, "User has no default range")
	}
	response := dto.GetOrPostDefaultRangeIDResponse{
		DefaultRangeID: defaultRangeID,
	}
	return e.JSON(http.StatusOK, response)
}

// SetDefaultRangeID - sets the default range ID for the user
func SetDefaultRangeID(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)

	var setDefaultRangeJSON dto.PostDefaultRangeIDRequest
	e.BindBody(&setDefaultRangeJSON)

	if setDefaultRangeJSON.DefaultRangeID == "" {
		return JSONError(e, http.StatusBadRequest, "defaultRangeID is required")
	}

	// Validate that the range exists
	rangeNumber, err := GetRangeNumberFromRangeID(setDefaultRangeJSON.DefaultRangeID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found: %v", setDefaultRangeJSON.DefaultRangeID, err))
	}

	// Check that the user has access to this range (unless admin)
	if !user.IsAdmin() && !HasRangeAccess(e, user.UserId(), rangeNumber) {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("User %s does not have access to range %s", user.UserId(), setDefaultRangeJSON.DefaultRangeID))
	}

	// Update the user's default range ID
	user.SetDefaultRangeId(setDefaultRangeJSON.DefaultRangeID)
	err = app.Save(user)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Unable to set defaultRangeID for user %s: %v", user.UserId(), err))
	}

	response := dto.GetOrPostDefaultRangeIDResponse{
		DefaultRangeID: setDefaultRangeJSON.DefaultRangeID,
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

	users := make([]dto.ListAllUsersResponseItem, 0)
	for _, userRecord := range usersRecords {
		if userRecord.GetString("userID") == "ROOT" {
			continue
		}
		userModel := &models.User{}
		userModel.SetProxyRecord(userRecord)
		users = append(users, dto.ListAllUsersResponseItem{
			Name:            userModel.Name(),
			UserID:          userModel.UserId(),
			UserNumber:      userModel.UserNumber(),
			DateCreated:     userModel.Created().Time(),
			DateLastActive:  userModel.LastActive().Time(),
			IsAdmin:         userModel.IsAdmin(),
			ProxmoxUsername: userModel.ProxmoxUsername(),
		})
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

// PostCredentials - updates the users Ludus and proxmox password
func PostCredentials(e *core.RequestEvent) error {

	var credsToUpdate dto.PostCredentialsRequest
	e.BindBody(&credsToUpdate)
	if credsToUpdate.ProxmoxPassword == "" {
		return JSONError(e, http.StatusBadRequest, "Missing proxmoxPassword value")
	}
	if credsToUpdate.UserID == "" {
		return JSONError(e, http.StatusBadRequest, "Missing userID value")
	}

	userRecord, err := app.FindFirstRecordByData("users", "userID", credsToUpdate.UserID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding user: %v", err))
	}
	if userRecord == nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("User %s not found", credsToUpdate.UserID))
	}
	user := &models.User{}
	user.SetProxyRecord(userRecord)

	if user.UserId() == "ROOT" {
		return JSONError(e, http.StatusBadRequest, "You cannot update the password for the root user")
	}

	actingUser := e.Get("user").(*models.User)
	if !actingUser.IsAdmin() && actingUser.UserId() != user.UserId() {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot update the password for another user")
	}

	err = setProxmoxSystemPassword(user.ProxmoxUsername(), user.ProxmoxRealm(), credsToUpdate.ProxmoxPassword)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error setting Proxmox system password: %v", err))
	}

	// Encrypt the new password
	encryptedPassword, err := EncryptStringForDatabase(credsToUpdate.ProxmoxPassword)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error encrypting Proxmox password: %v", err))
	}

	// Update the user record with the new password
	user.SetProxmoxPassword(encryptedPassword)
	user.SetPassword(credsToUpdate.ProxmoxPassword)
	err = app.Save(user)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving user: %v", err))
	}

	// File saved successfully. Return proper result
	response := dto.PostCredentialsResponse{
		Result: fmt.Sprintf("The Ludus and Proxmox password for %s has been successfully updated", user.UserId()),
	}
	return e.JSON(http.StatusOK, response)
}
