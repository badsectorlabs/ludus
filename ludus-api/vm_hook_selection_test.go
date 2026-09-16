package ludusapi

import (
	"context"
	"errors"
	"testing"

	"ludusapi/pluginrpc"

	goproxmox "github.com/luthermonson/go-proxmox"
)

type scopedVMTestState struct {
	selectionErr error
	deleteErr    error
	statusCalls  int
	deleteCalls  int
}

func scopedVMTestPlugin(state *scopedVMTestState, capabilities pluginrpc.VMHookCapabilities) *managedPlugin {
	return testManagedVMHookPlugin(capabilities, true, func(request pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
		switch request.Operation {
		case pluginrpc.VMHookSelect:
			return pluginrpc.VMHookResponse{Selected: request.VMName == "selected"}, state.selectionErr
		case pluginrpc.VMHookStatus:
			state.statusCalls++
			return pluginrpc.VMHookResponse{Decision: StartVMHandled, Status: VMStatusRunning}, nil
		case pluginrpc.VMHookBeforeDelete:
			state.deleteCalls++
			return pluginrpc.VMHookResponse{}, state.deleteErr
		default:
			return pluginrpc.VMHookResponse{Decision: StartVMHandled}, nil
		}
	})
}

func TestDeploymentHookSelectionIsPerVMAndAddressIndependent(t *testing.T) {
	state := &scopedVMTestState{}
	addressOnly := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, Address: true})
	s := &Server{plugins: []*managedPlugin{addressOnly}}
	plan, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary", "router"})
	if err != nil || plan["selected"].Start || !plan["selected"].Address || plan["ordinary"].Address || plan["router"].Address {
		t.Fatalf("address-only plan: %+v, %v", plan, err)
	}

	lifecycle := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, Start: true})
	s.plugins = []*managedPlugin{lifecycle}
	plan, err = s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary"})
	if err != nil || !plan["selected"].Start || plan["ordinary"].Start {
		t.Fatalf("start plan: %+v, %v", plan, err)
	}
}

func TestUnselectedVMDoesNotNeedPrivilegedStatusOrPower(t *testing.T) {
	state := &scopedVMTestState{}
	plugin := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, Start: true, Stop: true, Status: true, BeforeDelete: true})
	s := &Server{plugins: []*managedPlugin{plugin}}
	request := VMHookRequest{VMID: 126, VMName: "ordinary", RangeID: "RANGE", Pool: "RANGE"}
	status, handled, err := s.resolveVMStatus(context.Background(), request)
	if err != nil || handled || status != "" || state.statusCalls != 0 {
		t.Fatalf("unselected status: %q %v %v; calls=%d", status, handled, err, state.statusCalls)
	}
	for _, action := range []string{"on", "off"} {
		selected, err := s.selectsVMForPower(context.Background(), request, action)
		if err != nil || selected {
			t.Fatalf("unrelated power %s: %v %v", action, selected, err)
		}
	}
	if err := s.runBeforeDeleteVMHooks(context.Background(), request); err != nil || state.deleteCalls != 0 {
		t.Fatalf("unrelated cleanup ran: calls=%d err=%v", state.deleteCalls, err)
	}
}

func TestDeleteVetoRemainsDistinctFromProxmoxFailure(t *testing.T) {
	cause := errors.New("cleanup failed")
	state := &scopedVMTestState{deleteErr: cause}
	plugin := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, BeforeDelete: true})
	s := &Server{plugins: []*managedPlugin{plugin}}
	err := s.runBeforeDeleteVMHooks(context.Background(), VMHookRequest{VMName: "selected"})
	var hookErr *vmDeleteHookError
	if !errors.As(err, &hookErr) || !errors.Is(err, cause) {
		t.Fatalf("lost provider veto: %v", err)
	}
	if errors.As(errors.New("Proxmox deletion failed"), &hookErr) {
		t.Fatal("ordinary failure became a hook veto")
	}
}

func TestStatusBatchUsesOneVerifiedSnapshot(t *testing.T) {
	state := &scopedVMTestState{}
	plugin := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, Status: true})
	s := &Server{plugins: []*managedPlugin{plugin}}
	resources := []*goproxmox.ClusterResource{
		{VMID: 126, Name: "selected", Pool: "RANGE", Node: "pve", Type: "qemu"},
		{VMID: 127, Name: "selected", Pool: "OTHER", Node: "pve", Type: "qemu"},
	}
	loads := 0
	load := func(context.Context) ([]*goproxmox.ClusterResource, error) { loads++; return resources, nil }
	bodies := []vmHookServiceRequest{{VMID: 126, VMName: "selected", RangeID: "RANGE"}, {VMID: 127, VMName: "selected", RangeID: "OTHER"}}
	statuses, err := s.verifiedVMStatusBatch(context.Background(), bodies, load)
	if err != nil || len(statuses) != 2 || loads != 1 || state.statusCalls != 2 {
		t.Fatalf("batch=%v error=%v loads=%d calls=%d", statuses, err, loads, state.statusCalls)
	}
	state.statusCalls = 0
	bodies[1].RangeID = "WRONG"
	if _, err = s.verifiedVMStatusBatch(context.Background(), bodies, load); err == nil || state.statusCalls != 0 {
		t.Fatal("unverified batch dispatched provider calls")
	}
}

func TestSelectionFailureDoesNotFallBack(t *testing.T) {
	cause := errors.New("cannot read provider configuration")
	state := &scopedVMTestState{selectionErr: cause}
	plugin := scopedVMTestPlugin(state, pluginrpc.VMHookCapabilities{Select: true, Start: true, Status: true})
	s := &Server{plugins: []*managedPlugin{plugin}}
	if _, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected"}); !errors.Is(err, cause) {
		t.Fatalf("selection error lost: %v", err)
	}
	if _, err := s.resolveVMStatuses(context.Background(), []VMHookRequest{{VMID: 126, VMName: "selected"}}); !errors.Is(err, cause) {
		t.Fatalf("status error lost: %v", err)
	}
}
