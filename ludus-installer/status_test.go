package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestStatusRejectsInvalidContainerMetadata(t *testing.T) {
	for _, data := range []string{`{}`, `{"vmid":true}`, `{"vmid":"101"}`, `{"vmid":0}`, `{"vmid":-1}`, `{"vmid":101.5}`, `{"vmid":101} trailing`} {
		if _, err := statusVMID([]byte(data)); err == nil {
			t.Fatal("invalid host metadata accepted: " + data)
		}
	}
	vmid, err := statusVMID([]byte(`{"vmid":101,"version":"v2.4.0"}`))
	if err != nil || vmid != 101 {
		t.Fatalf("valid host metadata rejected: %v", err)
	}
}

func TestStatusUsesConfiguredPortsAndRejectsCoercions(t *testing.T) {
	public, admin, err := statusPorts([]byte("port: 9443\nadmin_port: 9444\n"))
	if err != nil || public != 9443 || admin != 9444 {
		t.Fatalf("custom ports were not preserved: %d %d %v", public, admin, err)
	}
	for _, value := range []string{"true", "'9443'", "9443.5", "0", "65536", "null"} {
		for _, field := range []string{"port", "admin_port"} {
			if _, _, err := statusPorts([]byte(field + ": " + value)); err == nil {
				t.Fatalf("invalid %s accepted: %s", field, value)
			}
		}
	}
}

func TestStatusRequiresBothServicesActive(t *testing.T) {
	for _, inactive := range []string{"ludus", "ludus-admin", ""} {
		t.Run("inactive="+inactive, func(t *testing.T) {
			dir := t.TempDir()
			// Match systemctl's any-active behavior if a caller supplies both units.
			script := "#!/bin/sh\nshift 2\nfor unit do\n  [ \"$unit\" = \"" + inactive + "\" ] || exit 0\ndone\nexit 3\n"
			if err := os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			err := requireActiveServices(context.Background())
			if (err != nil) != (inactive != "") {
				t.Fatalf("health with inactive service %q: %v", inactive, err)
			}
		})
	}
}

func TestStatusRequestsNeverSendCredentialsToProxyOrRedirect(t *testing.T) {
	var proxyCalls, redirectCalls, authorizedCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectCalls.Add(1)
	}))
	defer receiver.Close()
	const secret = "sensitive-local-root-key"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") == secret {
			authorizedCalls.Add(1)
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, receiver.URL, http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, secret)
	}))
	defer origin.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("NO_PROXY", "")
	_, portString, err := net.SplitHostPort(origin.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/redirect", "/error"} {
		_, err := statusRequest(context.Background(), port, path, secret)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatal("HTTP failure accepted or credentials disclosed")
		}
	}
	if authorizedCalls.Load() != 2 || proxyCalls.Load() != 0 || redirectCalls.Load() != 0 {
		t.Fatalf("credential destinations: origin=%d proxy=%d redirect=%d", authorizedCalls.Load(), proxyCalls.Load(), redirectCalls.Load())
	}
}
