package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestGuestConfigurationProtectsSecretsAndLocalAdmin(t *testing.T) {
	opts := Options{
		Endpoints: "https://192.0.2.1:8006 https://[2001:db8::1]:8006",
		TokenID:   "root@pam!ludus-lxc-111", TokenSecret: "secret: value\nexpose_admin_port: true",
		Node: "pve", VMStorage: "vm-pool", VMStorageFormat: "raw", ISOStorage: "shared-iso",
		NATIP: "192.0.2.253", NATGateway: "192.0.2.254", WireGuardEndpoint: "pve.example.test", WireGuardPort: 51821,
		License: "license: value\nport: 1234", APIPort: 8443, AdminPort: 8444,
		Offline: true, VerifyProxmoxTLS: true,
	}
	data, err := guestConfiguration(&opts, true, "encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["proxmox_token_secret"] != opts.TokenSecret || config["license_key"] != opts.License {
		t.Fatal("configuration corrupted a secret containing YAML syntax")
	}
	if config["expose_admin_port"] != false {
		t.Fatal("configuration exposes the local administrator API")
	}
	if config["port"] != opts.APIPort || config["admin_port"] != opts.AdminPort {
		t.Fatal("configuration did not preserve API ports")
	}
	if config["wireguard_endpoint"] != opts.WireGuardEndpoint || config["wireguard_port"] != opts.WireGuardPort {
		t.Fatal("WireGuard host endpoint or port changed")
	}
	if config["airgapped_install"] != true || config["host_managed_network"] != true {
		t.Fatal("offline or migration network policy was lost")
	}
	if config["proxmox_invalid_cert"] != false {
		t.Fatal("Proxmox TLS validation was disabled")
	}
	endpoints, ok := config["proxmox_endpoints"].([]any)
	if !ok || len(endpoints) != 2 {
		t.Fatal("endpoints were not serialized as a YAML sequence")
	}
	if _, err := guestConfiguration(&opts, false, ""); err == nil {
		t.Fatal("accepted an empty database encryption key")
	}
}

func TestImportedEndpointsRetainArchiveValuesAndLegacyDefaults(t *testing.T) {
	opts := Options{APIPort: 8080, AdminPort: 8081, WireGuardPort: 51820, TokenSecret: "destination-token"}
	if err := applyImportedEndpoints([]byte("port: 8443\nadmin_port: 8444\nwireguard_port: 51821\nwireguard_endpoint: preserved.example.test\n"), &opts); err != nil {
		t.Fatal(err)
	}
	if opts.APIPort != 8443 || opts.AdminPort != 8444 || opts.WireGuardPort != 51821 || opts.WireGuardEndpoint != "preserved.example.test" {
		t.Fatal("import did not preserve archive API and WireGuard endpoints")
	}
	if opts.TokenSecret != "destination-token" {
		t.Fatal("endpoint discovery changed destination credentials")
	}
	if err := applyImportedEndpoints([]byte("wireguard_endpoint: legacy.example.test\n"), &opts); err != nil {
		t.Fatal(err)
	}
	if opts.APIPort != 8080 || opts.AdminPort != 8081 || opts.WireGuardPort != 51820 {
		t.Fatal("legacy omitted ports did not use runtime defaults")
	}
	for _, invalid := range []string{"port: 0\n", "admin_port: 65536\n", "wireguard_port: -1\n", "port: not-a-port\n"} {
		if err := applyImportedEndpoints([]byte(invalid), &opts); err == nil {
			t.Fatal("accepted invalid imported endpoint configuration")
		}
	}
}

