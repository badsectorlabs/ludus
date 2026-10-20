package ludus

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// JSON writes v as a JSON response with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		Error(w, http.StatusInternalServerError, "encode response: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// Error writes {"error": message}, the shape Ludus API errors use.
func Error(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"error": message})
}

// Result writes {"result": message}, the shape Ludus API results use.
func Result(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"result": message})
}

// HostError writes the error response for a failed Host call: Forbidden → 403,
// NotFound → 404, Invalid → 400, Expired and Unavailable → 503, anything else → 500.
func HostError(w http.ResponseWriter, err error) {
	if err == nil {
		Error(w, http.StatusInternalServerError, "unknown host error")
		return
	}
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrExpired), errors.Is(err, ErrUnavailable):
		status = http.StatusServiceUnavailable
	}
	message := err.Error()
	var hostErr *protocol.HostError
	if errors.As(err, &hostErr) && hostErr.Message != "" {
		message = hostErr.Message
	}
	Error(w, status, message)
}

// DecodeJSON decodes the JSON request body into v.
func DecodeJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return fmt.Errorf("decode JSON request body: %w", err)
	}
	return nil
}
