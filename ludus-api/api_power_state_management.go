package ludusapi

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"ludusapi/dto"

	"github.com/pocketbase/pocketbase/core"
)

func PowerAction(e *core.RequestEvent, action string) error {
	var powerBody dto.PowerOffRangeRequest
	e.BindBody(&powerBody)

	// Get the proxmox client for the user here to check if the ROOT API key is being used
	// and fail early if it is
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}

	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	err = updateRangeVMData(e, usersRange, proxmoxClient)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}
	allVMs, err := getVMsForRange(usersRange.RangeId())
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get VMs for range: "+err.Error())
	}
	var vmids []int

	if len(powerBody.Machines) == 0 {
		return JSONError(e, http.StatusConflict, "you must specify a VM, comma separated list of VMs, or 'all'")
	} else if len(powerBody.Machines) == 1 && powerBody.Machines[0] == "all" {
		for _, vm := range allVMs {
			vmids = append(vmids, vm.ProxmoxId())
		}
	} else {
		// One or more machine names passed in
		vmIDsByName := make(map[string]int, len(allVMs))
		vmIDsByID := make(map[int]int, len(allVMs))
		for _, vm := range allVMs {
			vmIDsByName[vm.Name()] = vm.ProxmoxId()
			vmIDsByID[vm.ProxmoxId()] = vm.ProxmoxId()
		}

		var missingMachines []string
		for _, machineName := range powerBody.Machines {
			normalizedMachineName := strings.TrimSpace(machineName)
			var vmid int
			var found bool

			// If a numeric value is provided, prefer lookup by VMID first.
			parsedVMID, parseErr := strconv.Atoi(normalizedMachineName)
			if parseErr == nil {
				vmid, found = vmIDsByID[parsedVMID]
			}

			// If VMID lookup fails (or value is non-numeric), fall back to name lookup.
			if !found {
				vmid, found = vmIDsByName[normalizedMachineName]
			}
			if !found {
				missingMachines = append(missingMachines, normalizedMachineName)
				continue
			}
			vmids = append(vmids, vmid)
		}

		if len(missingMachines) > 0 {
			slices.Sort(missingMachines)
			return JSONError(e, http.StatusConflict, "Unable to find VM(s) in your range by name: "+strings.Join(missingMachines, ", "))
		}
	}

	if action == "off" {
		errs := PowerOffVMs(context.Background(), proxmoxClient, vmids)
		if len(errs) > 0 {
			var errStrs []string
			for _, err := range errs {
				errStrs = append(errStrs, err.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Unable to power off VMs: "+strings.Join(errStrs, ", "))
		}
	} else {
		errs := PowerOnVMs(context.Background(), proxmoxClient, vmids)
		if len(errs) > 0 {
			var errStrs []string
			for _, err := range errs {
				errStrs = append(errStrs, err.Error())
			}
			return JSONError(e, http.StatusInternalServerError, "Unable to power on VMs: "+strings.Join(errStrs, ", "))
		}
	}

	if len(powerBody.Machines) > 1 {
		return JSONResult(e, http.StatusOK, fmt.Sprintf("Powered %s %d VMs", action, len(powerBody.Machines)))
	} else {
		return JSONResult(e, http.StatusOK, fmt.Sprintf("Powered %s VM: %s", action, powerBody.Machines[0]))
	}
}

// PowerOffRange - powers off all range VMs
func PowerOffRange(e *core.RequestEvent) error {
	return PowerAction(e, "off")
}

// PowerOnRange - powers on all range VMs
func PowerOnRange(e *core.RequestEvent) error {
	return PowerAction(e, "on")
}
