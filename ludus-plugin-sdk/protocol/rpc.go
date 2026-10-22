package protocol

import (
	"fmt"
	"log"
	"net/http"
	"net/rpc"
	"sync"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/hashicorp/yamux"
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
	AppSessionClosed(AppSessionRequest) error
	Shutdown() error
}

// Implementation is served by plugin executables. It differs from Plugin only
// in Initialize, which also receives the dialed host-service client (nil when
// InitializeRequest.HostService is zero), and in App, the browser application
// handler (nil when the plugin has none). RPCServer serves App on a broker
// stream after a successful Initialize.
type Implementation interface {
	Metadata() (Metadata, error)
	Initialize(InitializeRequest, *HostClient) (InitializeResponse, error)
	Handle(Request) (Response, error)
	RunJob(JobRequest) error
	VMHook(VMHookRequest) (VMHookResponse, error)
	AppSessionClosed(AppSessionRequest) error
	App() http.Handler
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

func (c *RPCClient) AppSessionClosed(request AppSessionRequest) error {
	return c.client.Call("Plugin.AppSessionClosed", request, new(struct{}))
}

func (c *RPCClient) Shutdown() error {
	return c.client.Call("Plugin.Shutdown", new(struct{}), new(struct{}))
}

// DialApp connects to the application stream a plugin reported in
// InitializeResponse.AppStream. Each Open on the returned session is one
// HTTP connection to the plugin's application.
func (c *RPCClient) DialApp(stream uint32) (*yamux.Session, error) {
	conn, err := c.broker.Dial(stream)
	if err != nil {
		return nil, err
	}
	session, err := yamux.Client(conn, appYamuxConfig())
	if err != nil {
		conn.Close()
		return nil, err
	}
	return session, nil
}

func appYamuxConfig() *yamux.Config {
	config := yamux.DefaultConfig()
	config.LogOutput = nil
	config.Logger = log.New(log.Writer(), "ludus app stream: ", log.LstdFlags)
	return config
}

// RPCServer is the plugin-side net/rpc service.
type RPCServer struct {
	impl   Implementation
	broker *hcplugin.MuxBroker

	mu   sync.Mutex
	host *HostClient
	app  *http.Server
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
	if err != nil {
		*response = result
		return err
	}
	if handler := s.impl.App(); handler != nil {
		result.AppStream = s.serveApp(handler)
	}
	*response = result
	return nil
}

// serveApp accepts the host's application stream and serves handler on it.
// The host dials right after Initialize returns, within the accept window.
func (s *RPCServer) serveApp(handler http.Handler) uint32 {
	id := s.broker.NextId()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second}
	s.mu.Lock()
	s.app = server
	s.mu.Unlock()
	go func() {
		conn, err := s.broker.Accept(id)
		if err != nil {
			log.Printf("ludus plugin: host did not connect to the application stream: %v", err)
			return
		}
		session, err := yamux.Server(conn, appYamuxConfig())
		if err != nil {
			conn.Close()
			log.Printf("ludus plugin: application stream: %v", err)
			return
		}
		_ = server.Serve(session)
	}()
	return id
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

func (s *RPCServer) AppSessionClosed(request AppSessionRequest, _ *struct{}) error {
	return s.impl.AppSessionClosed(request)
}

func (s *RPCServer) Shutdown(_ *struct{}, _ *struct{}) error {
	err := s.impl.Shutdown()
	s.mu.Lock()
	if s.app != nil {
		_ = s.app.Close()
		s.app = nil
	}
	if s.host != nil {
		_ = s.host.Close()
		s.host = nil
	}
	s.mu.Unlock()
	return err
}
