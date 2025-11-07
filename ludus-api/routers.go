package ludusapi

import (
	"crypto/tls"
	"fmt"
	"io/fs"
	"log/slog"
	"ludusapi/models"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/types"
)

var LudusVersion string

type PocketBaseRoute struct {
	Name        string
	Method      string
	Pattern     string
	HandlerFunc func(*core.RequestEvent) error
}
type PocketBaseRoutes []PocketBaseRoute

const APIBasePath = "/api/v2"

// Routes is the list of the generated Route.
type Routes []PocketBaseRoute

var server *Server
var logger *slog.Logger
var adminProxy *httputil.ReverseProxy
var PB *pocketbase.PocketBase
var app core.App
var LudusPluginHandlerManager *HandlerManager

// NewRouter returns a new router.
func NewRouter(ludusVersion string, ludusServer *Server) *core.App {

	LudusPluginHandlerManager = NewHandlerManager()

	ludusServer.ParseConfig()
	server = ludusServer

	// PocketBase Config
	pbConfig := pocketbase.Config{
		HideStartBanner:      true,
		DefaultDev:           true,
		DefaultDataDir:       ServerConfiguration.DataDirectory,
		DefaultEncryptionEnv: "LUDUS_DB_ENCRYPTION_PASSWORD",
	}
	PB = pocketbase.NewWithConfig(pbConfig)
	app = PB.App

	migratecmd.MustRegister(app, PB.RootCmd, migratecmd.Config{
		// enable auto creation of migration files when making collection changes in the Dashboard
		Automigrate: false,
	})

	// Transition from using log.Printf to using slog.Info, slog.Error, etc.
	// Adopts the debug level from the main server logger
	if server.Logger != nil {
		logger = server.Logger
		slog.SetDefault(server.Logger)
	} else {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))
		slog.SetDefault(logger)
	}

	// We must bootstrap PocketBase before we can use it, and it creates the DB that is used by InitDb()
	if err := app.Bootstrap(); err != nil {
		logger.Error(fmt.Sprintf("Error bootstrapping PocketBase: %v", err))
	}

	InitDb()
	LudusVersion = ludusVersion

	docsAvailable := checkEmbeddedDocs()
	webUIAvailable := checkEmbeddedWebUI()

	// Setup a reverse proxy for the admin API
	adminURL, _ := url.Parse("https://127.0.0.1:8081")
	adminProxy = httputil.NewSingleHostReverseProxy(adminURL)
	customTransport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Allow self-signed certificate
		},
	}
	adminProxy.Transport = customTransport

	// Serve the web UI from PocketBase if available
	if webUIAvailable && os.Geteuid() != 0 {
		logger.Debug("Serving web UI at /ui")
		app.OnServe().BindFunc(func(se *core.ServeEvent) error {
			webUIFSRoot, err := fs.Sub(embeddedWebUI, "webUI")
			if err != nil {
				logger.Error(fmt.Sprintf("Error serving web UI: %v", err))
				return err
			}
			se.Router.GET("/ui/{path...}", apis.Static(webUIFSRoot, true))
			return se.Next()
		})
	}

	// Serve the docs from PocketBase if available
	if docsAvailable && os.Geteuid() != 0 {
		logger.Debug("Serving docs at /docs")
		app.OnServe().BindFunc(func(se *core.ServeEvent) error {
			docsFSRoot, err := fs.Sub(embeddedDocs, "docs")
			if err != nil {
				logger.Error(fmt.Sprintf("Error serving docs: %v", err))
				return err
			}
			se.Router.GET("/docs/{path...}", apis.Static(docsFSRoot, true))
			return se.Next()
		})
	}

	// Setup API key authentication for the PocketBase API
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.BindFunc(APIKeyAuthenticationMiddleware)
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "UserAndRangesLookupMiddleware",
			Func:     UserAndRangesLookupMiddleware,
			Priority: 997, // This should be the first of the custom middleware to run
		})
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "updateLastActiveTimeAndLog",
			Func:     updateLastActiveTimeAndLog,
			Priority: 998, // This should be the second of the custom middleware to run
		})
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "limitRootEndpoints",
			Func:     limitRootEndpoints,
			Priority: 999, // This should be the last middleware to run
		})

		return se.Next()
	})

	// Simple whoami
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/whoami", func(e *core.RequestEvent) error {
			if e.Auth == nil {
				return e.UnauthorizedError("Authentication failed", "You are not authenticated")
			}
			return e.String(http.StatusOK, "You are authenticated as "+e.Auth.GetString("name"))
		})
		return se.Next()
	})

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		RegisterRoutesWithPocketBase(se, routes)
		RegisterPluginPlaceholderRoutes(se)
		return se.Next()
	})

	return &app
}

