// Phase 2b: Proxmox API mocking via gock — intercepts HTTP calls so we can
// test functions that talk to Proxmox without a real instance.
package ludusapi

import (
	"net/http"
	"testing"

	goproxmox "github.com/luthermonson/go-proxmox"
	"gopkg.in/h2non/gock.v1"
)

const testProxmoxBaseURL = "https://proxmox.test:8006/api2/json"

// setupProxmoxMock creates a go-proxmox client with gock's intercepting transport,
// then pre-caches it in app.Store() so getRootGoProxmoxClient() returns it
// without needing real DB credentials.
func setupProxmoxMock(t *testing.T) {
	t.Helper()

	mockHTTPClient := &http.Client{Transport: gock.DefaultTransport}
	client := goproxmox.NewClient(testProxmoxBaseURL,
		goproxmox.WithHTTPClient(mockHTTPClient),
		goproxmox.WithAPIToken("root@pam!ludus", "fake-token-value"),
	)
	app.Store().Set("proxmoxClient_root", client)

	t.Cleanup(func() {
		gock.Off()
		app.Store().Remove("proxmoxClient_root")
	})
}

func TestPoolACLAction_GrantAndError(t *testing.T) {
	testApp := setupTestApp(t)
	swapGlobalApp(t, testApp)
	setupProxmoxMock(t)

	// Success: PUT /access/acl returns 200
	gock.New(testProxmoxBaseURL).
		Put("/access/acl").
		Reply(200).
		JSON(map[string]any{"data": nil})

	if err := poolACLAction("testuser", "pam", "TESTUSER", false); err != nil {
		t.Fatalf("grant should succeed, got: %v", err)
	}

	// Error: PUT /access/acl returns 500
	gock.New(testProxmoxBaseURL).
		Put("/access/acl").
		Reply(500)

	if err := poolACLAction("testuser", "pam", "TESTUSER", false); err == nil {
		t.Fatal("should return error on Proxmox 500")
	}
}

func TestRemovePool_SuccessAndNotFound(t *testing.T) {
	testApp := setupTestApp(t)
	swapGlobalApp(t, testApp)
	setupProxmoxMock(t)

	// Success: GET pool returns data, DELETE succeeds
	gock.New(testProxmoxBaseURL).
		Get("/pools/").
		MatchParam("poolid", "TESTPOOL").
		Reply(200).
		JSON(map[string]any{
			"data": []map[string]any{
				{"poolid": "TESTPOOL", "comment": "Created by Ludus", "members": []any{}},
			},
		})
	gock.New(testProxmoxBaseURL).
		Delete("/pools/TESTPOOL").
		Reply(200).
		JSON(map[string]any{"data": nil})

	if err := removePool("TESTPOOL"); err != nil {
		t.Fatalf("removePool should succeed, got: %v", err)
	}

	// Not found: Proxmox returns 500 for non-existent pools.
	// NOTE: This documents a potential bug — removePool checks for
	// "NOPOOL' does not exist" in the error, but go-proxmox's 500 handler
	// returns only "500 Internal Server Error" (discards the body).
	// The "does not exist" check can never match.
	gock.New(testProxmoxBaseURL).
		Get("/pools/").
		MatchParam("poolid", "NOPOOL").
		Reply(500)

	if err := removePool("NOPOOL"); err == nil {
		t.Fatal("removePool returns error for 500 — does-not-exist check cannot match")
	}
}
