package ludusapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"ludusapi/pluginrpc"
	"ludusapi/scheduler"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"sync"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	"github.com/pocketbase/pocketbase/core"
)

type managedPlugin struct {
	rpc      pluginrpc.Plugin
	client   *hcplugin.Client
	metadata pluginrpc.Metadata

	mu               sync.Mutex
	initialized      bool
	routesRegistered bool
}

type Server struct {
	pluginMu                   sync.Mutex
	plugins                    []*managedPlugin
	pluginsInitialized         bool
	pluginRoutesRegistered     bool
	licenseMu                  sync.RWMutex
	Version                    string
	VersionString              string
	LudusInstallPath           string
	Entitlements               []string
	LicenseMessage             string
	LicenseValid               bool
	LicenseKey                 string
	LicenseName                string
	LicenseExpiry              *time.Time
	Logger                     *slog.Logger
	Scheduler                  *scheduler.Scheduler
	PluginAPIURL               string
	PluginAPICertificateSHA256 string
	pluginAPIToken             string
	pluginReleaseLookup        pluginReleaseLookupFunc
	pluginReleaseDownload      pluginReleaseDownloadFunc
}

func (s *Server) LoadPlugin(path string) error {
	managed, err := startManagedPlugin(path, s.Logger)
	if err != nil {
		return err
	}

	s.pluginMu.Lock()
	for _, existing := range s.plugins {
		if existing.metadata.Name == managed.metadata.Name {
			s.pluginMu.Unlock()
			managed.client.Kill()
			logger.Info(fmt.Sprintf("Plugin %s is already loaded, skipping", managed.metadata.Name))
			return nil
		}
	}
	s.plugins = append(s.plugins, managed)
	initializeNow := s.pluginsInitialized
	registerRoutesNow := s.pluginRoutesRegistered
	s.pluginMu.Unlock()

	logger.Info(fmt.Sprintf("Loaded plugin: %s %s", managed.metadata.Name, managed.metadata.Version))

	if initializeNow {
		if err := s.initializePlugin(managed); err != nil {
			s.removePlugin(managed)
			managed.client.Kill()
			return err
		}
	}
	if registerRoutesNow {
		s.registerPluginRoutes(managed)
	}
	return nil
}

func startManagedPlugin(path string, serverLogger *slog.Logger) (*managedPlugin, error) {
	if logger == nil {
		logger = serverLogger
		if logger == nil {
			logger = slog.Default()
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat plugin executable: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("plugin path is not a regular file: %s", path)
	}
	if info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("plugin is not executable: %s", path)
	}

	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  pluginrpc.Handshake,
		Plugins:          pluginrpc.PluginMap(nil),
		Cmd:              exec.Command(path),
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolNetRPC},
		SyncStdout:       os.Stdout,
		SyncStderr:       os.Stderr,
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("start plugin process: %w", err)
	}
	rawPlugin, err := rpcClient.Dispense(pluginrpc.PluginName)
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("dispense plugin: %w", err)
	}
	rpcPlugin, ok := rawPlugin.(pluginrpc.Plugin)
	if !ok {
		client.Kill()
		return nil, fmt.Errorf("unexpected plugin RPC client type %T", rawPlugin)
	}
	metadata, err := rpcPlugin.Metadata()
	if err != nil {
		client.Kill()
		return nil, fmt.Errorf("read plugin metadata: %w", err)
	}
	if metadata.Name == "" {
		client.Kill()
		return nil, fmt.Errorf("plugin returned an empty name")
	}
	for _, route := range metadata.Routes {
		if route.Name == "" || route.Method == "" || route.Pattern == "" {
			client.Kill()
			return nil, fmt.Errorf("plugin %s returned an incomplete route declaration", metadata.Name)
		}
	}

	return &managedPlugin{
		rpc:      rpcPlugin,
		client:   client,
		metadata: metadata,
	}, nil
}

func readPluginMetadata(path string, serverLogger *slog.Logger) (pluginrpc.Metadata, error) {
	plugin, err := startManagedPlugin(path, serverLogger)
	if err != nil {
		return pluginrpc.Metadata{}, err
	}
	defer plugin.client.Kill()
	if err := plugin.rpc.Shutdown(); err != nil {
		return pluginrpc.Metadata{}, fmt.Errorf("shut down plugin metadata probe: %w", err)
	}
	return plugin.metadata, nil
}

