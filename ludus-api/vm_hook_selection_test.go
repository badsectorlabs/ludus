package ludusapi

import (
	"context"
	"errors"
	"testing"

	goproxmox "github.com/luthermonson/go-proxmox"
)

type scopedVMTestPlugin struct {
	lifecycleTestPlugin
	selectionErr error
	deleteErr    error
	statusCalls  int
}

func (p *scopedVMTestPlugin) SelectVM(_ context.Context, request VMHookRequest) (bool, error) {
	return request.VMName == "selected", p.selectionErr
}
func (p *scopedVMTestPlugin) VMStatus(context.Context, VMHookRequest) (VMStatusHookResult, error) {
	p.statusCalls++
	return VMStatusHookResult{Decision: StartVMHandled, Status: VMStatusRunning}, nil
}
func (p *scopedVMTestPlugin) BeforeDeleteVM(context.Context, VMHookRequest) error {
	p.deleted = true
	return p.deleteErr
}

type scopedAddressOnlyTestPlugin struct{ addressTestPlugin }

func (p *scopedAddressOnlyTestPlugin) SelectVM(_ context.Context, request VMHookRequest) (bool, error) {
	return request.VMName == "selected", nil
}

func TestDeploymentHookSelectionIsPerVMAndAddressIndependent(t *testing.T) {
	p := &scopedAddressOnlyTestPlugin{}
	s := &Server{plugins: []LudusPlugin{p}}
	plan, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary", "router"})
	if err != nil || plan["selected"].Start || !plan["selected"].Address || plan["ordinary"].Address || plan["router"].Address {
		t.Fatalf("address-only plan: %+v, %v", plan, err)
	}
	s.plugins = []LudusPlugin{&scopedVMTestPlugin{lifecycleTestPlugin: lifecycleTestPlugin{initialized: true}}}
	plan, err = s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary"})
	if err != nil || !plan["selected"].Start || plan["ordinary"].Start {
		t.Fatalf("start plan: %+v, %v", plan, err)
	}
}

func TestUnselectedVMDoesNotNeedPrivilegedStatusOrPower(t *testing.T) {
	p := &scopedVMTestPlugin{lifecycleTestPlugin: lifecycleTestPlugin{initialized: true}}
	s := &Server{plugins: []LudusPlugin{p}}
	request := VMHookRequest{VMID: 126, VMName: "ordinary", RangeID: "RANGE", Pool: "RANGE"}
	// No root socket is created by this test. An unselected VM must still work.
	status, handled, err := s.resolveVMStatus(context.Background(), request)
	if err != nil || handled || status != "" || p.statusCalls != 0 {
		t.Fatalf("unselected status: %q %v %v; calls=%d", status, handled, err, p.statusCalls)
	}
	for _, action := range []string{"on", "off"} {
		selected, err := s.selectsVMForPower(context.Background(), request, action)
		if err != nil || selected {
			t.Fatalf("unrelated power %s: %v %v", action, selected, err)
		}
	}
	if err := s.runBeforeDeleteVMHooks(context.Background(), request); err != nil || p.deleted {
		t.Fatalf("unrelated cleanup ran: %v, %v", p.deleted, err)
	}
}

func TestDeleteVetoRemainsDistinctFromProxmoxFailure(t *testing.T) {
	cause := errors.New("cleanup failed")
	p := &scopedVMTestPlugin{lifecycleTestPlugin: lifecycleTestPlugin{initialized: true}, deleteErr: cause}
	s := &Server{plugins: []LudusPlugin{p}}
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
	p := &scopedVMTestPlugin{lifecycleTestPlugin: lifecycleTestPlugin{initialized: true}}
	s := &Server{plugins: []LudusPlugin{p}}
	resources := []*goproxmox.ClusterResource{
		{VMID: 126, Name: "selected", Pool: "RANGE", Node: "pve", Type: "qemu"},
		{VMID: 127, Name: "selected", Pool: "OTHER", Node: "pve", Type: "qemu"},
	}
	loads := 0
	load := func(context.Context) ([]*goproxmox.ClusterResource, error) { loads++; return resources, nil }
	bodies := []vmHookServiceRequest{{VMID: 126, VMName: "selected", RangeID: "RANGE"}, {VMID: 127, VMName: "selected", RangeID: "OTHER"}}
	statuses, err := s.verifiedVMStatusBatch(context.Background(), bodies, load)
	if err != nil || len(statuses) != 2 || loads != 1 || p.statusCalls != 2 {
		t.Fatalf("batch=%v error=%v loads=%d calls=%d", statuses, err, loads, p.statusCalls)
	}
	p.statusCalls = 0
	bodies[1].RangeID = "WRONG"
	if _, err = s.verifiedVMStatusBatch(context.Background(), bodies, load); err == nil || p.statusCalls != 0 {
		t.Fatal("unverified batch dispatched provider calls")
	}
}

func TestSelectionFailureDoesNotFallBack(t *testing.T) {
	cause := errors.New("cannot read provider configuration")
	p := &scopedVMTestPlugin{selectionErr: cause}
	s := &Server{plugins: []LudusPlugin{p}}
	if _, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected"}); !errors.Is(err, cause) {
		t.Fatalf("selection error lost: %v", err)
	}
	if _, err := s.resolveVMStatuses(context.Background(), []VMHookRequest{{VMID: 126, VMName: "selected"}}); !errors.Is(err, cause) {
		t.Fatalf("status error lost: %v", err)
	}
}
