package protocol

import (
	"fmt"
	"net/rpc"
	"sync"

	hcplugin "github.com/hashicorp/go-plugin"
)

var Handshake = hcplugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "LUDUS_PLUGIN",
	MagicCookieValue: "ludus-plugin-rpc-v1",
}

// Plugin is the host's view of a plugin process.
type Plugin interface {
	Metadata() (Metadata, error)
	Initialize(InitializeRequest) (InitializeResponse, error)
	Handle(Request) (Response, error)
	RunJob(JobRequest) error
	VMHook(VMHookRequest) (VMHookResponse, error)
	Shutdown() error
}

// Implementation is served by plugin executables. It differs from Plugin only
// in Initialize, which also receives the dialed host-service client (nil when
// InitializeRequest.HostService is zero).
type Implementation interface {
	Metadata() (Metadata, error)
	Initialize(InitializeRequest, *HostClient) (InitializeResponse, error)
	Handle(Request) (Response, error)
	RunJob(JobRequest) error
	VMHook(VMHookRequest) (VMHookResponse, error)
	Shutdown() error
}

// RPCPlugin adapts Implementation and Plugin to go-plugin's net/rpc protocol.
type RPCPlugin struct {
	Impl Implementation
}

func (p *RPCPlugin) Server(broker *hcplugin.MuxBroker) (interface{}, error) {
	return &RPCServer{impl: p.Impl, broker: broker}, nil
}

func (p *RPCPlugin) Client(broker *hcplugin.MuxBroker, client *rpc.Client) (interface{}, error) {
	return &RPCClient{client: client, broker: broker}, nil
}

func PluginMap(impl Implementation) map[string]hcplugin.Plugin {
	return map[string]hcplugin.Plugin{PluginName: &RPCPlugin{Impl: impl}}
}

// Serve runs impl as a plugin process. It returns when the host disconnects.
func Serve(impl Implementation) {
	hcplugin.Serve(&hcplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(impl),
	})
}

// RPCClient is the host-side Plugin implementation.
type RPCClient struct {
	client *rpc.Client
	broker *hcplugin.MuxBroker
}

// Broker returns the stream broker the host uses to serve the host service.
func (c *RPCClient) Broker() *hcplugin.MuxBroker { return c.broker }

func (c *RPCClient) Metadata() (Metadata, error) {
	var response Metadata
	err := c.client.Call("Plugin.Metadata", new(struct{}), &response)
	return response, err
}

func (c *RPCClient) Initialize(request InitializeRequest) (InitializeResponse, error) {
	var response InitializeResponse
	err := c.client.Call("Plugin.Initialize", request, &response)
	return response, err
}

func (c *RPCClient) Handle(request Request) (Response, error) {
	var response Response
	err := c.client.Call("Plugin.Handle", request, &response)
	return response, err
}

func (c *RPCClient) RunJob(request JobRequest) error {
	return c.client.Call("Plugin.RunJob", request, new(struct{}))
}

func (c *RPCClient) VMHook(request VMHookRequest) (VMHookResponse, error) {
	var response VMHookResponse
	err := c.client.Call("Plugin.VMHook", request, &response)
	return response, err
}

func (c *RPCClient) Shutdown() error {
	return c.client.Call("Plugin.Shutdown", new(struct{}), new(struct{}))
}

// RPCServer is the plugin-side net/rpc service.
type RPCServer struct {
	impl   Implementation
	broker *hcplugin.MuxBroker

	mu   sync.Mutex
	host *HostClient
}

func (s *RPCServer) Metadata(_ *struct{}, response *Metadata) error {
	metadata, err := s.impl.Metadata()
	*response = metadata
	return err
}

func (s *RPCServer) Initialize(request InitializeRequest, response *InitializeResponse) error {
	s.mu.Lock()
	if s.host == nil && request.HostService != 0 {
		conn, err := s.broker.Dial(request.HostService)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("dial host service: %w", err)
		}
		s.host = NewHostClient(rpc.NewClient(conn))
	}
	host := s.host
	s.mu.Unlock()
	result, err := s.impl.Initialize(request, host)
	*response = result
	return err
}

func (s *RPCServer) Handle(request Request, response *Response) error {
	result, err := s.impl.Handle(request)
	*response = result
	return err
}

func (s *RPCServer) RunJob(request JobRequest, _ *struct{}) error {
	return s.impl.RunJob(request)
}

func (s *RPCServer) VMHook(request VMHookRequest, response *VMHookResponse) error {
	result, err := s.impl.VMHook(request)
	*response = result
	return err
}

func (s *RPCServer) Shutdown(_ *struct{}, _ *struct{}) error {
	err := s.impl.Shutdown()
	s.mu.Lock()
	if s.host != nil {
		_ = s.host.Close()
		s.host = nil
	}
	s.mu.Unlock()
	return err
}