func TestMigrationCandidateNetworkIsInitiallyQuarantined(t *testing.T) {
	opts := Options{VMID: 222, Hostname: "ludus", Storage: "local-lvm", RootFSSize: 250, Bridge: "vmbr0", IP: "192.0.2.10/24", Gateway: "192.0.2.1", VLANTag: "123", NATIP: "192.0.2.253", Nameserver: "192.0.2.53"}
	for _, migrating := range []bool{false, true} {
		args := guestCreateArgs(&opts, "/cache/image.tar.zst", migrating)
		values := make(map[string]string)
		for index := 3; index < len(args); index += 2 {
			values[args[index]] = args[index+1]
		}
		if values["--rootfs"] != "local-lvm:250" {
			t.Fatal("rootfs is not the requested 250 GiB")
		}
		if values["--net0"] != "name=eth0,bridge=vmbr0,ip=192.0.2.10/24,gw=192.0.2.1,tag=123,firewall=0" {
			t.Fatal("management bridge configuration changed")
		}
		if values["--unprivileged"] != "1" || values["--features"] != "nesting=1,keyctl=1" {
			t.Fatal("container privilege boundary changed")
		}
		if migrating {
			if values["--onboot"] != "0" || !strings.HasSuffix(values["--net1"], ",link_down=1") {
				t.Fatal("candidate can advertise the legacy NAT IP before cutover")
			}
		} else if values["--onboot"] != "1" || strings.Contains(values["--net1"], "link_down") {
			t.Fatal("fresh container networking was disabled")
		}
	}
	if err := createGuest(context.Background(), &Options{RootFSSize: 19}, "", "", nil); err == nil {
		t.Fatal("accepted undersized rootfs")
	}
}

func TestGuestIPv6ManagementUsesPVEIPv6Keys(t *testing.T) {
	opts := Options{IP: "2001:db8::20/64", Gateway: "2001:db8::1", Bridge: "vmbr0", NATIP: "192.0.2.253", RootFSSize: 250}
	args := guestCreateArgs(&opts, "/cache/image.tar.zst", false)
	for i, arg := range args {
		if arg == "--net0" {
			if args[i+1] != "name=eth0,bridge=vmbr0,ip6=2001:db8::20/64,gw6=2001:db8::1,firewall=0" {
				t.Fatalf("IPv6 management uses invalid PVE network keys: %s", args[i+1])
			}
			return
		}
	}
	t.Fatal("management NIC missing")
}

func TestProvisionWaitHonorsCancellationAndReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if err := waitProvision(ctx, time.Minute, time.Second, "bootstrap failed", func(context.Context) error { calls++; return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation result: %v", err)
	}
	if calls != 0 {
		t.Fatal("executed a readiness check after cancellation")
	}
	if err := waitProvision(context.Background(), time.Second, time.Millisecond, "not ready", func(context.Context) error {
		calls++
		if calls == 2 {
			return nil
		}
		return errors.New("not yet")
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("wait did not stop when ready")
	}
}

func TestSDNInterfacesSourceIsIdempotentAndKeepsConfiguration(t *testing.T) {
	for _, existing := range []string{
		"auto lo\niface lo inet loopback\nsource /etc/network/interfaces.d/*\n",
		"source-directory /etc/network/interfaces.d/\n",
		"source /etc/network/interfaces.d/sdn\n",
	} {
		filename := artifactFixture(t, t.TempDir(), "interfaces", []byte(existing))
		if err := ensureSDNInterfacesSource(filename); err != nil {
			t.Fatal(err)
		}
		actual, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if string(actual) != existing {
			t.Fatal("changed an existing SDN source declaration")
		}
	}
	filename := artifactFixture(t, t.TempDir(), "interfaces", []byte("# source /etc/network/interfaces.d/sdn\nauto vmbr0\n"))
	if err := ensureSDNInterfacesSource(filename); err != nil {
		t.Fatal(err)
	}
	if err := ensureSDNInterfacesSource(filename); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "# source /etc/network/interfaces.d/sdn\nauto vmbr0\n") || strings.Count(string(data), "\nsource /etc/network/interfaces.d/sdn\n") != 1 {
		t.Fatal("lost configuration or duplicated the SDN source")
	}
}

func TestOfflineMigrationMissingConntrackCannotInstallPackages(t *testing.T) {
	// No executable is available. The offline branch must terminate without
	// attempting package discovery, repository refreshes or installation.
	t.Setenv("PATH", t.TempDir())
	if err := ensureHostDependencies(context.Background(), &Options{Offline: true}, true); err == nil || !strings.Contains(err.Error(), "offline installation requires the conntrack host package") {
		t.Fatalf("unexpected offline dependency result: %v", err)
	}
}

