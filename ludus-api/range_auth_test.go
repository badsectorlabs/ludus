package ludusapi

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateWinRMClientCertMaterial(t *testing.T) {
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	caPEM, certPEM, keyPEM, err := generateWinRMClientCertMaterial(now)
	if err != nil {
		t.Fatalf("generateWinRMClientCertMaterial() error = %v", err)
	}

	caCert := mustParseCertificatePEM(t, caPEM)
	clientCert := mustParseCertificatePEM(t, certPEM)
	clientKey := mustParseRSAPrivateKeyPEM(t, keyPEM)

	if !caCert.IsCA {
		t.Fatal("CA certificate is not marked as a CA")
	}
	if clientCert.IsCA {
		t.Fatal("client certificate is marked as a CA")
	}
	if err := clientCert.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("client certificate is not signed by generated CA: %v", err)
	}
	if !hasExtKeyUsage(clientCert, x509.ExtKeyUsageClientAuth) {
		t.Fatal("client certificate is missing client auth EKU")
	}

	expectedUPN, err := winRMUPNSANExtension(winrmClientUPN)
	if err != nil {
		t.Fatalf("winRMUPNSANExtension() error = %v", err)
	}
	expectedUPNValue, err := hex.DecodeString("3025a023060a2b060104018237140203a0150c136c6f63616c75736572406c6f63616c686f7374")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(expectedUPN.Value, expectedUPNValue) {
		t.Fatalf("UPN SAN DER = %x, want %x", expectedUPN.Value, expectedUPNValue)
	}
	if !hasExtension(clientCert.Extensions, expectedUPN.Id, expectedUPN.Value) {
		t.Fatal("client certificate is missing expected UPN SAN")
	}
	if !rsaPublicKeysEqual(clientCert.PublicKey, &clientKey.PublicKey) {
		t.Fatal("client certificate public key does not match private key")
	}
}

