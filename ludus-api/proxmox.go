package ludusapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"ludusapi/models"
	"net/http"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/alessio/shellescape"
	"github.com/goforj/godump"
	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Telmate/proxmox-api-go/proxmox"
)

func GetProxmoxClientForUserUsingToken(e *core.RequestEvent) (*proxmox.Client, error) {
	user := e.Get("user").(*models.User)

	if user.Name() == "ROOT" {
		return nil, errors.New("ROOT user should not be used for this action")
	}

	tokenSecret, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
	if err != nil {
		return nil, errors.New("unable to decrypt proxmox token secret")
	}

	proxmoxClient, err := proxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json", nil, "", &tls.Config{InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert}, "", 300)
	if err != nil {
		return nil, errors.New("unable to create proxmox client: " + err.Error())
	}
	proxmoxClient.SetAPIToken(user.ProxmoxTokenId(), tokenSecret)
	return proxmoxClient, nil
}

func setProxmoxSystemPassword(username string, realm string, password string) error {
	// You can't set passwords using API tokens...
	// https://pve.proxmox.com/pve-docs/api-viewer/#/access/password
	// "This API endpoint is not available for API tokens."

	// proxmoxClient, err := getRootGoProxmoxClient()
	// if err != nil {
	// 	return errors.New("unable to create proxmox client: " + err.Error())
	// }
	// err = proxmoxClient.Password(context.TODO(), username+"@"+realm, password)
	// if err != nil {
	// 	return errors.New("unable to set proxmox system password: " + err.Error())
	// }
	// return nil

	// So we use the shell command instead

	if realm != "pam" {
		return errors.New("only PAM realm is supported for now")
	}

	// Make sure the username and password values are escaped
	shellEscapedUsername := shellescape.Quote(username + "@" + realm)
	shellEscapedPassword := shellescape.Quote(password)

	cmd := exec.Command("/usr/sbin/pveum", "passwd", shellEscapedUsername)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New("unable to set proxmox system password, stdin pipe failure: " + err.Error())
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		return errors.New("unable to set proxmox system password, command start failure: " + err.Error())
	}

	_, err = io.WriteString(stdin, shellEscapedPassword+"\n")
	if err != nil {
		return errors.New("unable to set proxmox system password, stdin write failure: " + err.Error())
	}
	_, err = io.WriteString(stdin, shellEscapedPassword+"\n")
	if err != nil {
		return errors.New("unable to set proxmox system password, stdin write 2 failure: " + err.Error())
	}
	if err := cmd.Wait(); err != nil {
		return errors.New("unable to set proxmox system password, command wait failure: " + err.Error())
	}
	return nil
}

// This newer proxmox library is not quite ready for use yet, although we do like it as it has types for everything
// One example where it falls short is that is can't set a description on a snapshot and requires permissions on each node.
// So we use the Telmate library for now.

func GetGoProxmoxClientForUserUsingToken(e *core.RequestEvent) (*goproxmox.Client, error) {
	user := e.Get("user").(*models.User)

	if user.Name() == "ROOT" {
		return nil, errors.New("ROOT user should not be used for this action")
	}

	tokenID := user.ProxmoxTokenId()
	tokenSecret, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
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

	// Create a logger with debug level if the debug flag is set
	var customLogger *goproxmox.LeveledLogger
	if DebugProxmox { // Resolved in routers.go, based on the LUDUS_DEBUG_PROXMOX environment variable
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelDebug}
	} else {
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelInfo}
	}

	client := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithAPIToken(tokenID, tokenSecret),
		goproxmox.WithLogger(customLogger),
	)
	return client, nil
}

func getRootGoProxmoxClient() (*goproxmox.Client, error) {
	rootUserRecord, err := app.FindFirstRecordByData("users", "userID", "ROOT")
	if err != nil {
		return nil, errors.New("unable to get root user object: " + err.Error())
	}
	rootUserObject := &models.User{}
	rootUserObject.SetProxyRecord(rootUserRecord)
	tokenID := rootUserObject.ProxmoxTokenId()
	tokenSecret, err := DecryptStringFromDatabase(rootUserObject.ProxmoxTokenSecret())
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

	var customLogger *goproxmox.LeveledLogger
	if DebugProxmox {
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelDebug}
	} else {
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelInfo}
	}

	client := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithAPIToken(tokenID, tokenSecret),
		goproxmox.WithLogger(customLogger),
	)
	return client, nil
}

