package ludusapi

import (
	crypto_rand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"

	"github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// checkEmbeddedDocs checks if the embeddedDocs variable is populated
func checkEmbeddedDocs() bool {
	// Try to read the contents of the 'docs' directory
	dirEntries, err := fs.ReadDir(embeddedDocs, "docs")
	if err != nil {
		// If there's an error, it means the variable is not properly populated
		return false
	}

	// Check if the directory is empty
	if len(dirEntries) == 0 {
		return false
	}

	return true
}

func HashString(password string) (string, error) {
	// Use a lower cost for the hash than the recommended 14
	// We have to hash the API key each request during the compare, so we don't want to use too much CPU
	// Also, since the API keys are generated as a random string with 243 bits of entropy,
	// this offsets the "low cost" of the hash as it would still take
	// ~ 2.5697 × 10^55 years to crack an API key with 100 million parallel guesses and a average time of .003 seconds per guess
	// For comparison, the universe is about 13.8 × 10^9 years old
	// High cost is good for users who pick bad passwords, but since we generate random keys, it is not a risk here
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 6)
	return string(bytes), err
}

func CheckHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GenerateAPIKey(user *UserObject) string {
	var bytes [8]byte
	_, err := crypto_rand.Read(bytes[:])
	if err != nil {
		panic("cannot seed math/rand package with cryptographically secure random number generator")
	}
	chars := []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789" +
		"@%-_+=") // Should be shell safe for setting the api key in an env var without single quotes
	length := 40
	var stringBuilder strings.Builder
	// Add the userID to the front of the API Key
	stringBuilder.WriteString(user.UserID)
	stringBuilder.WriteRune('.')
	for i := 0; i < length; i++ {
		stringBuilder.WriteRune(chars[rand.Intn(len(chars))])
	}
	return stringBuilder.String()
}

// Return the userID, either from API key context
// or from the query string - only if the user is an Admin
func getUserID(c *gin.Context) (string, bool) {

	userID, userIDInQueryString := c.GetQuery("userID")
	if !userIDInQueryString || userID == "" {
		userID = c.GetString("userID")
	} else {
		// If the userID was provided and is different from the API key value, make sure the request came from an admin
		if userID != c.GetString("userID") && !isAdmin(c, true) {
			return "", false // JSON set in isAdmin
		}
	}
	if UserIDRegex.MatchString(userID) {
		return userID, true
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provided userID does not match ^[A-Za-z0-9]{1,20}$"})
		return "", false
	}
}

type IP struct {
	Query string
}

func GetPublicIPviaAPI() string {
	req, err := http.Get("http://ip-api.com/json/")
	if err != nil {
		return err.Error()
	}
	defer req.Body.Close()

	body, err := io.ReadAll(req.Body)
	if err != nil {
		return err.Error()
	}

	var ip IP
	json.Unmarshal(body, &ip)

	return ip.Query
}

func removeStringExact(slice []string, target string) []string {
	var result []string
	for _, s := range slice {
		if s != target {
			result = append(result, s)
		}
	}
	return result
}

func removeElementThatContainsString(slice []string, target string) []string {
	var result []string
	for _, s := range slice {
		if !strings.Contains(s, target) {
			result = append(result, s)
		}
	}
	return result
}

func containsSubstring(slice []string, target string) bool {
	for _, s := range slice {
		if strings.Contains(s, target) {
			return true
		}
	}
	return false
}

// Gets a user object from the query string (if the user is an admin) or from
// API key context. Sets the return status and message when returning an error
func GetUserObject(c *gin.Context) (UserObject, error) {
	var user UserObject

	userID, success := getUserID(c)
	if !success {
		return user, errors.New("could not get userID from request content") // Status and JSON set in getUserID
	}

	result := db.First(&user, "user_id = ?", userID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		// Must check if the header status has already been written for this request before writing to avoid a panic
		if !c.Writer.Written() {
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		}
		return user, gorm.ErrRecordNotFound
	}
	return user, nil
}

