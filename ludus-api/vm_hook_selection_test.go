package ludusapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goproxmox "github.com/luthermonson/go-proxmox"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// serveTestVMHookSelection serves the admin service's /v1/select on a test
// socket and points the regular service's client at it.
func serveTestVMHookSelection(t *testing.T, admin *Server) {
	t.Helper()
	directory, err := os.MkdirTemp("", "vmhook")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "hooks.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/select", admin.handleVMHookServiceSelect)
	service := &http.Server{Handler: mux}
	go func() { _ = service.Serve(listener) }()
	t.Cleanup(func() { _ = service.Close() })
	previous := vmHookServiceClientSocket
	vmHookServiceClientSocket = socket
	t.Cleanup(func() { vmHookServiceClientSocket = previous })
}

func TestRegularServiceSelectsRootRunHooksThroughTheAdminService(t *testing.T) {
	state := &scopedVMTestState{}
	var selectRequests []protocol.VMHookRequest
	hookPlugin := testManagedVMHookPlugin(protocol.VMHookCapabilities{Select: true, Start: true, Status: true}, true,
		func(request protocol.VMHookRequest) (protocol.VMHookResponse, error) {
			if request.Operation == protocol.VMHookSelect {
				selectRequests = append(selectRequests, request)
				return protocol.VMHookResponse{Selected: request.VMName == "selected"}, state.selectionErr
			}
			t.Errorf("regular service selection ran %s", request.Operation)
			return protocol.VMHookResponse{}, nil
		})
	serveTestVMHookSelection(t, &Server{plugins: []*managedPlugin{hookPlugin}})

	// The regular service hosts no hook plugins; it knows the declarations of
	// the active root-run package on disk.
	regular := &Server{LudusInstallPath: t.TempDir()}
	declared := testExecutableManifest("hook-plugin")
	declared.Level, declared.RunAs = protocol.LevelSystem, protocol.RunAsRoot
	declared.VMHooks = protocol.VMHookCapabilities{Select: true, Start: true, Status: true}
	writeInstalledPackage(t, regular, declared, "#!/bin/sh\n")
	if regular.hasStartVMHooks() {
		t.Fatal("hooks counted before the package was known")
	}
	regular.refreshPluginDeclarations(regular.installedPackages())
	if !regular.hasStartVMHooks() || !regular.hasVMStatusHooks() || regular.hasStopVMHooks() || regular.hasBeforeDeleteVMHooks() {
		t.Fatalf("declared hooks = %+v", regular.declaredVMHooks())
	}

	ctx := context.Background()
	request := VMHookRequest{Source: StartVMSourceAPI, RangeID: "RANGE", Pool: "RANGE", VMID: 126, VMName: "selected"}
	if selected, err := regular.selectsVMForPower(ctx, request, "on"); err != nil || !selected {
		t.Fatalf("selected VM: %v, %v", selected, err)
	}
	if len(selectRequests) != 1 || selectRequests[0].VMID != 126 || selectRequests[0].RangeID != "RANGE" || selectRequests[0].Call == "" {
		t.Fatalf("admin service select requests = %+v", selectRequests)
	}
	ordinary := request
	ordinary.VMName = "ordinary"
	if selected, err := regular.selectsVMForPower(ctx, ordinary, "on"); err != nil || selected {
		t.Fatalf("ordinary VM: %v, %v", selected, err)
	}
	// No root-run plugin declares stop hooks, so stopping never asks.
	asked := len(selectRequests)
	if selected, err := regular.selectsVMForPower(ctx, request, "off"); err != nil || selected || len(selectRequests) != asked {
		t.Fatalf("stop selection: %v, %v, %d requests", selected, err, len(selectRequests)-asked)
	}
	plan, err := regular.deploymentVMHooks(ctx, "RANGE", []string{"selected", "ordinary"})
	if err != nil || !plan["selected"].Start || plan["selected"].Address || plan["ordinary"].Start {
		t.Fatalf("deployment plan = %+v, %v", plan, err)
	}
	if selectRequests[len(selectRequests)-1].Source != StartVMSourceDeploymentFirstBoot {
		t.Fatalf("deployment selection source = %q", selectRequests[len(selectRequests)-1].Source)
	}
	statusRequests, err := regular.selectVMStatusRequests(ctx, []VMHookRequest{request, ordinary})
	if err != nil || len(statusRequests) != 1 || statusRequests[0].VMName != "selected" {
		t.Fatalf("status selection = %+v, %v", statusRequests, err)
	}

	state.selectionErr = errors.New("provider unavailable")
	if _, err := regular.selectsVMForPower(ctx, request, "on"); err == nil || !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("selection failure: %v", err)
	}
	state.selectionErr = nil
	invalid := request
	invalid.VMName = "../selected"
	if _, err := regular.selectsVMForPower(ctx, invalid, "on"); err == nil {
		t.Fatal("admin service accepted an invalid VM identity")
	}
	if _, err := callVMHookSelect(ctx, request, protocol.VMHookCapabilities{Select: true}); err == nil {
		t.Fatal("admin service accepted a selection without hook operations")
	}
}

