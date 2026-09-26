package ludusapi

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"ludusapi/pluginrpc"
)

func (s *Server) hasVMAddressHooks() bool {
	for _, plugin := range s.pluginSnapshot() {
		if plugin.metadata.VMHooks.Address {
			return true
		}
	}
	return false
}

func (s *Server) runVMAddressHooks(ctx context.Context, request VMHookRequest) (VMAddressHookResult, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.Address {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return VMAddressHookResult{}, err
		}
		if !selected {
			continue
		}
		if !pluginIsInitialized(plugin) {
			return VMAddressHookResult{}, fmt.Errorf("plugin %s registered an address hook but is not initialized", plugin.metadata.Name)
		}
		result, err := pluginVMHook(ctx, plugin, pluginrpc.VMHookAddress, request)
		if err != nil {
			return VMAddressHookResult{}, fmt.Errorf("plugin %s failed address discovery for VMID %d: %w", plugin.metadata.Name, request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			ip := net.ParseIP(result.Address)
			if result.Ready && (ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
				return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an invalid bootstrap IPv4 address", plugin.metadata.Name)
			}
			if !result.Ready && result.Address != "" {
				return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an address before it was ready", plugin.metadata.Name)
			}
			return result, nil
		default:
			return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an invalid address hook decision", plugin.metadata.Name)
		}
	}
	return VMAddressHookResult{Decision: StartVMContinue}, nil
}

func (s *Server) handleVMHookServiceAddress(response http.ResponseWriter, request *http.Request) {
	body, ok := decodeVMHookServiceRequest(response, request)
	if !ok {
		return
	}
	hookRequest, err := s.verifiedVMHookRequest(request.Context(), body, StartVMSourceDeploymentFirstBoot)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	result, err := s.runVMAddressHooks(request.Context(), hookRequest)
	if err != nil {
		writeVMHookServiceResponse(response, http.StatusConflict, vmHookServiceResponse{Error: err.Error()})
		return
	}
	writeVMHookServiceResponse(response, http.StatusOK, vmHookServiceResponse{Handled: result.Decision == StartVMHandled, Ready: result.Ready, Address: result.Address})
}
