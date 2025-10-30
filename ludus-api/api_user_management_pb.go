package ludusapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"

	"ludusapi/dto"
	"ludusapi/models"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"gorm.io/gorm"
)

// AddUser - adds a user to the system
func AddUserPocketBase(c *core.RequestEvent) error {

	if !c.Auth.GetBool("isAdmin") {
		return JSONError(c, http.StatusUnauthorized, "You are not an admin and cannot add users")
	}

	if os.Geteuid() != 0 {
		return JSONError(c, http.StatusForbidden, "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users add command with --url https://127.0.0.1:8081")
	}

	var addUserJSON dto.AddUserJSONBody
	c.BindBody(&addUserJSON)

	if addUserJSON.Name == "" || addUserJSON.UserID == "" {
		return JSONError(c, http.StatusBadRequest, "Name and userID are required")
	}

	if !UserIDRegex.MatchString(addUserJSON.UserID) {
		return JSONError(c, http.StatusBadRequest, "provided userID does not match ^[A-Za-z0-9]{1,20}$")
	}

	// ADMIN is the pool used for generally available VMs (Nexus cache)
	// ROOT is the ID of the root ludus user
	// CICD is the ID of the CI/CD user
	// SHARED is the pool used for shared VMs (templates)
	// 0 is a bug in proxmox where creating a resource pool with ID 0 succeeds but doesn't actually create the pool
	reservedUserIDs := []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}

	// Do not allow users to be created with the reserved user IDs
	if slices.Contains(reservedUserIDs, addUserJSON.UserID) {
		return JSONError(c, http.StatusBadRequest, fmt.Sprintf("%s is a reserved user ID", addUserJSON.UserID))
	}

	// Validate there is an email and password
	if addUserJSON.Email == "" || addUserJSON.Password == "" {
		return JSONError(c, http.StatusBadRequest, "Email and password are required")
	}

	// Check that the password is at least 8 characters long
	if len(addUserJSON.Password) < 8 {
		return JSONError(c, http.StatusBadRequest, "Password must be at least 8 characters long")
	}

	// Check if the user already exists
	matchingUsers, err := app.CountRecords("users", dbx.HashExp{"userID": addUserJSON.UserID})
	if err != nil {
		return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error checking if user already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(c, http.StatusBadRequest, "User with that ID already exists")
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
		return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error checking if username already exists: %v", err))
	}
	if matchingUsers > 0 {
		return JSONError(c, http.StatusBadRequest, "User with that name already exists")
	}

	// Check if the username already exists on the host system
	if userExistsOnHostSystem(user.ProxmoxUsername()) {
		return JSONError(c, http.StatusBadRequest, "User with that name already exists on the host system. Ludus uses the PAM for user authentication, so you must use a unique username for each Ludus user.")
	}

	if poolExists(user.UserId()) {
		return JSONError(c, http.StatusBadRequest, fmt.Sprintf("Pool with the name %s already exists", user.UserId()))
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
		err := CreateDefaultUserRangePB(txApp, user.UserId())
		if err != nil {
			return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating user's default range: %v", err))
		}

		// Get the next available user number
		user.SetUserNumber(findNextAvailableUserNumberPB(txApp))

		// Refuse to create more than 150 users
		if user.UserNumber() > 150 {
			return JSONError(c, http.StatusBadRequest, "Cannot create more than 150 users per Ludus due to networking constraints")
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
		output, err := server.RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false, "")
		if err != nil {
			return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error running ansible playbook: %v", output))
		}

		apiKey := GenerateAPIKeyPB(user.UserId())
		hashedAPIKey, err := HashString(apiKey)
		if err != nil {
			return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error hashing API key: %v", err))
		}
		user.SetHashedApikey(hashedAPIKey)

		// Create a Proxmox API Token for the user
		tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user.ProxmoxUsername())
		if err != nil {
			wasError = true
			return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error creating Proxmox API token: %v", err))
		}

		encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
		if err != nil {
			wasError = true
			return JSONError(c, http.StatusInternalServerError, fmt.Sprintf("Error encrypting Proxmox API token secret: %v", err))
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
			APIKey:          apiKey,
		}
		return c.JSON(http.StatusCreated, response)
	})
	return nil
}

