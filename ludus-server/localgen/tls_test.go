package localgen

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureTLSCert(t *testing.T) {
	dir := t.TempDir()
	crt := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	if err := EnsureTLSCert(crt, key, "ludus.local", []net.IP{net.ParseIP("10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	// Loadable as a TLS pair
	if _, err := tls.LoadX509KeyPair(crt, key); err != nil {
		t.Fatalf("keypair: %v", err)
	}
	// SAN contains IP
	pemBytes, _ := os.ReadFile(crt)
	block, _ := pem.Decode(pemBytes)
	cert, _ := x509.ParseCertificate(block.Bytes)
	found := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("10.0.0.5")) {
			found = true
		}
	}
	if !found {
		t.Fatal("SAN IP missing")
	}
	// Idempotent: second call does not overwrite
	mtime1, _ := os.Stat(crt)
	_ = EnsureTLSCert(crt, key, "ludus.local", []net.IP{net.ParseIP("10.0.0.5")})
	mtime2, _ := os.Stat(crt)
	if !mtime1.ModTime().Equal(mtime2.ModTime()) {
		t.Fatal("cert was regenerated on second call")
	}
}
