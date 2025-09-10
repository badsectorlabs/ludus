package ludusapi

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// GetLicense returns the current license information
func GetLicense(c *gin.Context) {
	response := map[string]interface{}{
		"edition":     server.LicenseType,
		"licensed_to": server.LicenseName,
		"active":      server.LicenseValid,
		"message":     server.LicenseMessage,
	}

	if server.LicenseExpiry != nil {
		response["expires_at"] = server.LicenseExpiry.Format(time.RFC3339Nano)
	} else {
		response["expires_at"] = nil
	}

	c.JSON(http.StatusOK, response)
}
