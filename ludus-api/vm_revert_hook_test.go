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

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func revertTestPlugin(result protocol.VMHookResponse, hookErr error, requests *[]protocol.VMHookRequest) *managedPlugin {
	return testManagedVMHookPlugin(protocol.VMHookCapabilities{Revert: true}, true,
		func(request protocol.VMHookRequest) (protocol.VMHookResponse, error) {
			*requests = append(*requests, request)
			return result, hookErr
		})
}

func TestVMRevertDispatch(t *testing.T) {
	previousLogger := logger
	logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger = previousLogger })

	without := testManagedVMHookPlugin(protocol.VMHookCapabilities{Start: true}, true, func(protocol.VMHookRequest) (protocol.VMHookResponse, error) {
		t.Fatal("plugin without revert capability was called")
		return protocol.VMHookResponse{}, nil
	})
	var first, selected, last []protocol.VMHookRequest
	s := &Server{plugins: []*managedPlugin{
		without,
		revertTestPlugin(protocol.VMHookResponse{Decision: StartVMContinue}, nil, &first),
		revertTestPlugin(protocol.VMHookResponse{Decision: StartVMHandled}, nil, &selected),
		revertTestPlugin(protocol.VMHookResponse{Decision: StartVMHandled}, nil, &last),
	}}
	handled, err := s.runVMRevertHooks(context.Background(), VMHookRequest{VMID: 126, Snapshot: "ludus_automated_clean_snapshot"})
	if err != nil || !handled || len(first) != 1 || len(selected) != 1 || len(last) != 0 {
		t.Fatalf("dispatch: handled=%v err=%v calls=%d/%d/%d", handled, err, len(first), len(selected), len(last))
	}
	if request := selected[0]; request.Operation != protocol.VMHookRevert || request.Snapshot != "ludus_automated_clean_snapshot" {
		t.Fatalf("revert request = %+v", request)
	}

	if handled, err := (&Server{plugins: []*managedPlugin{without}}).runVMRevertHooks(context.Background(), VMHookRequest{}); err != nil || handled {
		t.Fatalf("revert without providers: handled=%v err=%v", handled, err)
	}
	var invalid []protocol.VMHookRequest
	s = &Server{plugins: []*managedPlugin{revertTestPlugin(protocol.VMHookResponse{Decision: 99}, nil, &invalid)}}
	if _, err := s.runVMRevertHooks(context.Background(), VMHookRequest{}); err == nil {
		t.Fatal("invalid revert decision was accepted")
	}
}

func TestVMRevertErrorDoesNotFallBack(t *testing.T) {
	var first, last []protocol.VMHookRequest
	s := &Server{plugins: []*managedPlugin{
		revertTestPlugin(protocol.VMHookResponse{}, errors.New("rollback failed"), &first),
		revertTestPlugin(protocol.VMHookResponse{Decision: StartVMHandled}, nil, &last),
	}}
	if handled, err := s.runVMRevertHooks(context.Background(), VMHookRequest{}); err == nil || handled || len(last) != 0 {
		t.Fatalf("provider error fell through: handled=%v err=%v", handled, err)
	}
}

func TestVMHookServiceRevertRequiresSnapshotName(t *testing.T) {
	var requests []protocol.VMHookRequest
	s := &Server{plugins: []*managedPlugin{revertTestPlugin(protocol.VMHookResponse{Decision: StartVMHandled}, nil, &requests)}}
	for _, snapshot := range []string{"", "1snapshot", "../snapshot", "a", strings.Repeat("a", 41)} {
		body := `{"rangeId":"RANGE","vmId":126,"vmName":"selected","snapshot":"` + snapshot + `"}`
		recorder := httptest.NewRecorder()
		s.handleVMHookServiceRevert(recorder, httptest.NewRequest(http.MethodPost, "/v1/revert", strings.NewReader(body)))
		if recorder.Code != http.StatusBadRequest || len(requests) != 0 {
			t.Fatalf("snapshot %q: status %d, %d plugin calls", snapshot, recorder.Code, len(requests))
		}
	}
}
