package ludusapi

import (
	"crypto/tls"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/static"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
)

var LudusVersion string

// Route is the information for every URI.
type Route struct {
	// Name is the name of this Route.
	Name string
	// Method is the string for the HTTP method. ex) GET, POST etc..
	Method string
	// Pattern is the pattern of the URI.
	Pattern string
	// HandlerFunc is the handler function of this route.
	HandlerFunc gin.HandlerFunc
}

type PocketBaseRoute struct {
	Method      string
	Pattern     string
	HandlerFunc func(*core.RequestEvent) error
}
type PocketBaseRoutes []PocketBaseRoute

const APIBasePath = "/api/v2"

// Routes is the list of the generated Route.
type Routes []Route

var server *Server
var Router *gin.Engine
var logger *slog.Logger
var adminProxy *httputil.ReverseProxy
var pbProxy *httputil.ReverseProxy
var PB *pocketbase.PocketBase
var app core.App
var LudusPluginHandlerManager *HandlerManager

// NewRouter returns a new router.
func NewRouter(ludusVersion string, ludusServer *Server) *gin.Engine {

	LudusPluginHandlerManager = NewHandlerManager()

	ludusServer.ParseConfig()
	server = ludusServer

	// PocketBase Config
	pbConfig := pocketbase.Config{
		HideStartBanner:      true,
		DefaultDev:           false,
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

	// We must bootstrap PocketBase before we can use it, and we use it for migrations in InitDB()
	if err := app.Bootstrap(); err != nil {
		logger.Error(fmt.Sprintf("Error bootstrapping PocketBase: %v", err))
	}

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.SetTrustedProxies(nil)

	RegisterPluginPlaceholderRoutes(router)
	RegisterRoutes(router, routes)
	InitDb()
	LudusVersion = ludusVersion

	docsAvailable := checkEmbeddedDocs()
	webUIAvailable := checkEmbeddedWebUI()

	// Keep a reference to the UI filesystem for SPA fallbacks
	var webUIFileSystem http.FileSystem
	var docsFileSystem http.FileSystem

	if docsAvailable {
		// Set up the route to serve the static site
		// The 'docs' is the directory inside the embedded file system
		docsFS, err := static.EmbedFolder(embeddedDocs, "docs")
		if err != nil {
			logger.Error(fmt.Sprintf("Error embedding docs: %v", err))
		}
		router.Use(static.Serve("/docs", docsFS))
		docsFileSystem = docsFS
		logger.Debug("Embedded documentation is available for this build of ludus-server.")
	} else {
		router.GET("/docs", func(c *gin.Context) {
			c.String(http.StatusOK, "Embedded documentation is not available for this build of ludus-server.")
		})
		logger.Debug("Embedded documentation is NOT available for this build of ludus-server.")
	}

	if webUIAvailable {
		webUIFS, err := static.EmbedFolder(embeddedWebUI, "webUI")
		if err != nil {
			logger.Error(fmt.Sprintf("Error embedding web UI: %v", err))
		}
		router.Use(static.Serve("/ui", webUIFS))
		webUIFileSystem = webUIFS

		logger.Debug("Embedded web UI is available for this build of ludus-server.")
	} else {
		router.GET("/ui", func(c *gin.Context) {
			c.String(http.StatusOK, "Embedded web UI is not available for this build of ludus-server.")
		})
		logger.Debug("Embedded web UI is NOT available for this build of ludus-server.")
	}

	Router = router

	// Setup a reverse proxy for the admin API
	adminURL, _ := url.Parse("https://127.0.0.1:8081")
	adminProxy = httputil.NewSingleHostReverseProxy(adminURL)
	customTransport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // Allow self-signed certificate
		},
	}
	adminProxy.Transport = customTransport

	// Setup a reverse proxy for the PocketBase API and web UI
	// Strip off the /pb prefix from the request URL as pocketbase doesn't support
	// running on a subpath
	pbURL, _ := url.Parse("http://127.0.0.1:8082")
	pbProxy = &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/pb")
			r.RequestURI = strings.TrimPrefix(r.RequestURI, "/pb")
			r.Host = pbURL.Host
			r.URL.Host = pbURL.Host
			r.URL.Scheme = pbURL.Scheme
			r.Header.Add("X-Forwarded-For", r.RemoteAddr)
		},
	}

	// Serve the web UI from PocketBase if available
	if webUIAvailable {
		logger.Debug("Serving web UI from PocketBase")
		app.OnServe().BindFunc(func(se *core.ServeEvent) error {
			webUIFSRoot, err := fs.Sub(embeddedWebUI, "webUI")
			if err != nil {
				logger.Error(fmt.Sprintf("Error serving web UI from PocketBase: %v", err))
				return err
			}
			se.Router.GET("/ui/{path...}", apis.Static(webUIFSRoot, true))
			return se.Next()
		})
	}

	// Serve the docs from PocketBase if available
	if docsAvailable {
		app.OnServe().BindFunc(func(se *core.ServeEvent) error {
			docsFSRoot, err := fs.Sub(embeddedDocs, "docs")
			if err != nil {
				logger.Error(fmt.Sprintf("Error serving docs from PocketBase: %v", err))
				return err
			}
			se.Router.GET("/docs/{path...}", apis.Static(docsFSRoot, true))
			return se.Next()
		})
	}

	// Setup API key authentication for the PocketBase API
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.BindFunc(APIKeyAuthenticationMiddleware)
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

	// Single unified NoRoute for SPA fallbacks and custom handling
	registerUnifiedNoRoute(router, docsAvailable, docsFileSystem, webUIAvailable, webUIFileSystem)

	// Only start PocketBase if not running as root (running as ludus)
	if os.Geteuid() != 0 {
		// Start PocketBase in a separate goroutine

		serveConfig := apis.ServeConfig{
			// HttpAddr:        fmt.Sprintf("%s:8082", ServerConfiguration.ProxmoxPublicIP),
			HttpAddr:        "127.0.0.1:8082",
			ShowStartBanner: false,
			AllowedOrigins:  []string{"*"},
		}

		go func() {
			logger.Info("Starting PocketBase")
			logger.Debug(fmt.Sprintf("Starting server on %s\n", serveConfig.HttpAddr))
			// PB has to be bootstrapped before we can serve but we bootstrapped previously
			if err := apis.Serve(app, serveConfig); err != nil {
				log.Fatalf("Failed to start the server: %v", err)
			}

			logger.Debug("PocketBase started")
		}()

	}

	return router
}