func createProxmoxAPITokenForUserWithoutContext(username string, userRealm string, proxmoxPassword string) (string, string, error) {
	proxmoxCredentials := &goproxmox.Credentials{
		Username: username + "@" + userRealm,
		Password: proxmoxPassword,
	}
	insecureHTTPClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert,
			},
		},
	}

	var customLogger *goproxmox.LeveledLogger
	if DebugProxmox {
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelDebug}
	} else {
		customLogger = &goproxmox.LeveledLogger{Level: goproxmox.LevelInfo}
	}
	proxmoxClient := goproxmox.NewClient(ServerConfiguration.ProxmoxURL+"/api2/json",
		goproxmox.WithHTTPClient(&insecureHTTPClient),
		goproxmox.WithCredentials(proxmoxCredentials),
		goproxmox.WithLogger(customLogger),
	)

	return createProxmoxAPITokenForUserWithClient(proxmoxClient, username, userRealm)
}

func createProxmoxAPITokenForUserWithClient(proxmoxClient *goproxmox.Client, username string, userRealm string) (string, string, error) {
	// Get the user object from go-proxmox
	goProxmoxUserObject, err := proxmoxClient.User(context.Background(), username+"@pam")
	if err != nil {
		log.Printf("Failed to retrieve created user %s@%s: %v", username, userRealm, err)
		return "", "", errors.New("failed to retrieve created user")
	}

	token := goproxmox.Token{
		TokenID: "ludus-token",
		Comment: "Ludus Token - Do not modify or delete",
		Privsep: false, // This token has the same permissions as the user
	}
	logger.Debug(fmt.Sprintf("Attempting to create API token '%s' for user '%s'\n", token.TokenID, username))
	apiToken, err := goProxmoxUserObject.NewAPIToken(context.Background(), token)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Remove the token and try again
			logger.Debug(fmt.Sprintf("API token already exists for user '%s', removing it and recreating", username))
			shellEscapedUsername := shellescape.Quote(username + "@" + userRealm)
			_, err = exec.Command("/usr/sbin/pveum", "user", "token", "del", shellEscapedUsername, "ludus-token").CombinedOutput()
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
	logger.Debug(fmt.Sprintf("Created API token '%s' for user '%s'\n", apiToken.FullTokenID, username))
	return apiToken.FullTokenID, apiToken.Value, nil
}

func createRootAPITokenWithShell() (string, string, error) {
	out, err := exec.Command("/usr/sbin/pveum", "user", "token", "add", "root@pam", "ludus-token", "-privsep", "0", "-comment", "'Ludus Token - Do not modify or delete'", "--output-format", "json").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "already exists") {
			// Remove the token and try again
			logger.Debug("API token already exists for root@pam, removing it and recreating")
			_, err = exec.Command("/usr/sbin/pveum", "user", "token", "del", "root@pam", "ludus-token").CombinedOutput()
			if err != nil {
				return "", "", errors.New("unable to remove existing root API token: " + err.Error())
			}
			out, err = exec.Command("/usr/sbin/pveum", "user", "token", "add", "root@pam", "ludus-token", "-privsep", "0", "-comment", "'Ludus Token - Do not modify or delete'", "--output-format", "json").CombinedOutput()
			if err != nil {
				return "", "", errors.New("unable to create root API token: " + err.Error() + " |" + string(out) + "| ")
			} else {
				logger.Debug("Created API token for root@pam")
			}
		} else {
			return "", "", errors.New("unable to create root API token: " + err.Error() + " |" + string(out) + "| ")
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
		return errors.New("unable to create proxmox client: " + err.Error())
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
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	pool, err := proxmoxClient.Pool(context.Background(), poolName)
	if err != nil {
		if strings.Contains(err.Error(), poolName+"' does not exist") {
			return nil
		}
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
		if strings.Contains(err.Error(), "no such user") {
			// User does not exist on the proxmox system, this is not an error for our use case
			return nil
		} else {
			return errors.New("unable to get user object: " + err.Error())
		}
	}
	err = user.Delete(context.Background())
	if err != nil {
		return errors.New("unable to delete user: " + err.Error())
	}
	return nil
}

// PowerOffVMs powers off a list of virtual machines identified by their VMIDs.
// It finds which node each VM belongs to, and if the VM is running, issues a stop command.
// Operations are performed in parallel for efficiency.
//
// ctx: The context for the operation.
// client: An initialized go-proxmox client.
// vmids: A slice of integers representing the VMIDs to be powered off.
// returns: A slice of errors encountered during the process. If the slice is empty, all operations were successful.
func PowerOffVMs(ctx context.Context, client *goproxmox.Client, vmids []int) []error {
	return PowerActionVMs(ctx, client, vmids, "off")
}

