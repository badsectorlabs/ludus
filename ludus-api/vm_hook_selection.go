package ludusapi

import (
	"context"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

type deploymentVMHookCapabilities struct {
	Start   bool `json:"start"`
	Address bool `json:"address"`
	Revert  bool `json:"revert"`
}

// deploymentVMHooks selects the deployment hooks for each VM and returns the
// nodes its plugins limit placement to, by VM name (VMs without a limit are
// absent). candidates are the nodes Ludus may place VMs on.
func (s *Server) deploymentVMHooks(ctx context.Context, rangeID string, names, candidates []string) (map[string]deploymentVMHookCapabilities, map[string][]string, error) {
	hooks := make(map[string]deploymentVMHookCapabilities)
	limits := make(map[string][]string)
	for _, name := range names {
		request := VMHookRequest{Source: StartVMSourceDeploymentFirstBoot, RangeID: rangeID, Pool: rangeID, VMName: name, Candidates: candidates}
		selected, nodes, err := s.selectedVMHooks(ctx, request, protocol.VMHookCapabilities{Start: true, Address: true, Revert: true})
		if err != nil {
			return nil, nil, err
		}
		hooks[name] = deploymentVMHookCapabilities{Start: selected.Start, Address: selected.Address, Revert: selected.Revert}
		if nodes != nil {
			limits[name] = nodes
		}
	}
	return hooks, limits, nil
}

func (s *Server) selectsVMForPower(ctx context.Context, request VMHookRequest, action string) (bool, error) {
	want := protocol.VMHookCapabilities{Start: action == "on", Stop: action == "off"}
	selected, _, err := s.selectedVMHooks(ctx, request, want)
	return selected.Any(), err
}

// Distinguish a provider veto from an ordinary Proxmox deletion failure.
type vmDeleteHookError struct{ error }

func (e *vmDeleteHookError) Unwrap() error { return e.error }

func (s *Server) selectsVMForDelete(ctx context.Context, request VMHookRequest) (bool, error) {
	selected, _, err := s.selectedVMHooks(ctx, request, protocol.VMHookCapabilities{BeforeDelete: true})
	if err != nil {
		return false, &vmDeleteHookError{err}
	}
	return selected.BeforeDelete, nil
}
