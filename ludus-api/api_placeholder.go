package ludusapi

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/pocketbase/pocketbase/core"
)

// HandlerManager holds the registered plugin handlers.
type HandlerManager struct {
	mu       sync.RWMutex
	handlers map[string]func(*core.RequestEvent) error
}

// NewHandlerManager creates a new HandlerManager.
func NewHandlerManager() *HandlerManager {
	return &HandlerManager{
		handlers: make(map[string]func(*core.RequestEvent) error),
	}
}

// RegisterHandler allows a plugin to register its handler for a specific route.
func (hm *HandlerManager) RegisterHandler(path string, handler func(*core.RequestEvent) error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.handlers[path] = handler
}

// GetHandler retrieves the handler for a specific route.
func (hm *HandlerManager) GetHandler(path string) (func(*core.RequestEvent) error, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	handler, exists := hm.handlers[path]
	return handler, exists
}

func PlaceholderHandler(e *core.RequestEvent) error {
	handler, exists := LudusPluginHandlerManager.GetHandler(e.Request.URL.Path)
	if exists {
		return handler(e)
	} else {
		return JSONError(e, http.StatusNotFound, "This endpoint is implemented in a plugin that is not loaded")
	}
}

func RegisterPluginPlaceholderRoutes(se *core.ServeEvent) {

	// We hard-code the PlaceholderHandler for plugin routes, and the plugin will register its own handler for the route
	var pluginRoutes = PocketBaseRoutes{
		PocketBaseRoute{
			Name:        "EnableAntiSandboxForVM",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/enable",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "InstallAntiSandboxDebs",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/install-custom",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "InstallStandardDebs",
			Method:      http.MethodPost,
			Pattern:     "/antisandbox/install-standard",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "GetAntisandboxStatus",
			Method:      http.MethodGet,
			Pattern:     "/antisandbox/status",
			HandlerFunc: PlaceholderHandler,
		},
	}

	RegisterRoutesWithPocketBase(se, pluginRoutes)
}

func RegisterPluginActualRoutes(routes PocketBaseRoutes) {
	for _, route := range routes {
		logger.Debug(fmt.Sprintf("Registering actual route for plugin: %s %s", route.Method, route.Pattern))
		LudusPluginHandlerManager.RegisterHandler(route.Pattern, route.HandlerFunc)
	}
}
