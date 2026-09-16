package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"
)

func generateSelfSignedCert() {
	// Check if the cert and key already exist
	if fileExists(ludusInstallPath+"/cert.pem") && fileExists(ludusInstallPath+"/key.pem") {
		return
	}

	// Generate a new RSA key
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	// Create a self-signed certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Ludus Self-Signed Cert"},
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Create the certificate
	certDER, _ := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)

	// Save the certificate
	certOut, _ := os.Create(ludusInstallPath + "/cert.pem")
	pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	certOut.Close()

	// Save the private key
	keyOut, _ := os.Create(ludusInstallPath + "/key.pem")
	pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	keyOut.Close()
}

func serverCertificatePaths() (string, string) {
	certPath := config.TLSCertFile
	keyPath := config.TLSKeyFile
	if !fileExists(certPath) || !fileExists(keyPath) {
		generateSelfSignedCert()
		certPath = ludusInstallPath + "/cert.pem"
		keyPath = ludusInstallPath + "/key.pem"
	}
	return certPath, keyPath
}

func certificateSHA256(certPath string) (string, error) {
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", fmt.Errorf("read TLS certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("decode TLS certificate %s", certPath)
	}
	fingerprint := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(fingerprint[:]), nil
}
