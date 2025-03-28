package ludusapi

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// Routes is the list of the generated Route.
type Routes []Route

var server *Server
var Router *gin.Engine

// NewRouter returns a new router.
func NewRouter(ludusVersion string, ludusServer *Server) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.SetTrustedProxies(nil)
	RegisterRoutes(router, routes)
	ludusServer.ParseConfig()
	InitDb()
	LudusVersion = ludusVersion

	if checkEmbeddedDocs() {
		// Set up the route to serve the static site
		// The 'docs' is the directory inside the embedded file system
		docs, _ := fs.Sub(embeddedDocs, "docs")
		router.StaticFS("/ludus", http.FS(docs))
	} else {
		router.GET("/ludus", func(c *gin.Context) {
			c.String(http.StatusOK, "Embedded documentation is not available for this build of ludus-server.")
		})
	}
	server = ludusServer
	Router = router

	return router
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
func validateAPIKey(c *gin.Context) {
	APIKey := c.Request.Header.Get("X-API-Key")

	if len(APIKey) == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No API Key provided"})
		c.Abort()
		return
	}

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

// This function makes sure the request is to a user endpoint if the server is running as root (i.e. :8081)
func limitRootEndpoints(c *gin.Context) {
	if os.Geteuid() == 0 && !strings.HasPrefix(c.Request.URL.Path, "/user") && !strings.HasPrefix(c.Request.URL.Path, "/antisandbox/") {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "The :8081 endpoint can only be used for user actions. Use the :8080 endpoint for all other actions."})
		return
	}
}

func RegisterRoutes(router *gin.Engine, routes Routes) {
	for _, route := range routes {
		switch route.Method {
		case http.MethodGet:
			router.GET(route.Pattern, validateAPIKey, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPost:
			router.POST(route.Pattern, validateAPIKey, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPut:
			router.PUT(route.Pattern, validateAPIKey, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodPatch:
			router.PATCH(route.Pattern, validateAPIKey, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
		case http.MethodDelete:
			router.DELETE(route.Pattern, validateAPIKey, updateLastActiveTimeAndLog, limitRootEndpoints, route.HandlerFunc)
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
		"RangeAccessAction",
		http.MethodPost,
		"/range/access",
		RangeAccessAction,
	},

	{
		"RangeAccessList",
		http.MethodGet,
		"/range/access",
		RangeAccessList,
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
}
