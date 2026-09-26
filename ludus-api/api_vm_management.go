package ludusapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// DestroyVM - halts and destroys a VM
func DestroyVM(e *core.RequestEvent) error {
	// Get vmID from path parameter
	vmIDStr := e.Request.PathValue("vmID")
	if vmIDStr == "" {
		return JSONError(e, http.StatusBadRequest, "VMID is required")
	}

	vmID, err := strconv.Atoi(vmIDStr)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid VMID: must be a number")
	}

	// Get the proxmox client
	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get proxmox client: "+err.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if server.hasBeforeDeleteVMHooks() {
		if os.Geteuid() != 0 {
			resource, lookupErr := getVMResource(ctx, proxmoxClient, vmID)
			if lookupErr != nil {
				return JSONError(e, http.StatusInternalServerError, lookupErr.Error())
			}
			selected, selectErr := server.selectsVMForDelete(ctx, VMHookRequest{
				Source: StartVMSourceAPI, RangeID: resource.Pool, Pool: resource.Pool,
				VMID: vmID, VMName: resource.Name, Node: resource.Node, Status: resource.Status,
			})
			if selectErr != nil {
				return JSONError(e, http.StatusInternalServerError, selectErr.Error())
			}
			if selected {
				return proxyToAdmin(e)
			}
		}
		usersRange, rangeErr := GetRange(e)
		if rangeErr != nil {
			return rangeErr
		}
		err = server.DestroyVM(ctx, proxmoxClient, usersRange.RangeId(), vmID)
	} else {
		// Preserve deletion by VMID using the caller's Proxmox permissions.
		err = destroyVM(ctx, proxmoxClient, vmID)
	}
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to destroy VM: "+err.Error())
	}

	logger.Debug(fmt.Sprintf("VM %d destroyed successfully", vmID))
	return JSONResult(e, http.StatusOK, fmt.Sprintf("VM %d destroyed successfully", vmID))
}

// DestroyVM runs plugin cleanup hooks against verified cluster metadata before
// performing the ordinary Proxmox stop-and-delete operation.
func (s *Server) DestroyVM(ctx context.Context, proxmoxClient *goproxmox.Client, rangeID string, vmID int) error {
	if !s.hasBeforeDeleteVMHooks() {
		return destroyVM(ctx, proxmoxClient, vmID)
	}
	resource, err := getVMResource(ctx, proxmoxClient, vmID)
	if err != nil {
		return err
	}
	request := VMHookRequest{
		Source: StartVMSourceAPI, RangeID: resource.Pool, VMID: vmID,
		VMName: resource.Name, Node: resource.Node, Pool: resource.Pool, Status: resource.Status,
	}
	selected, err := s.selectsVMForDelete(ctx, request)
	if err != nil {
		return err
	}
	if !selected {
		return destroyVM(ctx, proxmoxClient, vmID)
	}
	if resource.Type != "qemu" || resource.Template == 1 {
		return &vmDeleteHookError{fmt.Errorf("VMID %d is not a QEMU range VM", vmID)}
	}
	if resource.Pool != rangeID {
		return &vmDeleteHookError{fmt.Errorf("VMID %d belongs to pool %q, not range %q", vmID, resource.Pool, rangeID)}
	}
	if err := s.runBeforeDeleteVMHooks(ctx, request); err != nil {
		return err
	}
	return destroyVM(ctx, proxmoxClient, vmID)
}

func destroyVM(ctx context.Context, proxmoxClient *goproxmox.Client, vmID int) error {

	// Get the VM object
	vm, err := getVMObjectFromVMID(ctx, proxmoxClient, vmID)
	if err != nil {
		return err
	}

	// Stop the VM if it's running
	if vm.IsRunning() {
		logger.Debug(fmt.Sprintf("Stopping VM %d before destruction...", vmID))
		task, err := vm.Stop(ctx)
		if err != nil {
			return err
		}

		// Wait for the stop task to complete
		err = task.Wait(ctx, 1*time.Second, 30*time.Second)
		if err != nil {
			return err
		}
		logger.Debug(fmt.Sprintf("VM %d stopped successfully", vmID))
	}

	// Delete the VM
	logger.Debug(fmt.Sprintf("Destroying VM %d...", vmID))
	task, err := vm.Delete(ctx)
	if err != nil {
		return err
	}

	// Wait for the delete task to complete
	err = task.Wait(ctx, 1*time.Second, 30*time.Second)
	if err != nil {
		return err
	}

	return nil
}

func stopVM(ctx context.Context, proxmoxClient *goproxmox.Client, vmID int) error {
	vm, err := getVMObjectFromVMID(ctx, proxmoxClient, vmID)
	if err != nil {
		return err
	}

	// Stop the VM if it's running
	if vm.IsRunning() {
		logger.Debug(fmt.Sprintf("Stopping VM %d", vmID))
		task, err := vm.Stop(ctx)
		if err != nil {
			return err
		}
		// Wait for the stop task to complete
		err = task.Wait(ctx, 1*time.Second, 30*time.Second)
		if err != nil {
			return err
		}
		logger.Debug(fmt.Sprintf("VM %d stopped successfully", vmID))
	}
	return nil
}

func getVMObjectFromVMID(ctx context.Context, proxmoxClient *goproxmox.Client, vmID int) (*goproxmox.VirtualMachine, error) {
	// Find which node the VM is on
	nodeName, err := findNodeForVM(ctx, proxmoxClient, uint64(vmID))
	if err != nil {
		return nil, err
	}

	// Get the node object
	node, err := proxmoxClient.Node(ctx, nodeName)
	if err != nil {
		return nil, err
	}

	// Get the VM object
	vm, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return nil, err
	}

	return vm, nil
}
