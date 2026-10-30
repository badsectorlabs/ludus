package ludusapi

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keygen-sh/keygen-go/v3"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

func TestLookupLicensedPluginRelease(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		upgradeFrom    string
		emptyList      bool
		hasUpgrade     bool
		wantID         string
		wantVersion    string
	}{
		{
			name:           "upgrade from v-prefixed installed release",
			currentVersion: "v1.2.3",
			upgradeFrom:    "1.2.3",
			hasUpgrade:     true,
			wantID:         "upgrade-id",
			wantVersion:    "2.0.0",
		},
		{
			name:           "upgrade from unprefixed installed release",
			currentVersion: "1.2.3",
			upgradeFrom:    "1.2.3",
			hasUpgrade:     true,
			wantID:         "upgrade-id",
			wantVersion:    "2.0.0",
		},
		{
			name:           "installed release has no upgrade",
			currentVersion: "1.3.0",
			upgradeFrom:    "1.3.0",
		},
		{
			name:        "clean install discovers untagged release",
			upgradeFrom: "release-id",
			wantID:      "release-id",
			wantVersion: "1.3.0",
		},
		{
			name:           "non-semver plugin discovers untagged release",
			currentVersion: "dev",
			upgradeFrom:    "release-id",
			wantID:         "release-id",
			wantVersion:    "1.3.0",
		},
		{
			name:        "semantic latest predates most recently created release",
			upgradeFrom: "release-id",
			hasUpgrade:  true,
			wantID:      "upgrade-id",
			wantVersion: "2.0.0",
		},
		{
			name:      "no published stable releases",
			emptyList: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newPluginReleaseTestClient(t, func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/vnd.api+json")
				query := request.URL.Query()
				if query.Get("product") != "product-id" || query.Get("package") != "package-id" || query.Get("channel") != "stable" {
					response.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(response, `{"errors":[{"title":"Invalid scope","code":"BAD_REQUEST"}]}`)
					return
				}
				switch request.URL.Path {
				case "/v1/releases":
					if query.Get("status") != "PUBLISHED" {
						response.WriteHeader(http.StatusBadRequest)
						fmt.Fprint(response, `{"errors":[{"title":"Published status required","code":"BAD_REQUEST"}]}`)
						return
					}
					if test.emptyList {
						fmt.Fprint(response, `{"data":[]}`)
						return
					}
					fmt.Fprint(response, `{"data":[{"id":"release-id","type":"releases","attributes":{"name":null,"description":null,"version":"1.3.0","channel":"stable","status":"PUBLISHED","tag":null,"metadata":{},"created":"2026-07-17T00:00:00Z","updated":"2026-07-17T00:00:00Z"}}]}`)
				case "/v1/releases/" + test.upgradeFrom + "/upgrade":
					if test.hasUpgrade {
						fmt.Fprint(response, `{"data":{"id":"upgrade-id","type":"releases","attributes":{"name":null,"description":null,"version":"2.0.0","channel":"stable","status":"PUBLISHED","tag":null,"metadata":{},"created":"2026-07-16T00:00:00Z","updated":"2026-07-16T00:00:00Z"}}}`)
						return
					}
					response.WriteHeader(http.StatusNotFound)
					fmt.Fprint(response, `{"errors":[{"title":"Not found","detail":"No upgrade","code":"NOT_FOUND"}]}`)
				default:
					response.WriteHeader(http.StatusNotFound)
					fmt.Fprint(response, `{"errors":[{"title":"Not found","detail":"Release does not exist","code":"NOT_FOUND"}]}`)
				}
			})

			release, err := lookupLicensedPluginRelease(context.Background(), client, test.currentVersion, "product-id", "package-id")
			if err != nil {
				t.Fatalf("lookupLicensedPluginRelease() error = %v", err)
			}
			if test.wantID == "" {
				if release != nil {
					t.Fatalf("release = %#v, want nil when no release is available", release)
				}
			} else if release == nil || release.ID != test.wantID || release.Version != test.wantVersion {
				t.Fatalf("release = %#v, want ID %s and version %s", release, test.wantID, test.wantVersion)
			}
		})
	}
}

func TestLookupLicensedPluginReleasePreservesErrors(t *testing.T) {
	tests := []struct {
		name           string
		currentVersion string
		failDiscovery  bool
		status         int
	}{
		{name: "unauthorized listing", failDiscovery: true, status: http.StatusUnauthorized},
		{name: "missing listing is not an empty list", failDiscovery: true, status: http.StatusNotFound},
		{name: "listing server failure", failDiscovery: true, status: http.StatusInternalServerError},
		{name: "forbidden discovered release upgrade", status: http.StatusForbidden},
		{name: "discovered release upgrade server failure", status: http.StatusInternalServerError},
		{name: "forbidden installed release upgrade", currentVersion: "1.2.3", status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newPluginReleaseTestClient(t, func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/vnd.api+json")
				if request.URL.Path == "/v1/releases" && !test.failDiscovery {
					fmt.Fprint(response, `{"data":[{"id":"release-id","type":"releases","attributes":{"version":"1.3.0","channel":"stable","status":"PUBLISHED","tag":null}}]}`)
					return
				}
				code := "FORBIDDEN"
				if test.status == http.StatusNotFound {
					code = "NOT_FOUND"
				}
				response.WriteHeader(test.status)
				fmt.Fprintf(response, `{"errors":[{"title":"Request failed","code":%q}]}`, code)
			})

			release, err := lookupLicensedPluginRelease(context.Background(), client, test.currentVersion, "product-id", "package-id")
			if err == nil || release != nil {
				t.Fatalf("lookupLicensedPluginRelease() = (%#v, %v), want nil release and error", release, err)
			}
		})
	}
}