func TestProxmoxTokenTransportDoesNotFollowRedirectsOrLeakErrors(t *testing.T) {
	var destinationRequests atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationRequests.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	secret := "PRIVATE-TOKEN-CONTENTS"
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=root@pam!install="+secret {
			t.Error("missing token authorization")
		}
		switch r.URL.Path {
		case "/api2/json/version":
			_, _ = w.Write([]byte(`{"data":{"version":"9.0"}}`))
		case "/api2/json/redirect":
			http.Redirect(w, r, destination.URL, 302)
		default:
			http.Error(w, secret, 500)
		}
	}))
	defer source.Close()
	api, err := newProvisionAPI(&Options{TokenID: "root@pam!install", TokenSecret: secret})
	if err != nil {
		t.Fatal(err)
	}
	defer api.client.CloseIdleConnections()
	transport := api.client.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, source.Listener.Addr().String())
	}
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var version struct {
		Version string `json:"version"`
	}
	if err := api.request(context.Background(), http.MethodGet, "/version", nil, &version); err != nil {
		t.Fatal(err)
	}
	if version.Version != "9.0" {
		t.Fatal("invalid Proxmox response decoding")
	}
	if err := api.request(context.Background(), http.MethodGet, "/redirect", nil, nil); err == nil {
		t.Fatal("accepted redirect for a credentialed API request")
	}
	if destinationRequests.Load() != 0 {
		t.Fatal("sent token credentials to a redirect destination")
	}
	if err := api.request(context.Background(), http.MethodGet, "/failure", nil, nil); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("Proxmox error leaked credential contents or was ignored")
	}
	if _, err := newProvisionAPI(&Options{TokenID: "root@pam!install", TokenSecret: "secret\r\nInjected: header"}); err == nil {
		t.Fatal("accepted token header injection")
	}
}

func TestUpdateMetadataPreservesStateAndRefusesRetargeting(t *testing.T) {
	filename := artifactFixture(t, t.TempDir(), "metadata.json", []byte(`{"vmid":123,"version":"old","custom":{"keep":true}}`))
	if err := updateMetadataVersion(filename, "2.4.0-beta.1", 123); err != nil {
		t.Fatal(err)
	}
	metadata, vmid, mode, err := readUpdateMetadata(filename)
	if err != nil {
		t.Fatal(err)
	}
	if vmid != 123 || mode != 0600 {
		t.Fatal("metadata update changed VMID or permissions")
	}
	var version string
	if err := json.Unmarshal(metadata["version"], &version); err != nil {
		t.Fatal(err)
	}
	if version != "2.4.0-beta.1" || !strings.Contains(string(metadata["custom"]), "true") {
		t.Fatal("metadata update lost version or custom fields")
	}
	if err := updateMetadataVersion(filename, "wrong-container", 456); err == nil {
		t.Fatal("updated metadata for a different VMID")
	}
	for _, data := range []string{`{}`, `{"vmid":true}`, `{"vmid":"123"}`, `{"vmid":1.5}`, `{"vmid":0}`, `{"vmid":-1}`, `null`} {
		invalid := artifactFixture(t, t.TempDir(), "metadata.json", []byte(data))
		if _, _, _, err := readUpdateMetadata(invalid); err == nil {
			t.Fatalf("accepted invalid VMID metadata: %s", data)
		}
	}
}

func TestGuestTemporaryPathsCannotEscapeOrInjectArguments(t *testing.T) {
	if !safeGuestTemporary("/tmp/ludus-update.abCD1234", "ludus-update.") {
		t.Fatal("rejected a generated guest update path")
	}
	if !safeGuestTemporary("/tmp/ludus-import.abCD1234.tar.gz", "ludus-import.") {
		t.Fatal("rejected a generated state archive path")
	}
	for _, filename := range []string{"/tmp/ludus-update.", "/tmp/ludus-update.x/../../etc/shadow", "/tmp/ludus-update.x\n/etc/shadow", "/etc/ludus-update.123", "--help", "/tmp/ludus-update.a b", filepath.Join("/tmp", "another-file")} {
		if safeGuestTemporary(filename, "ludus-update.") {
			t.Fatalf("accepted unsafe guest path %q", filename)
		}
	}
}