func (s *Server) removePlugin(target *managedPlugin) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	for index, plugin := range s.plugins {
		if plugin == target {
			s.plugins = append(s.plugins[:index], s.plugins[index+1:]...)
			return
		}
	}
}

func (s *Server) InitializePlugins() error {
	s.pluginMu.Lock()
	s.pluginsInitialized = true
	plugins := append([]*managedPlugin(nil), s.plugins...)
	s.pluginMu.Unlock()

	var initializeErrors []error
	for _, plugin := range plugins {
		if err := s.initializePlugin(plugin); err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize plugin %s: %v", plugin.metadata.Name, err))
			s.removePlugin(plugin)
			plugin.client.Kill()
			initializeErrors = append(initializeErrors, err)
		}
	}
	return errors.Join(initializeErrors...)
}

func (s *Server) pluginPocketBaseConnection() (pluginrpc.PocketBaseConnection, error) {
	if s.PluginAPIURL == "" {
		return pluginrpc.PocketBaseConnection{}, errors.New("plugin PocketBase API URL is not configured")
	}
	token := s.pluginAPIToken
	if token == "" {
		if PB == nil || PB.App == nil {
			return pluginrpc.PocketBaseConnection{}, errors.New("PocketBase is not initialized")
		}
		superuser, err := PB.App.FindFirstRecordByData(core.CollectionNameSuperusers, "email", "root@ludus.internal")
		if err != nil {
			return pluginrpc.PocketBaseConnection{}, fmt.Errorf("find plugin API superuser: %w", err)
		}
		token, err = superuser.NewAuthToken()
		if err != nil {
			return pluginrpc.PocketBaseConnection{}, fmt.Errorf("create plugin API token: %w", err)
		}
	}
	return pluginrpc.PocketBaseConnection{
		URL:               s.PluginAPIURL,
		Token:             token,
		CertificateSHA256: s.PluginAPICertificateSHA256,
	}, nil
}

func (s *Server) initializePlugin(plugin *managedPlugin) error {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if plugin.initialized {
		return nil
	}

	ConfigMu.RLock()
	configuration, err := json.Marshal(ServerConfiguration)
	ConfigMu.RUnlock()
	if err != nil {
		return fmt.Errorf("encode configuration for plugin %s: %w", plugin.metadata.Name, err)
	}

	connection, err := s.pluginPocketBaseConnection()
	if err != nil {
		return fmt.Errorf("configure PocketBase API for plugin %s: %w", plugin.metadata.Name, err)
	}

	response, err := plugin.rpc.Initialize(pluginrpc.InitializeRequest{
		Configuration: configuration,
		Server:        s.pluginState(),
		UseSDN:        UseSDN,
		PocketBase:    connection,
	})
	if err != nil {
		return fmt.Errorf("initialize plugin %s: %w", plugin.metadata.Name, err)
	}

	for _, scheduledJob := range response.Jobs {
		if scheduledJob.Name == "" || scheduledJob.Interval <= 0 {
			return fmt.Errorf("plugin %s returned an invalid scheduled job", plugin.metadata.Name)
		}
		if s.Scheduler == nil {
			return fmt.Errorf("plugin %s requires scheduler job %s but the server scheduler is nil", plugin.metadata.Name, scheduledJob.Name)
		}
	}

	for _, scheduledJob := range response.Jobs {
		jobName := scheduledJob.Name
		s.Scheduler.Register(scheduler.Job{
			Name:     plugin.metadata.Name + ":" + jobName,
			Interval: scheduledJob.Interval,
			Fn: func(ctx context.Context) error {
				result, runErr := plugin.rpc.RunJob(jobName)
				if runErr != nil {
					return fmt.Errorf("run plugin job %s: %w", jobName, runErr)
				}
				s.applyPluginState(result.State)
				if jobName == "license-check" {
					return s.refreshLicensedPlugins(ctx)
				}
				return nil
			},
		})
	}

	s.applyPluginState(response.State)
	plugin.initialized = true
	return nil
}

