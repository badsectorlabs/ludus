package ludusapi

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// vmHookSnapshotPattern is Proxmox's snapshot name format (pve-configid, at
// most 40 characters).
var vmHookSnapshotPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{1,39}$`)

func (s *Server) hasVMRevertHooks() bool { return s.declaredVMHooks().Revert }

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

func (s *Server) handleVMHookServiceRevert(response http.ResponseWriter, request *http.Request) {
	body, ok := decodeVMHookServiceRequest(response, request)
	if !ok {
		return
	}
	if !vmHookSnapshotPattern.MatchString(body.Snapshot) {
		writeVMHookServiceResponse(response, http.StatusBadRequest, vmHookServiceResponse{Error: "snapshot must be a Proxmox snapshot name"})
		return
	}
	hookRequest, err := s.verifiedVMHookRequest(request.Context(), body, StartVMSourceSnapshotRevert)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	hookRequest.Snapshot = body.Snapshot
	handled, err := s.runVMRevertHooks(request.Context(), hookRequest)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	writeVMHookServiceResponse(response, http.StatusOK, vmHookServiceResponse{Handled: handled})
}
