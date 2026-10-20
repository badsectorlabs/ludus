package ludusapi

import (
	"context"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type deploymentVMHookCapabilities struct {
	Start   bool `json:"start"`
	Address bool `json:"address"`
}

func (s *Server) deploymentVMHooks(ctx context.Context, rangeID string, names []string) (map[string]deploymentVMHookCapabilities, error) {
	hooks := make(map[string]deploymentVMHookCapabilities)
	for _, name := range names {
		request := VMHookRequest{Source: StartVMSourceDeploymentFirstBoot, RangeID: rangeID, Pool: rangeID, VMName: name}
		selected, err := s.selectedVMHooks(ctx, request, protocol.VMHookCapabilities{Start: true, Address: true})
		if err != nil {
			return nil, err
		}
		hooks[name] = deploymentVMHookCapabilities{Start: selected.Start, Address: selected.Address}
	}
	return hooks, nil
}

func (s *Server) selectsVMForPower(ctx context.Context, request VMHookRequest, action string) (bool, error) {
	want := protocol.VMHookCapabilities{Start: action == "on", Stop: action == "off"}
	selected, err := s.selectedVMHooks(ctx, request, want)
	return selected.Any(), err
}

// Distinguish a provider veto from an ordinary Proxmox deletion failure.
type vmDeleteHookError struct{ error }

func (e *vmDeleteHookError) Unwrap() error { return e.error }

func (s *Server) selectsVMForDelete(ctx context.Context, request VMHookRequest) (bool, error) {
	selected, err := s.selectedVMHooks(ctx, request, protocol.VMHookCapabilities{BeforeDelete: true})
	if err != nil {
		return false, &vmDeleteHookError{err}
	}
	return selected.BeforeDelete, nil
}
