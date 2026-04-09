package ludusapi

import (
	"fmt"
	"net/http"
	"strings"
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
// The key is "METHOD path" to support different handlers for GET vs PUT on the same path.
func (hm *HandlerManager) RegisterHandler(method string, path string, handler func(*core.RequestEvent) error) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.handlers[method+" "+path] = handler
}

// GetHandler retrieves the handler for a specific route.
func (hm *HandlerManager) GetHandler(method string, path string) (func(*core.RequestEvent) error, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	handler, exists := hm.handlers[method+" "+path]
	return handler, exists
}


func PlaceholderHandler(e *core.RequestEvent) error {
	path := e.Request.URL.Path
	if strings.HasPrefix(path, APIBasePath) {
		path = strings.TrimPrefix(path, APIBasePath)
		if path == "" {
			path = "/"
		}
	}
	handler, exists := LudusPluginHandlerManager.GetHandler(e.Request.Method, path)
	if exists {
		return handler(e)
	}
	return JSONError(e, http.StatusNotFound, "This endpoint is implemented in a plugin that is not loaded")
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
		// Enterprise plugin routes
		PocketBaseRoute{
			Name:        "GetWireGuard",
			Method:      http.MethodGet,
			Pattern:     "/range/wireguard",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "GetWireGuardConfigForOctet",
			Method:      http.MethodPost,
			Pattern:     "/range/wireguard",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "SetupKMS",
			Method:      http.MethodPost,
			Pattern:     "/kms/install",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "CheckKMSStatus",
			Method:      http.MethodGet,
			Pattern:     "/kms/status",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "LicenseWindows",
			Method:      http.MethodPost,
			Pattern:     "/kms/license",
			HandlerFunc: PlaceholderHandler,
		},
		// Quota Management routes (Enterprise)
		PocketBaseRoute{
			Name:        "GetQuotaStatus",
			Method:      http.MethodGet,
			Pattern:     "/user/quotas",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "GetAllQuotaStatus",
			Method:      http.MethodGet,
			Pattern:     "/user/quotas/all",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "GetQuotaDefaults",
			Method:      http.MethodGet,
			Pattern:     "/user/quotas/defaults",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "GetGroupQuotas",
			Method:      http.MethodGet,
			Pattern:     "/groups/quotas",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "SetUserQuota",
			Method:      http.MethodPut,
			Pattern:     "/user/quotas",
			HandlerFunc: PlaceholderHandler,
		},
		PocketBaseRoute{
			Name:        "SetGroupQuota",
			Method:      http.MethodPut,
			Pattern:     "/groups/quotas",
			HandlerFunc: PlaceholderHandler,
		},
	}

	RegisterRoutesWithPocketBase(se, pluginRoutes)
}

func RegisterPluginActualRoutes(routes PocketBaseRoutes) {
	for _, route := range routes {
		logger.Debug(fmt.Sprintf("Registering actual route for plugin: %s %s", route.Method, route.Pattern))
		LudusPluginHandlerManager.RegisterHandler(route.Method, route.Pattern, route.HandlerFunc)
	}
}
