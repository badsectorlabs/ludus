package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func artifactFixture(t *testing.T, directory, name string, data []byte) string {
	t.Helper()
	filename := filepath.Join(directory, name)
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func artifactManifestLine(name string, data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}

func TestArtifactManifestRejectsAmbiguityAndUnsafePaths(t *testing.T) {
	payload := []byte("release")
	manifest, err := parseChecksumManifest([]byte(artifactManifestLine("one/ludus-installer", payload) + artifactManifestLine("two/ludus-installer", payload)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manifest.digest("ludus-installer"); err == nil {
		t.Fatal("accepted duplicate basenames")
	}
	if _, err := manifest.digest("missing"); err == nil {
		t.Fatal("accepted absent artifact")
	}
	for _, name := range []string{"../escape", "nested/../../escape", "/absolute", `nested\escape`, "directory/", "./"} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseChecksumManifest([]byte(artifactManifestLine(name, payload))); err == nil {
				t.Fatal("accepted unsafe manifest path")
			}
		})
	}
	for _, data := range []string{"", "garbage", "bad  image", strings.Repeat("g", 64) + "  image\n"} {
		if _, err := parseChecksumManifest([]byte(data)); err == nil {
			t.Fatalf("accepted malformed manifest %q", data)
		}
	}
	digest := sha256.Sum256(payload)
	if _, err := parseChecksumManifest([]byte(hex.EncodeToString(digest[:]) + "\n")); err == nil {
		t.Fatal("accepted a digest without an artifact")
	}
	manifest, err = parseChecksumManifest([]byte("# release\n" + strings.ToUpper(hex.EncodeToString(digest[:])) + " *./release/image.tar.zst\n"))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := manifest.digest("image.tar.zst")
	if err != nil {
		t.Fatal(err)
	}
	if actual != digest {
		t.Fatal("uppercase or binary-mode checksum entry was decoded incorrectly")
	}
}

func TestArtifactVerificationRejectsCorruptInputs(t *testing.T) {
	for _, name := range []string{"image.tar.zst", "install.iso", "license.lic", "state.tar.gz", "ludus-enterprise.plugin", "ludus-installer"} {
		t.Run(name, func(t *testing.T) {
			data := []byte("verified release artifact")
			file := artifactFixture(t, t.TempDir(), name, data)
			manifest, err := parseChecksumManifest([]byte(artifactManifestLine("release/"+name, data)))
			if err != nil {
				t.Fatal(err)
			}
			if err := manifest.verify(file, name); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("modified release artifact"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := manifest.verify(file, name); err == nil {
				t.Fatal("accepted corrupted artifact")
			}
		})
	}
}

func TestManifestSignaturesAuthenticateExactBytes(t *testing.T) {
	manifest := []byte(artifactManifestLine("image.tar.zst", []byte("release")))
	digest := sha256.Sum256(manifest)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []crypto.Signer{rsaKey, ecKey} {
		t.Run(fmt.Sprintf("%T", key), func(t *testing.T) {
			public, err := x509.MarshalPKIXPublicKey(key.Public())
			if err != nil {
				t.Fatal(err)
			}
			keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})
			signature, err := key.Sign(rand.Reader, digest[:], crypto.SHA256)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyManifestSignature(manifest, signature, keyPEM); err != nil {
				t.Fatal(err)
			}
			if err := verifyManifestSignature(append(append([]byte(nil), manifest...), '\n'), signature, keyPEM); err == nil {
				t.Fatal("accepted modified signed manifest")
			}
			signature[0] ^= 0x80
			if err := verifyManifestSignature(manifest, signature, keyPEM); err == nil {
				t.Fatal("accepted modified signature")
			}
			if err := verifyManifestSignature(manifest, signature, []byte("not a key")); err == nil {
				t.Fatal("accepted invalid public key")
			}
		})
	}
}

