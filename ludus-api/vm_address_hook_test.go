package ludusapi

import (
	"context"
	"errors"
	"testing"
)

type addressTestPlugin struct {
	legacyVMTestPlugin
	result VMAddressHookResult
	err    error
	calls  int
}

func (p *addressTestPlugin) VMAddress(context.Context, VMHookRequest) (VMAddressHookResult, error) {
	p.calls++
	return p.result, p.err
}

func TestVMAddressOptionalCompatibility(t *testing.T) {
	for _, plugins := range [][]LudusPlugin{nil, {&legacyVMTestPlugin{}}, {&continuingVMTestPlugin{}}} {
		s := &Server{plugins: plugins}
		result, err := s.runVMAddressHooks(context.Background(), VMHookRequest{})
		if err != nil || result.Decision != StartVMContinue || result.Ready || result.Address != "" {
			t.Fatalf("legacy fallback changed: %+v %v", result, err)
		}
	}
}
func TestVMAddressDispatch(t *testing.T) {
	for _, tc := range []struct {
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
		t.Run(tc.name, func(t *testing.T) {
			first := &addressTestPlugin{result: VMAddressHookResult{Decision: StartVMContinue}}
			selected := &addressTestPlugin{result: tc.result}
			last := &addressTestPlugin{}
			s := &Server{plugins: []LudusPlugin{first, selected, last}}
			_, err := s.runVMAddressHooks(context.Background(), VMHookRequest{})
			if (err != nil) != tc.fail || first.calls != 1 || selected.calls != 1 || last.calls != 0 {
				t.Fatalf("dispatch failed: %v", err)
			}
		})
	}
}
func TestVMAddressErrorDoesNotFallBack(t *testing.T) {
	first := &addressTestPlugin{err: errors.New("router lookup failed")}
	last := &addressTestPlugin{}
	s := &Server{plugins: []LudusPlugin{first, last}}
	if _, err := s.runVMAddressHooks(context.Background(), VMHookRequest{}); err == nil || last.calls != 0 {
		t.Fatal("provider error fell through")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	first.calls = 0
	if _, err := s.runVMAddressHooks(ctx, VMHookRequest{}); !errors.Is(err, context.Canceled) || first.calls != 0 {
		t.Fatal("canceled discovery called provider")
	}
}
