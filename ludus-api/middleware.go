package ludusapi

import (
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
		return JSONError(e, http.StatusUnauthorized, fmt.Sprintf("user %s not found", userID))
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
				return JSONError(e, http.StatusUnauthorized, fmt.Sprintf("user %s not found", requestedUserID))
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

	// Expand the user record to load relationships (works for both API key and JWT/session auth)
	errs := e.App.ExpandRecord(e.Auth, []string{"ranges", "groups"}, nil)
	if len(errs) > 0 {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error expanding user record: %v", errs))
	}

	// Load and store the user's ranges as proxy records
	var userRanges []*models.Range
	userRangesRecords := e.Auth.ExpandedAll("ranges")
	for _, r := range userRangesRecords {
		thisRange := &models.Range{}
		thisRange.SetProxyRecord(r)
		userRanges = append(userRanges, thisRange)
	}
	// Save the user's ranges to the context
	e.Set("ranges", userRanges)

	// Create a User proxy record and save it to the context
	user := &models.User{}
	user.SetProxyRecord(e.Auth)
	e.Set("user", user)
	logger.Debug(fmt.Sprintf("Set user record to: %s", user.UserId()))

	// Check if the user is requesting a specific range
	rangeID := e.Request.URL.Query().Get("rangeID")
	if rangeID != "" {
		// Check if the user has access to this range by looking up the rangeID in the array of ranges in the user record
		hasAccess := false
		for _, r := range userRanges {
			if r.RangeId() == rangeID {
				hasAccess = true
				break
			}
		}
		if !hasAccess {
			return JSONError(e, http.StatusForbidden, fmt.Sprintf("User %s does not have access to range %s", e.Auth.GetString("userID"), rangeID))
		}
	} else {
		rangeID = e.Auth.GetString("defaultRangeID")
		// Allow ROOT to bypass the default range check
		if rangeID == "" && e.Auth.GetString("userID") != "ROOT" {
			return JSONError(e, http.StatusNotFound, "User has no default range and no rangeID was provided in the request")
		} else if rangeID == "" && e.Auth.GetString("userID") == "ROOT" {
			rangeID = "ROOT"
			e.Set("range", &models.Range{})
			return e.Next()
		}
	}

	// Get the range object to use for the remainder of the request
	rawRangeRecord, err := e.App.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Range %s not found", rangeID))
	}
	rangeRecord := &models.Range{}
	rangeRecord.SetProxyRecord(rawRangeRecord)
	e.Set("range", rangeRecord)

	return e.Next()
}

// This function makes sure the request is to a user endpoint if the server is running as root (i.e. :8081)
func limitRootEndpoints(e *core.RequestEvent) error {
	logger.Debug(fmt.Sprintf("Request URL: %s", e.Request.URL.Path))
	if os.Geteuid() == 0 &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user") &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/antisandbox/") &&
		!strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/ranges/create") &&
		!(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/range") && e.Request.Method == http.MethodDelete) {
		return JSONError(e, http.StatusInternalServerError, "The :8081 endpoint can only be used for user, range creation/deletion, and anti-sandbox actions. Use the :8080 endpoint for all other actions.")
	} else if os.Geteuid() != 0 &&
		(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/user") ||
			strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/antisandbox/") ||
			strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/ranges/create") ||
			(strings.HasPrefix(e.Request.URL.Path, APIBasePath+"/range") && e.Request.Method == http.MethodDelete)) {
		// Reverse proxy to the admin API
		adminProxy.ServeHTTP(e.Response, e.Request)

		// Abort the middleware chain to prevent the user-facing server
		// from also handling the request and writing a second response.
		return nil
	}

	return e.Next()

}