// Gets a range object for the user from the query string (if the user is an admin) or from
// API key context. Sets the return status and message when returning and error
func GetRangeObject(c *gin.Context) (RangeObject, error) {
	var usersRange RangeObject

	// If we have already stored the range object for this context, just return it
	usersRangeFromContext, usersRangeExists := c.Get("rangeObject")
	if usersRangeExists {
		return usersRangeFromContext.(RangeObject), nil // Type assert the "any" returned from c.Get to RangeObject
	}

	userID, success := getUserID(c)
	if !success {
		return usersRange, gorm.ErrRecordNotFound // Status and JSON set in getUserID
	}

	// Check if a specific range number was provided in the query
	rangeNumberStr, hasRangeNumber := c.GetQuery("rangeNumber")
	if hasRangeNumber {
		rangeNumber, parseErr := strconv.ParseInt(rangeNumberStr, 10, 32)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid range number"})
			return usersRange, errors.New("invalid range number")
		}

		// Verify user has access to this range
		if !HasRangeAccess(db, userID, int32(rangeNumber)) {
			c.JSON(http.StatusForbidden, gin.H{"error": "User does not have access to this range"})
			return usersRange, errors.New("access denied")
		}

		var rangeErr error
		usersRange, rangeErr = GetRangeObjectByNumber(db, int32(rangeNumber))
		if rangeErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Range not found"})
			return usersRange, rangeErr
		}
	} else {
		// Get user's default range (first accessible range)
		var defaultErr error
		usersRange, defaultErr = GetUserDefaultRange(db, userID)
		if defaultErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "UserID " + userID + " has no accessible ranges"})
			return usersRange, defaultErr
		}
	}

	// Set the "rangeObject" key in the gin context to avoid repeated look ups
	c.Set("rangeObject", usersRange)

	return usersRange, nil
}

// updates the VM and range data for a user extracted from the context
func updateUsersRangeVMData(c *gin.Context) error {
	usersRange, err := GetRangeObject(c)
	if err != nil {
		return errors.New("unable to get users range") // JSON error is set in getRangeObject
	}

	userID, success := getUserID(c)
	if !success {
		return errors.New("unable to get user ID") // JSON set in getUserID
	}

	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return errors.New("unable to get proxmox client")
	}

	rawVMs, err := proxmoxClient.GetVmList()
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unable to list VMs"})
		return errors.New("unable to login to list VMs")
	}

	// Clear the DB of any previous VMs for this range
	db.Where("range_number = ?", usersRange.RangeNumber).Delete(&VmObject{})

	var rangeVMCount = 0

	// Get the router VM name for this range
	routerVMName, err := GetRouterVMName(c)
	fmt.Println("routerVMName", routerVMName)
	if err != nil {
		// If we can't get the router name, continue without router identification
		routerVMName = ""
	}

	// Loop over the VMs and add them to the DB
	// Save the network for this user to compare IPs against
	_, network, _ := net.ParseCIDR(fmt.Sprintf("10.%d.0.0/16", usersRange.RangeNumber))
	vms := rawVMs["data"].([]interface{})
	for vmCounter := range vms {
		vm := vms[vmCounter].(map[string]interface{})
		// Skip shared templates
		if vm["pool"] == nil || vm["name"] == nil || vm["template"] == nil {
			continue // A vm with these values as nil will cause the conversions to panic
		}
		if vm["pool"].(string) != userID ||
			strings.HasSuffix(vm["name"].(string), "-template") ||
			int(vm["template"].(float64)) == 1 {
			continue
		}
		var thisVM VmObject
		thisVM.ProxmoxID = int32(vm["vmid"].(float64))

		// Get IP
		thisVM.IP = "null"
		vmr := proxmox.NewVmRef(int(thisVM.ProxmoxID))
		interfaces, err := proxmoxClient.GetVmAgentNetworkInterfaces(vmr)
		if err == nil {
		interfaceLoop:
			for _, thisInterface := range interfaces {
				for _, ip := range thisInterface.IpAddresses {
					if network.Contains(ip) {
						thisVM.IP = ip.String()
						break interfaceLoop
					}
				}
			}
		}
		if thisVM.IP == "null" {
			// Fetch the IP address from the user's range config if the VM is set to use force_ip
			thisVM.IP = GetIPForVMFromConfig(c, vm["name"].(string))
		}

		thisVM.RangeNumber = usersRange.RangeNumber
		thisVM.Name = vm["name"].(string)
		if vm["status"].(string) == "running" {
			thisVM.PoweredOn = true
		} else {
			thisVM.PoweredOn = false
		}

		// Check if this VM is the router
		thisVM.IsRouter = (vm["name"].(string) == routerVMName)

		db.Create(&thisVM)
		rangeVMCount += 1
	}

	db.Model(&usersRange).Update("number_of_vms", rangeVMCount)

	return nil
}

