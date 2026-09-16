package ludusapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ludusapi/pluginrpc"
)

type vmHookTestRPC struct {
	hook func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error)
}

func (*vmHookTestRPC) Metadata() (pluginrpc.Metadata, error) {
	return pluginrpc.Metadata{Name: "test"}, nil
}
func (*vmHookTestRPC) Initialize(pluginrpc.InitializeRequest) (pluginrpc.InitializeResponse, error) {
	return pluginrpc.InitializeResponse{}, nil
}
func (*vmHookTestRPC) Handle(pluginrpc.Request) (pluginrpc.Response, error) {
	return pluginrpc.Response{}, nil
}
func (*vmHookTestRPC) RunJob(string) (pluginrpc.JobResponse, error) {
	return pluginrpc.JobResponse{}, nil
}
func (*vmHookTestRPC) Shutdown() error { return nil }
func (p *vmHookTestRPC) VMHook(request pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
	if p.hook == nil {
		return pluginrpc.VMHookResponse{}, nil
	}
	return p.hook(request)
}

func testManagedVMHookPlugin(capabilities pluginrpc.VMHookCapabilities, initialized bool, hook func(pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error)) *managedPlugin {
	return &managedPlugin{
		rpc:         &vmHookTestRPC{hook: hook},
		metadata:    pluginrpc.Metadata{Name: "test", VMHooks: capabilities},
		initialized: initialized,
	}
}

func TestVMLifecycleHookDispatch(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })

	var operations []pluginrpc.VMHookOperation
	plugin := testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{Start: true, Stop: true, Status: true, BeforeDelete: true}, true,
		func(request pluginrpc.VMHookRequest) (pluginrpc.VMHookResponse, error) {
			operations = append(operations, request.Operation)
			if request.Operation == pluginrpc.VMHookStatus {
				return pluginrpc.VMHookResponse{Decision: StartVMHandled, Status: VMStatusRunning}, nil
			}
			return pluginrpc.VMHookResponse{Decision: StartVMHandled}, nil
		})
	server := &Server{plugins: []*managedPlugin{plugin}}
	request := VMHookRequest{RangeID: "range", VMID: 126, VMName: "range-vm"}

	if handled, err := server.runStartVMHooks(context.Background(), request); err != nil || !handled {
		t.Fatalf("start hook: handled=%v error=%v", handled, err)
	}
	if handled, err := server.runStopVMHooks(context.Background(), request); err != nil || !handled {
		t.Fatalf("stop hook: handled=%v error=%v", handled, err)
	}
	status, handled, err := server.runVMStatusHooks(context.Background(), request)
	if err != nil || !handled || status != VMStatusRunning {
		t.Fatalf("status hook: status=%q handled=%v error=%v", status, handled, err)
	}
	if err := server.runBeforeDeleteVMHooks(context.Background(), request); err != nil {
		t.Fatalf("delete hook: %v", err)
	}
	want := []pluginrpc.VMHookOperation{pluginrpc.VMHookStart, pluginrpc.VMHookStop, pluginrpc.VMHookStatus, pluginrpc.VMHookBeforeDelete}
	if len(operations) != len(want) {
		t.Fatalf("operations = %v, want %v", operations, want)
	}
	for index := range want {
		if operations[index] != want[index] {
			t.Fatalf("operations = %v, want %v", operations, want)
		}
	}
}

func TestVMLifecycleHookFailsClosedWhenUninitialized(t *testing.T) {
	plugin := testManagedVMHookPlugin(pluginrpc.VMHookCapabilities{Start: true}, false, nil)
	server := &Server{plugins: []*managedPlugin{plugin}}
	if _, err := server.runStartVMHooks(context.Background(), VMHookRequest{VMID: 126}); err == nil {
		t.Fatal("uninitialized lifecycle plugin was skipped")
	}
}

func TestVMHookServiceAcceptsAnsibleStringScalars(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/start", strings.NewReader(
		`{"rangeId":"range","vmId":"126","vmName":"range-vm","firstBoot":"true"}`,
	))
	body, ok := decodeVMHookServiceRequest(recorder, request)
	if !ok {
		t.Fatalf("request rejected with status %d: %s", recorder.Code, recorder.Body.String())
	}
	if int(body.VMID) != 126 || !bool(body.FirstBoot) {
		t.Fatalf("unexpected decoded request: %#v", body)
	}
}

func TestVMHookIdentityVerificationRetriesStaleCloneMetadata(t *testing.T) {
	calls := 0
	expected := VMHookRequest{VMID: 111, VMName: "range-router", Pool: "range"}
	got, err := verifyVMHookRequestWithRetry(context.Background(), 3, 0, func() (VMHookRequest, error) {
		calls++
		if calls < 3 {
			return VMHookRequest{}, errors.New("stale clone identity")
		}
		return expected, nil
	})
	if err != nil || got != expected || calls != 3 {
		t.Fatalf("request=%+v calls=%d error=%v", got, calls, err)
	}
}

func TestVMHookIdentityVerificationDoesNotAcceptPersistentMismatch(t *testing.T) {
	for _, attempts := range []int{1, 3} {
		calls := 0
		mismatch := errors.New("wrong pool")
		got, err := verifyVMHookRequestWithRetry(context.Background(), attempts, 0, func() (VMHookRequest, error) {
			calls++
			return VMHookRequest{}, mismatch
		})
		if !errors.Is(err, mismatch) || got != (VMHookRequest{}) || calls != attempts {
			t.Fatalf("request=%+v calls=%d error=%v", got, calls, err)
		}
	}
}

func TestVMHookIdentityVerificationCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := verifyVMHookRequestWithRetry(ctx, 3, time.Hour, func() (VMHookRequest, error) {
		calls++
		cancel()
		return VMHookRequest{}, errors.New("stale clone identity")
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls=%d error=%v", calls, err)
	}
}
