package ludusapi

import (
	"context"
	"errors"
	"testing"

	"ludusapi/pluginrpc"
)

func addressTestPlugin(result VMAddressHookResult, hookErr error, calls *int) *managedPlugin {
	return testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{Address: true}, true,
		func(request pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
			*calls++
			return result, hookErr
		})
}

func TestVMAddressOptionalCompatibility(t *testing.T) {
	legacy := testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{}, true, func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
		t.Fatal("plugin without address capability was called")
		return pluginrpc.VMHookResponse{}, nil
	})
	for _, plugins := range [][]*managedPlugin{nil, {legacy}} {
		s := &Server{plugins: plugins}
		result, err := s.runVMAddressHooks(context.Background(), VMHookRequest{})
		if err != nil || result.Decision != StartVMContinue || result.Ready || result.Address != "" {
			t.Fatalf("legacy fallback changed: %+v %v", result, err)
		}
	}
}

func TestVMAddressDispatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		result VMAddressHookResult
		fail   bool
	}{
		{"pending", VMAddressHookResult{Decision: StartVMHandled}, false},
		{"ready", VMAddressHookResult{Decision: StartVMHandled, Ready: true, Address: "10.3.10.241"}, false},
		{"invalid-address", VMAddressHookResult{Decision: StartVMHandled, Ready: true, Address: "not-an-ip"}, true},
		{"self-assigned", VMAddressHookResult{Decision: StartVMHandled, Ready: true, Address: "169.254.1.1"}, true},
		{"pending-address", VMAddressHookResult{Decision: StartVMHandled, Address: "10.3.10.241"}, true},
		{"invalid-decision", VMAddressHookResult{Decision: 99}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			firstCalls, selectedCalls, lastCalls := 0, 0, 0
			first := addressTestPlugin(VMAddressHookResult{Decision: StartVMContinue}, nil, &firstCalls)
			selected := addressTestPlugin(test.result, nil, &selectedCalls)
			last := addressTestPlugin(VMAddressHookResult{}, nil, &lastCalls)
			s := &Server{plugins: []*managedPlugin{first, selected, last}}
			_, err := s.runVMAddressHooks(context.Background(), VMHookRequest{})
			if (err != nil) != test.fail || firstCalls != 1 || selectedCalls != 1 || lastCalls != 0 {
				t.Fatalf("dispatch failed: err=%v calls=%d/%d/%d", err, firstCalls, selectedCalls, lastCalls)
			}
		})
	}
}

func TestVMAddressErrorDoesNotFallBack(t *testing.T) {
	firstCalls, lastCalls := 0, 0
	first := addressTestPlugin(VMAddressHookResult{}, errors.New("router lookup failed"), &firstCalls)
	last := addressTestPlugin(VMAddressHookResult{}, nil, &lastCalls)
	s := &Server{plugins: []*managedPlugin{first, last}}
	if _, err := s.runVMAddressHooks(context.Background(), VMHookRequest{}); err == nil || lastCalls != 0 {
		t.Fatal("provider error fell through")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	firstCalls = 0
	if _, err := s.runVMAddressHooks(ctx, VMHookRequest{}); !errors.Is(err, context.Canceled) || firstCalls != 0 {
		t.Fatal("canceled discovery called provider")
	}
}
