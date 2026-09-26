package ludusapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"ludusapi/models"
	"ludusapi/pluginrpc"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

var pluginRuntimeInstallPath = ludusInstallPath

// InitializePluginRuntime prepares the isolated plugin process to use the host's
// configuration and PocketBase Web API. Plugins never open the host database.
func InitializePluginRuntime(request pluginrpc.InitializeRequest) (*Server, error) {
	var configuration Configuration
	if err := json.Unmarshal(request.Configuration, &configuration); err != nil {
		return nil, fmt.Errorf("decode server configuration: %w", err)
	}

	ConfigMu.Lock()
	ServerConfiguration = configuration
	ConfigMu.Unlock()

	pluginServer := &Server{
		Version:          request.Server.Version,
		VersionString:    request.Server.VersionString,
		LudusInstallPath: request.Server.LudusInstallPath,
		Entitlements:     append([]string(nil), request.Server.Entitlements...),
		LicenseMessage:   request.Server.LicenseMessage,
		LicenseValid:     request.Server.LicenseValid,
		LicenseKey:       request.Server.LicenseKey,
		LicenseName:      request.Server.LicenseName,
		LicenseExpiry:    request.Server.LicenseExpiry,
	}

	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	pluginServer.Logger = logger
	server = pluginServer
	if request.Server.LudusInstallPath != "" {
		pluginRuntimeInstallPath = request.Server.LudusInstallPath
	}
	LudusVersion = request.Server.Version
	LudusPluginHandlerManager = NewHandlerManager()

	dataClient, err := NewPocketBaseClient(PocketBaseConnection{
		URL:               request.PocketBase.URL,
		Token:             request.PocketBase.Token,
		CertificateSHA256: request.PocketBase.CertificateSHA256,
	})
	if err != nil {
		return nil, fmt.Errorf("configure plugin PocketBase client: %w", err)
	}
	ClosePluginRuntime()
	pluginPocketBase = dataClient
	pluginPocketBase.StartTokenRefresh(logger)

	return pluginServer, nil
}

var pluginPocketBase *PocketBaseClient

func PluginPocketBase() (*PocketBaseClient, error) {
	if pluginPocketBase == nil {
		return nil, fmt.Errorf("plugin PocketBase client is not initialized")
	}
	return pluginPocketBase, nil
}

func ClosePluginRuntime() {
	closeRootPVEClient()
	if pluginPocketBase != nil {
		pluginPocketBase.Close()
		pluginPocketBase = nil
	}
}

func PluginServerState(server *Server) pluginrpc.ServerState {
	return pluginrpc.ServerState{
		Version:          server.Version,
		VersionString:    server.VersionString,
		LudusInstallPath: server.LudusInstallPath,
		Entitlements:     append([]string(nil), server.Entitlements...),
		LicenseMessage:   server.LicenseMessage,
		LicenseValid:     server.LicenseValid,
		LicenseKey:       server.LicenseKey,
		LicenseName:      server.LicenseName,
		LicenseExpiry:    server.LicenseExpiry,
	}
}

// DropPluginFiles writes embedded plugin resources into the Ludus install tree.
func DropPluginFiles(pluginName string, filesystems ...fs.FS) error {
	if pluginName == "Ludus Enterprise" && os.Geteuid() == 0 {
		logger.Info(fmt.Sprintf("Not dropping files for plugin: %s (root)", pluginName))
		return nil
	}

	for index, embeddedFS := range filesystems {
		if embeddedFS == nil {
			continue
		}
		logger.Info(fmt.Sprintf("Dropping embedded filesystem %d for plugin: %s", index+1, pluginName))
		if err := fs.WalkDir(embeddedFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			destination := filepath.Join(pluginRuntimeInstallPath, path)
			if entry.IsDir() {
				return os.MkdirAll(destination, 0755)
			}
			data, err := fs.ReadFile(embeddedFS, path)
			if err != nil {
				return err
			}
			return os.WriteFile(destination, data, 0644)
		}); err != nil {
			return fmt.Errorf("write embedded filesystem %d: %w", index+1, err)
		}
	}
	return nil
}

