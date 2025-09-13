package ludusapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/goforj/godump"
	goproxmox "github.com/luthermonson/go-proxmox"

	"github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/gin-gonic/gin"
)

func GetProxmoxClientForUser(c *gin.Context) (*proxmox.Client, error) {
	user, err := GetUserObject(c)
	if err != nil {
		return nil, errors.New("unable to get user object") // JSON error is set in GetUserObject
	}

	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return nil, errors.New("could not get proxmox password for user") // JSON set in getProxmoxPasswordForUser
	}

	// func NewClient(apiUrl string, hclient *http.Client, http_headers string, tls *tls.Config, proxyString string, taskTimeout int) (client *Client, err error) {
	proxmoxClient, err := proxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json", nil, "", &tls.Config{InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert}, "", 300)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unable to create proxmox client"})
		return nil, errors.New("unable to create proxmox client")
	}
	err = proxmoxClient.Login(user.ProxmoxUsername+"@pam", proxmoxPassword, "")
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "unable to login to proxmox: " + err.Error()})
		return nil, errors.New("unable to login to proxmox")
	}
	return proxmoxClient, nil
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

// This newer proxmox library is not quite ready for use yet, although we do like it as it has types for everything
// One example where it falls short is that is can't set a description on a snapshot and requires permissions on each node.
// So we use the Telmate library for now.

func GetGoProxmoxClientForUser(c *gin.Context) (*goproxmox.Client, error) {
	user, err := GetUserObject(c)
	if err != nil {
		return nil, errors.New("unable to get user object") // JSON error is set in GetUserObject
	}

	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return nil, errors.New("could not get proxmox password for user") // JSON set in getProxmoxPasswordForUser
	}
	credentials := goproxmox.Credentials{
		Username: user.ProxmoxUsername + "@pam",
		Password: proxmoxPassword,
	}
	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert,
			},
		},
	}

	client := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithCredentials(&credentials),
	)
	return client, nil
}

func GetGoProxmoxClientForUserUsingToken(c *gin.Context) (*goproxmox.Client, error) {
	user, err := GetUserObject(c)
	if err != nil {
		return nil, errors.New("unable to get user object") // JSON error is set in GetUserObject
	}

	if user.Name == "ROOT" {
		return nil, errors.New("ROOT user should not be used for this action")
	}

	tokenID := user.ProxmoxTokenID
	tokenSecret, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret)
	if err != nil {
		return nil, errors.New("unable to decrypt proxmox token secret")
	}

	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert,
			},
		},
	}

	// Create a logger with debug level
	logger := &goproxmox.LeveledLogger{Level: goproxmox.LevelDebug}

	client := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithAPIToken(tokenID, tokenSecret),
		goproxmox.WithLogger(logger),
	)
	return client, nil
}

func getRootGoProxmoxClient() (*goproxmox.Client, error) {
	var rootUserObject UserObject
	db.First(&rootUserObject, "user_id = ?", "ROOT")

	tokenID := rootUserObject.ProxmoxTokenID
	tokenSecret, err := DecryptStringFromDatabase(rootUserObject.ProxmoxTokenSecret)
	if err != nil {
		return nil, errors.New("unable to decrypt proxmox token secret for root user: " + err.Error())
	}
	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert,
			},
		},
	}

	client := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithAPIToken(tokenID, tokenSecret),
	)
	return client, nil
}

// This function creates a new API token for a user. It uses the user's password on disk to create the token.
// func createProxmoxAPITokenForUserWithContext(c *gin.Context, user UserObject) (string, string, error) {
// 	// Use the user's password on disk (just created) to create a new API token
// 	proxmoxClient, err := GetGoProxmoxClientForUser(c)
// 	if err != nil {
// 		log.Printf("Error creating proxmox client for user %s: %v", user.ProxmoxUsername, err)
// 		return "", "", errors.New("could not create proxmox client for user")
// 	}

// 	return createProxmoxAPITokenForUserWithClient(proxmoxClient, user)
// }

