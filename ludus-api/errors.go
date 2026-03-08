package ludusapi

import (
	"github.com/pocketbase/pocketbase/core"
)

// JSONError returns a JSON response with the given error code and message
func JSONError(e *core.RequestEvent, errorCode int, message string) error {
	return e.JSON(errorCode, map[string]any{"error": message})
}

// JSONResult returns a JSON response with the given success code and message as the value of the result key
func JSONResult(e *core.RequestEvent, successCode int, message string) error {
	return e.JSON(successCode, map[string]any{"result": message})
}