// PluginRequestFromEvent serializes the HTTP request and authenticated record
// identifiers. The plugin reloads those records through the host Web API.
func PluginRequestFromEvent(event *core.RequestEvent) (pluginrpc.Request, error) {
	body, err := io.ReadAll(event.Request.Body)
	if err != nil {
		return pluginrpc.Request{}, fmt.Errorf("read plugin request body: %w", err)
	}

	request := pluginrpc.Request{
		Method:     event.Request.Method,
		Path:       event.Request.URL.Path,
		RawQuery:   event.Request.URL.RawQuery,
		Header:     event.Request.Header.Clone(),
		Body:       body,
		RemoteAddr: event.Request.RemoteAddr,
	}
	if event.Auth != nil {
		request.AuthCollection = event.Auth.Collection().Name
		request.AuthRecordID = event.Auth.Id
	}
	if user, ok := event.Get("user").(*models.User); ok && user != nil && user.Record != nil {
		request.UserRecordID = user.Record.Id
	}
	if usersRange, ok := event.Get("range").(*models.Range); ok && usersRange != nil && usersRange.Record != nil {
		request.RangeRecordID = usersRange.Record.Id
		request.RootDummyRange = usersRange.Record.Id == "" && usersRange.RangeId() == "ROOT"
	}
	return request, nil
}

// PluginRequestEvent reconstructs a PocketBase request event inside a plugin
// without creating another PocketBase app or opening its database.
func PluginRequestEvent(request pluginrpc.Request) (*core.RequestEvent, *httptest.ResponseRecorder, error) {
	url := request.Path
	if request.RawQuery != "" {
		url += "?" + request.RawQuery
	}
	httpRequest, err := http.NewRequest(request.Method, url, bytes.NewReader(request.Body))
	if err != nil {
		return nil, nil, fmt.Errorf("rebuild plugin HTTP request: %w", err)
	}
	httpRequest.Header = http.Header(request.Header).Clone()
	httpRequest.RemoteAddr = request.RemoteAddr

	recorder := httptest.NewRecorder()
	event := &core.RequestEvent{
		Event: router.Event{
			Request:  httpRequest,
			Response: recorder,
		},
	}

	dataClient, err := PluginPocketBase()
	if err != nil {
		return nil, nil, err
	}

	if request.AuthRecordID != "" {
		auth, findErr := dataClient.FindRecordByID(httpRequest.Context(), request.AuthCollection, request.AuthRecordID)
		if findErr != nil {
			return nil, nil, fmt.Errorf("reload plugin auth record: %w", findErr)
		}
		event.Auth = auth
	}
	if request.UserRecordID != "" {
		record, findErr := dataClient.FindRecordByID(httpRequest.Context(), "users", request.UserRecordID, "ranges", "groups")
		if findErr != nil {
			return nil, nil, fmt.Errorf("reload plugin user record: %w", findErr)
		}
		user := &models.User{}
		user.SetProxyRecord(record)
		event.Set("user", user)
	}
	if request.RangeRecordID != "" {
		record, findErr := dataClient.FindRecordByID(httpRequest.Context(), "ranges", request.RangeRecordID)
		if findErr != nil {
			return nil, nil, fmt.Errorf("reload plugin range record: %w", findErr)
		}
		usersRange := &models.Range{}
		usersRange.SetProxyRecord(record)
		event.Set("range", usersRange)
	} else if request.RootDummyRange {
		record := core.NewRecord(core.NewBaseCollection("ranges"))
		usersRange := &models.Range{}
		usersRange.SetProxyRecord(record)
		usersRange.SetRangeId("ROOT")
		usersRange.SetName("ROOT")
		usersRange.SetTestingEnabled(false)
		usersRange.SetRangeNumber(1)
		event.Set("range", usersRange)
	}

	return event, recorder, nil
}

func RunPluginHandler(request pluginrpc.Request, handler func(*core.RequestEvent) error) (pluginrpc.Response, error) {
	event, recorder, err := PluginRequestEvent(request)
	if err != nil {
		return pluginrpc.Response{}, err
	}
	if err := handler(event); err != nil {
		return pluginrpc.Response{}, err
	}
	result := recorder.Result()
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		return pluginrpc.Response{}, fmt.Errorf("read plugin response: %w", err)
	}
	return pluginrpc.Response{
		Status: result.StatusCode,
		Header: result.Header.Clone(),
		Body:   body,
	}, nil
}

func WritePluginResponse(event *core.RequestEvent, response pluginrpc.Response) error {
	for name, values := range response.Header {
		for _, value := range values {
			event.Response.Header().Add(name, value)
		}
	}
	status := response.Status
	if status == 0 {
		status = http.StatusOK
	}
	event.Response.WriteHeader(status)
	_, err := event.Response.Write(response.Body)
	return err
}
