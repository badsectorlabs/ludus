package ludusapi

import (
	"context"
	"fmt"
	"net/http"
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

	err = destroyVM(ctx, proxmoxClient, vmID)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to destroy VM: "+err.Error())
	}

	logger.Debug(fmt.Sprintf("VM %d destroyed successfully", vmID))
	return JSONResult(e, http.StatusOK, fmt.Sprintf("VM %d destroyed successfully", vmID))
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
