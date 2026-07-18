package ludusapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"ludusapi/models"
	"ludusapi/pveclient"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/goforj/godump"
	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"

	"github.com/Telmate/proxmox-api-go/proxmox"
)

// ErrProxmoxVMNotFound is returned by getNodeForVMByName when no cluster VM matches the name.
var ErrProxmoxVMNotFound = errors.New("proxmox VM not found")

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

// This newer proxmox library is not quite ready for use yet, although we do like it as it has types for everything
// One example where it falls short is that is can't set a description on a snapshot and requires permissions on each node.
// So we use the Telmate library for now.

func GetGoProxmoxClientForUserUsingToken(e *core.RequestEvent) (*goproxmox.Client, error) {

	cachedClient := e.Get("proxmoxClient_" + e.Get("user").(*models.User).UserId())
	if cachedClient != nil {
		return cachedClient.(*goproxmox.Client), nil
	}

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
	e.Set("proxmoxClient_"+user.UserId(), client)
	return client, nil
}

var (
	rootPVEClient     *pveclient.Client
	rootPVEClientOnce sync.Once
	rootPVEClientErr  error
)

// GetRootPVEClient returns the singleton failover-aware Proxmox client built
// from ServerConfiguration. Callers should prefer this over GetRootGoProxmoxClient.
func GetRootPVEClient() (*pveclient.Client, error) {
	rootPVEClientOnce.Do(func() {
		rootPVEClient, rootPVEClientErr = pveclient.New(ServerConfiguration.PVEClientConfig())
	})
	return rootPVEClient, rootPVEClientErr
}

func GetRootGoProxmoxClient() (*goproxmox.Client, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	return pc.Raw(), nil
}

func createProxmoxAPITokenForUserWithoutContext(username string, userRealm string) (string, string, error) {
	return createProxmoxAPITokenForUserWithClient(nil, username, userRealm)
}

func createProxmoxAPITokenForUserWithClient(_ *goproxmox.Client, username, userRealm string) (string, string, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return "", "", err
	}
	ctx := context.Background()
	userid := username + "@" + userRealm
	logger.Debug(fmt.Sprintf("Attempting to create API token 'ludus-token' for user '%s'\n", userid))
	tok, err := pc.CreateToken(ctx, userid, "ludus-token", false)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			// Remove the token and try again via the API
			logger.Debug(fmt.Sprintf("API token already exists for user '%s', removing it and recreating", userid))
			if derr := pc.DeleteToken(ctx, userid, "ludus-token"); derr != nil {
				return "", "", errors.New("unable to remove existing API token: " + derr.Error())
			}
			tok, err = pc.CreateToken(ctx, userid, "ludus-token", false)
			if err != nil {
				return "", "", errors.New("failed to create API token: " + err.Error())
			}
		} else {
			return "", "", errors.New("failed to create API token: " + err.Error())
		}
	}
	logger.Debug(fmt.Sprintf("Created API token '%s' for user '%s'\n", tok.FullTokenID, userid))
	return tok.FullTokenID, tok.Value, nil
}

// Deprecated: root token is provisioned at install time and read from config.
// Kept as a stub so callers compile during transition; remove in Phase D cleanup.
func createRootAPITokenWithShell() (string, string, error) {
	return ServerConfiguration.ProxmoxTokenID, ServerConfiguration.ProxmoxTokenSecret, nil
}

func createPool(poolName string) error {
	proxmoxClient, err := GetRootGoProxmoxClient()
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
	proxmoxClient, err := GetRootGoProxmoxClient()
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

func giveUserAccessToRange(username string, realm string, poolName string, rangeNumber int) error {
	if err := sdnVNetACLAction(username, realm, rangeNumber, false); err != nil {
		return err
	}
	return poolACLAction(username, realm, poolName, false)
}

func removeUserAccessFromRange(username string, realm string, poolName string, rangeNumber int) error {
	if err := sdnVNetACLAction(username, realm, rangeNumber, true); err != nil {
		return err
	}
	return poolACLAction(username, realm, poolName, true)
}

func poolACLAction(username string, realm string, poolName string, revoke bool) error {
	proxmoxClient, err := GetRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}
	PVEVMAdminACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/pool/%s", poolName),
		Roles:     "PVEVMAdmin,PVESDNAdmin,PVEPoolUser",
		Users:     username + "@" + realm,
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(revoke),
	}
	err = proxmoxClient.UpdateACL(context.Background(), PVEVMAdminACL)
	if err != nil {
		return errors.New("unable to set pool permissions for user: " + err.Error())
	}

	return nil
}

