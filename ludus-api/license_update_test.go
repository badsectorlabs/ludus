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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/keygen-sh/keygen-go/v3"
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
				if query.Get("product") != LicenseProductLudus || query.Get("package") != "package-id" || query.Get("channel") != "stable" {
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

			release, err := lookupLicensedPluginRelease(context.Background(), client, test.currentVersion, "package-id")
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

			release, err := lookupLicensedPluginRelease(context.Background(), client, test.currentVersion, "package-id")
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
		release, err := lookupLicensedPluginRelease(ctx, client, version, "package-id")
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

func TestActivatePluginUpdateValidatesMetadata(t *testing.T) {
	tests := []struct {
		name            string
		pathEnvironment string
		pluginName      string
	}{
		{
			name:            "enterprise",
			pathEnvironment: "LUDUS_ENTERPRISE_PLUGIN",
			pluginName:      EnterprisePluginName,
		},
		{
			name:            "external plugin",
			pathEnvironment: "LUDUS_PLUGIN_PATH",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sourcePath := os.Getenv(test.pathEnvironment)
			if sourcePath == "" {
				t.Skipf("%s is not set", test.pathEnvironment)
			}
			sourceMetadata, err := readPluginMetadata(sourcePath, nil)
			if err != nil {
				t.Fatalf("read source plugin metadata: %v", err)
			}
			if sourceMetadata.Version == "" {
				t.Fatal("source plugin has no version")
			}
			if test.pluginName == "" {
				test.pluginName = sourceMetadata.Name
			}

			targetDir := t.TempDir()
			candidatePath := filepath.Join(targetDir, ".candidate.plugin")
			targetPath := filepath.Join(targetDir, "installed.plugin")
			if err := copyPluginExecutable(sourcePath, candidatePath); err != nil {
				t.Fatalf("copy plugin candidate: %v", err)
			}
			if err := os.WriteFile(targetPath, []byte("old plugin"), 0755); err != nil {
				t.Fatalf("write old plugin: %v", err)
			}

			server := &Server{}
			if err := server.activatePluginUpdate(candidatePath, targetPath, sourceMetadata.Version, test.pluginName); err != nil {
				t.Fatalf("activatePluginUpdate() error = %v", err)
			}
			installedMetadata, err := readPluginMetadata(targetPath, nil)
			if err != nil {
				t.Fatalf("read activated plugin metadata: %v", err)
			}
			if installedMetadata.Name != test.pluginName || installedMetadata.Version != sourceMetadata.Version {
				t.Fatalf("activated plugin metadata = %#v, want name %q and version %q", installedMetadata, test.pluginName, sourceMetadata.Version)
			}

			if err := copyPluginExecutable(sourcePath, candidatePath); err != nil {
				t.Fatalf("copy mismatched plugin candidate: %v", err)
			}
			protectedPath := filepath.Join(targetDir, "known-good.plugin")
			if err := os.WriteFile(protectedPath, []byte("known good plugin"), 0755); err != nil {
				t.Fatalf("write known-good plugin: %v", err)
			}
			if err := server.activatePluginUpdate(candidatePath, protectedPath, sourceMetadata.Version+".1", test.pluginName); err == nil {
				t.Fatal("activatePluginUpdate() returned nil for a mismatched plugin version")
			}
			got, err := os.ReadFile(protectedPath)
			if err != nil {
				t.Fatalf("read plugin after rejected activation: %v", err)
			}
			if string(got) != "known good plugin" {
				t.Fatalf("plugin after rejected activation = %q, want existing plugin", got)
			}
		})
	}
}

func copyPluginExecutable(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	sourceInfo, err := source.Stat()
	if err != nil {
		return err
	}
	target, err := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, sourceInfo.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(target, source)
	closeErr := target.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
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

	targetPath := filepath.Join(t.TempDir(), EnterprisePluginFilename)
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