func PowerOnVMs(ctx context.Context, client *goproxmox.Client, vmids []int) []error {
	return PowerActionVMs(ctx, client, vmids, "on")
}

func PowerActionVMs(ctx context.Context, client *goproxmox.Client, vmids []int, action string) []error {

	// 1. Get a client for the Proxmox cluster.
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return []error{fmt.Errorf("failed to get cluster client: %w", err)}
	}

	// 2. To find which node a VM is on, we first list all VMs in the cluster.
	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return []error{fmt.Errorf("failed to list VMs in the cluster: %w", err)}
	}

	// 3. Create a map for quick lookup of a VMID to its node name.
	vmNodeMap := make(map[int]string)
	for _, res := range resources {
		if res.Type == "qemu" { // Assuming we are targeting QEMU VMs
			vmNodeMap[int(res.VMID)] = res.Node
		}
	}

	var wg sync.WaitGroup
	// Use a buffered channel to collect errors from goroutines.
	errChan := make(chan error, len(vmids))

	// 4. Iterate over the requested VMIDs and process them in parallel.
	for _, vmid := range vmids {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// 5. Find the node for the current VMID.
			nodeName, found := vmNodeMap[id]
			if !found {
				errChan <- fmt.Errorf("VMID %d not found in the cluster", id)
				return
			}

			// 6. Get the specific node object.
			node, err := client.Node(ctx, nodeName)
			if err != nil {
				errChan <- fmt.Errorf("failed to get node %s for VMID %d: %w", nodeName, id, err)
				return
			}

			// 7. Get the virtual machine object.
			vm, err := node.VirtualMachine(ctx, id)
			if err != nil {
				errChan <- fmt.Errorf("failed to get VM object for VMID %d: %w", id, err)
				return
			}

			var task *goproxmox.Task
			if action == "off" {
				// 8. Check if the VM is running before attempting to stop it.
				if !vm.IsRunning() {
					logger.Debug(fmt.Sprintf("VM %d on node %s is already stopped. Skipping.\n", id, nodeName))
					return
				}

				// 9. Issue the stop command. This returns a task.
				logger.Debug(fmt.Sprintf("Initiating power off for VM %d on node %s...\n", id, nodeName))
				var err error
				task, err = vm.Stop(ctx)
				if err != nil {
					errChan <- fmt.Errorf("failed to initiate stop for VMID %d: %w", id, err)
					return
				}
			} else if action == "on" {
				if vm.IsRunning() {
					logger.Debug(fmt.Sprintf("VM %d on node %s is already running. Skipping.\n", id, nodeName))
					return
				}
				var err error
				task, err = vm.Start(ctx)
				if err != nil {
					errChan <- fmt.Errorf("failed to initiate start for VMID %d: %w", id, err)
					return
				}
			} else {
				errChan <- fmt.Errorf("invalid action: %s", action)
				return
			}

			// 10. Wait for the power-off task to complete.
			// A timeout is used to prevent the function from hanging indefinitely.
			err = task.Wait(ctx, 2*time.Second, 3*time.Minute) // Poll every 2s, timeout after 3m
			if err != nil {
				errChan <- fmt.Errorf("error while waiting for VMID %d to stop: %w", id, err)
			} else {
				logger.Debug(fmt.Sprintf("Successfully powered off VM %d.\n", id))
			}
		}(vmid)
	}

	// Wait for all goroutines to finish.
	wg.Wait()
	close(errChan)

	// Collect any errors that occurred.
	var allErrors []error
	for err := range errChan {
		allErrors = append(allErrors, err)
	}

	return allErrors
}

func getAllVMs(e *core.RequestEvent, ctx context.Context, client *goproxmox.Client) (goproxmox.ClusterResources, error) {

	cachedAllVMs := e.Get("allVMs")
	if cachedAllVMs != nil {
		return cachedAllVMs.(goproxmox.ClusterResources), nil
	}

	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, errors.New("unable to get cluster info: " + err.Error())
	}

	// Get all resources of type "vm" (which includes 'qemu' and 'lxc' types)
	allVMs, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return nil, errors.New("unable to list VMs from cluster: " + err.Error())
	}
	e.Set("allVMs", allVMs)
	return allVMs, nil
}

func findNodeForVM(ctx context.Context, client *goproxmox.Client, vmid uint64) (string, error) {
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return "", err
	}

	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return "", err
	}

	for _, res := range resources {
		if res.VMID == vmid {
			return res.Node, nil
		}
	}

	return "", fmt.Errorf("VMID %d not found in cluster", vmid)
}
