package ludusapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPocketBaseClientReadsAndUpdatesPinnedLoopbackAPI(t *testing.T) {
	var updatedTimeout string
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if token := request.Header.Get("Authorization"); token != "plugin-token" {
			t.Errorf("Authorization = %q, want plugin-token", token)
			http.Error(response, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/collections/users/records":
			fmt.Fprint(response, `{
				"page":1,
				"perPage":500,
				"totalItems":1,
				"totalPages":1,
				"items":[{
					"id":"user-record",
					"collectionName":"users",
					"userID":"alice",
					"expand":{"ranges":[{
						"id":"range-record",
						"collectionName":"ranges",
						"rangeID":"7"
					}]}
				}]
			}`)
		case request.Method == http.MethodPatch && request.URL.Path == "/api/collections/ranges/records/range-record":
			var fields map[string]any
			if err := json.NewDecoder(request.Body).Decode(&fields); err != nil {
				t.Errorf("decode update: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			updatedTimeout, _ = fields["inactivityShutdownTimeout"].(string)
			fmt.Fprintf(response, `{
				"id":"range-record",
				"collectionName":"ranges",
				"rangeID":"7",
				"inactivityShutdownTimeout":%q
			}`, updatedTimeout)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	fingerprint := sha256.Sum256(server.Certificate().Raw)
	client, err := NewPocketBaseClient(PocketBaseConnection{
		URL:               server.URL,
		Token:             "plugin-token",
		CertificateSHA256: hex.EncodeToString(fingerprint[:]),
	})
	if err != nil {
		t.Fatalf("NewPocketBaseClient() error = %v", err)
	}
	defer client.Close()

	records, err := client.ListRecords(context.Background(), "users", "", "ranges")
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(records) != 1 || records[0].GetString("userID") != "alice" {
		t.Fatalf("ListRecords() = %#v", records)
	}
	expandedRange := records[0].ExpandedOne("ranges")
	if expandedRange == nil || expandedRange.Collection().Name != "ranges" || expandedRange.GetString("rangeID") != "7" {
		t.Fatalf("expanded range = %#v", expandedRange)
	}

	updated, err := client.UpdateRecord(context.Background(), "ranges", "range-record", map[string]any{
		"inactivityShutdownTimeout": "45m",
	})
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if updatedTimeout != "45m" || updated.GetString("inactivityShutdownTimeout") != "45m" {
		t.Fatalf("updated timeout = %q, record = %#v", updatedTimeout, updated)
	}
}

func TestPocketBaseClientRejectsWrongCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fmt.Fprint(response, `{"page":1,"totalPages":0,"items":[]}`)
	}))
	defer server.Close()

	client, err := NewPocketBaseClient(PocketBaseConnection{
		URL:               server.URL,
		Token:             "plugin-token",
		CertificateSHA256: strings.Repeat("00", sha256.Size),
	})
	if err != nil {
		t.Fatalf("NewPocketBaseClient() error = %v", err)
	}
	defer client.Close()

	_, err = client.ListRecords(context.Background(), "ranges", "")
	if err == nil || !strings.Contains(err.Error(), "certificate fingerprint mismatch") {
		t.Fatalf("ListRecords() error = %v, want certificate fingerprint mismatch", err)
	}
}

