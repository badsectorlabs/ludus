package ludus

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestHandleRejectsOversizedResponseInsteadOfTruncating(t *testing.T) {
	var writeErr error
	p := &Plugin{Routes: []Route{{Name: "big", Method: http.MethodGet, Pattern: "/big", Handler: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Partial", "yes")
		_, _ = w.Write([]byte(strings.Repeat("a", 10)))
		_, writeErr = w.Write([]byte(strings.Repeat("b", 10)))
	}}}}
	impl := NewImplementation(p).(*implementation)
	if impl.maxBody != protocol.MaxBodyBytes {
		t.Fatalf("default response limit = %d, want protocol.MaxBodyBytes", impl.maxBody)
	}
	impl.maxBody = 16

	response, err := impl.Handle(protocol.Request{Route: "big", Method: http.MethodGet, Path: "/big"})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if writeErr == nil {
		t.Fatal("write past the limit succeeded")
	}
	if response.Status != http.StatusInternalServerError || response.Header["X-Partial"] != nil {
		t.Fatalf("response = %d %v, want 500 without handler headers", response.Status, response.Header)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body, &body); err != nil || !strings.Contains(body["error"], "exceeds 16 bytes") {
		t.Fatalf("body = %q (%v), want overflow error", response.Body, err)
	}
}
