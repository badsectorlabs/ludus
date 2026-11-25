package ludusapi

import (
	"context"
	"fmt"
	"slices"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type VMHookRequest = protocol.VMHookRequest
type StartVMHookRequest = protocol.VMHookRequest
type StartVMHookResult = protocol.VMHookDecision
type VMStatus = protocol.VMStatus
type VMStatusHookResult = protocol.VMHookResponse
type VMAddressHookResult = protocol.VMHookResponse

const (
	StartVMContinue = protocol.VMHookContinue
	StartVMHandled  = protocol.VMHookHandled

	VMStatusRunning  = protocol.VMStatusRunning
	VMStatusStopped  = protocol.VMStatusStopped
	VMStatusStarting = protocol.VMStatusStarting
	VMStatusStopping = protocol.VMStatusStopping

	StartVMSourceAPI                 = "api"
	StartVMSourceDeployment          = "deployment"
	StartVMSourceDeploymentFirstBoot = "deployment-first-boot"
	StartVMSourceSnapshotRevert      = "snapshot-revert"
)

func pluginIsInitialized(plugin *managedPlugin) bool {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	return plugin.initialized
}

func pluginVMHook(ctx context.Context, plugin *managedPlugin, operation protocol.VMHookOperation, request VMHookRequest) (protocol.VMHookResponse, error) {
	if err := ctx.Err(); err != nil {
		return protocol.VMHookResponse{}, err
	}
	call, done := plugin.host.beginPluginCall(ctx)
	defer done()
	request.Call = call
	request.Operation = operation
	response, err := plugin.rpc.VMHook(request)
	if err != nil {
		return protocol.VMHookResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return protocol.VMHookResponse{}, err
	}
	return response, nil
}

func pluginSelectsVM(ctx context.Context, plugin *managedPlugin, request VMHookRequest) (bool, error) {
	selected, _, err := pluginSelection(ctx, plugin, request)
	return selected, err
}

// pluginSelection reports whether the plugin selects the VM and, for a
// selected VM, the nodes it limits placement to (nil when unlimited).
func pluginSelection(ctx context.Context, plugin *managedPlugin, request VMHookRequest) (bool, []string, error) {
	if err := ctx.Err(); err != nil {
		return false, nil, err
	}
	if !plugin.manifest.VMHooks.Select {
		return true, nil, nil
	}
	result, err := pluginVMHook(ctx, plugin, protocol.VMHookSelect, request)
	if err != nil {
		return false, nil, fmt.Errorf("plugin %s could not select VMID %d: %w", plugin.manifest.Name, request.VMID, err)
	}
	if !result.Selected {
		return false, nil, nil
	}
	return true, result.Nodes, nil
}

// limitNodes narrows the nodes a VM may be placed on (nil: unlimited) by a
// plugin's limit. It fails when no node satisfies both.
func limitNodes(vmName string, current, limit []string) ([]string, error) {
	if len(limit) == 0 {
		return current, nil
	}
	if current == nil {
		return append([]string(nil), limit...), nil
	}
	var both []string
	for _, node := range current {
		if slices.Contains(limit, node) {
			both = append(both, node)
		}
	}
	if len(both) == 0 {
		return nil, fmt.Errorf("VM hook plugins allow no common node for %s: %v and %v", vmName, current, limit)
	}
	return both, nil
}

// declaredVMHooks is the union of the VM hook capabilities this service can
// dispatch: those of its own plugins and, in the regular service, those
// declared by active root-run plugins, which the admin service hosts.
func (s *Server) declaredVMHooks() protocol.VMHookCapabilities {
	var hooks protocol.VMHookCapabilities
	if s == nil {
		return hooks
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	hooks = s.remoteVMHooks
	for _, plugin := range s.plugins {
		hooks = unionVMHooks(hooks, plugin.manifest.VMHooks)
	}
	return hooks
}

func unionVMHooks(a, b protocol.VMHookCapabilities) protocol.VMHookCapabilities {
	return protocol.VMHookCapabilities{
		Select: a.Select || b.Select, Start: a.Start || b.Start, Stop: a.Stop || b.Stop,
		Status: a.Status || b.Status, BeforeDelete: a.BeforeDelete || b.BeforeDelete, Address: a.Address || b.Address,
		Revert: a.Revert || b.Revert,
	}
}

// hookOperations keeps only the lifecycle operations (not Select) of hooks
// that are also in want.
func hookOperations(hooks, want protocol.VMHookCapabilities) protocol.VMHookCapabilities {
	return protocol.VMHookCapabilities{
		Start: hooks.Start && want.Start, Stop: hooks.Stop && want.Stop, Status: hooks.Status && want.Status,
		BeforeDelete: hooks.BeforeDelete && want.BeforeDelete, Address: hooks.Address && want.Address,
		Revert: hooks.Revert && want.Revert,
	}
}

// selectedVMHooks returns the operations in want that some plugin providing
// them selects for the VM, and the nodes those plugins limit its placement to
// (nil when unlimited). Plugins this service hosts are asked directly;
// root-run plugins declared to the regular service are asked by the admin
// service through the VM hook socket.
func (s *Server) selectedVMHooks(ctx context.Context, request VMHookRequest, want protocol.VMHookCapabilities) (protocol.VMHookCapabilities, []string, error) {
	selected, nodes, err := s.selectLocalVMHooks(ctx, request, want)
	if err != nil {
		return protocol.VMHookCapabilities{}, nil, err
	}
	s.pluginMu.Lock()
	remote := hookOperations(s.remoteVMHooks, want)
	s.pluginMu.Unlock()
	if !remote.Any() {
		return selected, nodes, nil
	}
	remoteSelected, remoteNodes, err := callVMHookSelect(ctx, request, remote)
	if err != nil {
		return protocol.VMHookCapabilities{}, nil, err
	}
	if nodes, err = limitNodes(request.VMName, nodes, remoteNodes); err != nil {
		return protocol.VMHookCapabilities{}, nil, err
	}
	return unionVMHooks(selected, hookOperations(remoteSelected, want)), nodes, nil
}

// selectLocalVMHooks asks this service's plugins that provide any operation in
// want whether they select the VM, and collects their placement limits.
func (s *Server) selectLocalVMHooks(ctx context.Context, request VMHookRequest, want protocol.VMHookCapabilities) (protocol.VMHookCapabilities, []string, error) {
	var selected protocol.VMHookCapabilities
	var nodes []string
	for _, plugin := range s.pluginSnapshot() {
		operations := hookOperations(plugin.manifest.VMHooks, want)
		if !operations.Any() {
			continue
		}
		applies, limit, err := pluginSelection(ctx, plugin, request)
		if err != nil {
			return protocol.VMHookCapabilities{}, nil, err
		}
		if applies {
			selected = unionVMHooks(selected, operations)
			if nodes, err = limitNodes(request.VMName, nodes, limit); err != nil {
				return protocol.VMHookCapabilities{}, nil, err
			}
		}
	}
	return selected, nodes, nil
}

func (s *Server) hasStartVMHooks() bool { return s.declaredVMHooks().Start }

func (s *Server) hasStopVMHooks() bool { return s.declaredVMHooks().Stop }

func (s *Server) hasVMStatusHooks() bool { return s.declaredVMHooks().Status }

func (s *Server) hasBeforeDeleteVMHooks() bool { return s.declaredVMHooks().BeforeDelete }

func (s *Server) hasVMRevertHooks() bool { return s.declaredVMHooks().Revert }

func (s *Server) hasVMLifecycleHooks() bool {
	return hookOperations(s.declaredVMHooks(), protocol.VMHookCapabilities{Start: true, Stop: true, Status: true, BeforeDelete: true, Address: true, Revert: true}).Any()
}

func (s *Server) runStartVMHooks(ctx context.Context, request StartVMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.manifest.VMHooks.Start {
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
			return false, fmt.Errorf("plugin %s registered a start VM hook but is not initialized", plugin.manifest.Name)
		}
		result, err := pluginVMHook(ctx, plugin, protocol.VMHookStart, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected start of VMID %d: %w", plugin.manifest.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled start of VMID %d", plugin.manifest.Name, request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid start VM hook result %d for VMID %d", plugin.manifest.Name, result.Decision, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runStopVMHooks(ctx context.Context, request VMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.manifest.VMHooks.Stop {
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
			return false, fmt.Errorf("plugin %s registered a stop VM hook but is not initialized", plugin.manifest.Name)
		}
		result, err := pluginVMHook(ctx, plugin, protocol.VMHookStop, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected stop of VMID %d: %w", plugin.manifest.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled stop of VMID %d", plugin.manifest.Name, request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid stop VM hook result %d for VMID %d", plugin.manifest.Name, result.Decision, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runVMStatusHooks(ctx context.Context, request VMHookRequest) (VMStatus, bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.manifest.VMHooks.Status {
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
			return "", false, fmt.Errorf("plugin %s registered a VM status hook but is not initialized", plugin.manifest.Name)
		}
		result, err := pluginVMHook(ctx, plugin, protocol.VMHookStatus, request)
		if err != nil {
			return "", false, fmt.Errorf("plugin %s failed status for VMID %d: %w", plugin.manifest.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			switch result.Status {
			case VMStatusRunning, VMStatusStopped, VMStatusStarting, VMStatusStopping:
				return result.Status, true, nil
			default:
				return "", false, fmt.Errorf("plugin %s returned invalid status %q for VMID %d", plugin.manifest.Name, result.Status, request.VMID)
			}
		default:
			return "", false, fmt.Errorf("plugin %s returned invalid VM status hook result %d for VMID %d", plugin.manifest.Name, result.Decision, request.VMID)
		}
	}
	return "", false, nil
}

func (s *Server) runBeforeDeleteVMHooks(ctx context.Context, request VMHookRequest) error {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.manifest.VMHooks.BeforeDelete {
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
			return &vmDeleteHookError{fmt.Errorf("plugin %s registered a before-delete VM hook but is not initialized", plugin.manifest.Name)}
		}
		if _, err := pluginVMHook(ctx, plugin, protocol.VMHookBeforeDelete, request); err != nil {
			return &vmDeleteHookError{fmt.Errorf("plugin %s rejected deletion of VMID %d: %w", plugin.manifest.Name, request.VMID, err)}
		}
	}
	return nil
}

// runVMRevertHooks offers a snapshot revert to the plugins that select the VM.
// A handled revert has rolled the VM back to request.Snapshot and restored the
// state in request.Status; otherwise Ludus reverts the VM itself.
func (s *Server) runVMRevertHooks(ctx context.Context, request VMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.manifest.VMHooks.Revert {
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
			return false, fmt.Errorf("plugin %s registered a revert VM hook but is not initialized", plugin.manifest.Name)
		}
		result, err := pluginVMHook(ctx, plugin, protocol.VMHookRevert, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s failed to revert VMID %d to snapshot %s: %w", plugin.manifest.Name, request.VMID, request.Snapshot, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled revert of VMID %d to snapshot %s", plugin.manifest.Name, request.VMID, request.Snapshot))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid revert VM hook result %d for VMID %d", plugin.manifest.Name, result.Decision, request.VMID)
		}
	}
	return false, nil
}