// registerUnifiedNoRoute registers a single NoRoute handler that:
// - reverse proxies to PocketBase any requests to /pb
// - serves SPA index.html for /ui and /docs routes when embedded and the path is otherwise not found (i.e. a direct link to /ui/blah or /docs/blah)
// - for other unmatched paths, serves a 404 JSON response
func registerUnifiedNoRoute(router *gin.Engine, docsAvailable bool, docsFileSystem http.FileSystem, webUIAvailable bool, webUIFileSystem http.FileSystem) {
	router.NoRoute(func(c *gin.Context) {
		logger.Debug(fmt.Sprintf("No route found for %s", c.Request.URL.Path))

		// UI SPA fallback
		if c.Request.Method == http.MethodGet &&
			!strings.ContainsRune(c.Request.URL.Path, '.') &&
			strings.HasPrefix(c.Request.URL.Path, "/ui") {
			if webUIAvailable && webUIFileSystem != nil {
				logger.Debug(fmt.Sprintf("Serving UI index.html for %s", c.Request.URL.Path))
				index, err := webUIFileSystem.Open("index.html")
				if err != nil {
					logger.Error(fmt.Sprintf("Error opening UI index.html: %v", err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Error opening UI index.html for SPA fallback"})
					return
				}
				defer index.Close()
				stat, _ := index.Stat()
				http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), index)
				return
			}
		}

		// Docs SPA fallback
		if c.Request.Method == http.MethodGet &&
			!strings.ContainsRune(c.Request.URL.Path, '.') &&
			strings.HasPrefix(c.Request.URL.Path, "/docs") {
			if docsAvailable {
				logger.Debug(fmt.Sprintf("Serving docs index.html for %s", c.Request.URL.Path))
				index, err := docsFileSystem.Open("index.html")
				if err != nil {
					logger.Error(fmt.Sprintf("Error opening docs index.html: %v", err))
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Error opening docs index.html for SPA fallback"})
					return
				}
				defer index.Close()
				stat, _ := index.Stat()
				http.ServeContent(c.Writer, c.Request, "index.html", stat.ModTime(), index)
				return
			}
		}

		// If the request is to the PocketBase API, reverse proxy it
		if strings.HasPrefix(c.Request.URL.Path, "/pb") {
			logger.Debug(fmt.Sprintf("Reverse proxying request to PocketBase: %s", c.Request.URL.Path))
			pbProxy.ServeHTTP(c.Writer, c.Request)
			c.Abort()
			return
		}

		// Custom logic for any other unmatched path that doesn't start with /docs or /ui or /pb
		if !strings.HasPrefix(c.Request.URL.Path, "/docs") && !strings.HasPrefix(c.Request.URL.Path, "/ui") && !strings.HasPrefix(c.Request.URL.Path, "/pb") {
			c.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
			return
		}
	})
}

// Index is the index handler.
func Index(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Ludus Server %s - %s", LudusVersion, server.LicenseMessage)})
}

// Ensure the user is an admin, otherwise returns a 401 response
// Note: the calling handler must return if the return value of this is false
// otherwise the user may get two JSON blobs (the error and the actual response)
func isAdmin(c *gin.Context, setJSON bool) bool {
	isAdmin := c.GetBool("isAdmin")
	if !isAdmin {
		if setJSON {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "This is an admin only endpoint or you queried another user and are not an admin"})
		}
		return false
	}
	return true
}