func sdnVNetACLAction(username string, realm string, rangeNumber int, revoke bool) error {
	proxmoxClient, err := GetRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}

	vnetName := fmt.Sprintf("r%d", rangeNumber)
	SDNVNetACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/sdn/zones/%s/%s", ServerConfiguration.SDNZone, vnetName),
		Roles:     "PVESDNUser",
		Users:     username + "@" + realm,
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(revoke),
	}
	err = proxmoxClient.UpdateACL(context.Background(), SDNVNetACL)
	if err != nil {
		return errors.New("unable to set SDN permissions for user: " + err.Error())
	}
	return nil
}

var proxmoxGroupNameRegex = regexp.MustCompile(`^[A-Za-z0-9_\-]+$`)

func poolExists(poolName string) bool {
	proxmoxClient, err := GetRootGoProxmoxClient()
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

	proxmoxClient, err := GetRootGoProxmoxClient()
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
	proxmoxClient, err := GetRootGoProxmoxClient()
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
	proxmoxClient, err := GetRootGoProxmoxClient()
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
	proxmoxClient, err := GetRootGoProxmoxClient()
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

func grantGroupAccessToRangeInProxmox(groupID string, poolName string, rangeNumber int) error {
	if err := grantGroupAccessToSDNVNet(groupID, rangeNumber); err != nil {
		return err
	}
	return groupACLAction(groupID, poolName, false)
}

func revokeGroupAccessToRangeInProxmox(groupID string, poolName string, rangeNumber int) error {
	if err := revokeGroupAccessToSDNVNet(groupID, rangeNumber); err != nil {
		return err
	}
	return groupACLAction(groupID, poolName, true)
}

func groupACLAction(groupID string, poolName string, revoke bool) error {
	proxmoxClient, err := GetRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}

	PVEVMAdminACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/pool/%s", poolName),
		Groups:    groupID,
		Roles:     "PVEVMAdmin,PVESDNAdmin,PVEPoolUser",
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
	proxmoxClient, err := GetRootGoProxmoxClient()
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

func getNodeForVMByName(e *core.RequestEvent, vmName string) (string, error) {

	ctx := context.TODO()
	client, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return "", err
	}
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return "", err
	}

	resources, err := cluster.Resources(ctx, "vm")
	if err != nil {
		return "", err
	}

	for _, res := range resources {
		if res.Name == vmName {
			return res.Node, nil
		}
	}

	return "", fmt.Errorf("%w: VM %s not found in cluster", ErrProxmoxVMNotFound, vmName)
}

func getVMsForPool(e *core.RequestEvent, ctx context.Context, poolName string, client *goproxmox.Client) ([]goproxmox.ClusterResource, error) {

	cachedVMsForPool := e.Get("getVMsForPool_" + poolName)
	if cachedVMsForPool != nil {
		return cachedVMsForPool.([]goproxmox.ClusterResource), nil
	}

	type poolMember struct {
		ID       string `json:"id,omitempty"`
		Type     string `json:"type,omitempty"`
		Node     string `json:"node,omitempty"`
		Name     string `json:"name,omitempty"`
		Status   string `json:"status,omitempty"`
		Template uint64 `json:"template,omitempty"`
		VMID     uint64 `json:"vmid,omitempty"`
	}
	var poolData struct {
		Members []poolMember `json:"members"`
	}
	if err := proxmoxAPIGet(ctx, "/pools/"+url.PathEscape(poolName), &poolData); err != nil {
		return nil, errors.New("unable to get pool by ID: " + err.Error())
	}
	vmsForPool := make([]goproxmox.ClusterResource, 0)
	for _, member := range poolData.Members {
		if member.Type == "qemu" {
			vm := goproxmox.ClusterResource{
				ID:       member.ID,
				Type:     member.Type,
				Node:     member.Node,
				Name:     member.Name,
				Status:   member.Status,
				Template: member.Template,
				VMID:     member.VMID,
			}
			vm = hydratePoolVMResource(ctx, client, vm)
			if vm.Template != 1 {
				vmsForPool = append(vmsForPool, vm)
			}
		}
	}
	e.Set("getVMsForPool_"+poolName, vmsForPool)
	return vmsForPool, nil
}

