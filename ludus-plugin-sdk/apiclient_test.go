package ludus_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ludus "gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestAPIClientRejectsUnsafeEndpoints(t *testing.T) {
	valid := strings.Repeat("ab", sha256.Size)
	for _, api := range []protocol.APIEndpoint{
		{URL: "https://ludus.example.com:8080", CertificateSHA256: valid},
		{URL: "http://10.2.99.1:8080"},
		{URL: "ftp://127.0.0.1:8080"},
		{URL: ""},
		{URL: "https://127.0.0.1:8080"},
		{URL: "https://127.0.0.1:8080", CertificateSHA256: "abcd"},
		{URL: "https://127.0.0.1:8080", CertificateSHA256: strings.Repeat("zz", sha256.Size)},
	} {
		if _, err := ludus.APIClient(api); err == nil {
			t.Errorf("APIClient(%+v) succeeded", api)
		}
	}
	for _, api := range []protocol.APIEndpoint{
		{URL: "http://localhost:8080"},
		{URL: "http://[::1]:8080"},
		{URL: "https://127.0.0.1:8080", CertificateSHA256: valid},
	} {
		if _, err := ludus.APIClient(api); err != nil {
			t.Errorf("APIClient(%+v) error = %v", api, err)
		}
	}
}

func TestAPIClientTrustsPinnedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{"result":"ok"}`)
	}))
	defer server.Close()

	fingerprint := sha256.Sum256(server.Certificate().Raw)
	client, err := ludus.APIClient(protocol.APIEndpoint{URL: server.URL, CertificateSHA256: hex.EncodeToString(fingerprint[:])})
	if err != nil {
		t.Fatalf("APIClient() error = %v", err)
	}
	response, err := client.Get(server.URL + "/api/v2/range")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != `{"result":"ok"}` {
		t.Fatalf("GET = %d %q", response.StatusCode, body)
	}
}

func TestAPIClientRejectsWrongCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{"result":"ok"}`)
	}))
	defer server.Close()

	client, err := ludus.APIClient(protocol.APIEndpoint{URL: server.URL, CertificateSHA256: strings.Repeat("00", sha256.Size)})
	if err != nil {
		t.Fatalf("APIClient() error = %v", err)
	}
	_, err = client.Get(server.URL + "/api/v2/range")
	if err == nil || !strings.Contains(err.Error(), "certificate fingerprint mismatch") {
		t.Fatalf("GET error = %v, want certificate fingerprint mismatch", err)
	}
}