func TestPocketBaseClientRefreshesExpiringToken(t *testing.T) {
	initialToken := testPluginToken(t, time.Now().Add(time.Minute))
	refreshCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/collections/_superusers/auth-refresh":
			if token := request.Header.Get("Authorization"); token != initialToken {
				t.Errorf("refresh Authorization = %q, want initial token", token)
			}
			refreshCalls++
			fmt.Fprint(response, `{"token":"refreshed-token","record":{"id":"plugin"}}`)
		case "/api/collections/ranges/records":
			if token := request.Header.Get("Authorization"); token != "refreshed-token" {
				t.Errorf("list Authorization = %q, want refreshed-token", token)
			}
			fmt.Fprint(response, `{"page":1,"perPage":500,"totalItems":0,"totalPages":0,"items":[]}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client, err := NewPocketBaseClient(PocketBaseConnection{
		URL:   server.URL,
		Token: initialToken,
	})
	if err != nil {
		t.Fatalf("NewPocketBaseClient() error = %v", err)
	}
	defer client.Close()

	if _, err := client.ListRecords(context.Background(), "ranges", ""); err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if refreshCalls != 1 {
		t.Fatalf("auth refresh calls = %d, want 1", refreshCalls)
	}
}

func testPluginToken(t *testing.T, expires time.Time) string {
	t.Helper()
	claims, err := json.Marshal(map[string]int64{"exp": expires.Unix()})
	if err != nil {
		t.Fatalf("marshal token claims: %v", err)
	}
	return "header." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func TestPocketBaseClientRejectsNonLoopbackURL(t *testing.T) {
	_, err := NewPocketBaseClient(PocketBaseConnection{
		URL:   "https://pocketbase.example.com",
		Token: "plugin-token",
	})
	if err == nil || !strings.Contains(err.Error(), "local host") {
		t.Fatalf("NewPocketBaseClient() error = %v, want local host rejection", err)
	}
}

func TestPluginRangeAccessIncludesDirectAndGroupUsers(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/collections/ranges/records":
			if request.URL.Query().Get("filter") != "rangeNumber = 7" {
				http.Error(response, "unexpected range", http.StatusBadRequest)
				return
			}
			fmt.Fprint(response, `{"items":[{"id":"range-seven","rangeNumber":7,"rangeID":"LAB"}],"totalPages":1}`)
		case "/api/collections/users/records":
			if request.URL.Query().Get("filter") != `ranges.id ?= "range-seven"` {
				http.Error(response, "unexpected range access filter", http.StatusBadRequest)
				return
			}
			fmt.Fprint(response, `{"items":[{"id":"direct","userID":"ZOE","userNumber":9,"name":"Zoe"}],"totalPages":1}`)
		case "/api/collections/groups/records":
			if request.URL.Query().Get("filter") != `ranges.id ?= "range-seven"` || request.URL.Query().Get("expand") != "members,managers" {
				http.Error(response, "missing group access context", http.StatusBadRequest)
				return
			}
			fmt.Fprint(response, `{"items":[{"id":"team","expand":{"members":[{"id":"member","userID":"ALICE","userNumber":2,"name":"Alice"}],"managers":[{"id":"manager","userID":"BOB","userNumber":3,"name":"Bob"}]}}],"totalPages":1}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer api.Close()

	client, err := NewPocketBaseClient(PocketBaseConnection{URL: api.URL, Token: "plugin-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	previousApp, previousClient := app, pluginPocketBase
	app, pluginPocketBase = nil, client
	t.Cleanup(func() {
		app, pluginPocketBase = previousApp, previousClient
	})

	users := GetRangeAccessibleUsers(7)
	if len(users) != 3 {
		t.Fatalf("accessible users = %+v, want direct user, group member, and manager", users)
	}
	for index, want := range []struct {
		id         string
		number     int
		name       string
		accessType string
	}{
		{"ALICE", 2, "Alice", "Group Member"},
		{"BOB", 3, "Bob", "Group Manager"},
		{"ZOE", 9, "Zoe", "Direct"},
	} {
		got := users[index]
		if got.UserID != want.id || got.UserNumber != want.number || got.Name != want.name || got.AccessType != want.accessType {
			t.Fatalf("accessible user %d = %+v, want %+v", index, got, want)
		}
	}
}

func TestPluginRootProxmoxClientReloadsCredentialsAfterShutdown(t *testing.T) {
	previousConfiguration := ServerConfiguration
	previousApp, previousClient := app, pluginPocketBase
	t.Cleanup(func() {
		ClosePluginRuntime()
		app, pluginPocketBase = previousApp, previousClient
		ServerConfiguration = previousConfiguration
	})
	app = nil
	ServerConfiguration.DatabaseEncryptionKey = "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48"

	var tokenSecret, encryptedSecret string
	api := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/collections/users/records":
			if request.URL.Query().Get("filter") != `userID = "ROOT"` {
				http.Error(response, "unexpected user", http.StatusBadRequest)
				return
			}
			fmt.Fprintf(response, `{"items":[{"id":"root-user","userID":"ROOT","proxmoxTokenID":"root@pam!rpc","proxmoxTokenSecret":%q}],"totalPages":1}`, encryptedSecret)
		case "/api2/json/version":
			if request.Header.Get("Authorization") != "PVEAPIToken=root@pam!rpc="+tokenSecret {
				http.Error(response, "invalid Proxmox credentials", http.StatusUnauthorized)
				return
			}
			fmt.Fprint(response, `{"data":{"version":"9.0","release":"9.0","repoid":"test"}}`)
		default:
			http.NotFound(response, request)
		}
	}))
	defer api.Close()
	ServerConfiguration.ProxmoxURL = api.URL

	for _, secret := range []string{"original-token", "rotated-token"} {
		tokenSecret = secret
		var err error
		encryptedSecret, err = EncryptStringForDatabase(secret)
		if err != nil {
			t.Fatal(err)
		}
		pluginPocketBase, err = NewPocketBaseClient(PocketBaseConnection{URL: api.URL, Token: "plugin-token"})
		if err != nil {
			t.Fatal(err)
		}
		client, err := GetRootGoProxmoxClient()
		if err != nil {
			t.Fatal(err)
		}
		version, err := client.Version(context.Background())
		if err != nil {
			t.Fatalf("Proxmox request with %s credentials: %v", secret, err)
		}
		if version.Version != "9.0" {
			t.Fatalf("Proxmox version = %q, want 9.0", version.Version)
		}
		ClosePluginRuntime()
	}
}
