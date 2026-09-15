package ludusapi

import (
	"context"
	"fmt"
)

// VMHookSelector optionally limits a plugin's lifecycle hooks to its own VMs.
// Selection must be read-only and work in both the regular and admin processes.
// During deployment planning VMID can be zero: select by the range's YAML and
// VM name, then verify actual cluster identity again before executing hooks.
// Older hook implementations without a selector retain their existing scope.
type VMHookSelector interface {
	SelectVM(context.Context, VMHookRequest) (bool, error)
}

type deploymentVMHookCapabilities struct {
	Start   bool `json:"start"`
	Address bool `json:"address"`
}

func (s *Server) deploymentVMHooks(ctx context.Context, rangeID string, names []string) (map[string]deploymentVMHookCapabilities, error) {
	hooks := make(map[string]deploymentVMHookCapabilities)
	for _, name := range names {
		request := VMHookRequest{Source: StartVMSourceDeploymentFirstBoot, RangeID: rangeID, Pool: rangeID, VMName: name}
		capabilities := deploymentVMHookCapabilities{}
		for _, plugin := range s.plugins {
			_, starts := plugin.(StartVMHook)
			_, addresses := plugin.(VMAddressHook)
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

func pluginSelectsVM(ctx context.Context, plugin LudusPlugin, request VMHookRequest) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if selector, ok := plugin.(VMHookSelector); ok {
		selected, err := selector.SelectVM(ctx, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s could not select VMID %d: %w", plugin.Name(), request.VMID, err)
		}
		return selected, nil
	}
	return true, nil
}

func (s *Server) selectsVMForPower(ctx context.Context, request VMHookRequest, action string) (bool, error) {
	for _, plugin := range s.plugins {
		_, start := plugin.(StartVMHook)
		_, stop := plugin.(StopVMHook)
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
	for _, plugin := range s.plugins {
		if _, ok := plugin.(BeforeDeleteVMHook); !ok {
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
