package ludusapi

import (
	"github.com/gorilla/websocket"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"ludusapi/pluginrpc"
	"testing"
)

func TestGuacamoleWebApplicationEndpointBoundary(t *testing.T) {
	for _, test := range []struct {
		id, url, token string
		valid          bool
	}{
		{"ludus-guacamole", "http://127.0.0.1:12345", "01234567890123456789012345678901", true},
		{"ludus-guacamole", "http://[::1]:12345", "01234567890123456789012345678901", true},
		{"other-plugin", "http://127.0.0.1:12345", "01234567890123456789012345678901", false},
		{"ludus-guacamole", "http://192.0.2.1:80", "01234567890123456789012345678901", false},
		{"ludus-guacamole", "http://localhost:80", "01234567890123456789012345678901", false},
		{"ludus-guacamole", "http://127.0.0.1:80/path", "01234567890123456789012345678901", false},
		{"ludus-guacamole", "http://user@127.0.0.1:80", "01234567890123456789012345678901", false},
		{"ludus-guacamole", "http://127.0.0.1:80", "short", false},
	} {
		p := &managedPlugin{metadata: pluginrpc.Metadata{ID: test.id}}
		err := configurePluginWebApplication(p, &pluginrpc.WebApplication{URL: test.url, Token: test.token})
		if (err == nil) != test.valid {
			t.Errorf("%s %s: %v", test.id, test.url, err)
		}
	}
}

// Exercise the registered route and PocketBase response wrapper, including a
// real HTTP upgrade. This catches proxy regressions that endpoint tests cannot.
func TestGuacamoleProxyPreservesWebSocketAndPrivateHeaders(t *testing.T) {
	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir(), HideStartBanner: true})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer pb.ResetBootstrapState()
	collection := core.NewBaseCollection("plugin_resources")
	collection.Fields.Add(&core.TextField{Name: "pluginID"}, &core.TextField{Name: "state"})
	if err := pb.Save(collection); err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(collection)
	record.Set("pluginID", "ludus-guacamole")
	record.Set("state", "approved")
	if err := pb.Save(record); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("a", 64)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-API-KEY") != "" || r.Header.Get("X-Ludus-App-Token") != secret || r.Header.Get("X-Ludus-App-Base") != "/guacamole/"+record.Id+"/" {
			t.Error("proxy leaked credentials or trusted caller-supplied private headers")
			http.Error(w, "invalid headers", 400)
			return
		}
		upgrade := websocket.Upgrader{Subprotocols: []string{"guacamole"}}
		conn, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err == nil {
			conn.WriteMessage(kind, data)
		}
	}))
	defer upstream.Close()
	p := &PluginResources{app: pb, runtimes: map[string]*resourceRuntime{record.Id: {plugin: &managedPlugin{metadata: pluginrpc.Metadata{ID: "ludus-guacamole"}, webApplication: &pluginrpc.WebApplication{URL: upstream.URL, Token: secret}}}}}
	router, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	registerPluginResourceRoutes(&core.ServeEvent{App: pb, Router: router}, p)
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	host := httptest.NewServer(mux)
	defer host.Close()
	path := "/guacamole/" + record.Id + "/session/websocket-tunnel"
	dialer := websocket.Dialer{Subprotocols: []string{"guacamole"}}
	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(host.URL, "http")+path, http.Header{"Authorization": {"Bearer ludus-secret"}, "X-Api-Key": {"api-secret"}, "X-Ludus-App-Token": {"spoof"}, "X-Ludus-App-Base": {"/other/"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if conn.Subprotocol() != "guacamole" {
		t.Fatal("lost Guacamole subprotocol")
	}
	message := "4.sync,3.123;"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(message)); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != message {
		t.Fatalf("upgrade or payload failed: %q %v", data, err)
	}
	record.Set("state", "removed")
	if err := pb.Save(record); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get(host.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("unapproved resource remained exposed: %d", response.StatusCode)
	}
}
