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

func TestGuacamoleEndpointAndBasePathPassThroughSDK(t *testing.T) {
	initialized := false
	p := &Plugin{Init: func(*Call, Env) error { initialized = true; return nil }, WebApplication: func() *protocol.WebApplication {
		if !initialized {
			t.Fatal("endpoint requested before initialization")
		}
		return &protocol.WebApplication{URL: "http://127.0.0.1:12345", Token: strings.Repeat("a", 32)}
	}, Routes: []Route{{Name: "launch", Method: "POST", Pattern: "/launch", Handler: func(w http.ResponseWriter, r *http.Request) {
		if CallFrom(r).GuacAppBasePath != "/guacamole/resource/" {
			t.Fatal("lost host-assigned application path")
		}
		w.WriteHeader(204)
	}}}}
	impl := NewImplementation(p)
	reply, err := impl.Initialize(protocol.InitializeRequest{}, nil)
	if err != nil || reply.WebApplication == nil || reply.WebApplication.URL != "http://127.0.0.1:12345" {
		t.Fatalf("Initialize: %v %v", reply, err)
	}
	response, err := impl.Handle(protocol.Request{Route: "launch", Method: "POST", Path: "/launch", GuacAppBasePath: "/guacamole/resource/"})
	if err != nil || response.Status != 204 {
		t.Fatalf("Handle: %v %v", response, err)
	}
}