func TestVMHooksRequireRootRunPlugins(t *testing.T) {
	m := testExecutableManifest("hook-plugin")
	m.Level = protocol.LevelSystem
	m.VMHooks = protocol.VMHookCapabilities{Start: true}
	if _, err := readPluginPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"})); err == nil || !strings.Contains(err.Error(), "runAs") {
		t.Fatalf("upload of a ludus-run hook plugin: %v", err)
	}
	s := &Server{LudusInstallPath: t.TempDir()}
	dir := writeInstalledPackage(t, s, m, "#!/bin/sh\n")
	if _, err := readPackageManifest(dir); err == nil {
		t.Fatal("loaded a ludus-run hook plugin from disk")
	}
	if len(s.installedPackages()) != 0 {
		t.Fatal("a ludus-run hook plugin was listed as installed")
	}
	if _, err := s.startPlugin(dir, m, protocol.SourceServer, ""); err == nil || !strings.Contains(err.Error(), "runAs") {
		t.Fatalf("started a ludus-run hook plugin: %v", err)
	}
	m.RunAs = protocol.RunAsRoot
	if _, err := readPluginPackage(testPackage(t, m, map[string]string{protocol.ExecutableFile: "#!/bin/sh\n"})); err != nil {
		t.Fatalf("root-run hook plugin rejected: %v", err)
	}
}

type scopedVMTestState struct {
	selectionErr error
	deleteErr    error
	statusCalls  int
	deleteCalls  int
}

func scopedVMTestPlugin(state *scopedVMTestState, capabilities protocol.VMHookCapabilities) *managedPlugin {
	return testManagedVMHookPlugin(capabilities, true, func(request protocol.VMHookRequest) (protocol.VMHookResponse, error) {
		switch request.Operation {
		case protocol.VMHookSelect:
			return protocol.VMHookResponse{Selected: request.VMName == "selected"}, state.selectionErr
		case protocol.VMHookStatus:
			state.statusCalls++
			return protocol.VMHookResponse{Decision: StartVMHandled, Status: VMStatusRunning}, nil
		case protocol.VMHookBeforeDelete:
			state.deleteCalls++
			return protocol.VMHookResponse{}, state.deleteErr
		default:
			return protocol.VMHookResponse{Decision: StartVMHandled}, nil
		}
	})
}

func TestDeploymentHookSelectionIsPerVMAndAddressIndependent(t *testing.T) {
	state := &scopedVMTestState{}
	addressOnly := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, Address: true})
	s := &Server{plugins: []*managedPlugin{addressOnly}}
	plan, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary", "router"})
	if err != nil || plan["selected"].Start || !plan["selected"].Address || plan["ordinary"].Address || plan["router"].Address {
		t.Fatalf("address-only plan: %+v, %v", plan, err)
	}

	lifecycle := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, Start: true})
	s.plugins = []*managedPlugin{lifecycle}
	plan, err = s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected", "ordinary"})
	if err != nil || !plan["selected"].Start || plan["ordinary"].Start {
		t.Fatalf("start plan: %+v, %v", plan, err)
	}
}

func TestUnselectedVMDoesNotNeedPrivilegedStatusOrPower(t *testing.T) {
	state := &scopedVMTestState{}
	plugin := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, Start: true, Stop: true, Status: true, BeforeDelete: true})
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
	plugin := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, BeforeDelete: true})
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
	plugin := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, Status: true})
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
	plugin := scopedVMTestPlugin(state, protocol.VMHookCapabilities{Select: true, Start: true, Status: true})
	s := &Server{plugins: []*managedPlugin{plugin}}
	if _, err := s.deploymentVMHooks(context.Background(), "RANGE", []string{"selected"}); !errors.Is(err, cause) {
		t.Fatalf("selection error lost: %v", err)
	}
	if _, err := s.resolveVMStatuses(context.Background(), []VMHookRequest{{VMID: 126, VMName: "selected"}}); !errors.Is(err, cause) {
		t.Fatalf("status error lost: %v", err)
	}
}