func TestSignedLocalManifestRequiresAllTrustInputs(t *testing.T) {
	directory := t.TempDir()
	manifest := []byte(artifactManifestLine("image.tar.zst", []byte("release")))
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(manifest)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Offline: true,
		ChecksumFile:      artifactFixture(t, directory, "checksums.txt", manifest),
		ChecksumSignature: artifactFixture(t, directory, "checksums.sig", signature),
		ChecksumPublicKey: artifactFixture(t, directory, "signing.pem", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public})),
	}
	if _, err := loadLocalManifest(&opts); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"manifest", "signature", "key"} {
		invalid := opts
		switch missing {
		case "manifest":
			invalid.ChecksumFile = ""
		case "signature":
			invalid.ChecksumSignature = ""
		case "key":
			invalid.ChecksumPublicKey = ""
		}
		if _, err := loadLocalManifest(&invalid); err == nil {
			t.Fatalf("accepted missing %s", missing)
		}
	}
	if err := os.WriteFile(opts.ChecksumFile, []byte(artifactManifestLine("image.tar.zst", []byte("tampered"))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadLocalManifest(&opts); err == nil {
		t.Fatal("accepted manifest with bad signature")
	}
}

func TestStagedArtifactsArePrivateVerifiedSnapshots(t *testing.T) {
	sourceData := []byte("original plugin")
	source := artifactFixture(t, t.TempDir(), "plugin", sourceData)
	manifest, err := parseChecksumManifest([]byte(artifactManifestLine("plugin", sourceData)))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stageLocalArtifact(t.TempDir(), source, "staged-plugin", manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("later replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manifest.verify(staged, "plugin"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(staged)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("staged artifact mode %o", info.Mode().Perm())
	}
	if _, err := stageLocalArtifact(t.TempDir(), source, "staged-plugin", manifest); err == nil {
		t.Fatal("accepted modified source")
	}
	for _, invalid := range []string{t.TempDir(), artifactFixture(t, t.TempDir(), "empty", nil), filepath.Join(t.TempDir(), "missing")} {
		if _, err := stageLocalArtifact(t.TempDir(), invalid, "artifact", nil); err == nil {
			t.Fatal("skip verification accepted unreadable, empty or non-file media")
		}
	}
}

func TestOfflinePreparationRequiresCompleteMediaEvenWhenSkippingVerification(t *testing.T) {
	directory := t.TempDir()
	template := artifactFixture(t, directory, "image.tar.zst", []byte("development image"))
	bginfo := artifactFixture(t, t.TempDir(), "bginfo.exe", []byte("BgInfo"))
	cases := []Options{
		{Offline: true, SkipVerification: true},
		{Offline: true, SkipVerification: true, TemplateFile: template},
		{Offline: true, SkipVerification: true, TemplateFile: template, ISODirectory: directory},
		{Offline: true, SkipVerification: true, TemplateFile: template, ISODirectory: directory, BgInfoFile: bginfo, LicenseFile: "license", License: "enterprise"},
		{Offline: true, SkipVerification: true, TemplateFile: template, ISODirectory: directory, BgInfoFile: bginfo, LicenseFile: "license", EnterprisePlugin: "plugin"},
		{Offline: true, TemplateFile: template, ISODirectory: directory, BgInfoFile: bginfo},
	}
	for index, opts := range cases {
		if artifacts, err := prepareArtifacts(context.Background(), &opts); err == nil {
			artifacts.close()
			t.Fatalf("accepted incomplete media case %d", index)
		}
	}
	for _, name := range airgappedISOFilenames {
		artifactFixture(t, directory, name, []byte("ISO"))
	}
	// Every ISO is present, but the appliance does not ship BgInfo.
	opts := Options{Offline: true, SkipVerification: true, TemplateFile: template, ISODirectory: directory}
	if artifacts, err := prepareArtifacts(context.Background(), &opts); err == nil {
		artifacts.close()
		t.Fatal("accepted offline media without bginfo.exe")
	}
	// A complete directory with one empty ISO must also fail before host changes.
	artifactFixture(t, directory, airgappedISOFilenames[len(airgappedISOFilenames)-1], nil)
	opts.BgInfoFile = bginfo
	if artifacts, err := prepareArtifacts(context.Background(), &opts); err == nil {
		artifacts.close()
		t.Fatal("accepted empty required ISO")
	}
}

func TestOfflineDownloadsCannotContactNetwork(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	opts := Options{Offline: true, Version: "2.4.0"}
	if err := downloadArtifact(context.Background(), &opts, server.URL, "image", filepath.Join(t.TempDir(), "image")); err == nil {
		t.Fatal("offline download succeeded")
	}
	if requests.Load() != 0 {
		t.Fatal("offline download contacted the network")
	}
}

func TestDownloadRequiresSuccessAndPublishesAtomically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/missing") {
			http.Error(w, "not found", 404)
			return
		}
		_, _ = w.Write([]byte("release bytes"))
	}))
	defer server.Close()
	opts := Options{Version: "2.4.0"}
	filename := artifactFixture(t, t.TempDir(), "image", []byte("existing"))
	if err := downloadArtifact(context.Background(), &opts, server.URL, "missing", filename); err == nil {
		t.Fatal("accepted failed HTTP response")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Fatal("failed download replaced existing artifact")
	}
	if err := downloadArtifact(context.Background(), &opts, server.URL, "image", filename); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "release bytes" {
		t.Fatal("download did not publish complete bytes")
	}
}

