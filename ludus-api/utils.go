package ludusapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"ludusapi/dto"
	"ludusapi/models"
	"net"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/alessio/shellescape"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
	"golang.org/x/crypto/bcrypt"

	goproxmox "github.com/luthermonson/go-proxmox"
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

func checkEmbeddedWebUI() bool {
	dirEntries, err := fs.ReadDir(embeddedWebUI, "webUI")
	if err != nil {
		return false
	}

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
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 4)
	return string(bytes), err
}

func CheckHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GenerateAPIKey(userID string) string {
	key := security.RandomString(40)
	return userID + "." + key
}

type IP struct {
	Query string
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

// updates the VM and range data for the provided range using the provided proxmox client
func updateRangeVMData(e *core.RequestEvent, targetRange *models.Range, proxmoxClient *goproxmox.Client) error {

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get all resources of type "vm" (which includes 'qemu' and 'lxc' types)
	allVMs, err := getAllVMs(e, ctx, proxmoxClient)
	if err != nil {
		return errors.New("unable to list VMs from cluster")
	}

	// Clear the DB of any previous VMs for this range
	logger.Debug(fmt.Sprintf("Clearing VMs for range %s with range number %d", targetRange.RangeId(), targetRange.RangeNumber()))
	_, err = app.DB().NewQuery("DELETE FROM vms WHERE range = {:range_id}").
		Bind(dbx.Params{
			"range_id": targetRange.Id,
		}).Execute()
	if err != nil {
		return errors.New("unable to clear VMs for range: " + err.Error())
	}

	// Get the router VM name for this range
	routerVMName, err := GetRouterVMName(targetRange)
	logger.Debug("routerVMName is: " + routerVMName)
	if err != nil {
		// If we can't get the router name, continue without router identification
		routerVMName = ""
	}

	// Create a network object from the range's CIDR to check if a VM's IP belongs to it
	_, network, _ := net.ParseCIDR(fmt.Sprintf("10.%d.0.0/16", targetRange.RangeNumber()))

	var rangeVMCount = 0
	vmCollection, err := app.FindCollectionByNameOrId("vms")
	if err != nil {
		return errors.New("unable to find vms collection: " + err.Error())
	}

	for _, vmResource := range allVMs {
		// We are only interested in QEMU VMs that belong to the range's pool and are not templates.
		if vmResource.Type != "qemu" || vmResource.Pool != targetRange.RangeId() || vmResource.Template == 1 || strings.HasSuffix(vmResource.Name, "-template") {
			continue
		}

		rawVM := core.NewRecord(vmCollection)
		thisVM := &models.VMs{}
		thisVM.SetProxyRecord(rawVM)

		thisVM.SetProxmoxId(int(vmResource.VMID))

		// Get IP from guest agent if possible
		thisVM.SetIp("null")
		node, err := proxmoxClient.Node(ctx, vmResource.Node)
		if err != nil {
			logger.Warn(fmt.Sprintf("Could not get node object for %s to fetch IP for VM %s: %s", vmResource.Node, vmResource.Name, err.Error()))
		} else {
			vm, err := node.VirtualMachine(ctx, int(vmResource.VMID))
			if err != nil {
				logger.Warn(fmt.Sprintf("Could not get VM object for %s to fetch IP: %s", vmResource.Name, err.Error()))
			} else {
				interfaces, err := vm.AgentGetNetworkIFaces(ctx)
				if err == nil {
				interfaceLoop:
					for _, thisInterface := range interfaces {
						for _, ipInfo := range thisInterface.IPAddresses {
							ipAddr := net.ParseIP(ipInfo.IPAddress)
							if ipAddr != nil && network.Contains(ipAddr) {
								thisVM.SetIp(ipAddr.String())
								break interfaceLoop // IP found, no need to check other interfaces/addresses
							}
						}
					}
				} else {
					logger.Debug(fmt.Sprintf("Could not get agent network interfaces for VM %s: %s", vmResource.Name, err.Error()))
				}
			}
		}

		if thisVM.Ip() == "null" {
			// Fallback: Fetch the IP address from the user's range config if the VM is set to use force_ip
			thisVM.SetIp(GetIPForVMFromConfig(targetRange, vmResource.Name))
		}

		thisVM.SetRange(targetRange)
		thisVM.SetName(vmResource.Name)
		thisVM.SetPoweredOn(vmResource.Status == goproxmox.StatusVirtualMachineRunning)
		thisVM.SetIsRouter(vmResource.Name == routerVMName)

		logger.Debug(fmt.Sprintf("Adding VM %s to range %s with range number %d", thisVM.Name(), targetRange.RangeId(), thisVM.Range().RangeNumber()))
		err = app.Save(thisVM)
		if err == nil {
			rangeVMCount++
		} else {
			logger.Error(fmt.Sprintf("Unable to add VM %s to database: %s", thisVM.Name(), err.Error()))
		}
	}

	targetRange.SetNumberOfVms(rangeVMCount)
	err = app.Save(targetRange)
	if err != nil {
		return errors.New("unable to update range: " + err.Error())
	}

	logger.Debug(fmt.Sprintf("Updated range %s with %d VMs", targetRange.RangeId(), rangeVMCount))

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

func getUIDandGIDFromUsername(username string) (int, int, error) {
	runnerUser, err := user.Lookup(username)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to lookup user %s", err)
	}

	uid, err := strconv.Atoi(runnerUser.Uid)
	if err != nil {
		logger.Error("Failed to convert UID to integer: " + err.Error())
		return 0, 0, fmt.Errorf("failed to convert UID to integer: %s", err)
	}

	gid, err := strconv.Atoi(runnerUser.Gid)
	if err != nil {
		logger.Error("Failed to convert GID to integer: " + err.Error())
		return 0, 0, fmt.Errorf("failed to convert GID to integer: %s", err)
	}

	return uid, gid, nil
}

// userExistsOnHostSystem checks if a user exists on the host system
func userExistsOnHostSystem(username string) bool {
	shellEscapedUsername := shellescape.Quote(username)
	cmd := exec.Command("/usr/bin/id", shellEscapedUsername)
	return cmd.Run() == nil
}

// removeUserFromHostSystem removes a user from the host system
func removeUserFromHostSystem(username string) error {
	shellEscapedUsername := shellescape.Quote(username)
	cmd := exec.Command("/usr/sbin/userdel", "-r", shellEscapedUsername)
	err := cmd.Run()
	if err != nil {
		if err.Error() == "exit status 6" {
			// User does not exist on the host system, this is not an error for our use case
			return nil
		} else {
			fmt.Printf("Failed to remove user %s from host system: %s\n", username, err)
			return err
		}
	}
	return nil
}

// HasRangeAccess checks if a user has access to a range through direct assignment or group membership
func HasRangeAccess(userID string, rangeNumber int) bool {
	// Check direct user-to-range assignment
	userRecord, err := app.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding user: %s", err.Error()))
		return false
	}
	app.ExpandRecord(userRecord, []string{"ranges"}, nil)
	userRanges := userRecord.ExpandedAll("ranges")
	for _, rangeRecord := range userRanges {
		if rangeRecord.GetInt("rangeNumber") == int(rangeNumber) {
			return true
		}
	}

	// Check group-based access
	groupRecords, err := app.FindRecordsByFilter(
		"groups", // collection name
		"members.id ?= {:user_id} || managers.id ?= {:user_id}", // filter
		"-created", // sort
		0,          // limit
		0,          // offset
		dbx.Params{
			"user_id": userRecord.Id,
		},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding groups: %s", err.Error()))
		return false
	}
	logger.Debug(fmt.Sprintf("Found %d groups for user %s", len(groupRecords), userRecord.GetString("userID")))
	for _, groupRecord := range groupRecords {
		app.ExpandRecord(groupRecord, []string{"ranges"}, nil)
		for _, rangeRecord := range groupRecord.ExpandedAll("ranges") {
			if rangeRecord.GetInt("rangeNumber") == int(rangeNumber) {
				return true
			}
		}
	}

	return false
}

