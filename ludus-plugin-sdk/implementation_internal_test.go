package ludus

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	hcplugin "github.com/hashicorp/go-plugin"
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

// The application is served over the real go-plugin broker and yamux stream:
// only requests carrying the host's AppContext reach it, with that session's
// identity, and the private header never reaches the handler.
func TestAppServedOverBrokerStreamWithSessionContext(t *testing.T) {
	p := &Plugin{ID: "app-test", Name: "App", Version: "1", App: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := CallFrom(r)
		if r.Header.Get(protocol.AppContextHeader) != "" {
			t.Error("app context header reached the handler")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"path": r.URL.Path, "user": c.Auth.UserID, "session": c.AppSession, "base": c.AppBasePath, "call": string(c.ID)})
	})}
	client, _ := hcplugin.TestPluginRPCConn(t, protocol.PluginMap(NewImplementation(p)), nil)
	t.Cleanup(func() { client.Close() })
	raw, err := client.Dispense(protocol.PluginName)
	if err != nil {
		t.Fatal(err)
	}
	rpcClient := raw.(*protocol.RPCClient)
	response, err := rpcClient.Initialize(protocol.InitializeRequest{Call: "init"})
	if err != nil || response.AppStream == 0 {
		t.Fatalf("Initialize = %+v, %v; want an app stream", response, err)
	}
	session, err := rpcClient.DialApp(response.AppStream)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	httpClient := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return session.Open() }}}
	get := func(header string) (int, string) {
		request, _ := http.NewRequest(http.MethodGet, "http://app/terminal", nil)
		if header != "" {
			request.Header.Set(protocol.AppContextHeader, header)
		}
		result, err := httpClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer result.Body.Close()
		body, _ := io.ReadAll(result.Body)
		return result.StatusCode, string(body)
	}
	if status, _ := get(""); status != http.StatusForbidden {
		t.Fatalf("request without app context = %d, want 403", status)
	}
	encoded, _ := protocol.EncodeAppContext(protocol.AppContext{Session: "s1", Call: "session-call", BasePath: "/plugin-apps/r1/s1/", Auth: protocol.Principal{UserID: "ALICE"}})
	status, body := get(encoded)
	var seen map[string]string
	if status != http.StatusOK || json.Unmarshal([]byte(body), &seen) != nil {
		t.Fatalf("app request = %d %q", status, body)
	}
	if seen["path"] != "/terminal" || seen["user"] != "ALICE" || seen["session"] != "s1" || seen["base"] != "/plugin-apps/r1/s1/" || seen["call"] != "session-call" {
		t.Fatalf("app handler saw %v", seen)
	}
}

func TestAppDeclarationReachesManifest(t *testing.T) {
	data, err := ManifestJSON(&Plugin{ID: "app-test", Name: "App", Version: "1", App: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	m, err := protocol.ParseManifest(data)
	if err != nil || !m.App {
		t.Fatalf("manifest app = %v (%v), want true", m.App, err)
	}
	if _, err := ManifestJSON(&Plugin{ID: "app-test", Name: "App", Version: "1", AppSessionClosed: func(string) {}}); err == nil {
		t.Fatal("AppSessionClosed without App was accepted")
	}
}
