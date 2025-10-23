package ludusapi

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
)

// HandlerManager holds the registered plugin handlers.
type HandlerManager struct {
	mu       sync.RWMutex
	handlers map[string]gin.HandlerFunc
}

// NewHandlerManager creates a new HandlerManager.
func NewHandlerManager() *HandlerManager {
	return &HandlerManager{
		handlers: make(map[string]gin.HandlerFunc),
	}
}

// RegisterHandler allows a plugin to register its handler for a specific route.
func (hm *HandlerManager) RegisterHandler(path string, handler gin.HandlerFunc) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.handlers[path] = handler
}

// GetHandler retrieves the handler for a specific route.
func (hm *HandlerManager) GetHandler(path string) (gin.HandlerFunc, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	handler, exists := hm.handlers[path]
	return handler, exists
}

func PlaceholderHandler(c *gin.Context) {
	handler, exists := LudusPluginHandlerManager.GetHandler(c.FullPath())
	if exists {
		handler(c)
	} else {
		c.JSON(http.StatusNotFound, gin.H{"error": "This endpoint is implemented in a plugin that is not loaded"})
	}
}

func RegisterPluginPlaceholderRoutes(router *gin.Engine) {

	// We hard-code the PlaceholderHandler for plugin routes, and the plugin will register its own handler for the route
	var pluginRoutes = Routes{
		Route{
			Name:        "EnableAntiSandboxForVM",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/enable",
			HandlerFunc: PlaceholderHandler,
		},
		Route{
			Name:        "InstallAntiSandboxDebs",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/install-custom",
			HandlerFunc: PlaceholderHandler,
		},
		Route{
			Name:        "InstallStandardDebs",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/install-standard",
			HandlerFunc: PlaceholderHandler,
		},
		Route{
			Name:        "GetAntisandboxStatus",
			Method:      http.MethodGet,
			Pattern:     "/antisandbox/status",
			HandlerFunc: PlaceholderHandler,
		},
	}

	for _, route := range pluginRoutes {
		logger.Debug(fmt.Sprintf("Registering placeholder route for plugin: %s %s", route.Method, route.Pattern))
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

func RegisterPluginActualRoutes(routes Routes) {
	for _, route := range routes {
		logger.Debug(fmt.Sprintf("Registering actual route for plugin: %s %s", route.Method, route.Pattern))
		LudusPluginHandlerManager.RegisterHandler(route.Pattern, route.HandlerFunc)
	}
}