func createProxmoxAPITokenForUserWithoutContext(user UserObject) (string, string, error) {
	proxmoxPasswordRaw, err := GetFileContents(fmt.Sprintf("%s/users/%s/proxmox_password", ludusInstallPath, user.ProxmoxUsername))
	proxmoxPassword := strings.TrimSuffix(proxmoxPasswordRaw, "\n")

	if err != nil {
		return "", "", errors.New("could not get proxmox password for user")
	}
	proxmoxCredentials := &goproxmox.Credentials{
		Username: user.ProxmoxUsername + "@pam",
		Password: proxmoxPassword,
	}
	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert,
			},
		},
	}
	proxmoxClient := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithCredentials(proxmoxCredentials),
	)

	return createProxmoxAPITokenForUserWithClient(proxmoxClient, user)
}

func createProxmoxAPITokenForUserWithClient(proxmoxClient *goproxmox.Client, user UserObject) (string, string, error) {
	// Get the user object from go-proxmox
	goProxmoxUserObject, err := proxmoxClient.User(context.Background(), user.ProxmoxUsername+"@pam")
	if err != nil {
		log.Printf("Failed to retrieve created user %s@pam: %v", user.ProxmoxUsername, err)
		return "", "", errors.New("failed to retrieve created user")
	}

	token := goproxmox.Token{
		TokenID: "ludus-token",
		Comment: "Ludus Token - Do not modify or delete",
		Privsep: false, // This token has the same permissions as the user
	}
	logger.Debug(fmt.Sprintf("Attempting to create API token '%s' for user '%s'\n", token.TokenID, user.UserID))
	apiToken, err := goProxmoxUserObject.NewAPIToken(context.Background(), token)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Remove the token and try again
			logger.Debug(fmt.Sprintf("API token already exists for user '%s', removing it and recreating", user.UserID))
			_, err = RunWithOutput(fmt.Sprintf("pveum user token del %s@pam ludus-token", user.ProxmoxUsername))
			if err != nil {
				return "", "", errors.New("unable to remove existing API token: " + err.Error())
			}
			apiToken, err = goProxmoxUserObject.NewAPIToken(context.Background(), token)
			if err != nil {
				return "", "", errors.New("failed to create API token: " + err.Error())
			}
		} else {
			return "", "", errors.New("failed to create API token: " + err.Error())
		}
	}
	logger.Debug(fmt.Sprintf("Created API token '%s' for user '%s'\n", apiToken.FullTokenID, user.UserID))
	return apiToken.FullTokenID, apiToken.Value, nil
}

func createRootAPITokenWithShell() (string, string, error) {
	out, err := RunWithOutput("pveum user token add root@pam ludus-token -privsep 0 -comment 'Ludus Token - Do not modify or delete' --output-format json")
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Remove the token and try again
			log.Printf("API token already exists for root@pam, removing it and recreating")
			_, err = RunWithOutput("pveum user token del root@pam ludus-token")
			if err != nil {
				return "", "", errors.New("unable to remove existing root API token: " + err.Error())
			}
			out, err = RunWithOutput("pveum user token add root@pam ludus-token -privsep 0 -comment 'Ludus Token - Do not modify or delete' --output-format json")
			if err != nil {
				return "", "", errors.New("unable to create root API token: " + err.Error())
			}
		} else {
			return "", "", errors.New("unable to create root API token: " + err.Error())
		}
	}
	type TokenResponse struct {
		TokenID string `json:"full-tokenid"`
		Value   string `json:"value"`
	}
	var tokenResponse TokenResponse
	err = json.Unmarshal([]byte(out), &tokenResponse)
	if err != nil {
		return "", "", errors.New("unable to unmarshal token response: " + err.Error())
	}
	return tokenResponse.TokenID, tokenResponse.Value, nil
}

func createPool(poolName string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client")
	}
	err = proxmoxClient.NewPool(context.Background(), poolName, "Created by Ludus")
	if err != nil {
		return errors.New("unable to create pool: " + err.Error())
	}
	return nil
}

func removePool(poolName string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client")
	}
	pool, err := proxmoxClient.Pool(context.Background(), poolName)
	if err != nil {
		return errors.New("unable to get pool object: " + err.Error())
	}
	err = pool.Delete(context.Background())
	if err != nil {
		return errors.New("unable to delete pool: " + err.Error())
	}
	return nil
}

func giveUserAccessToPool(username string, realm string, poolName string) error {
	return poolACLAction(username, realm, poolName, false)
}

func removeUserAccessFromPool(username string, realm string, poolName string) error {
	return poolACLAction(username, realm, poolName, true)
}