func hydratePoolVMResource(ctx context.Context, client *goproxmox.Client, resource goproxmox.ClusterResource) goproxmox.ClusterResource {
	if resource.VMID == 0 && strings.HasPrefix(resource.ID, "qemu/") {
		if vmid, err := strconv.ParseUint(strings.TrimPrefix(resource.ID, "qemu/"), 10, 64); err == nil {
			resource.VMID = vmid
		}
	}
	if resource.ID == "" && resource.VMID != 0 {
		resource.ID = fmt.Sprintf("qemu/%d", resource.VMID)
	}
	if resource.Type == "" {
		resource.Type = "qemu"
	}

	nodeName := resource.Node
	if nodeName == "" {
		node, err := findNodeForVM(ctx, client, resource.VMID)
		if err != nil {
			logger.Warn(fmt.Sprintf("Could not find node for pool VMID %d: %s", resource.VMID, err.Error()))
			return resource
		}
		nodeName = node
		resource.Node = nodeName
	}

	var status struct {
		Name    string  `json:"name,omitempty"`
		Status  string  `json:"status,omitempty"`
		CPU     float64 `json:"cpu,omitempty"`
		CPUs    uint64  `json:"cpus,omitempty"`
		Mem     uint64  `json:"mem,omitempty"`
		MaxMem  uint64  `json:"maxmem,omitempty"`
		Disk    uint64  `json:"disk,omitempty"`
		MaxDisk uint64  `json:"maxdisk,omitempty"`
		NetIn   uint64  `json:"netin,omitempty"`
		NetOut  uint64  `json:"netout,omitempty"`
		Uptime  uint64  `json:"uptime,omitempty"`
	}
	if err := proxmoxAPIGet(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/status/current", nodeName, resource.VMID), &status); err != nil {
		logger.Warn(fmt.Sprintf("Could not get status for pool VMID %d on node %s: %s", resource.VMID, nodeName, err.Error()))
	} else {
		if status.Name != "" {
			resource.Name = status.Name
		}
		resource.Node = nodeName
		resource.Status = status.Status
		resource.CPU = status.CPU
		resource.MaxCPU = status.CPUs
		resource.Mem = status.Mem
		resource.MaxMem = status.MaxMem
		resource.Disk = status.Disk
		resource.MaxDisk = status.MaxDisk
		resource.NetIn = status.NetIn
		resource.NetOut = status.NetOut
		resource.Uptime = status.Uptime
	}

	var config struct {
		Name     string `json:"name,omitempty"`
		Template int    `json:"template,omitempty"`
	}
	if err := proxmoxAPIGet(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/config", nodeName, resource.VMID), &config); err != nil {
		logger.Warn(fmt.Sprintf("Could not get config for pool VMID %d on node %s: %s", resource.VMID, nodeName, err.Error()))
	} else {
		if config.Name != "" {
			resource.Name = config.Name
		}
		if config.Template == 1 {
			resource.Template = 1
		}
	}

	if resource.Name == "" && nodeName != "" && resource.VMID != 0 {
		var nodeVMs []struct {
			Name    string `json:"name,omitempty"`
			Status  string `json:"status,omitempty"`
			VMID    uint64 `json:"vmid,omitempty"`
			CPUs    uint64 `json:"cpus,omitempty"`
			MaxMem  uint64 `json:"maxmem,omitempty"`
			Mem     uint64 `json:"mem,omitempty"`
			MaxDisk uint64 `json:"maxdisk,omitempty"`
			Disk    uint64 `json:"disk,omitempty"`
			NetIn   uint64 `json:"netin,omitempty"`
			NetOut  uint64 `json:"netout,omitempty"`
			Uptime  uint64 `json:"uptime,omitempty"`
		}
		if err := proxmoxAPIGet(ctx, fmt.Sprintf("/nodes/%s/qemu", nodeName), &nodeVMs); err != nil {
			logger.Warn(fmt.Sprintf("Could not get node VM list for pool VMID %d on node %s: %s", resource.VMID, nodeName, err.Error()))
		} else {
			for _, nodeVM := range nodeVMs {
				if nodeVM.VMID == resource.VMID {
					resource.Name = nodeVM.Name
					if resource.Status == "" {
						resource.Status = nodeVM.Status
					}
					if resource.MaxCPU == 0 {
						resource.MaxCPU = nodeVM.CPUs
					}
					if resource.MaxMem == 0 {
						resource.MaxMem = nodeVM.MaxMem
					}
					if resource.Mem == 0 {
						resource.Mem = nodeVM.Mem
					}
					if resource.MaxDisk == 0 {
						resource.MaxDisk = nodeVM.MaxDisk
					}
					if resource.Disk == 0 {
						resource.Disk = nodeVM.Disk
					}
					if resource.NetIn == 0 {
						resource.NetIn = nodeVM.NetIn
					}
					if resource.NetOut == 0 {
						resource.NetOut = nodeVM.NetOut
					}
					if resource.Uptime == 0 {
						resource.Uptime = nodeVM.Uptime
					}
					break
				}
			}
		}
	}
	if resource.Name == "" {
		logger.Warn(fmt.Sprintf("Pool VMID %d on node %s has no name after hydration", resource.VMID, nodeName))
	}

	return resource
}

