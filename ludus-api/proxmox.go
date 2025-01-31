package ludusapi

import (
	"crypto/tls"
	"errors"
	"net/http"

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
