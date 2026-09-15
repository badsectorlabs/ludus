package ludusapi

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// VMAddressHook optionally supplies a newly started VM's bootstrap address.
// A handled result with Ready=false requests another discovery attempt.
// Plugins without this capability retain inventory-based address discovery.
type VMAddressHook interface {
	VMAddress(context.Context, VMHookRequest) (VMAddressHookResult, error)
}

type VMAddressHookResult struct {
	Decision StartVMHookResult
	Ready    bool
	Address  string
}

func (s *Server) hasVMAddressHooks() bool {
	for _, plugin := range s.plugins {
		if _, ok := plugin.(VMAddressHook); ok {
			return true
		}
	}
	return false
}

func (s *Server) runVMAddressHooks(ctx context.Context, request VMHookRequest) (VMAddressHookResult, error) {
	for _, plugin := range s.plugins {
		handler, ok := plugin.(VMAddressHook)
		if !ok {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return VMAddressHookResult{}, err
		}
		if !selected {
			continue
		}
		if !plugin.Initialized() {
			return VMAddressHookResult{}, fmt.Errorf("plugin %s registered an address hook but is not initialized", plugin.Name())
		}
		result, err := handler.VMAddress(ctx, request)
		if err != nil {
			return VMAddressHookResult{}, fmt.Errorf("plugin %s failed address discovery for VMID %d: %w", plugin.Name(), request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			ip := net.ParseIP(result.Address)
			if result.Ready && (ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
				return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an invalid bootstrap IPv4 address", plugin.Name())
			}
			if !result.Ready && result.Address != "" {
				return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an address before it was ready", plugin.Name())
			}
			return result, nil
		default:
			return VMAddressHookResult{}, fmt.Errorf("plugin %s returned an invalid address hook decision", plugin.Name())
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
