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
	"os/user"
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
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
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

// Get the proxmox password for a user
// Sets the context JSON error and returns an empty string on error
func getProxmoxPasswordForUser(user UserObject, c *gin.Context) string {
	if user.ProxmoxUsername == "root" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "The ROOT API key should only be used to create other admin users. Use the command: ludus users add --admin --name 'first last' --userid FL"})
		return ""
	}
	proxmoxPassword, err := GetFileContents(fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, user.ProxmoxUsername))

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return ""
	}
	return strings.TrimSuffix(proxmoxPassword, "\n")
}

// Gets a user object from the query string (if the user is an admin) or from
// API key context. Sets the return status and message when returning an error
func getUserObject(c *gin.Context) (UserObject, error) {
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

	usersRange.UserID = userID
	result := db.First(&usersRange, "user_id = ?", userID)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "UserID " + userID + " has no range"})
		return usersRange, gorm.ErrRecordNotFound
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

	proxmoxClient, err := getProxmoxClientForUser(c)
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

		thisVM.RangeNumber = usersRange.RangeNumber
		thisVM.Name = vm["name"].(string)
		if vm["status"].(string) == "running" {
			thisVM.PoweredOn = true
		} else {
			thisVM.PoweredOn = false
		}

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
