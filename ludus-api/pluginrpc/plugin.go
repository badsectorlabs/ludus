package pluginrpc

import (
	"net/rpc"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
)

const (
	PluginName      = "ludus"
	ProtocolVersion = 2
)

var Handshake = hcplugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "LUDUS_PLUGIN",
	MagicCookieValue: "ludus-plugin-rpc-v1",
}

type Route struct {
	Name    string
	Method  string
	Pattern string
}

type Metadata struct {
	Name    string
	Version string
	Routes  []Route
}

type ServerState struct {
	Version          string
	VersionString    string
	LudusInstallPath string
	Entitlements     []string
	LicenseMessage   string
	LicenseValid     bool
	LicenseKey       string
	LicenseName      string
	LicenseExpiry    *time.Time
}

type PocketBaseConnection struct {
	URL               string
	Token             string
	CertificateSHA256 string
}

type InitializeRequest struct {
	Configuration []byte
	Server        ServerState
	UseSDN        bool
	PocketBase    PocketBaseConnection
}

type ScheduledJob struct {
	Name     string
	Interval time.Duration
}

type InitializeResponse struct {
	Jobs  []ScheduledJob
	State ServerState
}

type Request struct {
	Method         string
	Route          string
	Path           string
	RawQuery       string
	Header         map[string][]string
	Body           []byte
	RemoteAddr     string
	AuthCollection string
	AuthRecordID   string
	UserRecordID   string
	RangeRecordID  string
	RootDummyRange bool
}

type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

type JobResponse struct {
	State ServerState
}

// Plugin is the interface used by the Ludus host. Its implementation is an RPC
// client when called by the host and the real plugin when served by a plugin
// process.
type Plugin interface {
	Metadata() (Metadata, error)
	Initialize(InitializeRequest) (InitializeResponse, error)
	Handle(Request) (Response, error)
	RunJob(string) (JobResponse, error)
	Shutdown() error
}

// Implementation is implemented by plugin executables and served over net/rpc.
type Implementation interface {
	Plugin
}

type RPCPlugin struct {
	Impl Implementation
}

func (p *RPCPlugin) Server(*hcplugin.MuxBroker) (interface{}, error) {
	return &RPCServer{Impl: p.Impl}, nil
}

func (p *RPCPlugin) Client(_ *hcplugin.MuxBroker, client *rpc.Client) (interface{}, error) {
	return &RPCClient{client: client}, nil
}

func PluginMap(impl Implementation) map[string]hcplugin.Plugin {
	return map[string]hcplugin.Plugin{
		PluginName: &RPCPlugin{Impl: impl},
	}
}

func Serve(impl Implementation) {
	hcplugin.Serve(&hcplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(impl),
	})
}

type RPCClient struct {
	client *rpc.Client
}

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

func (c *RPCClient) RunJob(name string) (JobResponse, error) {
	var response JobResponse
	err := c.client.Call("Plugin.RunJob", name, &response)
	return response, err
}

func (c *RPCClient) Shutdown() error {
	return c.client.Call("Plugin.Shutdown", new(struct{}), new(struct{}))
}

type RPCServer struct {
	Impl Implementation
}

func (s *RPCServer) Metadata(_ *struct{}, response *Metadata) error {
	var err error
	*response, err = s.Impl.Metadata()
	return err
}

func (s *RPCServer) Initialize(request InitializeRequest, response *InitializeResponse) error {
	var err error
	*response, err = s.Impl.Initialize(request)
	return err
}

func (s *RPCServer) Handle(request Request, response *Response) error {
	var err error
	*response, err = s.Impl.Handle(request)
	return err
}

func (s *RPCServer) RunJob(name string, response *JobResponse) error {
	var err error
	*response, err = s.Impl.RunJob(name)
	return err
}

func (s *RPCServer) Shutdown(_ *struct{}, _ *struct{}) error {
	return s.Impl.Shutdown()
}