// DeleteUser - removes a user to the system
func DeleteUserPB(c *gin.Context) {
	if !isAdmin(c, true) {
		return
	}

	if os.Geteuid() != 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users rm command with --url https://127.0.0.1:8081"})
		return
	}

	userID := c.Param("userID")
	if len(userID) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "userID not provided"})
		return
	}

	// Deleting yourself fails as it removes the ansible home directory before the play ends and thus modules are not available to finish the play
	if userID == c.GetString("userID") {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "You cannot remove yourself"})
		return
	}

	var user UserObject
	result := db.First(&user, "user_id = ?", userID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s not found", userID)})
		return
	}

	playbook := []string{ludusInstallPath + "/ansible/user-management/del-user.yml"}
	extraVars := map[string]interface{}{
		"username":      user.ProxmoxUsername,
		"user_id":       user.UserID,
		"user_number":   user.UserNumber,
		"user_is_admin": user.IsAdmin,
	}
	output, err := server.RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}

	err = db.Delete(&user).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Remove the user user from the UserRangeAccess and UserGroupMembership tables
	err = db.Delete(&UserRangeAccess{}, "user_id = ?", user.UserID).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	err = db.Delete(&UserGroupMembership{}, "user_id = ?", user.UserID).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	removeUserFromProxmox(user.ProxmoxUsername, "pam")
	removeUserFromPocketBaseByID(user.PocketbaseID)

	c.JSON(http.StatusOK, gin.H{"result": "User deleted"})
}

// GetAPIKey - reset and retrieve the Ludus API key for the user
func GetAPIKeyPB(c *gin.Context) {

	userID, success := getUserID(c)
	if !success {
		return
	}

	var user UserObject
	db.First(&user, "user_id = ?", userID)

	// User not found
	if user.UserID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found for UserID: " + userID})
		return
	}

	// Generate a new API and update the database
	apiKey := GenerateAPIKey(&user)
	user.HashedAPIKey, _ = HashString(apiKey)
	db.Model(&user).Where("user_id = ?", userID).Update("hashed_api_key", user.HashedAPIKey)

	// Add the plaintext API Key to this response only
	var userDataWithAPIKey map[string]interface{}
	userMap, _ := json.Marshal(user)
	json.Unmarshal(userMap, &userDataWithAPIKey)
	userDataWithAPIKey["apiKey"] = apiKey

	c.JSON(http.StatusCreated, gin.H{"result": userDataWithAPIKey})
}

// GetCredentials - get the proxmox creds for the user
func GetCredentialsPB(c *gin.Context) {
	user, err := GetUserObject(c)
	if err != nil {
		return // JSON set in GetUserObject
	}
	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return // JSON status set in getProxmoxPasswordForUser
	}
	c.JSON(http.StatusOK, gin.H{"result": map[string]interface{}{"proxmoxUsername": user.ProxmoxUsername, "proxmoxPassword": proxmoxPassword}})
}

// GetWireguardConfig - retrieves a WireGuard configuration file for the user
func GetWireguardConfigPB(c *gin.Context) {
	user, err := GetUserObject(c)
	if err != nil {
		return // JSON set in GetUserObject
	}
	wireGuardConfig, err := GetFileContents(fmt.Sprintf("%s/users/%s/%s_client.conf", ludusInstallPath, user.ProxmoxUsername, user.UserID))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": map[string]interface{}{"wireGuardConfig": wireGuardConfig}})
}

// ListAllUsers - lists all users
func ListAllUsersPB(c *gin.Context) {

	if !isAdmin(c, true) {
		return
	}

	var users []UserObject
	db.Find(&users)

	c.JSON(http.StatusOK, users)
}

// ListUser - lists user details
func ListUserPB(c *gin.Context) {

	var users []UserObject

	userID, success := getUserID(c)
	if !success {
		return
	}

	db.First(&users, "user_id = ?", userID)

	c.JSON(http.StatusOK, users)
}

// PasswordReset - resets a user's proxmox password
func PasswordResetPB(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Not implemented"})
}

// PostCredentials - updates the users proxmox password
func PostCredentialsPB(c *gin.Context) {
	callingUser, err := GetUserObject(c)
	if err != nil {
		return // JSON set in GetUserObject
	}

	type PostCredentials struct {
		UserID          string `json:"userID"`
		ProxmoxPassword string `json:"proxmoxPassword"`
	}

	var credsToUpdate PostCredentials
	c.Bind(&credsToUpdate)

	if credsToUpdate.ProxmoxPassword == "" {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "Missing proxmoxPassword value"})
		return
	}

	if credsToUpdate.UserID != "" {
		// userID provided, make sure the user is an admin
		if !isAdmin(c, true) {
			return
		}
	} else {
		credsToUpdate.UserID = callingUser.UserID
	}
	var user UserObject
	result := db.First(&user, "user_id = ?", credsToUpdate.UserID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	filePath := fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, user.ProxmoxUsername)
	err = os.WriteFile(filePath, []byte(credsToUpdate.ProxmoxPassword), 0660)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Unable to save the range config"})
		return
	}

	// File saved successfully. Return proper result
	c.JSON(http.StatusOK, gin.H{"result": "Your proxmox password has been successfully updated in Ludus. THIS DOES NOT UPDATE THE PROXMOX PASSWORD ON THE HOST SYSTEM. YOU MUST UPDATE THE PROXMOX PASSWORD ON THE HOST SYSTEM MANUALLY TO MATCH THE PASSWORD YOU SET IN LUDUS."})
}