// GetRangeAccessibleUsers returns all userIDs who can access a specific range
func GetRangeAccessibleUsers(rangeNumber int) []dto.ListRangeUsersResponseItem {
	var result []dto.ListRangeUsersResponseItem

	rangeRecord, err := GetRangeObjectByNumber(rangeNumber)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding range: %s", err.Error()))
		return nil
	}

	// Find all users who have direct access to the range by querying the user table looking for the range.Id in the user's ranges array
	userRecords, err := app.FindRecordsByFilter(
		"users",                    // collection name
		"ranges.id ?= {:range_id}", // filter
		"-created",                 // sort
		0,                          // limit
		0,                          // offset
		dbx.Params{
			"range_id": rangeRecord.Id,
		},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding users: %s", err.Error()))
		return nil
	}
	for _, userRecord := range userRecords {
		result = append(result, dto.ListRangeUsersResponseItem{
			UserID:     userRecord.GetString("userID"),
			Name:       userRecord.GetString("name"),
			AccessType: "Direct",
		})
	}

	// Find all users who are managers or members of a group with access to the range by querying the group table looking for the range.Id in the group's ranges array
	groupRecords, err := app.FindRecordsByFilter(
		"groups",                   // collection name
		"ranges.id ?= {:range_id}", // filter
		"-created",                 // sort
		0,                          // limit
		0,                          // offset
		dbx.Params{
			"range_id": rangeRecord.Id,
		},
	)
	if err != nil {
		logger.Error(fmt.Sprintf("Error finding groups: %s", err.Error()))
		return nil
	}
	for _, groupRecord := range groupRecords {
		app.ExpandRecord(groupRecord, []string{"members", "managers"}, nil)
		for _, member := range groupRecord.ExpandedAll("members") {
			result = append(result, dto.ListRangeUsersResponseItem{
				UserID:     member.GetString("userID"),
				Name:       member.GetString("name"),
				AccessType: "Group Member",
			})
		}
		for _, manager := range groupRecord.ExpandedAll("managers") {
			result = append(result, dto.ListRangeUsersResponseItem{
				UserID:     manager.GetString("userID"),
				Name:       manager.GetString("name"),
				AccessType: "Group Manager",
			})
		}
	}

	// Sort the result to ensure consistent ordering
	slices.SortFunc(result, func(a, b dto.ListRangeUsersResponseItem) int {
		return strings.Compare(a.UserID, b.UserID)
	})

	return result
}