func (s *Server) pluginState() pluginrpc.ServerState {
	s.licenseMu.RLock()
	defer s.licenseMu.RUnlock()
	return pluginrpc.ServerState{
		Version:          s.Version,
		VersionString:    s.VersionString,
		LudusInstallPath: s.LudusInstallPath,
		Entitlements:     append([]string(nil), s.Entitlements...),
		LicenseMessage:   s.LicenseMessage,
		LicenseValid:     s.LicenseValid,
		LicenseKey:       s.LicenseKey,
		LicenseName:      s.LicenseName,
		LicenseExpiry:    s.LicenseExpiry,
	}
}

func (s *Server) applyPluginState(state pluginrpc.ServerState) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	// Preserve host entitlements when a plugin reports a valid license with an
	// empty entitlement list (offline/fallback paths often cannot re-fetch them).
	if len(state.Entitlements) > 0 || !state.LicenseValid {
		s.Entitlements = append(s.Entitlements[:0], state.Entitlements...)
	}
	s.LicenseMessage = state.LicenseMessage
	s.LicenseValid = state.LicenseValid
	s.LicenseKey = state.LicenseKey
	s.LicenseName = state.LicenseName
	s.LicenseExpiry = state.LicenseExpiry
}

func (s *Server) RegisterPluginRoutes(_ *core.App) {
	s.pluginMu.Lock()
	s.pluginRoutesRegistered = true
	plugins := append([]*managedPlugin(nil), s.plugins...)
	s.pluginMu.Unlock()

	if LudusPluginHandlerManager == nil {
		LudusPluginHandlerManager = NewHandlerManager()
	}
	for _, plugin := range plugins {
		s.registerPluginRoutes(plugin)
	}
}

func (s *Server) registerPluginRoutes(plugin *managedPlugin) {
	plugin.mu.Lock()
	defer plugin.mu.Unlock()
	if plugin.routesRegistered {
		return
	}
	for _, declaredRoute := range plugin.metadata.Routes {
		route := declaredRoute
		logger.Info(fmt.Sprintf("Registering route for plugin %s: %s %s", plugin.metadata.Name, route.Method, route.Pattern))
		LudusPluginHandlerManager.RegisterHandler(route.Method, route.Pattern, func(event *core.RequestEvent) error {
			request, err := PluginRequestFromEvent(event)
			if err != nil {
				return JSONError(event, http.StatusInternalServerError, err.Error())
			}
			request.Route = route.Name
			response, err := plugin.rpc.Handle(request)
			if err != nil {
				return JSONError(event, http.StatusInternalServerError, fmt.Sprintf("plugin %s request failed: %v", plugin.metadata.Name, err))
			}
			return WritePluginResponse(event, response)
		})
	}
	plugin.routesRegistered = true
}

func (s *Server) ShutdownPlugins() {
	s.pluginMu.Lock()
	plugins := append([]*managedPlugin(nil), s.plugins...)
	s.plugins = nil
	s.pluginsInitialized = false
	s.pluginRoutesRegistered = false
	s.pluginMu.Unlock()

	for _, plugin := range plugins {
		if err := plugin.rpc.Shutdown(); err != nil {
			logger.Info(fmt.Sprintf("Error shutting down plugin %s: %v", plugin.metadata.Name, err))
		}
		plugin.client.Kill()
	}
}

func (s *Server) LoadedPluginNames() []string {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	names := make([]string, 0, len(s.plugins))
	for _, plugin := range s.plugins {
		names = append(names, plugin.metadata.Name)
	}
	return names
}

func (s *Server) loadedPluginMetadata(name string) (pluginrpc.Metadata, bool) {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	for _, plugin := range s.plugins {
		if plugin.metadata.Name == name {
			return plugin.metadata, true
		}
	}
	return pluginrpc.Metadata{}, false
}

// HasEntitlement checks if the server license has a specific entitlement
func (s *Server) HasEntitlement(code string) bool {
	s.licenseMu.RLock()
	defer s.licenseMu.RUnlock()
	return slices.Contains(s.Entitlements, code)
}
