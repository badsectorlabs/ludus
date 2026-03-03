package ludusapi

import (
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// GetLicense returns the current license information
func GetLicense(e *core.RequestEvent) error {
	response := map[string]interface{}{
		"entitlements": server.Entitlements,
		"licensed_to":  server.LicenseName,
		"active":       server.LicenseValid,
		"message":      server.LicenseMessage,
	}

	if server.LicenseExpiry != nil {
		response["expires_at"] = server.LicenseExpiry.Format(time.RFC3339)
	} else {
		response["expires_at"] = nil
	}

	return e.JSON(http.StatusOK, response)
}
