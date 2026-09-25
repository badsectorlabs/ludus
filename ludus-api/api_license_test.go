package ludusapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/keygen-sh/keygen-go/v3"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

type invalidLicenseTransport struct{}

func (invalidLicenseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": {"application/vnd.api+json"}},
		Body:       io.NopCloser(strings.NewReader(`{"errors":[{"title":"Unauthorized","code":"LICENSE_INVALID","source":{"pointer":"/headers/authorization"}}]}`)),
		Request:    request,
	}, nil
}

func TestGetLicenseReportsInvalidKey(t *testing.T) {
	if FileExists(ludusInstallPath + "/license.lic") {
		t.Skip("requires online license checking without an installed license.lic")
	}

	originalServer := server
	originalClient, originalPublicKey := keygen.HTTPClient, keygen.PublicKey
	originalAccount, originalProduct := keygen.Account, keygen.Product
	originalKey, originalURL, originalAgent := keygen.LicenseKey, keygen.APIURL, keygen.UserAgent
	t.Cleanup(func() {
		server = originalServer
		keygen.HTTPClient, keygen.PublicKey = originalClient, originalPublicKey
		keygen.Account, keygen.Product = originalAccount, originalProduct
		keygen.LicenseKey, keygen.APIURL, keygen.UserAgent = originalKey, originalURL, originalAgent
	})
	keygen.HTTPClient = &http.Client{Transport: invalidLicenseTransport{}}
	keygen.PublicKey = ""
	server = &Server{
		LicenseKey:     "invalid-key",
		LicenseValid:   true,
		LicenseMessage: "License active",
	}

	server.checkLicense()

	response := httptest.NewRecorder()
	event := &core.RequestEvent{Event: router.Event{
		Request:  httptest.NewRequest(http.MethodGet, "/license", nil),
		Response: response,
	}}
	if err := GetLicense(event); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK {
		t.Fatalf("GET /license returned HTTP %d: %s", response.Code, response.Body.String())
	}
	var license struct {
		Active  bool   `json:"active"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &license); err != nil {
		t.Fatal(err)
	}
	if license.Active || license.Message != "license key is invalid" {
		t.Fatalf("GET /license retained stale license state: %s", response.Body.String())
	}
}
