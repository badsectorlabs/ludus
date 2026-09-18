package rest

import (
	"ludus/logger"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-resty/resty/v2"
)

func TestProcessRESTResultRejectsPendingPasswordRotation(t *testing.T) {
	logger.InitLogger(false)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"Password rotation is pending verification on Proxmox"}`))
	}))
	defer server.Close()

	response, err := resty.New().R().Get(server.URL)
	if err != nil {
		t.Fatalf("request test server: %v", err)
	}
	result, success := processRESTResult(response, nil)
	if success {
		t.Fatal("HTTP 503 was reported as success")
	}
	if result != nil {
		t.Fatalf("HTTP 503 returned a result: %q", result)
	}
}
