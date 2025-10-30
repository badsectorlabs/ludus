package ludusapi

import "github.com/pocketbase/pocketbase/core"

// JSONError returns a JSON response with the given error code and message
func JSONError(e *core.RequestEvent, errorCode int, message string) error {
	return e.JSON(errorCode, map[string]any{"error": message})
}
