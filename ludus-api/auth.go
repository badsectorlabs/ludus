package ludusapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func APIKeyAuthenticationMiddleware(e *core.RequestEvent) error {
	apiKey := e.Request.Header.Get("X-API-KEY")

	// If no API key is present, pass the request to the next handler
	// without interfering. This allows standard JWT auth to proceed.
	if apiKey == "" {
		return e.Next()
	}

	parts := strings.Split(apiKey, ".")
	if len(parts) != 2 {
		// return e.UnauthorizedError("Malformed API Key provided", nil)
		return JSONError(e, http.StatusUnauthorized, "Malformed API Key provided")
	}

	userID := parts[0]

	record, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		// return e.UnauthorizedError("Authentication failed", map[string]validation.Error{
		// 	"error_data": validation.NewError("user_not_found", fmt.Sprintf("user %s not found", userID)),
		// })
		return JSONError(e, http.StatusUnauthorized, fmt.Sprintf("user %s not found", userID))
	}

	storedHash := record.GetString("hashedAPIKey")
	logger.Debug(fmt.Sprintf("storedHash: %s", storedHash))
	if !CheckHash(apiKey, storedHash) {
		// return e.UnauthorizedError("Authentication failed", map[string]validation.Error{
		// 	"error_data": validation.NewError("invalid_api_key", "Invalid API key"),
		// })
		return JSONError(e, http.StatusUnauthorized, "Invalid API key")
	}

	// Check if the request path has a ?userID= parameter
	requestedUserID := e.Request.URL.Query().Get("userID")
	if requestedUserID != "" && requestedUserID != userID {
		// If the user specified in the API key is an admin, impersonate the user specified in the ?userID= parameter
		if record.Get("isAdmin").(bool) {
			record, err = e.App.FindFirstRecordByData("users", "userID", requestedUserID)
			if err != nil {
				// return e.UnauthorizedError("Authentication failed", map[string]validation.Error{
				// 	"error_data": validation.NewError("user_not_found", fmt.Sprintf("user %s not found", requestedUserID)),
				// })
				return JSONError(e, http.StatusUnauthorized, fmt.Sprintf("user %s not found", requestedUserID))
			}
		} else {
			// return e.UnauthorizedError("Authentication failed", map[string]validation.Error{
			// 	"error_data": validation.NewError("not_admin", "You are not an admin and cannot impersonate other users"),
			// })
			return JSONError(e, http.StatusUnauthorized, "You are not an admin and cannot impersonate other users")
		}
	}

	// Set this request as authenticated
	e.Auth = record

	return e.Next()
}
