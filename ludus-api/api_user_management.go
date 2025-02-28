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

// AddUser - adds a user to the system
func AddUser(c *gin.Context) {

	if !isAdmin(c, true) {
		return
	}

	if os.Geteuid() != 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users add command with --url https://127.0.0.1:8081"})
		return
	}

	callingUser, err := GetUserObject(c)
	if err != nil {
		return // JSON set in GetUserObject
	}

	var user UserObject
	c.Bind(&user)

	if user.Name != "" && user.UserID != "" {
		if UserIDRegex.MatchString(user.UserID) {

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

			// Make a range for the user
			var usersRange RangeObject
			usersRange.UserID = user.UserID
			usersRange.NumberOfVMs = 0
			usersRange.TestingEnabled = false

			// Find the next available range number for the new user
			usersRange.RangeNumber = findNextAvailableRangeNumber(db, ServerConfiguration.ReservedRangeNumbers)

			result := db.Create(&usersRange)
			if result.Error != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error creating user's range object (range number %d): %v", usersRange.RangeNumber, result.Error)})
				return
			}
			// Query the DB to get the autoincremented rangeID
			db.First(&usersRange, "user_id = ?", user.UserID)

			// Refuse to create more than 150 users
			if usersRange.RangeNumber > 150 {
				// Remove the usersRange from the database
				db.Delete(&usersRange)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot create more than 150 users per Ludus due to networking constraints"})
				return
			}

			playbook := []string{ludusInstallPath + "/ansible/user-management/add-user.yml"}
			extraVars := map[string]interface{}{
				"username":            user.ProxmoxUsername,
				"user_range_id":       user.UserID,
				"second_octet":        usersRange.RangeNumber,
				"proxmox_public_ip":   ServerConfiguration.ProxmoxPublicIP,
				"user_is_admin":       user.IsAdmin,
				"portforward_enabled": user.PortforwardingEnabled,
			}
			output, err := server.RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false, "")
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": output})
				// Remove the range record since creation failed
				db.Where("user_id = ?", user.UserID).Delete(&usersRange)
				return
			}
			// If this endpoint is called by a user that is not ROOT and this is their first ansible action, their log file will be owned by root
			// Chown the ansible log file to ludus to prevent errors when they use the normal ludus endpoint (which runs as ludus)
			chownFileToUsername(fmt.Sprintf("%s/users/%s/ansible.log", ludusInstallPath, callingUser.ProxmoxUsername), "ludus")

			user.DateCreated = time.Now()
			user.DateLastActive = time.Now()
			apiKey := GenerateAPIKey(&user)
			user.HashedAPIKey, _ = HashString(apiKey)
			db.Create(&user)

			// Add the plaintext API Key to this response only
			var userDataWithAPIKey map[string]interface{}
			userMap, _ := json.Marshal(user)
			json.Unmarshal(userMap, &userDataWithAPIKey)
			userDataWithAPIKey["apiKey"] = apiKey
			delete(userDataWithAPIKey, "HashedAPIKey")

			c.JSON(http.StatusCreated, gin.H{"result": userDataWithAPIKey})
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "provided userID does not match ^[A-Za-z0-9]{1,20}$"})
		}

	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Fields are empty"})
	}
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
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s not found", user.UserID)})
		return
	}

	var usersRange RangeObject
	result = db.First(&usersRange, "user_id = ?", user.UserID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("User %s range not found", user.UserID)})
		return
	}

	var usersAccess RangeAccessObject
	rangeAccessResult := db.First(&usersAccess, "target_user_id = ?", user.UserID)
	if !errors.Is(rangeAccessResult.Error, gorm.ErrRecordNotFound) && rangeAccessResult.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting user access grants for %s", user.UserID)})
		return
	}

	// Search for any RangeAccessObjects where the user.UserID is in the SourceUserIDs array
	var userAccessGrants []RangeAccessObject
	rangeAccessGrants := db.Where("source_user_ids LIKE ?", fmt.Sprintf("%%%s%%", user.UserID)).Find(&userAccessGrants)
	if !errors.Is(rangeAccessGrants.Error, gorm.ErrRecordNotFound) && rangeAccessGrants.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error getting user access grants that contain user %s", user.UserID)})
		return
	}

	var errorUserIDs []string
	// Remove any RangeAccessObjects where the user.UserID is in the SourceUserIDs array
	if rangeAccessGrants.RowsAffected != 0 {
		// Loop over rows returned in the userAccessGrants object
		for _, accessObject := range userAccessGrants {
			// Remove the user from the SourceUserIDs array
			accessObject.SourceUserIDs = removeStringExact(accessObject.SourceUserIDs, user.UserID)
			if len(accessObject.SourceUserIDs) == 0 {
				// If the SourceUserIDs array is empty, delete the object
				db.Delete(&accessObject)
			} else {
				db.Save(&accessObject)
			}
			// Revoke the user being deleted access to the range
			var targetUserRangeObject RangeObject
			db.First(&targetUserRangeObject, "user_id = ?", accessObject.TargetUserID)

			var tarGetUserObject UserObject
			db.First(&tarGetUserObject, "user_id = ?", accessObject.TargetUserID)

			var sourceUserRangeObject RangeObject
			db.First(&sourceUserRangeObject, "user_id = ?", user.UserID)

			extraVars := map[string]interface{}{
				"target_username":           tarGetUserObject.ProxmoxUsername,
				"target_range_id":           tarGetUserObject.UserID,
				"target_range_second_octet": targetUserRangeObject.RangeNumber,
				"source_username":           user.ProxmoxUsername,
				"source_range_id":           user.UserID,
				"source_range_second_octet": sourceUserRangeObject.RangeNumber,
			}
			output, err := server.RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/range-access.yml"}, nil, extraVars, "revoke", false, "")
			if err != nil {
				routerWANFatalRegex := regexp.MustCompile(`fatal:.*?192\.0\.2\\"`)
				if routerWANFatalRegex.MatchString(output) {
					errorUserIDs = append(errorUserIDs, accessObject.TargetUserID)
				}
			}
		}
	}

	playbook := []string{ludusInstallPath + "/ansible/user-management/del-user.yml"}
	extraVars := map[string]interface{}{
		"username":      user.ProxmoxUsername,
		"user_range_id": user.UserID,
		"second_octet":  usersRange.RangeNumber,
		"user_is_admin": user.IsAdmin,
	}
	output, err := server.RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false, "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}

	// There are users with access to this user's range, revoke their access
	if !errors.Is(rangeAccessResult.Error, gorm.ErrRecordNotFound) {
		db.Delete(&usersAccess, "target_user_id = ?", user.UserID)
		for _, userID := range usersAccess.SourceUserIDs {
			// Remove the network range of the user being deleted from each source user's WireGuard config
			var sourceUser UserObject
			db.First(&sourceUser, "user_id = ?", userID)
			sourceUserWireGuardConfigPath := fmt.Sprintf("%s/users/%s/%s_client.conf", ludusInstallPath, sourceUser.ProxmoxUsername, sourceUser.UserID)
			sourceUserWireGuardConfig, err := GetFileContents(sourceUserWireGuardConfigPath)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error reading WireGuard config for user %s", sourceUser.UserID)})
				return
			}
			// Replace the network with an empty string, it may end in a comma or not
			sourceUserWireGuardConfig = strings.ReplaceAll(sourceUserWireGuardConfig, fmt.Sprintf("10.%d.0.0/16, ", usersRange.RangeNumber), "")
			sourceUserWireGuardConfig = strings.ReplaceAll(sourceUserWireGuardConfig, fmt.Sprintf(", 10.%d.0.0/16", usersRange.RangeNumber), "")
			err = os.WriteFile(sourceUserWireGuardConfigPath, []byte(sourceUserWireGuardConfig), 0660)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error writing WireGuard config for user %s", sourceUser.UserID)})
				return
			}
		}
	}
	db.Delete(&user, "user_id = ?", userID)
	db.Delete(&usersRange, "user_id = ?", user.UserID)

	if len(errorUserIDs) > 0 {
		c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("User deleted but access was not revoked from ranges: %v\nIf these users do not have a range deployed, this is ok.\nOtherwise, the next user created with range number %d could have access to their range if they modify their WireGuard config manually.", errorUserIDs, usersRange.RangeNumber)})
		return
	} else {
		c.JSON(http.StatusOK, gin.H{"result": "User deleted"})
	}
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
	c.JSON(http.StatusOK, gin.H{"result": "Your proxmox password has been successfully updated."})
}
