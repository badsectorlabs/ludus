package ludusapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

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

	tokenID := user.ProxmoxTokenID
	tokenSecret := user.ProxmoxTokenSecret

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
		log.Fatalf("Failed to retrieve created user: %v", err)
		return "", "", errors.New("failed to retrieve created user")
	}

	token := goproxmox.Token{
		TokenID: "ludus-token",
		Comment: "Ludus Token - Do not modify or delete",
		Privsep: false, // This token has the same permissions as the user
	}
	fmt.Printf("Attempting to create API token '%s' for user '%s'\n", token.TokenID, user.UserID)
	apiToken, err := goProxmoxUserObject.NewAPIToken(context.Background(), token)
	if err != nil {
		return "", "", errors.New("failed to create API token")
	}
	fmt.Printf("Created API token '%s' for user '%s'\n", apiToken.FullTokenID, user.UserID)
	return apiToken.FullTokenID, apiToken.Value, nil
}
