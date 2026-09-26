package pluginrpc

import "testing"

type baseTestPlugin struct{}

func (*baseTestPlugin) Metadata() (Metadata, error) { return Metadata{Name: "base"}, nil }
func (*baseTestPlugin) Initialize(InitializeRequest) (InitializeResponse, error) {
	return InitializeResponse{}, nil
}
func (*baseTestPlugin) Handle(Request) (Response, error) { return Response{}, nil }
func (*baseTestPlugin) RunJob(string) (JobResponse, error) {
	return JobResponse{}, nil
}
func (*baseTestPlugin) Shutdown() error { return nil }

type hookTestPlugin struct{ baseTestPlugin }

func (*hookTestPlugin) VMHook(request VMHookRequest) (VMHookResponse, error) {
	return VMHookResponse{Decision: VMHookHandled, Selected: request.VMName == "owned"}, nil
}

func TestVMHookRPCIsOptional(t *testing.T) {
	server := &RPCServer{Impl: &baseTestPlugin{}}
	if err := server.VMHook(VMHookRequest{}, &VMHookResponse{}); err == nil {
		t.Fatal("base plugin unexpectedly implements VM lifecycle hooks")
	}
}

func TestVMHookRPCDispatch(t *testing.T) {
	server := &RPCServer{Impl: &hookTestPlugin{}}
	var response VMHookResponse
	if err := server.VMHook(VMHookRequest{Operation: VMHookSelect, VMName: "owned"}, &response); err != nil {
		t.Fatal(err)
	}
	if !response.Selected || response.Decision != VMHookHandled {
		t.Fatalf("unexpected VM hook response: %+v", response)
	}
}