func TestArtifactURLsRejectTraversalAndCredentials(t *testing.T) {
	for _, version := range []string{"", ".", "..", "../2.4.0", "2.4/next", "2.4\\next", "2.4\nInjected"} {
		if _, err := releaseArtifactURL("https://lxc.ludus.cloud", version, "image"); err == nil {
			t.Fatalf("accepted unsafe version %q", version)
		}
	}
	for _, base := range []string{"file:///tmp", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment"} {
		if _, err := releaseArtifactURL(base, "2.4.0", "image"); err == nil {
			t.Fatalf("accepted unsafe base %q", base)
		}
	}
	location, err := releaseArtifactURL("https://example.com/releases/", "2.4.0-beta.1", "ludus-installer")
	if err != nil {
		t.Fatal(err)
	}
	if location != "https://example.com/releases/2.4.0-beta.1/ludus-installer" {
		t.Fatalf("unexpected artifact URL %s", location)
	}
}

func TestSharedISOStorageValidation(t *testing.T) {
	parse := func(text string) map[string]json.RawMessage {
		t.Helper()
		var result map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, config := range []string{`{"shared":1,"content":"images,iso"}`, `{"shared":"1","content":["iso"]}`, `{"shared":true,"content":"iso"}`} {
		if err := validateSharedISOStorage(parse(config), parse(`{"active":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	for _, values := range [][2]string{
		{`{"shared":0,"content":"iso"}`, `{"active":1}`},
		{`{"shared":1,"content":"images"}`, `{"active":1}`},
		{`{"shared":1,"content":"isomedia"}`, `{"active":1}`},
		{`{"shared":1,"content":"iso"}`, `{"active":0}`},
		{`{"shared":1,"content":"iso"}`, `{"active":1,"enabled":0}`},
	} {
		if err := validateSharedISOStorage(parse(values[0]), parse(values[1])); err == nil {
			t.Fatal("accepted unavailable or non-shared ISO pool")
		}
	}
}

func TestSiteCACertificateValidation(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Unix(0, 0), NotAfter: time.Unix(2000000000, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := validateCACertificate(data); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{nil, []byte("not a certificate"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), append(append([]byte(nil), data...), []byte("trailing garbage")...)} {
		if err := validateCACertificate(invalid); err == nil {
			t.Fatal("accepted invalid CA input")
		}
	}
}