func getDomainIPString(rangeSlice []string, domain string) string {
	for _, item := range rangeSlice {
		// Extract the domain part from the current item.
		parts := strings.Split(item, " ")
		if len(parts) < 2 {
			continue
		}

		currentDomain := strings.TrimSpace(parts[0])

		// Check for an exact domain match.
		if currentDomain == domain {
			start := strings.Index(item, "(")
			end := strings.Index(item, ")")
			if start != -1 && end != -1 {
				// Extract and return the IP address.
				return item[start+1 : end]
			}
		}
	}
	// Return an empty string if the domain is not found.
	return ""
}

// Chown a file to a user and their group
func chownFileToUsername(filePath string, username string) {
	runnerUser, err := user.Lookup(username)
	if err != nil {
		fmt.Printf("Failed to lookup user %s for chown of %s\n", err, filePath)
		return
	}

	uid, err := strconv.Atoi(runnerUser.Uid)
	if err != nil {
		fmt.Printf("Failed to convert UID to integer: %s\n", err)
		return
	}

	gid, err := strconv.Atoi(runnerUser.Gid)
	if err != nil {
		fmt.Printf("Failed to convert GID to integer: %s\n", err)
		return
	}

	// Change ownership of the file
	err = os.Chown(filePath, uid, gid)
	if err != nil {
		fmt.Printf("Failed to change ownership of the file: %s\n", err)
		return
	}
}

// userExistsOnHostSystem checks if a user exists on the host system
func userExistsOnHostSystem(username string) bool {
	cmd := exec.Command("id", username)
	return cmd.Run() == nil
}

// removeUserFromHostSystem removes a user from the host system
func removeUserFromHostSystem(username string) {
	cmd := exec.Command("userdel", "-r", username)
	err := cmd.Run()
	if err != nil {
		fmt.Printf("Failed to remove user %s from host system: %s\n", username, err)
	}
}

// New utility functions for group-based access system

// HasRangeAccess checks if a user has access to a range through direct assignment or group membership
func HasRangeAccess(db *gorm.DB, userID string, rangeNumber int32) bool {
	// Check direct user-to-range assignment
	var userRangeAccess UserRangeAccess
	if err := db.Where("user_id = ? AND range_number = ?", userID, rangeNumber).First(&userRangeAccess).Error; err == nil {
		return true
	}

	// Check group-based access
	var count int64
	err := db.Table("user_group_memberships").
		Joins("JOIN group_range_accesses ON user_group_memberships.group_id = group_range_accesses.group_id").
		Where("user_group_memberships.user_id = ? AND group_range_accesses.range_number = ?", userID, rangeNumber).
		Count(&count).Error

	return err == nil && count > 0
}