func TestLookupLicensedPluginReleasePreservesCancellation(t *testing.T) {
	client := newPluginReleaseTestClient(t, func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusInternalServerError)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, version := range []string{"", "1.2.3"} {
		release, err := lookupLicensedPluginRelease(ctx, client, version, "product-id", "package-id")
		if !errors.Is(err, context.Canceled) || release != nil {
			t.Fatalf("lookupLicensedPluginRelease(%q) = (%#v, %v), want nil release and context.Canceled", version, release, err)
		}
	}
}

func newPluginReleaseTestClient(t *testing.T, handler http.HandlerFunc) *keygen.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := keygen.NewClientWithOptions(&keygen.ClientOptions{
		APIURL:     server.URL,
		APIPrefix:  "v1",
		APIVersion: LicenseAPIVersion,
		LicenseKey: "test-license",
	})
	client.HTTPClient = server.Client()
	return client
}

func TestInstallLicensedPackageValidatesManifest(t *testing.T) {
	s := &Server{LudusInstallPath: t.TempDir()}
	dir := s.licensedPackageDir(enterprisePluginID)
	manifest := testExecutableManifest(enterprisePluginID)
	manifest.Name, manifest.Version = "Ludus Enterprise", "1.2.3"
	manifest.Level, manifest.RunAs = protocol.LevelSystem, protocol.RunAsRoot
	release := func(m protocol.Manifest) []byte {
		return testPackage(t, m, map[string]string{protocol.ExecutableFile: "enterprise " + m.Version})
	}

	other := manifest
	other.ID = "ludus-other"
	if err := s.installLicensedPackage(release(other), enterprisePluginSpec, "1.2.3"); err == nil {
		t.Fatal("installed a release declaring another package ID")
	}
	if err := s.installLicensedPackage(release(manifest), enterprisePluginSpec, "1.2.4"); err == nil {
		t.Fatal("installed a release whose version differs from the release")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("rejected releases left an installed package")
	}

	if err := s.installLicensedPackage(release(manifest), enterprisePluginSpec, "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	installed, err := readPackageManifest(dir)
	if err != nil || installed.Version != "1.2.3" {
		t.Fatalf("installed manifest = %+v, %v", installed, err)
	}
	approval, approved, err := readPluginApproval(dir)
	if err != nil || !approved || approval.Source != protocol.SourceLicensed || approval.SHA256 != sha256Hex([]byte("enterprise 1.2.3")) ||
		approval.Level != protocol.LevelSystem || approval.RunAs != protocol.RunAsRoot {
		t.Fatalf("approval = %+v, %v, %v", approval, approved, err)
	}
	info, err := os.Stat(filepath.Join(dir, protocol.ExecutableFile))
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("executable mode = %v, %v", info, err)
	}

	update := manifest
	update.Version = "1.3.0"
	if err := s.installLicensedPackage(release(update), enterprisePluginSpec, "1.3.1"); err == nil {
		t.Fatal("installed a mismatched update")
	}
	if installed, err := readPackageManifest(dir); err != nil || installed.Version != "1.2.3" {
		t.Fatalf("rejected update replaced the installed package: %+v, %v", installed, err)
	}
	if err := s.installLicensedPackage(release(update), enterprisePluginSpec, "1.3.0"); err != nil {
		t.Fatal(err)
	}
	if installed, err := readPackageManifest(dir); err != nil || installed.Version != "1.3.0" {
		t.Fatalf("update not installed: %+v, %v", installed, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(s.pluginsRoot(), ".staging-*")); len(leftovers) != 0 {
		t.Fatalf("staging directories left behind: %v", leftovers)
	}
}

func TestInstallDownloadedArtifactReplacesOnlyVerifiedFile(t *testing.T) {
	payload := []byte("new signed plugin")
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	digest := sha512.Sum512(payload)
	signature, err := privateKey.Sign(rand.Reader, digest[:], &ed25519.Options{
		Hash:    crypto.SHA512,
		Context: "package-id",
	})
	if err != nil {
		t.Fatalf("sign artifact: %v", err)
	}

	download := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Write(payload)
	}))
	defer download.Close()

	targetPath := filepath.Join(t.TempDir(), "ludus-enterprise_1.0.0.zip")
	if err := os.WriteFile(targetPath, []byte("old plugin"), 0755); err != nil {
		t.Fatalf("write existing plugin: %v", err)
	}
	artifact := &keygen.Artifact{
		URL:       download.URL,
		Signature: base64.RawStdEncoding.EncodeToString(signature),
	}
	if err := installDownloadedArtifact(download.Client(), artifact, targetPath, hex.EncodeToString(publicKey), "package-id"); err != nil {
		t.Fatalf("installDownloadedArtifact() error = %v", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read installed plugin: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("installed plugin = %q, want %q", got, payload)
	}

	if err := os.WriteFile(targetPath, []byte("known good plugin"), 0755); err != nil {
		t.Fatalf("restore existing plugin: %v", err)
	}
	artifact.Signature = base64.RawStdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	if err := installDownloadedArtifact(download.Client(), artifact, targetPath, hex.EncodeToString(publicKey), "package-id"); err == nil {
		t.Fatal("installDownloadedArtifact() returned nil for an invalid signature")
	}
	got, err = os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read plugin after rejected update: %v", err)
	}
	if string(got) != "known good plugin" {
		t.Fatalf("plugin after rejected update = %q, want existing plugin", got)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".*"))
	if err != nil {
		t.Fatalf("list temporary artifacts: %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary artifacts were not removed: %v", temporaryFiles)
	}
}
