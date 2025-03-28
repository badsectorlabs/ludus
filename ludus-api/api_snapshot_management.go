package ludusapi

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/gin-gonic/gin"
)

type SnapshotInfo struct {
	Name        string `json:"name"`
	IncludesRAM bool   `json:"includesRAM"`
	Description string `json:"description"`
	Snaptime    uint   `json:"snaptime"`
	Parent      string `json:"parent"`
	VMID        int32  `json:"vmid"`
	VMName      string `json:"vmname"`
}

type ErrorInfo struct {
	VMID   int32  `json:"vmid"`
	VMName string `json:"vmname"`
	Error  string `json:"error"`
}

// GetSnapshots - retrieves a list of snapshots for the user's range
func GetSnapshots(c *gin.Context) {
	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return // JSON set in getProxmoxClientForUser
	}
	var snapshots []SnapshotInfo
	var errors []ErrorInfo

	// Get VMIDs from query parameters
	vmIDs := c.Query("vmids")
	if vmIDs == "" {
		// We have no VMIDs, assume we want all VMs
		updateUsersRangeVMData(c)
		usersRange, err := GetRangeObject(c)
		if err != nil {
			return // JSON set in getRangeObject
		}
		var allVMs []VmObject
		db.Where("range_number = ?", usersRange.RangeNumber).Find(&allVMs)
		log.Printf("%v\n", allVMs)
		for _, vm := range allVMs {
			if vm.ProxmoxID != 0 {
				vmr := proxmox.NewVmRef(int(vm.ProxmoxID))
				vmr.SetNode(ServerConfiguration.ProxmoxNode) // Assuming all VMs are on the same node for this example

				rawSnapshots, err := proxmox.ListSnapshots(proxmoxClient, vmr)
				if err != nil {
					errors = append(errors, ErrorInfo{VMID: vm.ProxmoxID, VMName: vm.Name, Error: "Error getting snapshots: " + err.Error()})
					continue
				}

				formattedSnapshots := rawSnapshots.FormatSnapshotsList()
				for _, snap := range formattedSnapshots {
					snapshots = append(snapshots, SnapshotInfo{
						Name:        string(snap.Name),
						IncludesRAM: snap.VmState,
						Description: snap.Description,
						Snaptime:    snap.SnapTime,
						Parent:      string(snap.Parent),
						VMID:        vm.ProxmoxID,
						VMName:      vm.Name,
					})
				}
			}
		}
	} else {
		// We have VMIDs, assume we want snapshots for specific VMs
		vmIDsArray := strings.Split(vmIDs, ",")
		for _, vmID := range vmIDsArray {
			vmIDInt, err := strconv.Atoi(vmID)
			if err != nil {
				errors = append(errors, ErrorInfo{Error: "Invalid VM ID: " + vmID})
				continue
			}
			vmr := proxmox.NewVmRef(vmIDInt)
			vmr.SetNode(ServerConfiguration.ProxmoxNode) // Assuming all VMs are on the same node for this example

			rawSnapshots, err := proxmox.ListSnapshots(proxmoxClient, vmr)
			if err != nil {
				errors = append(errors, ErrorInfo{VMID: int32(vmIDInt), VMName: "", Error: "Error getting snapshots: " + err.Error()})
				continue
			}
			vmInfo, err := proxmoxClient.GetVmInfo(vmr)
			if err != nil {
				errors = append(errors, ErrorInfo{VMID: int32(vmIDInt), VMName: "", Error: "Error getting VM info for VM " + vmID + ": " + err.Error()})
				continue
			}
			// First check if the VM name exists before casting to string
			var vmName string
			if vmInfo["name"] != nil {
				vmName = vmInfo["name"].(string)
			} else {
				vmName = ""
			}

			formattedSnapshots := rawSnapshots.FormatSnapshotsList()
			for _, snap := range formattedSnapshots {
				snapshots = append(snapshots, SnapshotInfo{
					Name:        string(snap.Name),
					IncludesRAM: snap.VmState,
					Description: snap.Description,
					Snaptime:    snap.SnapTime,
					Parent:      string(snap.Parent),
					VMID:        int32(vmIDInt),
					VMName:      vmName,
				})
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"snapshots": snapshots,
		"errors":    errors,
	})
}

type SnapshotCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	VMIDs       []int  `json:"vmids"`
	IncludeRAM  bool   `json:"includeRAM"`
}

type SnapshotGenericResponse struct {
	SuccessArray []int       `json:"success"`
	Errors       []ErrorInfo `json:"errors"`
}

// CreateSnapshot - creates a snapshot for specified VMs or all VMs in the range
func CreateSnapshot(c *gin.Context) {
	var payload SnapshotCreatePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": []int{}, "errors": []ErrorInfo{{Error: "Invalid request payload: " + err.Error()}}})
		return
	}

	// Validate snapshot name
	if payload.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": []int{}, "errors": []ErrorInfo{{Error: "Snapshot name is required"}}})
		return
	}

	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return // JSON set in GetProxmoxClientForUser
	}

	var successArray []int
	var errors []ErrorInfo

	snapshotConfig := proxmox.ConfigSnapshot{
		Name:        proxmox.SnapshotName(payload.Name),
		Description: payload.Description,
		VmState:     payload.IncludeRAM,
	}

	// If no VMIDs provided, get all VMs in the range
	if len(payload.VMIDs) == 0 {
		updateUsersRangeVMData(c)
		usersRange, err := GetRangeObject(c)
		if err != nil {
			return // JSON set in getRangeObject
		}

		var allVMs []VmObject
		db.Where("range_number = ?", usersRange.RangeNumber).Find(&allVMs)

		for _, vm := range allVMs {
			if vm.ProxmoxID != 0 {
				err = createSnapshotForVM(proxmoxClient, int(vm.ProxmoxID), snapshotConfig)
				if err != nil {
					errors = append(errors, ErrorInfo{
						VMID:   vm.ProxmoxID,
						VMName: vm.Name,
						Error:  err.Error(),
					})
				} else {
					successArray = append(successArray, int(vm.ProxmoxID))
				}
			}
		}
	} else {
		// Create snapshots for specified VMIDs
		for _, vmID := range payload.VMIDs {
			err = createSnapshotForVM(proxmoxClient, vmID, snapshotConfig)
			if err != nil {
				errors = append(errors, ErrorInfo{
					VMID:   int32(vmID),
					VMName: "",
					Error:  err.Error(),
				})
			} else {
				successArray = append(successArray, vmID)
			}
		}
	}

	c.JSON(http.StatusOK, SnapshotGenericResponse{
		SuccessArray: successArray,
		Errors:       errors,
	})
}

// Helper function to create a snapshot for a specific VM
func createSnapshotForVM(proxmoxClient *proxmox.Client, vmID int, snapshotConfig proxmox.ConfigSnapshot) error {
	vmr := proxmox.NewVmRef(vmID)
	_, err := proxmoxClient.GetVmInfo(vmr)
	if err != nil {
		return err
	}
	err = snapshotConfig.Create(proxmoxClient, vmr)
	if err != nil {
		return err
	} else {
		return nil
	}
}

type SnapshotGenericPayload struct {
	Name  string `json:"name"`
	VMIDs []int  `json:"vmids"`
}

