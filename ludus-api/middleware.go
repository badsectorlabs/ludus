package ludusapi

import (
	"database/sql"
	"fmt"
	"ludusapi/models"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
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
		return JSONError(e, http.StatusUnauthorized, "Malformed API Key provided")
	}

	userID := parts[0]

	record, err := e.App.FindFirstRecordByData("users", "userID", userID)
	if err != nil {
		return JSONError(e, http.StatusUnauthorized, fmt.Sprintf("User %s from API key not found", userID))
	}

	storedHash := record.GetString("hashedAPIKey")
	if !CheckHash(apiKey, storedHash) {
		return JSONError(e, http.StatusUnauthorized, "Invalid API key")
	}

	// Check if the request path has a ?userID= parameter
	requestedUserID := e.Request.URL.Query().Get("userID")
	if requestedUserID != "" && requestedUserID != userID {
		// If the user specified in the API key is an admin, impersonate the user specified in the ?userID= parameter
		if record.Get("isAdmin").(bool) {
			record, err = e.App.FindFirstRecordByData("users", "userID", requestedUserID)
			if err != nil {
				return JSONError(e, http.StatusBadRequest, fmt.Sprintf("User %s from query parameter not found", requestedUserID))
			}
		} else {
			return JSONError(e, http.StatusUnauthorized, "You are not an admin and cannot impersonate other users")
		}
	}

	// Set this request as authenticated
	// Note: The user record will be expanded in userAndRangesLookupMiddleware
	e.Auth = record

	return e.Next()
}

// Updates the date_last_active column in the database for the user making the API call
// Also logs the API action to a file
func updateLastActiveTimeAndLog(e *core.RequestEvent) error {
	// Prevent locking issues with proxied requests, don't log the last active time for ludus-admin requests
	// as they are already logged by the regular ludus service proxy
	// Also skip logging for requests to the PocketBase web UI or unauthenticated requests
	if os.Geteuid() == 0 || e.Auth == nil || e.Get("user") == nil || e.Auth.IsSuperuser() {
		return e.Next()
	}

	user := e.Get("user").(*models.User)
	now, err := types.ParseDateTime(time.Now().UTC().Round(time.Duration(time.Millisecond)))
	if err != nil {
		logger.Error(fmt.Sprintf("Error parsing now: %v", err))
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error parsing now: %v", err))
	}
	user.SetLastActive(now)
	err = e.App.Save(user)
	if err != nil {
		logger.Error(fmt.Sprintf("Error saving user: %v", err))
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error saving user: %v", err))
	}
	return e.Next()
}

func userAndRangesLookupMiddleware(e *core.RequestEvent) error {
	// If the user is not authenticated, pass the request to the next handler, don't try to populate the user and ranges context
	if e.Auth == nil || e.Auth.IsSuperuser() {
		return e.Next()
	}

	// Create a User proxy record and save it to the context
	user := &models.User{}
	user.SetProxyRecord(e.Auth)
	e.App.ExpandRecord(e.Auth, []string{"ranges", "groups"}, nil)
	e.Set("user", user)

	// Check if the user is requesting a specific range
	rangeID := e.Request.URL.Query().Get("rangeID")
	if rangeID != "" {
		rangeNumber, err := GetRangeNumberFromRangeID(rangeID)
		if err != nil {
			return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found: %v", rangeID, err))
		}
		if !HasRangeAccess(e, user.UserId(), rangeNumber) && !e.Auth.GetBool("isAdmin") {
			return JSONError(e, http.StatusForbidden, fmt.Sprintf("User %s does not have access to range %s", e.Auth.GetString("userID"), rangeID))
		}
	} else {
		rangeID = e.Auth.GetString("defaultRangeID")
		// Allow ROOT to bypass the default range check
		if rangeID == "" && e.Auth.GetString("userID") != "ROOT" {
			return JSONError(e, http.StatusNotFound, "User has no default range and no rangeID was provided in the request")
		} else if rangeID == "" && e.Auth.GetString("userID") == "ROOT" {
			rangeCollection, err := e.App.FindCollectionByNameOrId("ranges")
			if err != nil {
				return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding ranges collection: %v", err))
			}
			dummyRangeRecord := core.NewRecord(rangeCollection)
			dummyRange := &models.Range{}
			dummyRange.SetProxyRecord(dummyRangeRecord)
			dummyRange.SetRangeId("ROOT")
			dummyRange.SetName("ROOT")
			dummyRange.SetTestingEnabled(false)
			dummyRange.SetRangeNumber(1)
			e.Set("range", dummyRange)
			return e.Next()
		}
	}

	// Get the range object to use for the remainder of the request
	rawRangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil && err != sql.ErrNoRows {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding range: %v", err))
	} else if err == sql.ErrNoRows {
		logger.Debug(fmt.Sprintf("Range %s not found during middleware lookup, continuing with no range", rangeID))
	} else {
		rangeRecord := &models.Range{}
		rangeRecord.SetProxyRecord(rawRangeRecord)
		e.Set("range", rangeRecord)
	}

	return e.Next()
}

// This function makes sure the request is to a user endpoint if the server is running as root (i.e. :8081)
func limitRootEndpoints(e *core.RequestEvent) error {
	logger.Debug(fmt.Sprintf("Request URL: %s", e.Request.URL.Path))
	if os.Geteuid() == 0 &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user") &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/antisandbox/") &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/ranges/create") &&
		!(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/range") && e.Request.Method == http.MethodDelete) &&
		!(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user/credentials") && e.Request.Method == http.MethodPost) {
		return JSONError(e, http.StatusInternalServerError, "The :8081 endpoint can only be used for user, range creation/deletion, and anti-sandbox actions. Use the :8080 endpoint for all other actions.")
	} else if os.Geteuid() != 0 &&
		(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user") ||
			strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/antisandbox/") ||
			strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/ranges/create") ||
			(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/range") && e.Request.Method == http.MethodDelete) ||
			(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user/credentials") && e.Request.Method == http.MethodPost)) {
		// Reverse proxy to the admin API
		adminProxy.ServeHTTP(e.Response, e.Request)

		// Abort the middleware chain to prevent the user-facing server
		// from also handling the request and writing a second response.
		return nil
	}

	return e.Next()

}

func requireAuth(e *core.RequestEvent) error {
	// Check auth for all endpoints in our base path
	if e.Auth == nil && strings.HasPrefix(e.Request.URL.Path, APIBasePath) {
		return JSONError(e, http.StatusUnauthorized, "Authentication failed. Provide a valid API key in the X-API-KEY header or a valid JWT token in the Authorization header.")
	}
	return e.Next()
}

func redirectBaseURLToUI(e *core.RequestEvent) error {
	if e.Request.URL.Path == "/" {
		return e.Redirect(http.StatusTemporaryRedirect, "/ui")
	}
	return e.Next()
}

func restrictPocketBaseEndpoints(e *core.RequestEvent) error {
	if strings.HasPrefix(e.Request.URL.Path, "/_") && os.Getenv("LUDUS_ENABLE_SUPERADMIN") != "ill-be-careful" {
		return JSONError(e, http.StatusForbidden, "Superadmin access is disabled. Enable it by setting the LUDUS_ENABLE_SUPERADMIN environment variable to 'ill-be-careful'.")
	}
	return e.Next()
}
