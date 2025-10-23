package ludusapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var UserIDRegex = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

type UserWithEmailAndPassword struct {
	UserObject
	Password string `json:"password"`
	Email    string `json:"email"`
}

// AddUser - adds a user to the system
func AddUser(c *gin.Context) {

	if !isAdmin(c, true) {
		return
	}

	if os.Geteuid() != 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users add command with --url https://127.0.0.1:8081"})
		return
	}

	var user UserWithEmailAndPassword
	c.Bind(&user)

	if user.Name == "" || user.UserID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name and userID are required"})
		return
	}

	if !UserIDRegex.MatchString(user.UserID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provided userID does not match ^[A-Za-z0-9]{1,20}$"})
		return
	}

	// ADMIN is the pool used for generally available VMs (Nexus cache)
	// ROOT is the ID of the root ludus user
	// CICD is the ID of the CI/CD user
	// SHARED is the pool used for shared VMs (templates)
	// 0 is a bug in proxmox where creating a resource pool with ID 0 succeeds but doesn't actually create the pool
	reservedUserIDs := []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}

	// Do not allow users to be created with the reserved user IDs
	if slices.Contains(reservedUserIDs, user.UserID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is a reserved user ID", user.UserID)})
		return
	}

	// Validate there is an email and password
	if user.Email == "" || user.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email and password are required"})
		return
	}

	// Check that the password is at least 8 characters long
	if len(user.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 8 characters long"})
		return
	}

	var users []UserObject
	db.First(&users, "user_id = ?", user.UserID)
	if len(users) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User with that ID already exists"})
		return
	}
	// Convert to lower-case, and replace spaces with "-"
	user.ProxmoxUsername = strings.ReplaceAll(strings.ToLower(user.Name), " ", "-")

	db.First(&users, "proxmox_username = ?", user.ProxmoxUsername)
	if len(users) > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User with that name already exists"})
		return
	}

	// Check if the username already exists on the host system
	if userExistsOnHostSystem(user.ProxmoxUsername) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User with that name already exists on the host system. Ludus uses the PAM for user authentication, so you must use a unique username for each Ludus user."})
		return
	}

	if poolExists(user.UserID) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Pool with the name %s already exists", user.UserID)})
		return
	}

	// Start database transaction and setup the error handling to clean up if anything fails during user creation
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to start database transaction: %v", tx.Error)})
		return
	}
	wasError := false
	defer func() {
		if wasError {
			tx.Rollback()
			// Remove the user from the host system - we validated the user did not exist on the host system before running the playbook, so this is safe to do
			removeUserFromHostSystem(user.ProxmoxUsername)
			removeUserFromProxmox(user.ProxmoxUsername, "pam")
			removeUserFromPocketBaseByID(user.PocketbaseID)
			removePool(user.UserID)
		}
	}()

	// Create a default range for the user using the new utility function, also creates a UserRangeAccess record
	err := CreateDefaultUserRange(tx, user.UserID)
	if err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating user's default range: %v", err)})
		return
	}

	// Get the next available user number
	user.UserNumber = findNextAvailableUserNumber(db)

	// Refuse to create more than 150 users
	if user.UserNumber > 150 {
		tx.Rollback()
		c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot create more than 150 users per Ludus due to networking constraints"})
		return
	}

	playbook := []string{ludusInstallPath + "/ansible/user-management/add-user.yml"}
	extraVars := map[string]interface{}{
		"username":          user.ProxmoxUsername,
		"user_id":           user.UserID,
		"user_number":       user.UserNumber,
		"proxmox_public_ip": ServerConfiguration.ProxmoxPublicIP,
		"user_is_admin":     user.IsAdmin,
		"proxmox_password":  user.Password,
	}
	output, err := server.RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false, "")
	if err != nil {
		wasError = true
		if !c.Writer.Written() {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": output})
		}
		return
	}

	user.DateCreated = time.Now()
	user.DateLastActive = time.Now()
	apiKey := GenerateAPIKey(&user.UserObject)
	user.HashedAPIKey, _ = HashString(apiKey)

	// Create a new user in Pocketbase
	pocketBaseUserID, err := createUserInPocketBase(user, user.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		wasError = true
		return
	}
	user.PocketbaseID = pocketBaseUserID

	// Create a Proxmox API Token for the user
	tokenID, tokenSecret, err := createProxmoxAPITokenForUserWithoutContext(user.UserObject)
	if err != nil {
		wasError = true
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	encryptedTokenSecret, err := EncryptStringForDatabase(tokenSecret)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		wasError = true
		return
	}

	user.ProxmoxTokenID = tokenID
	user.ProxmoxTokenSecret = encryptedTokenSecret

	// Create the user in the database
	err = tx.Create(&user.UserObject).Error
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		wasError = true
		return
	}

	// Commit the transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to commit database transaction: %v", err)})
		wasError = true
		return
	}

	// Add the plaintext API Key to this response only
	var userDataWithAPIKey map[string]interface{}
	userMap, _ := json.Marshal(user.UserObject)
	json.Unmarshal(userMap, &userDataWithAPIKey)
	userDataWithAPIKey["apiKey"] = apiKey
	delete(userDataWithAPIKey, "HashedAPIKey")

	c.JSON(http.StatusCreated, gin.H{"result": userDataWithAPIKey})

}

// DeleteUser - removes a user to the system
func DeleteUser(c *gin.Context) {
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
func GetAPIKey(c *gin.Context) {

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
func GetCredentials(c *gin.Context) {
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
func GetWireguardConfig(c *gin.Context) {
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
func ListAllUsers(c *gin.Context) {

	if !isAdmin(c, true) {
		return
	}

	var users []UserObject
	db.Find(&users)

	c.JSON(http.StatusOK, users)
}

// ListUser - lists user details
func ListUser(c *gin.Context) {

	var users []UserObject

	userID, success := getUserID(c)
	if !success {
		return
	}

	db.First(&users, "user_id = ?", userID)

	c.JSON(http.StatusOK, users)
}

// PasswordReset - resets a user's proxmox password
func PasswordReset(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": "Not implemented"})
}

// PostCredentials - updates the users proxmox password
func PostCredentials(c *gin.Context) {
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