func TestWinRMClientCertMaterialValidAt(t *testing.T) {
	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	caPEM, certPEM, keyPEM, err := generateWinRMClientCertMaterial(now)
	if err != nil {
		t.Fatalf("generateWinRMClientCertMaterial() error = %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "client_cert.pem")
	keyPath := filepath.Join(dir, "client_key.pem")
	caPath := filepath.Join(dir, "ca_cert.pem")

	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, caPEM, 0644); err != nil {
		t.Fatal(err)
	}

	if !winRMClientCertMaterialValidAt(certPath, keyPath, caPath, now) {
		t.Fatal("generated certificate material was not valid")
	}
	if winRMClientCertMaterialValidAt(certPath, keyPath, caPath, now.Add(3651*24*time.Hour)) {
		t.Fatal("expired credentials were accepted")
	}
	otherCA, _, otherKey, err := generateWinRMClientCertMaterial(now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, otherKey, 0600); err != nil {
		t.Fatal(err)
	}
	if winRMClientCertMaterialValidAt(certPath, keyPath, caPath, now) {
		t.Fatal("a private key not matching the certificate was accepted")
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(caPath, otherCA, 0644); err != nil {
		t.Fatal(err)
	}
	if winRMClientCertMaterialValidAt(certPath, keyPath, caPath, now) {
		t.Fatal("a certificate signed by another range's CA was accepted")
	}

	if err := os.Remove(caPath); err != nil {
		t.Fatal(err)
	}
	if winRMClientCertMaterialValidAt(certPath, keyPath, caPath, now) {
		t.Fatal("certificate material without a CA certificate should be invalid")
	}
}

func TestAuthMaterialPathsForRange(t *testing.T) {
	paths, err := authMaterialPathsForRange("TEST2")
	if err != nil {
		t.Fatalf("authMaterialPathsForRange() error = %v", err)
	}

	expectedBase := filepath.Join(ludusInstallPath, "ranges", "TEST2", "machine-credentials")
	if paths.MachineCredDir != expectedBase {
		t.Fatalf("MachineCredDir = %q, want %q", paths.MachineCredDir, expectedBase)
	}

	nestedPaths, err := authMaterialPathsForRange("TEAM/TEST2")
	if err != nil {
		t.Fatalf("authMaterialPathsForRange() slash-delimited range ID error = %v", err)
	}
	expectedNestedBase := filepath.Join(ludusInstallPath, "ranges", "TEAM", "TEST2", "machine-credentials")
	if nestedPaths.MachineCredDir != expectedNestedBase {
		t.Fatalf("nested MachineCredDir = %q, want %q", nestedPaths.MachineCredDir, expectedNestedBase)
	}

	for _, rangeID := range []string{"", ".", "../TEST2", "TEST2/other/extra/depth", "TEST2/../other"} {
		if _, err := authMaterialPathsForRange(rangeID); err == nil {
			t.Fatalf("authMaterialPathsForRange(%q) succeeded, want error", rangeID)
		}
	}
}

func TestExistingAuthMaterialFailsClosedWithoutRotation(t *testing.T) {
	rangesDir := t.TempDir()
	paths := authMaterialPaths(rangesDir, filepath.Join(rangesDir, "TEST2", "machine-credentials"))
	if err := validateAuthMaterial(paths, time.Now()); !os.IsNotExist(err) {
		t.Fatalf("missing credentials should be reported without creation, got %v", err)
	}
	if _, err := os.Stat(paths.MachineCredDir); !os.IsNotExist(err) {
		t.Fatal("read-only credential lookup created material")
	}
	if err := os.MkdirAll(paths.MachineCredDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := generateAuthMaterial(paths, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths, time.Now()); err != nil {
		t.Fatalf("generated material was rejected: %v", err)
	}
	if err := markAuthMaterialInitialized(paths); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	for _, path := range []string{paths.SSHKeyPath, paths.SSHPubKeyPath, paths.WinRMCACertPath, paths.WinRMCertPath, paths.WinRMKeyPath} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		original[path] = contents
	}
	for _, test := range []struct {
		name   string
		path   string
		remove bool
	}{
		{"missing public key", paths.SSHPubKeyPath, true},
		{"corrupt SSH private key", paths.SSHKeyPath, false},
		{"missing WinRM CA", paths.WinRMCACertPath, true},
		{"corrupt WinRM private key", paths.WinRMKeyPath, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.remove {
				if err := os.Remove(test.path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(test.path, []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ensureAuthMaterial(paths); err == nil {
				t.Fatal("invalid existing credentials were silently repaired")
			}
			for path, want := range original {
				if path == test.path {
					continue
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("failure changed previously deployed material %s", path)
				}
			}
			if test.remove {
				if _, err := os.Lstat(test.path); !os.IsNotExist(err) {
					t.Fatal("missing material was regenerated")
				}
			} else {
				got, err := os.ReadFile(test.path)
				if err != nil || string(got) != "corrupt" {
					t.Fatal("corrupt material was overwritten")
				}
			}
			if err := os.WriteFile(test.path, original[test.path], 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := os.Chmod(paths.SSHKeyPath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths, time.Now()); err == nil {
		t.Fatal("group-readable private key was accepted")
	}
	if err := os.Chmod(paths.SSHKeyPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.SSHKeyPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private-key")
	if err := os.WriteFile(outside, original[paths.SSHKeyPath], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.SSHKeyPath); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths, time.Now()); err == nil {
		t.Fatal("credential symlink outside the range was accepted")
	}
	if err := os.RemoveAll(paths.MachineCredDir); err != nil {
		t.Fatal(err)
	}
	if err := ensureAuthMaterial(paths); err == nil {
		t.Fatal("complete loss of previously provisioned credentials triggered generation")
	}
	if _, err := os.Lstat(paths.MachineCredDir); !os.IsNotExist(err) {
		t.Fatal("lost material was regenerated")
	}
}

func TestCertificateAuthenticationRequiresEntitlement(t *testing.T) {
	for _, test := range []struct {
		name         string
		defaults     map[string]interface{}
		entitlements []string
		wantEnabled  bool
		wantError    bool
	}{
		{"ordinary range", map[string]interface{}{}, nil, false, false},
		{"disabled", map[string]interface{}{"use_cert_auth": false}, nil, false, false},
		{"unlicensed", map[string]interface{}{"use_cert_auth": true}, nil, false, true},
		{"licensed", map[string]interface{}{"use_cert_auth": true}, []string{"ENTERPRISE_PLUGIN"}, true, false},
		{"nonboolean", map[string]interface{}{"use_cert_auth": "true"}, []string{"ENTERPRISE_PLUGIN"}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			enabled, err := certAuthEnabled(test.defaults, test.entitlements)
			if enabled != test.wantEnabled || (err != nil) != test.wantError {
				t.Fatalf("certAuthEnabled() = %v, %v", enabled, err)
			}
		})
	}
}

func mustParseCertificatePEM(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("expected CERTIFICATE PEM block, got %#v", block)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}
	return cert
}

func mustParseRSAPrivateKeyPEM(t *testing.T, data []byte) *rsa.PrivateKey {
	t.Helper()
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		t.Fatalf("expected RSA PRIVATE KEY PEM block, got %#v", block)
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS1PrivateKey() error = %v", err)
	}
	return key
}
