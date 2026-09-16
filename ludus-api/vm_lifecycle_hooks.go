package ludusapi

import (
	"context"
	"fmt"

	"ludusapi/pluginrpc"
)

type VMHookRequest = pluginrpc.VMHookRequest
type StartVMHookRequest = pluginrpc.VMHookRequest
type StartVMHookResult = pluginrpc.VMHookDecision
type VMStatus = pluginrpc.VMStatus
type VMStatusHookResult = pluginrpc.VMHookResponse
type VMAddressHookResult = pluginrpc.VMHookResponse

const (
	StartVMContinue = pluginrpc.VMHookContinue
	StartVMHandled  = pluginrpc.VMHookHandled

	VMStatusRunning  = pluginrpc.VMStatusRunning
	VMStatusStopped  = pluginrpc.VMStatusStopped
	VMStatusStarting = pluginrpc.VMStatusStarting
	VMStatusStopping = pluginrpc.VMStatusStopping

	StartVMSourceAPI                 = "api"
	StartVMSourceDeployment          = "deployment"
	StartVMSourceDeploymentFirstBoot = "deployment-first-boot"
)

func (s *Server) pluginSnapshot() []*managedPlugin {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	return append([]*managedPlugin(nil), s.plugins...)
}

func pluginIsInitialized(plugin *managedPlugin) bool {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	return plugin.initialized
}

func pluginVMHook(ctx context.Context, plugin *managedPlugin, operation pluginrpc.VMHookOperation, request VMHookRequest) (pluginrpc.VMHookResponse, error) {
	if err := ctx.Err(); err != nil {
		return pluginrpc.VMHookResponse{}, err
	}
	hookClient, ok := plugin.rpc.(pluginrpc.VMHookClient)
	if !ok {
		return pluginrpc.VMHookResponse{}, fmt.Errorf("plugin %s does not support VM hook RPC", plugin.metadata.Name)
	}
	request.Operation = operation
	response, err := hookClient.VMHook(request)
	if err != nil {
		return pluginrpc.VMHookResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return pluginrpc.VMHookResponse{}, err
	}
	return response, nil
}

func pluginSelectsVM(ctx context.Context, plugin *managedPlugin, request VMHookRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if !plugin.metadata.VMHooks.Select {
		return true, nil
	}
	result, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookSelect, request)
	if err != nil {
		return false, fmt.Errorf("plugin %s could not select VMID %d: %w", plugin.metadata.Name, request.VMID, err)
	}
	return result.Selected, nil
}

func (s *Server) hasStartVMHooks() bool {
	for _, plugin := range s.pluginSnapshot() {
		if plugin.metadata.VMHooks.Start {
			return true
		}
	}
	return false
}

func (s *Server) hasStopVMHooks() bool {
	for _, plugin := range s.pluginSnapshot() {
		if plugin.metadata.VMHooks.Stop {
			return true
		}
	}
	return false
}

func (s *Server) hasVMStatusHooks() bool {
	for _, plugin := range s.pluginSnapshot() {
		if plugin.metadata.VMHooks.Status {
			return true
		}
	}
	return false
}

func (s *Server) hasBeforeDeleteVMHooks() bool {
	for _, plugin := range s.pluginSnapshot() {
		if plugin.metadata.VMHooks.BeforeDelete {
			return true
		}
	}
	return false
}

func (s *Server) hasVMLifecycleHooks() bool {
	return s.hasStartVMHooks() || s.hasStopVMHooks() || s.hasVMStatusHooks() || s.hasBeforeDeleteVMHooks() || s.hasVMAddressHooks()
}

func (s *Server) runStartVMHooks(ctx context.Context, request StartVMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.Start {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return false, err
		}
		if !selected {
			continue
		}
		if !pluginIsInitialized(plugin) {
			return false, fmt.Errorf("plugin %s registered a start VM hook but is not initialized", plugin.metadata.Name)
		}
		result, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookStart, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected start of VMID %d: %w", plugin.metadata.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled start of VMID %d", plugin.metadata.Name, request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid start VM hook result %d for VMID %d", plugin.metadata.Name, result.Decision, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runStopVMHooks(ctx context.Context, request VMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.Stop {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return false, err
		}
		if !selected {
			continue
		}
		if !pluginIsInitialized(plugin) {
			return false, fmt.Errorf("plugin %s registered a stop VM hook but is not initialized", plugin.metadata.Name)
		}
		result, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookStop, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected stop of VMID %d: %w", plugin.metadata.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled stop of VMID %d", plugin.metadata.Name, request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid stop VM hook result %d for VMID %d", plugin.metadata.Name, result.Decision, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runVMStatusHooks(ctx context.Context, request VMHookRequest) (VMStatus, bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.Status {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return "", false, err
		}
		if !selected {
			continue
		}
		if !pluginIsInitialized(plugin) {
			return "", false, fmt.Errorf("plugin %s registered a VM status hook but is not initialized", plugin.metadata.Name)
		}
		result, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookStatus, request)
		if err != nil {
			return "", false, fmt.Errorf("plugin %s failed status for VMID %d: %w", plugin.metadata.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			switch result.Status {
			case VMStatusRunning, VMStatusStopped, VMStatusStarting, VMStatusStopping:
				return result.Status, true, nil
			default:
				return "", false, fmt.Errorf("plugin %s returned invalid status %q for VMID %d", plugin.metadata.Name, result.Status, request.VMID)
			}
		default:
			return "", false, fmt.Errorf("plugin %s returned invalid VM status hook result %d for VMID %d", plugin.metadata.Name, result.Decision, request.VMID)
		}
	}
	return "", false, nil
}

func (s *Server) runBeforeDeleteVMHooks(ctx context.Context, request VMHookRequest) error {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.BeforeDelete {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return &vmDeleteHookError{err}
		}
		if !selected {
			continue
		}
		if !pluginIsInitialized(plugin) {
			return &vmDeleteHookError{fmt.Errorf("plugin %s registered a before-delete VM hook but is not initialized", plugin.metadata.Name)}
		}
		if _, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookBeforeDelete, request); err != nil {
			return &vmDeleteHookError{fmt.Errorf("plugin %s rejected deletion of VMID %d: %w", plugin.metadata.Name, request.VMID, err)}
		}
	}
	return nil
}