// RollbackSnapshot - rolls back a snapshot for specified VMs or all VMs in the range
func RollbackSnapshot(c *gin.Context) {
	var payload SnapshotGenericPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": []int{}, "errors": []ErrorInfo{{Error: "Invalid request payload: " + err.Error()}}})
		return
	}

	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return // JSON set in GetProxmoxClientForUser
	}

	var successArray []int
	var errors []ErrorInfo

	snapshotName := proxmox.SnapshotName(payload.Name)

	if len(payload.VMIDs) == 0 {
		updateUsersRangeVMData(c)
		usersRange, err := GetRangeObject(c)
		if err != nil {
			return // JSON set in getRangeObject
		}

		var allVMs []VmObject
		db.Where("range_number = ?", usersRange.RangeNumber).Find(&allVMs)

		for _, vm := range allVMs {
			if vm.ProxmoxID != 0 {
				err = rollbackSnapshotForVM(proxmoxClient, int(vm.ProxmoxID), snapshotName)
				if err != nil {
					errors = append(errors, ErrorInfo{VMID: vm.ProxmoxID, VMName: vm.Name, Error: err.Error()})
				} else {
					successArray = append(successArray, int(vm.ProxmoxID))
				}
			}
		}
	} else {
		// Rollback snapshots for specified VMIDs
		for _, vmID := range payload.VMIDs {
			err = rollbackSnapshotForVM(proxmoxClient, vmID, snapshotName)
			if err != nil {
				errors = append(errors, ErrorInfo{VMID: int32(vmID), VMName: "", Error: err.Error()})
			} else {
				successArray = append(successArray, vmID)
			}
		}
	}

	c.JSON(http.StatusOK, SnapshotGenericResponse{
		SuccessArray: successArray,
		Errors:       errors,
	})
}

// Helper function to rollback a snapshot for a specific VM
func rollbackSnapshotForVM(proxmoxClient *proxmox.Client, vmID int, snapshotName proxmox.SnapshotName) error {
	vmr := proxmox.NewVmRef(vmID)
	_, err := proxmoxClient.GetVmInfo(vmr)
	if err != nil {
		return err
	}
	_, err = snapshotName.Rollback(proxmoxClient, vmr)
	if err != nil {
		return err
	} else {
		return nil
	}
}

// RemoveSnapshot - removes a snapshot for specified VMs or all VMs in the range
func RemoveSnapshot(c *gin.Context) {
	var payload SnapshotGenericPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": []int{}, "errors": []ErrorInfo{{Error: "Invalid request payload: " + err.Error()}}})
		return
	}

	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return // JSON set in GetProxmoxClientForUser
	}

	var successArray []int
	var errors []ErrorInfo

	snapshotName := proxmox.SnapshotName(payload.Name)

	if len(payload.VMIDs) == 0 {
		updateUsersRangeVMData(c)
		usersRange, err := GetRangeObject(c)
		if err != nil {
			return // JSON set in getRangeObject
		}

		var allVMs []VmObject
		db.Where("range_number = ?", usersRange.RangeNumber).Find(&allVMs)

		for _, vm := range allVMs {
			if vm.ProxmoxID != 0 {
				err = removeSnapshotForVM(proxmoxClient, int(vm.ProxmoxID), snapshotName)
				if err != nil {
					errors = append(errors, ErrorInfo{VMID: vm.ProxmoxID, VMName: vm.Name, Error: err.Error()})
				} else {
					successArray = append(successArray, int(vm.ProxmoxID))
				}
			}
		}
	} else {
		// Remove snapshots for specified VMIDs
		for _, vmID := range payload.VMIDs {
			err = removeSnapshotForVM(proxmoxClient, vmID, snapshotName)
			if err != nil {
				errors = append(errors, ErrorInfo{VMID: int32(vmID), VMName: "", Error: err.Error()})
			} else {
				successArray = append(successArray, vmID)
			}
		}
	}

	c.JSON(http.StatusOK, SnapshotGenericResponse{
		SuccessArray: successArray,
		Errors:       errors,
	})
}

// Helper function to remove a snapshot for a specific VM
func removeSnapshotForVM(proxmoxClient *proxmox.Client, vmID int, snapshotName proxmox.SnapshotName) error {
	vmr := proxmox.NewVmRef(vmID)
	_, err := proxmoxClient.GetVmInfo(vmr)
	if err != nil {
		return err
	}
	_, err = snapshotName.Delete(proxmoxClient, vmr)
	if err != nil {
		return err
	} else {
		return nil
	}
}