// Index is the index handler.
func Index(e *core.RequestEvent) error {
	return JSONResult(e, http.StatusOK, "Ludus Server "+LudusVersion+" - "+server.LicenseMessage)
}

// Updates the date_last_active column in the database for the user making the API call
// Also logs the API action to a file
func updateLastActiveTimeAndLog(e *core.RequestEvent) error {
	// Prevent locking issues with proxied requests, don't log the last active time for ludus-admin requests
	// as they are already logged by the regular ludus service proxy
	if os.Geteuid() == 0 || e.Auth == nil || e.Get("user") == nil || e.Auth.IsSuperuser() {
		return e.Next()
	}

	user := e.Get("user").(*models.User)
	user.SetLastActive(types.NowDateTime())
	err := e.App.Save(user)
	if err != nil {
		logger.Error(fmt.Sprintf("Error saving user: %v", err))
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving user: %v", err))
	}
	return e.Next()
}

// This function makes sure the request is to a user endpoint if the server is running as root (i.e. :8081)
func limitRootEndpoints(e *core.RequestEvent) error {
	logger.Debug(fmt.Sprintf("Request URL: %s", e.Request.URL.Path))
	if os.Geteuid() == 0 &&
		!strings.HasPrefix(e.Request.URL.Path, "/user") &&
		!strings.HasPrefix(e.Request.URL.Path, "/antisandbox/") &&
		!strings.HasPrefix(e.Request.URL.Path, "/ranges/create") &&
		!(strings.HasPrefix(e.Request.URL.Path, "/range") && e.Request.Method == http.MethodDelete) {
		return JSONError(e, http.StatusInternalServerError, "The :8081 endpoint can only be used for user, range creation/deletion, and anti-sandbox actions. Use the :8080 endpoint for all other actions.")
	} else if os.Geteuid() != 0 &&
		(strings.HasPrefix(e.Request.URL.Path, "/user") ||
			strings.HasPrefix(e.Request.URL.Path, "/antisandbox/") ||
			strings.HasPrefix(e.Request.URL.Path, "/ranges/create") ||
			(strings.HasPrefix(e.Request.URL.Path, "/range") && e.Request.Method == http.MethodDelete)) {
		// Reverse proxy to the admin API
		adminProxy.ServeHTTP(e.Response, e.Request)

		// Abort the middleware chain to prevent the user-facing server
		// from also handling the request and writing a second response.
		return nil
	}

	return e.Next()

}

func RegisterRoutesWithPocketBase(se *core.ServeEvent, routes PocketBaseRoutes) {
	for _, route := range routes {
		logger.Debug(fmt.Sprintf("Registering route: %s %s", route.Method, route.Pattern))
		switch route.Method {
		case http.MethodGet:
			se.Router.GET(APIBasePath+route.Pattern, route.HandlerFunc)
		case http.MethodPost:
			se.Router.POST(APIBasePath+route.Pattern, route.HandlerFunc)
		case http.MethodPut:
			se.Router.PUT(APIBasePath+route.Pattern, route.HandlerFunc)
		case http.MethodPatch:
			se.Router.PATCH(APIBasePath+route.Pattern, route.HandlerFunc)
		case http.MethodDelete:
			se.Router.DELETE(APIBasePath+route.Pattern, route.HandlerFunc)
		}
	}
}

