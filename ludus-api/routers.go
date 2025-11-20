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

	"github.com/goforj/godump"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/ui"
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
var DebugProxmox bool

// NewRouter returns a new router.
func NewRouter(ludusVersion string, ludusServer *Server) *core.App {

	LudusPluginHandlerManager = NewHandlerManager()

	ludusServer.ParseConfig()
	server = ludusServer

	// Resolve the Proxmox debug flag here for speed (vs every call to GetGoProxmoxClientForUserUsingToken)
	if os.Getenv("LUDUS_DEBUG_PROXMOX") == "1" {
		DebugProxmox = true
	}

	// PocketBase Config
	pbConfig := pocketbase.Config{
		HideStartBanner:      true,
		DefaultDev:           os.Getenv("LUDUS_DEBUG_DATABASE") == "1",
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
		os.Exit(1)
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

	// Register all custom middleware. These will apply to every request.
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		// API key authentication for the PocketBase API
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "APIKeyAuthenticationMiddleware",
			Func:     APIKeyAuthenticationMiddleware,
			Priority: 1000, // This runs before any other custom middleware, authenticates API keys and sets the user record and range record in the request context
		})
		// Lookup the user and ranges for the request, store them in the request context
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "userAndRangesLookupMiddleware",
			Func:     userAndRangesLookupMiddleware,
			Priority: 1001,
		})
		// Update the last active time for the user and log the API action
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "updateLastActiveTimeAndLog",
			Func:     updateLastActiveTimeAndLog,
			Priority: 1002,
		})
		// Limit the endpoints that can be accessed by the root user, and reverse proxy to the admin API for endpoints that need root access
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "limitRootEndpoints",
			Func:     limitRootEndpoints,
			Priority: 1003,
		})
		// Require authentication for all requests
		se.Router.Bind(&hook.Handler[*core.RequestEvent]{
			Id:       "requireAuth",
			Func:     requireAuth,
			Priority: 1004, // This should be the last middleware to run
		})

		return se.Next()
	})

	// Make /admin serve the same content as /_ (the pocketbase admin UI)
	// This code is copied from the PocketBase codebase with just the path changed, https://github.com/pocketbase/pocketbase/blob/1dc5e061b8bbc7374e99c3fe6f153db25e71f860/apis/serve.go#L80-L94
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/admin/{path...}", apis.Static(ui.DistDirFS, false)).
			BindFunc(func(e *core.RequestEvent) error {
				// ignore root path
				if e.Request.PathValue(apis.StaticWildcardParam) != "" {
					e.Response.Header().Set("Cache-Control", "max-age=1209600, stale-while-revalidate=86400")
				}

				// add a default CSP
				if e.Response.Header().Get("Content-Security-Policy") == "" {
					e.Response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' http://127.0.0.1:* https://tile.openstreetmap.org data: blob:; connect-src 'self' http://127.0.0.1:* https://nominatim.openstreetmap.org; script-src 'self' 'sha256-GRUzBA7PzKYug7pqxv5rJaec5bwDCw1Vo6/IXwvD3Tc='")
				}

				return e.Next()
			}).
			Bind(apis.Gzip())
		return se.Next()
	})

	// Simple whoami to test authentication
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET(APIBasePath+"/whoami", func(e *core.RequestEvent) error {
			if e.Auth == nil {
				return e.UnauthorizedError("Authentication failed", "You are not authenticated")
			}
			var usersRange *models.Range
			usersRangeFromContext := e.Get("range")
			if usersRangeFromContext != nil {
				usersRange = usersRangeFromContext.(*models.Range)
			}
			rangeString := godump.DumpJSONStr(usersRange)
			userString := godump.DumpJSONStr(e.Auth)
			return e.String(http.StatusOK, "{\"user\": "+userString+", \"range\": "+rangeString+"}")
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

func Index(e *core.RequestEvent) error {
	return e.Redirect(http.StatusTemporaryRedirect, "/ui")
}

func RegisterRoutesWithPocketBase(se *core.ServeEvent, routes PocketBaseRoutes) {
	// Redirect / to /ui
	se.Router.GET("/", Index)
	for _, route := range routes {
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

func Version(e *core.RequestEvent) error {
	return JSONResult(e, http.StatusOK, "Ludus Server "+LudusVersion+" - "+server.LicenseMessage)
}

var routes = PocketBaseRoutes{
	{
		"Version",
		http.MethodGet,
		"/",
		Version,
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
		"/range/{rangeID}/vms",
		DeleteRangeVMs,
	},

	{
		"DeleteTemplate",
		http.MethodDelete,
		"/template/{templateName}",
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
		"/user/{userID}",
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
		"/groups/{groupName}",
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
		"/groups/{groupName}/users/{userID}",
		AddUserToGroup,
	},

	{
		"RemoveUserFromGroup",
		http.MethodDelete,
		"/groups/{groupName}/users/{userID}",
		RemoveUserFromGroup,
	},

	{
		"AddRangeToGroup",
		http.MethodPost,
		"/groups/{groupName}/ranges/{rangeID}",
		AddRangeToGroup,
	},

	{
		"RemoveRangeFromGroup",
		http.MethodDelete,
		"/groups/{groupName}/ranges/{rangeID}",
		RemoveRangeFromGroup,
	},

	{
		"ListGroupMembers",
		http.MethodGet,
		"/groups/{groupName}/users",
		ListGroupMembers,
	},

	{
		"ListGroupRanges",
		http.MethodGet,
		"/groups/{groupName}/ranges",
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
		"/ranges/assign/{userID}/{rangeID}",
		AssignRangeToUser,
	},

	{
		"RevokeRangeFromUser",
		http.MethodDelete,
		"/ranges/revoke/{userID}/{rangeID}",
		RevokeRangeFromUser,
	},

	{
		"ListRangeUsers",
		http.MethodGet,
		"/ranges/{rangeID}/users",
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
		"MigrateSQLiteToPocketBase",
		http.MethodPost,
		"/migrate/sqlite",
		MigrateSQLiteToPocketBaseHandler,
	},
}