func poolACLAction(username string, realm string, poolName string, revoke bool) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	PVEVMAdminACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/pool/%s", poolName),
		Roles:     "PVEVMAdmin,PVESDNAdmin",
		Users:     username + "@" + realm,
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(revoke),
	}
	err = proxmoxClient.UpdateACL(context.Background(), PVEVMAdminACL)
	if err != nil {
		return errors.New("unable to set permissions for user: " + err.Error())
	}

	return nil
}

var proxmoxGroupNameRegex = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

func poolExists(poolName string) bool {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return false
	}
	pools, err := proxmoxClient.Pools(context.Background())
	if err != nil {
		logger.Error("unable to get proxmox pools: " + err.Error())
		return false
	}

	for _, pool := range pools {
		if pool.PoolID == poolName {
			return true
		}
	}

	return false
}

func createGroupInProxmox(groupName string) error {

	// Alphanumeric and hyphen only
	if !proxmoxGroupNameRegex.MatchString(groupName) {
		return errors.New("group name must be alphanumeric, hyphens, and underscores only")
	}

	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	err = proxmoxClient.NewGroup(context.Background(), groupName, "Created by Ludus")
	if err != nil {
		return errors.New("unable to create group: " + err.Error())
	}
	return nil
}

func removeGroupFromProxmox(groupName string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	group, err := proxmoxClient.Group(context.Background(), groupName)
	if err != nil {
		return errors.New("unable to get group object: " + err.Error())
	}
	err = group.Delete(context.Background())
	if err != nil {
		return errors.New("unable to delete group: " + err.Error())
	}
	return nil
}

func addUserToGroupInProxmox(username string, realm string, groupName string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	// Get the user object from go-proxmox, then add them to the group by updating their user configuration
	user, err := proxmoxClient.User(context.Background(), username+"@"+realm)
	if err != nil {
		return errors.New("unable to get user object: " + err.Error())
	}

	userOptions := goproxmox.UserOptions{
		Comment:   user.Comment,
		Email:     user.Email,
		Enable:    user.Enable,
		Expire:    user.Expire,
		Firstname: user.Firstname,
		Groups:    append(user.Groups, groupName),
		Keys:      user.Keys,
		Lastname:  user.Lastname,
	}

	err = user.Update(context.Background(), userOptions)
	if err != nil {
		return errors.New("unable to add user to group: " + err.Error())
	}
	return nil
}

func removeUserFromGroupInProxmox(username string, realm string, groupName string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	// Get the user object from go-proxmox, then remove them from the group by updating their user configuration
	user, err := proxmoxClient.User(context.Background(), username+"@"+realm)
	if err != nil {
		return errors.New("unable to get user object: " + err.Error())
	}
	user.Groups = slices.DeleteFunc(user.Groups, func(group string) bool {
		return group == groupName
	})

	userOptions := goproxmox.UserOptions{
		Comment:   user.Comment,
		Email:     user.Email,
		Enable:    user.Enable,
		Expire:    user.Expire,
		Firstname: user.Firstname,
		Groups:    user.Groups,
		Keys:      user.Keys,
		Lastname:  user.Lastname,
	}

	err = user.Update(context.Background(), userOptions)
	if err != nil {
		return errors.New("unable to remove user from group: " + err.Error())
	}
	return nil
}

func grantGroupAccessToRangeInProxmox(groupID string, poolName string) error {
	return groupACLAction(groupID, poolName, false)
}

func revokeGroupAccessToRangeInProxmox(groupID string, poolName string) error {
	return groupACLAction(groupID, poolName, true)
}

func groupACLAction(groupID string, poolName string, revoke bool) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}

	PVEVMAdminACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/pool/%s", poolName),
		Groups:    groupID,
		Roles:     "PVEVMAdmin,PVESDNAdmin",
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(revoke),
	}
	logger.Debug(fmt.Sprintf("Attempting to set permissions for group '%s' to pool '%s'\n", groupID, poolName))
	logger.Debug(godump.DumpStr(PVEVMAdminACL))
	err = proxmoxClient.UpdateACL(context.Background(), PVEVMAdminACL)
	if err != nil {
		return errors.New("unable to set permissions for group: " + err.Error())
	}

	return nil
}

func removeUserFromProxmox(username string, realm string) error {
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	user, err := proxmoxClient.User(context.Background(), username+"@"+realm)
	if err != nil {
		return errors.New("unable to get user object: " + err.Error())
	}
	user.Delete(context.Background())
	if err != nil {
		return errors.New("unable to delete user: " + err.Error())
	}
	return nil
}
