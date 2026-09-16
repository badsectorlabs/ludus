package ludusapi

import (
	"context"
)

type deploymentVMHookCapabilities struct {
	Start   bool `json:"start"`
	Address bool `json:"address"`
}

func (s *Server) deploymentVMHooks(ctx context.Context, rangeID string, names []string) (map[string]deploymentVMHookCapabilities, error) {
	hooks := make(map[string]deploymentVMHookCapabilities)
	for _, name := range names {
		request := VMHookRequest{Source: StartVMSourceDeploymentFirstBoot, RangeID: rangeID, Pool: rangeID, VMName: name}
		capabilities := deploymentVMHookCapabilities{}
		for _, plugin := range s.pluginSnapshot() {
			starts := plugin.metadata.VMHooks.Start
			addresses := plugin.metadata.VMHooks.Address
			if !starts && !addresses {
				continue
			}
			selected, err := pluginSelectsVM(ctx, plugin, request)
			if err != nil {
				return nil, err
			}
			if selected {
				capabilities.Start = capabilities.Start || starts
				capabilities.Address = capabilities.Address || addresses
			}
		}
		hooks[name] = capabilities
	}
	return hooks, nil
}

func (s *Server) selectsVMForPower(ctx context.Context, request VMHookRequest, action string) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		start := plugin.metadata.VMHooks.Start
		stop := plugin.metadata.VMHooks.Stop
		if (action == "on" && !start) || (action == "off" && !stop) {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil || selected {
			return selected, err
		}
	}
	return false, nil
}

// Distinguish a provider veto from an ordinary Proxmox deletion failure.
type vmDeleteHookError struct{ error }

func (e *vmDeleteHookError) Unwrap() error { return e.error }

func (s *Server) selectsVMForDelete(ctx context.Context, request VMHookRequest) (bool, error) {
	for _, plugin := range s.pluginSnapshot() {
		if !plugin.metadata.VMHooks.BeforeDelete {
			continue
		}
		selected, err := pluginSelectsVM(ctx, plugin, request)
		if err != nil {
			return false, &vmDeleteHookError{err}
		}
		if selected {
			return true, nil
		}
	}
	return false, nil
}