var routes = PocketBaseRoutes{
	{
		"Index",
		http.MethodGet,
		"/",
		Index,
	},

	{
		"GetLicense",
		http.MethodGet,
		"/license",
		GetLicense,
	},

	{
		"AbortAnsible",
		http.MethodPost,
		"/range/abort",
		AbortAnsible,
	},

	{
		"AbortPacker",
		http.MethodPost,
		"/templates/abort",
		AbortPacker,
	},

	{
		"BuildTemplates",
		http.MethodPost,
		"/templates",
		BuildTemplates,
	},

	{
		"PowerOffRange",
		http.MethodPut,
		"/range/poweroff",
		PowerOffRange,
	},

	{
		"PowerOnRange",
		http.MethodPut,
		"/range/poweron",
		PowerOnRange,
	},

	{
		"DeployRange",
		http.MethodPost,
		"/range/deploy",
		DeployRange,
	},

	{
		"Deny",
		http.MethodPost,
		"/testing/deny",
		Deny,
	},

	{
		"DeleteRange",
		http.MethodDelete,
		"/range",
		DeleteRange,
	},

	{
		"DeleteRangeVMs",
		http.MethodDelete,
		"/range/:rangeID/vms",
		DeleteRangeVMs,
	},

	{
		"DeleteTemplate",
		http.MethodDelete,
		"/template/:templateName",
		DeleteTemplate,
	},

	{
		"GetConfig",
		http.MethodGet,
		"/range/config",
		GetConfig,
	},

	{
		"GetConfigExample",
		http.MethodGet,
		"/range/config/example",
		GetConfigExample,
	},

	{
		"GetEtcHosts",
		http.MethodGet,
		"/range/etchosts",
		GetEtcHosts,
	},

	{
		"GetLogs",
		http.MethodGet,
		"/range/logs",
		GetLogs,
	},

	{
		"GetPackerLogs",
		http.MethodGet,
		"/templates/logs",
		GetPackerLogs,
	},

	{
		"GetRDP",
		http.MethodGet,
		"/range/rdpconfigs",
		GetRDP,
	},

	{
		"GetSSHConfig",
		http.MethodGet,
		"/range/sshconfig",
		GetSSHConfig,
	},

	{
		"InstallRoleFromTar",
		http.MethodPut,
		"/ansible/role/fromtar",
		InstallRoleFromTar,
	},

	{
		"ActionRoleFromInternet",
		http.MethodPost,
		"/ansible/role",
		ActionRoleFromInternet,
	},

	{
		"ActionCollectionFromInternet",
		http.MethodPost,
		"/ansible/collection",
		ActionCollectionFromInternet,
	},

	{
		"ListRange",
		http.MethodGet,
		"/range",
		ListRange,
	},

	{
		"PutConfig",
		http.MethodPut,
		"/range/config",
		PutConfig,
	},

	{
		"Allow",
		http.MethodPost,
		"/testing/allow",
		Allow,
	},

	{
		"StartTesting",
		http.MethodPut,
		"/testing/start",
		StartTesting,
	},

	{
		"StopTesting",
		http.MethodPut,
		"/testing/stop",
		StopTesting,
	},

	{
		"AddUser",
		http.MethodPost,
		"/user",
		AddUser,
	},

	{
		"DeleteUser",
		http.MethodDelete,
		"/user/:userID",
		DeleteUser,
	},

	{
		"GetAnsibleInventoryForRange",
		http.MethodGet,
		"/range/ansibleinventory",
		GetAnsibleInventoryForRange,
	},

	{
		"GetRolesAndCollections",
		http.MethodGet,
		"/ansible",
		GetRolesAndCollections,
	},

	{
		"GetAnsibleTagsForDeployment",
		http.MethodGet,
		"/range/tags",
		GetAnsibleTagsForDeployment,
	},

	{
		"GetAPIKey",
		http.MethodGet,
		"/user/apikey",
		GetAPIKey,
	},

	{
		"GetCredentials",
		http.MethodGet,
		"/user/credentials",
		GetCredentials,
	},

	{
		"GetTemplates",
		http.MethodGet,
		"/templates",
		GetTemplates,
	},

	{
		"GetTemplateStatus",
		http.MethodGet,
		"/templates/status",
		GetTemplateStatus,
	},

	{
		"GetWireguardConfig",
		http.MethodGet,
		"/user/wireguard",
		GetWireguardConfig,
	},

	{
		"ListAllUsers",
		http.MethodGet,
		"/user/all",
		ListAllUsers,
	},

	{
		"ListAllRanges",
		http.MethodGet,
		"/range/all",
		ListAllRanges,
	},

	{
		"ListUser",
		http.MethodGet,
		"/user",
		ListUser,
	},

	{
		"PasswordReset",
		http.MethodPost,
		"/user/passwordreset",
		PasswordReset,
	},

	{
		"PostCredentials",
		http.MethodPost,
		"/user/credentials",
		PostCredentials,
	},

	{
		"PutTemplateTar",
		http.MethodPut,
		"/templates",
		PutTemplateTar,
	},

	{
		"GetSnapshots",
		http.MethodGet,
		"/snapshots/list",
		GetSnapshots,
	},

	{
		"RollbackSnapshot",
		http.MethodPost,
		"/snapshots/rollback",
		RollbackSnapshot,
	},

	{
		"CreateSnapshot",
		http.MethodPost,
		"/snapshots/create",
		CreateSnapshot,
	},

	{
		"RemoveSnapshot",
		http.MethodPost,
		"/snapshots/remove",
		RemoveSnapshot,
	},

	{
		"UpdateVMs",
		http.MethodPost,
		"/testing/update",
		UpdateVMs,
	},

	// Group management routes
	{
		"CreateGroup",
		http.MethodPost,
		"/groups",
		CreateGroup,
	},

	{
		"DeleteGroup",
		http.MethodDelete,
		"/groups/:groupName",
		DeleteGroup,
	},

	{
		"ListGroups",
		http.MethodGet,
		"/groups",
		ListGroups,
	},

	{
		"AddUserToGroup",
		http.MethodPost,
		"/groups/:groupName/users/:userID",
		AddUserToGroup,
	},

	{
		"RemoveUserFromGroup",
		http.MethodDelete,
		"/groups/:groupName/users/:userID",
		RemoveUserFromGroup,
	},

	{
		"AddRangeToGroup",
		http.MethodPost,
		"/groups/:groupName/ranges/:rangeID",
		AddRangeToGroup,
	},

	{
		"RemoveRangeFromGroup",
		http.MethodDelete,
		"/groups/:groupName/ranges/:rangeID",
		RemoveRangeFromGroup,
	},

	{
		"ListGroupMembers",
		http.MethodGet,
		"/groups/:groupName/users",
		ListGroupMembers,
	},

	{
		"ListGroupRanges",
		http.MethodGet,
		"/groups/:groupName/ranges",
		ListGroupRanges,
	},

	// Enhanced range management routes
	{
		"CreateRange",
		http.MethodPost,
		"/ranges/create",
		CreateRange,
	},

	{
		"AssignRangeToUser",
		http.MethodPost,
		"/ranges/assign/:userID/:rangeID",
		AssignRangeToUser,
	},

	{
		"RevokeRangeFromUser",
		http.MethodDelete,
		"/ranges/revoke/:userID/:rangeID",
		RevokeRangeFromUser,
	},

	{
		"ListRangeUsers",
		http.MethodGet,
		"/ranges/:rangeID/users",
		ListRangeUsers,
	},

	{
		"ListUserAccessibleRanges",
		http.MethodGet,
		"/ranges/accessible",
		ListUserAccessibleRanges,
	},

	// Migration routes
	{
		"MigrateSQLiteToPostgreSQL",
		http.MethodPost,
		"/migrate/sqlite",
		MigrateSQLiteToPocketBaseHandler,
	},
}