func proxmoxAPIGet(ctx context.Context, path string, out interface{}) error {
	base := strings.TrimRight(activeProxmoxEndpoint(), "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api2/json"+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "PVEAPIToken="+ServerConfiguration.ProxmoxTokenID+"="+ServerConfiguration.ProxmoxTokenSecret)

	httpClient := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: ServerConfiguration.ProxmoxInvalidCert},
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("proxmox API returned %s for %s", resp.Status, path)
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil
	}
	return json.Unmarshal(envelope.Data, out)
}

func lookupProxmoxVMName(ctx context.Context, nodeName string, vmid uint64) string {
	if vmid == 0 {
		return ""
	}

	if nodeName == "" {
		var resources []struct {
			Name string `json:"name,omitempty"`
			Node string `json:"node,omitempty"`
			Type string `json:"type,omitempty"`
			VMID uint64 `json:"vmid,omitempty"`
		}
		if err := proxmoxAPIGet(ctx, "/cluster/resources?type=vm", &resources); err == nil {
			for _, resource := range resources {
				if resource.VMID == vmid && resource.Type == "qemu" {
					if resource.Name != "" {
						return resource.Name
					}
					nodeName = resource.Node
					break
				}
			}
		}
	}

	if nodeName != "" {
		var config struct {
			Name string `json:"name,omitempty"`
		}
		if err := proxmoxAPIGet(ctx, fmt.Sprintf("/nodes/%s/qemu/%d/config", nodeName, vmid), &config); err == nil && config.Name != "" {
			return config.Name
		}

		var nodeVMs []struct {
			Name string `json:"name,omitempty"`
			VMID uint64 `json:"vmid,omitempty"`
		}
		if err := proxmoxAPIGet(ctx, fmt.Sprintf("/nodes/%s/qemu", nodeName), &nodeVMs); err == nil {
			for _, nodeVM := range nodeVMs {
				if nodeVM.VMID == vmid {
					return nodeVM.Name
				}
			}
		}
	}

	return ""
}

// waitForPoolEmpty waits until the specified pool has no non-template VMs.
// It polls Proxmox directly (without using cached results) until either the
// pool is empty or the context is done.
func waitForPoolEmpty(ctx context.Context, client *goproxmox.Client, poolName string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for pool %s to be empty: %w", poolName, ctx.Err())
		case <-ticker.C:
			poolData, err := client.Pool(ctx, poolName, "qemu")
			if err != nil {
				// If the pool no longer exists, consider it empty
				if strings.Contains(err.Error(), poolName+"' does not exist") {
					return nil
				}
				return fmt.Errorf("unable to get pool by ID while waiting for empty: %w", err)
			}

			remaining := 0
			for _, vm := range poolData.Members {
				if vm.Type == "qemu" && vm.Template != 1 {
					remaining++
				}
			}

			if remaining == 0 {
				return nil
			}
		}
	}
}

func grantGroupAccessToSDNVNet(groupID string, rangeNumber int) error {
	return sdnGroupVNetACLAction(groupID, rangeNumber, false)
}

func revokeGroupAccessToSDNVNet(groupID string, rangeNumber int) error {
	return sdnGroupVNetACLAction(groupID, rangeNumber, true)
}

func sdnGroupVNetACLAction(groupID string, rangeNumber int, revoke bool) error {
	proxmoxClient, err := GetRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}

	SDNVNetName := fmt.Sprintf("r%d", rangeNumber)

	SDNVNetACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/sdn/zones/%s/%s", ServerConfiguration.SDNZone, SDNVNetName),
		Groups:    groupID,
		Roles:     "PVESDNUser",
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(revoke),
	}
	logger.Debug(fmt.Sprintf("Attempting to set permissions for group '%s' to SDN VNet '%s'\n", groupID, SDNVNetName))
	logger.Debug(godump.DumpStr(SDNVNetACL))
	err = proxmoxClient.UpdateACL(context.Background(), SDNVNetACL)
	if err != nil {
		return errors.New("unable to set permissions for group: " + err.Error())
	}

	return nil
}
