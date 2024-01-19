package ludusapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var UserIDRegex = regexp.MustCompile(`^[A-Za-z0-9]{1,20}$`)

// AddUser - adds a user to the system
func AddUser(c *gin.Context) {

	if !isAdmin(c) {
		return
	}

	if os.Geteuid() != 0 {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "You must use the ludus-admin server on 127.0.0.1:8081 to use this endpoint.\nUse SSH to tunnel to this port with the command: ssh -L 8081:127.0.0.1:8081 root@<ludus IP>\nIn a different terminal re-run the ludus users add command with --url https://127.0.0.1:8081"})
		return
	}

	var user UserObject
	c.Bind(&user)

	if user.Name != "" && user.UserID != "" {
		if UserIDRegex.MatchString(user.UserID) {

			if user.UserID == "ADMIN" || user.UserID == "ROOT" || user.UserID == "CICD" {
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("%s is a reserved user ID", user.UserID)})
				return
			}

			var users []UserObject
			db.First(&users, "user_id = ?", user.UserID)
			if len(users) > 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "User already exists"})
				return
			}
			// Convert to lower-case, and replace spaces with "-"
			user.ProxmoxUsername = strings.ReplaceAll(strings.ToLower(user.Name), " ", "-")

			// Make a range for the user
			var usersRange RangeObject
			usersRange.UserID = user.UserID
			usersRange.NumberOfVMs = 0
			usersRange.TestingEnabled = false
			db.Create(&usersRange)
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
				"username":          user.ProxmoxUsername,
				"user_range_id":     user.UserID,
				"second_octet":      usersRange.RangeNumber,
				"proxmox_public_ip": ServerConfiguration.ProxmoxPublicIP,
				"user_is_admin":     user.IsAdmin,
			}
			output, err := RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": output})
				// Remove the range record since creation failed
				db.Where("user_id = ?", user.UserID).Delete(&usersRange)
				return
			}

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
	if !isAdmin(c) {
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

	var user UserObject
	result := db.First(&user, "user_id = ?", userID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	var usersRange RangeObject
	result = db.First(&usersRange, "user_id = ?", user.UserID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "User's range not found"})
		return
	}

	playbook := []string{ludusInstallPath + "/ansible/user-management/del-user.yml"}
	extraVars := map[string]interface{}{"username": user.ProxmoxUsername, "user_range_id": user.UserID, "second_octet": usersRange.RangeNumber}
	output, err := RunAnsiblePlaybookWithVariables(c, playbook, []string{}, extraVars, "", false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}

	db.Delete(&user, "user_id = ?", userID)
	db.Delete(&usersRange, "user_id = ?", user.UserID)

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
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return // JSON status set in getProxmoxPasswordForUser
	}
	c.JSON(http.StatusOK, gin.H{"result": map[string]interface{}{"proxmoxUsername": user.ProxmoxUsername, "proxmoxPassword": proxmoxPassword}})
}

// GetWireguardConfig - retrieves a WireGuard configuration file for the user
func GetWireguardConfig(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
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

	if !isAdmin(c) {
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
	callingUser, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
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
		if !isAdmin(c) {
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
