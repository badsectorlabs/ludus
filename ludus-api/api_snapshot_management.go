package ludusapi

import (
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"strconv"
	"strings"

	"github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// GetSnapshots - retrieves a list of snapshots for the user's range
func GetSnapshots(e *core.RequestEvent) error {
	proxmoxClient, err := GetProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}

	goProxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get go proxmox client: "+err.Error())
	}

	response := dto.GetSnapshotsResponse{}
	snapshotsResponse := []dto.GetSnapshotsResponseSnapshotsItem{}
	errorsResponse := []dto.GetSnapshotsResponseErrorsItem{}

	usersRange := e.Get("range").(*models.Range)
	// Get VMIDs from query parameters
	vmIDs := e.Request.URL.Query().Get("vmids")
	if vmIDs == "" {
		// We have no VMIDs, assume we want all VMs
		updateRangeVMData(e, usersRange, goProxmoxClient)
		usersRange := e.Get("range").(*models.Range)
		allVMs, err := getVMsForRange(usersRange.RangeId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to get VMs for range: "+err.Error())
		}
		for _, vm := range allVMs {
			if vm.ProxmoxId() != 0 {
				vmr := proxmox.NewVmRef(vm.ProxmoxId())
				vmr.SetNode(ServerConfiguration.ProxmoxNode) // Assuming all VMs are on the same node for this example

				rawSnapshots, err := proxmox.ListSnapshots(proxmoxClient, vmr)
				if err != nil {
					errorsResponse = append(errorsResponse, dto.GetSnapshotsResponseErrorsItem{Vmid: int32(vm.ProxmoxId()), Vmname: vm.Name(), Error: "Error getting snapshots: " + err.Error()})
					continue
				}

				formattedSnapshots := rawSnapshots.FormatSnapshotsList()
				for _, snap := range formattedSnapshots {
					snapshotsResponse = append(snapshotsResponse, dto.GetSnapshotsResponseSnapshotsItem{
						Name:        string(snap.Name),
						IncludesRAM: snap.VmState,
						Description: string(snap.Description),
						Snaptime:    int64(snap.SnapTime),
						Parent:      string(snap.Parent),
						Vmid:        int32(vm.ProxmoxId()),
						Vmname:      vm.Name(),
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
				errorsResponse = append(errorsResponse, dto.GetSnapshotsResponseErrorsItem{Error: "Invalid VM ID: " + vmID})
				continue
			}
			vmr := proxmox.NewVmRef(vmIDInt)
			vmr.SetNode(ServerConfiguration.ProxmoxNode) // Assuming all VMs are on the same node for this example

			rawSnapshots, err := proxmox.ListSnapshots(proxmoxClient, vmr)
			if err != nil {
				errorsResponse = append(errorsResponse, dto.GetSnapshotsResponseErrorsItem{Vmid: int32(vmIDInt), Vmname: "", Error: "Error getting snapshots: " + err.Error()})
				continue
			}
			vmInfo, err := proxmoxClient.GetVmInfo(vmr)
			if err != nil {
				errorsResponse = append(errorsResponse, dto.GetSnapshotsResponseErrorsItem{Vmid: int32(vmIDInt), Vmname: "", Error: "Error getting VM info for VM " + vmID + ": " + err.Error()})
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
				snapshotsResponse = append(snapshotsResponse, dto.GetSnapshotsResponseSnapshotsItem{
					Name:        string(snap.Name),
					IncludesRAM: snap.VmState,
					Description: string(snap.Description),
					Snaptime:    int64(snap.SnapTime),
					Parent:      string(snap.Parent),
					Vmid:        int32(vmIDInt),
					Vmname:      vmName,
				})
			}
		}
	}

	return e.JSON(http.StatusOK, response)
}

// CreateSnapshot - creates a snapshot for specified VMs or all VMs in the range
func CreateSnapshot(e *core.RequestEvent) error {
	var payload dto.SnapshotsTakeRequest
	e.BindBody(&payload)
	proxmoxClient, err := GetProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}

	var successArray []int64
	var errors []dto.SnapshotsTakeResponseErrorsItem

	snapshotConfig := proxmox.ConfigSnapshot{
		Name:        proxmox.SnapshotName(payload.Name),
		Description: payload.Description,
		VmState:     payload.IncludeRAM,
	}

	usersRange := e.Get("range").(*models.Range)
	proxmoxGoClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get go proxmox client: "+err.Error())
	}

	// If no VMIDs provided, get all VMs in the range
	if len(payload.Vmids) == 0 {
		updateRangeVMData(e, usersRange, proxmoxGoClient)

		allVMs, err := getVMsForRange(usersRange.RangeId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to get VMs for range: "+err.Error())
		}

		for _, vm := range allVMs {
			if vm.ProxmoxId() != 0 {
				err = createSnapshotForVM(proxmoxClient, vm.ProxmoxId(), snapshotConfig)
				if err != nil {
					errors = append(errors, dto.SnapshotsTakeResponseErrorsItem{
						Vmid:   int32(vm.ProxmoxId()),
						Vmname: vm.Name(),
						Error:  err.Error(),
					})
				} else {
					successArray = append(successArray, int64(vm.ProxmoxId()))
				}
			}
		}
	} else {
		// Create snapshots for specified VMIDs
		for _, vmID := range payload.Vmids {
			err = createSnapshotForVM(proxmoxClient, vmID, snapshotConfig)
			if err != nil {
				errors = append(errors, dto.SnapshotsTakeResponseErrorsItem{
					Vmid:   int32(vmID),
					Vmname: "",
					Error:  err.Error(),
				})
			} else {
				successArray = append(successArray, int64(vmID))
			}
		}
	}

	response := dto.SnapshotsTakeResponse{
		Success: successArray,
		Errors:  errors,
	}
	return e.JSON(http.StatusOK, response)
}