// GetUserAccessibleRanges returns all range numbers a user can access
func GetUserAccessibleRanges(db *gorm.DB, userID string) []int32 {
	var rangeNumbers []int32

	// Get direct range assignments
	db.Model(&UserRangeAccess{}).
		Where("user_id = ?", userID).
		Pluck("range_number", &rangeNumbers)

	// Get group-based range access
	var groupRangeNumbers []int32
	db.Table("user_group_memberships").
		Joins("JOIN group_range_accesses ON user_group_memberships.group_id = group_range_accesses.group_id").
		Where("user_group_memberships.user_id = ?", userID).
		Pluck("group_range_accesses.range_number", &groupRangeNumbers)

	// Combine and deduplicate
	rangeMap := make(map[int32]bool)
	for _, num := range rangeNumbers {
		rangeMap[num] = true
	}
	for _, num := range groupRangeNumbers {
		rangeMap[num] = true
	}

	result := make([]int32, 0, len(rangeMap))
	for num := range rangeMap {
		result = append(result, num)
	}

	// Sort the result to ensure consistent ordering
	slices.Sort(result)

	return result
}

// GetRangeAccessibleUsers returns all users who can access a specific range
func GetRangeAccessibleUsers(db *gorm.DB, rangeNumber int32) []string {
	var userIDs []string

	// Get direct user assignments
	db.Model(&UserRangeAccess{}).
		Where("range_number = ?", rangeNumber).
		Pluck("user_id", &userIDs)

	// Get group-based user access
	var groupUserIDs []string
	db.Table("group_range_accesses").
		Joins("JOIN user_group_memberships ON group_range_accesses.group_id = user_group_memberships.group_id").
		Where("group_range_accesses.range_number = ?", rangeNumber).
		Pluck("user_group_memberships.user_id", &groupUserIDs)

	// Combine and deduplicate
	userMap := make(map[string]bool)
	for _, id := range userIDs {
		userMap[id] = true
	}
	for _, id := range groupUserIDs {
		userMap[id] = true
	}

	result := make([]string, 0, len(userMap))
	for id := range userMap {
		result = append(result, id)
	}

	// Sort the result to ensure consistent ordering
	slices.Sort(result)

	return result
}

// CreateDefaultUserRange creates a default range for a user and assigns direct access
func CreateDefaultUserRange(db *gorm.DB, userID string) error {
	// Find next available range number
	rangeNumber := findNextAvailableRangeNumber(db, ServerConfiguration.ReservedRangeNumbers)

	// Create the range with default name
	rangeObj := RangeObject{
		Name:        fmt.Sprintf("Default Range for %s", userID),
		Description: "Default range created automatically for user",
		Purpose:     "General testing and development",
		UserID:      userID,
		RangeNumber: rangeNumber,
		NumberOfVMs: 0,
		RangeState:  "NEVER DEPLOYED",
	}

	if err := db.Create(&rangeObj).Error; err != nil {
		return err
	}

	// Create direct access record
	userRangeAccess := UserRangeAccess{
		UserID:      userID,
		RangeNumber: rangeNumber,
	}

	return db.Create(&userRangeAccess).Error
}

// GetRangeObjectByNumber gets a range object by range number (for multi-range support)
func GetRangeObjectByNumber(db *gorm.DB, rangeNumber int32) (RangeObject, error) {
	var rangeObj RangeObject
	err := db.Where("range_number = ?", rangeNumber).First(&rangeObj).Error
	return rangeObj, err
}

// GetUserDefaultRange gets the default range for a user (range where user_id matches the user's ID)
func GetUserDefaultRange(db *gorm.DB, userID string) (RangeObject, error) {
	var rangeObj RangeObject

	// First try to get a range where the user_id field matches the current user's ID
	err := db.Where("user_id = ?", userID).First(&rangeObj).Error
	if err == nil {
		return rangeObj, nil
	}

	// If no range with matching user_id is found, fall back to the first accessible range
	accessibleRanges := GetUserAccessibleRanges(db, userID)
	if len(accessibleRanges) == 0 {
		return rangeObj, gorm.ErrRecordNotFound
	}

	err = db.Where("range_number = ?", accessibleRanges[0]).First(&rangeObj).Error
	return rangeObj, err
}