func mustGetUserFromRequest(e *core.RequestEvent) *models.User {
	rawUser := e.Get("user")
	if rawUser == nil {
		panic("User not found in request context")
	}
	userRecord := rawUser.(*models.User)
	return userRecord
}

func mustGetRangeFromRequest(e *core.RequestEvent) *models.Range {
	rawRange := e.Get("range")
	if rawRange == nil {
		panic("Range not found in request context")
	}
	rangeRecord := rawRange.(*models.Range)
	return rangeRecord
}

// CreateDefaultUserRange creates a default range for a user and assigns direct access
func CreateDefaultUserRange(e *core.RequestEvent, txApp core.App, user *models.User) error {
	// Find next available range number
	rangeNumber := findNextAvailableRangeNumber(txApp)

	rangeCollection, err := txApp.FindCollectionByNameOrId("ranges")
	if err != nil {
		return err
	}
	rawRangeRecord := core.NewRecord(rangeCollection)
	rangeRecord := &models.Range{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	rangeRecord.SetRangeNumber(rangeNumber)
	rangeRecord.SetRangeId(user.UserId())
	rangeRecord.SetName(fmt.Sprintf("Default Range for %s", user.Name()))
	rangeRecord.SetDescription("Default range created automatically for user")
	rangeRecord.SetPurpose("General testing and development")
	rangeRecord.SetNumberOfVms(0)
	rangeRecord.SetRangeState("NEVER DEPLOYED")
	if err := txApp.Save(rangeRecord); err != nil {
		return err
	}
	os.MkdirAll(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, rangeRecord.RangeId()), 0755)
	copyFileContents(fmt.Sprintf("%s/ansible/user-files/range-config.example.yml", ludusInstallPath), fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, rangeRecord.RangeId()))
	chownDirToUsernameRecursive(fmt.Sprintf("%s/ranges/%s", ludusInstallPath, rangeRecord.RangeId()), "ludus")

	// Add the range to the request context
	e.Set("range", rangeRecord)

	// This will be saved when the user is saved later
	user.SetDefaultRangeId(rangeRecord.RangeId())
	user.Set("ranges+", rangeRecord.Id)

	err = manageVmbrInterfaceLocally(rangeNumber, true)
	if err != nil {
		txApp.Delete(rangeRecord)
		return err
	}

	err = createPool(user.UserId())
	if err != nil {
		txApp.Delete(rangeRecord)
		manageVmbrInterfaceLocally(rangeNumber, false)
		return err
	}
	return err
}

// GetRangeObjectByNumber gets a range object by range number (for multi-range support)
func GetRangeObjectByNumber(rangeNumber int) (*models.Range, error) {
	rawRangeRecord, err := app.FindFirstRecordByData("ranges", "rangeNumber", rangeNumber)
	if err != nil {
		return nil, fmt.Errorf("error finding range: %w", err)
	}
	rangeRecord := models.Range{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	return &rangeRecord, nil
}

// GetUserDefaultRange gets the default range for a user (range where user_id matches the user's ID)
func GetUserDefaultRange(userID string) (*models.Range, error) {
	userRecord, err := app.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		return nil, fmt.Errorf("error finding user: %w", err)
	}
	defaultRangeID := userRecord.GetString("defaultRangeID")
	if defaultRangeID == "" {
		return nil, fmt.Errorf("user %s has no default range", userID)
	}
	rawRangeRecord, err := app.FindFirstRecordByData("ranges", "rangeId", defaultRangeID)
	if err != nil {
		return nil, fmt.Errorf("error finding default range: %w", err)
	}
	rangeRecord := models.Range{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	return &rangeRecord, nil
}

// AppendIfMissing appends an element to a slice only if it's not already present.
func AppendIfMissing(slice []string, elem string) []string {
	for _, v := range slice {
		if v == elem {
			return slice // Element already exists, return the original slice
		}
	}
	return append(slice, elem) // Element not found, append it
}

func GetRangeNumberFromRangeID(rangeID string) (int, error) {
	rangeRecord, err := app.FindFirstRecordByData("ranges", "rangeId", rangeID)
	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("not in database")
	} else if err != nil {
		return 0, fmt.Errorf("error finding range: %w", err)
	}
	return rangeRecord.GetInt("rangeNumber"), nil
}