// RollbackSnapshot - rolls back a snapshot for specified VMs or all VMs in the range
func RollbackSnapshot(e *core.RequestEvent) error {
	var payload dto.SnapshotsRollbackRequest
	e.BindBody(&payload)

	proxmoxClient, err := GetProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}
	proxmoxGoClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get go proxmox client: "+err.Error())
	}

	var successArray []int64
	var errors []dto.SnapshotsRollbackResponseErrorsItem

	snapshotName := proxmox.SnapshotName(payload.Name)

	usersRange := e.Get("range").(*models.Range)

	if len(payload.Vmids) == 0 {
		updateRangeVMData(e, usersRange, proxmoxGoClient)
		allVMs, err := getVMsForRange(usersRange.RangeId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to get VMs for range: "+err.Error())
		}

		for _, vm := range allVMs {
			if vm.ProxmoxId() != 0 {
				err = rollbackSnapshotForVM(proxmoxClient, vm.ProxmoxId(), snapshotName)
				if err != nil {
					errors = append(errors, dto.SnapshotsRollbackResponseErrorsItem{Vmid: int32(vm.ProxmoxId()), Vmname: vm.Name(), Error: err.Error()})
				} else {
					successArray = append(successArray, int64(vm.ProxmoxId()))
				}
			}
		}
	} else {
		// Rollback snapshots for specified VMIDs
		for _, vmID := range payload.Vmids {
			err = rollbackSnapshotForVM(proxmoxClient, int(vmID), snapshotName)
			if err != nil {
				errors = append(errors, dto.SnapshotsRollbackResponseErrorsItem{Vmid: int32(vmID), Vmname: "", Error: err.Error()})
			} else {
				successArray = append(successArray, int64(vmID))
			}
		}
	}

	response := dto.SnapshotsRollbackResponse{
		Success: successArray,
		Errors:  errors,
	}
	return e.JSON(http.StatusOK, response)
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
func RemoveSnapshot(e *core.RequestEvent) error {
	var payload dto.SnapshotsRemoveRequest
	e.BindBody(&payload)

	proxmoxClient, err := GetProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}

	proxmoxGoClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get go proxmox client: "+err.Error())
	}

	usersRange := e.Get("range").(*models.Range)
	var successArray []int64
	var errors []dto.SnapshotsRemoveResponseErrorsItem

	snapshotName := proxmox.SnapshotName(payload.Name)

	if len(payload.Vmids) == 0 {
		updateRangeVMData(e, usersRange, proxmoxGoClient)
		allVMs, err := getVMsForRange(usersRange.RangeId())
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to get VMs for range: "+err.Error())
		}

		for _, vm := range allVMs {
			if vm.ProxmoxId() != 0 {
				err = removeSnapshotForVM(proxmoxClient, vm.ProxmoxId(), snapshotName)
				if err != nil {
					errors = append(errors, dto.SnapshotsRemoveResponseErrorsItem{Vmid: int32(vm.ProxmoxId()), Vmname: vm.Name(), Error: err.Error()})
				} else {
					successArray = append(successArray, int64(vm.ProxmoxId()))
				}
			}
		}
	} else {
		// Remove snapshots for specified VMIDs
		for _, vmID := range payload.Vmids {
			err = removeSnapshotForVM(proxmoxClient, int(vmID), snapshotName)
			if err != nil {
				errors = append(errors, dto.SnapshotsRemoveResponseErrorsItem{Vmid: int32(vmID), Vmname: "", Error: err.Error()})
			} else {
				successArray = append(successArray, int64(vmID))
			}
		}
	}

	response := dto.SnapshotsRemoveResponse{
		Success: successArray,
		Errors:  errors,
	}
	return e.JSON(http.StatusOK, response)
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