// Updates the date_last_active column in the database for the user making the API call
// Also logs the API action to a file
func updateLastActiveTimeAndLog(c *gin.Context) {
	// Prevent locking issues with proxied requests, don't log the last active time for ludus-admin requests
	// as they are already logged by the regular ludus service proxy
	if os.Geteuid() == 0 {
		return
	}

	anyTypeUser, exists := c.Get("thisUser")
	if !exists {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "error getting this user from context"})
		return
	}
	user, ok := anyTypeUser.(UserObject)
	if !ok {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "error converting context stored user interface to user object"})
		return
	}
	db.Model(&user).Update("date_last_active", time.Now())
}

// Validates the API key header and sets the userID, thisUser, and isAdmin value in the gin context
// If no API key is provided, it will check for a JWT token in the Authorization header
func authenticationMiddleware(c *gin.Context) {
	APIKey := c.Request.Header.Get("X-API-Key")

	if len(APIKey) == 0 {
		// Check for JWT token
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "No API Key or JWT token provided"})
			return
		} else {
			token := ""
			// Check for "Bearer" scheme
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}

			if token == "" {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization token is missing"})
				return
			}

			// The FindAuthRecordByToken function handles parsing, signature verification,
			// and checking for the record's existence.
			// It accepts the token string and an optional list of token types to validate against.
			// By default, it checks for the standard 'auth' token type.
			record, err := app.FindAuthRecordByToken(token, core.TokenTypeAuth)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
				return
			}

			// Attach the authenticated record to the context
			c.Set("authRecord", record)

			// Get the user object from the database using the UUID from the validated JWT token
			var user UserObject
			userLookupError := db.First(&user, "pocketbase_id = ?", record.Id).Error
			if userLookupError != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": fmt.Sprintf("Error looking up user with ID %s in database: %v", record.Id, userLookupError)})
				return
			}
			c.Set("email", record.Email())
			c.Set("thisUser", user)
			c.Set("userID", user.UserID)
			if user.IsAdmin {
				c.Set("isAdmin", true)
			} else {
				c.Set("isAdmin", false)
			}

			return

		}

	} else { // API Key provided
		// Check that we can pull the userID and apikey from what the user provided
		apiKeySplit := strings.Split(APIKey, ".")
		if len(apiKeySplit) != 2 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Malformed API Key provided"})
			c.Abort()
			return
		}
		userID := apiKeySplit[0]

		var user UserObject
		db.First(&user, "user_id = ?", userID)

		// Note, we stored the hash of the whole key, with userID, so check against that
		if CheckHash(APIKey, user.HashedAPIKey) {
			if user.IsAdmin {
				c.Set("isAdmin", true)
			} else {
				c.Set("isAdmin", false)
			}
			c.Set("userID", userID)
			c.Set("thisUser", user)
			return
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication failed"})
			c.Abort()
			return
		}
	}
}

// This function makes sure the request is to a user endpoint if the server is running as root (i.e. :8081)
func limitRootEndpoints(c *gin.Context) {
	logger.Debug(fmt.Sprintf("Request URL: %s", c.Request.URL.Path))
	if os.Geteuid() == 0 &&
		!strings.HasPrefix(c.Request.URL.Path, "/user") &&
		!strings.HasPrefix(c.Request.URL.Path, "/antisandbox/") &&
		!strings.HasPrefix(c.Request.URL.Path, "/ranges/create") &&
		!(strings.HasPrefix(c.Request.URL.Path, "/range") && c.Request.Method == http.MethodDelete) {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "The :8081 endpoint can only be used for user, range creation/deletion, and anti-sandbox actions. Use the :8080 endpoint for all other actions."})
		return
	} else if os.Geteuid() != 0 &&
		(strings.HasPrefix(c.Request.URL.Path, "/user") ||
			strings.HasPrefix(c.Request.URL.Path, "/antisandbox/") ||
			strings.HasPrefix(c.Request.URL.Path, "/ranges/create") ||
			(strings.HasPrefix(c.Request.URL.Path, "/range") && c.Request.Method == http.MethodDelete)) {
		// Reverse proxy to the admin API
		adminProxy.ServeHTTP(c.Writer, c.Request)

		// Abort the middleware chain to prevent the user-facing server
		// from also handling the request and writing a second response.
		c.Abort()
		return
	}

}

func RegisterRoutes(router *gin.Engine, routes Routes) {
	for _, route := range routes {
		switch route.Method {
		case http.MethodGet:
			router.GET(route.Pattern, authenticationMiddleware, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPost:
			router.POST(route.Pattern, authenticationMiddleware, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPut:
			router.PUT(route.Pattern, authenticationMiddleware, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPatch:
			router.PATCH(route.Pattern, authenticationMiddleware, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodDelete:
			router.DELETE(route.Pattern, authenticationMiddleware, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		}
	}
}

func RegisterRoutesWithPocketBase(se *core.ServeEvent, routes PocketBaseRoutes) {
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

var routes = Routes{
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
