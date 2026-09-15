package ludusapi

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

type lifecycleTestPlugin struct {
	initialized bool
	deleted     bool
}

func (p *lifecycleTestPlugin) Name() string             { return "lifecycle-test" }
func (p *lifecycleTestPlugin) Initialize(*Server) error { p.initialized = true; return nil }
func (p *lifecycleTestPlugin) RegisterRoutes(*core.App) {}
func (p *lifecycleTestPlugin) GetEmbeddedFSs() []fs.FS  { return nil }
func (p *lifecycleTestPlugin) Shutdown() error          { return nil }
func (p *lifecycleTestPlugin) Initialized() bool        { return p.initialized }
func (p *lifecycleTestPlugin) RoutesRegistered() bool   { return true }
func (p *lifecycleTestPlugin) StartVM(context.Context, VMHookRequest) (StartVMHookResult, error) {
	return StartVMHandled, nil
}
func (p *lifecycleTestPlugin) StopVM(context.Context, VMHookRequest) (StartVMHookResult, error) {
	return StartVMHandled, nil
}
func (p *lifecycleTestPlugin) VMStatus(context.Context, VMHookRequest) (VMStatusHookResult, error) {
	return VMStatusHookResult{Decision: StartVMHandled, Status: VMStatusRunning}, nil
}
func (p *lifecycleTestPlugin) BeforeDeleteVM(context.Context, VMHookRequest) error {
	p.deleted = true
	return nil
}

func TestVMLifecycleHookDispatch(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	defer func() { logger = previousLogger }()

	plugin := &lifecycleTestPlugin{initialized: true}
	server := &Server{plugins: []LudusPlugin{plugin}}
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
	if err := server.runBeforeDeleteVMHooks(context.Background(), request); err != nil || !plugin.deleted {
		t.Fatalf("delete hook: called=%v error=%v", plugin.deleted, err)
	}
}

func TestVMLifecycleHookFailsClosedWhenUninitialized(t *testing.T) {
	server := &Server{plugins: []LudusPlugin{&lifecycleTestPlugin{}}}
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
