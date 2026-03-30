package ludusapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"time"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

const (
	machineCredDir = ludusInstallPath + "/resources/machine-credentials"
	sshKeyDir      = machineCredDir + "/ssh"
	sshKeyPath     = sshKeyDir + "/ludus_ed25519"
	sshPubKeyPath  = sshKeyDir + "/ludus_ed25519.pub"
	winrmCertDir   = machineCredDir + "/winrm"
	winrmCertPath  = winrmCertDir + "/client_cert.pem"
	winrmKeyPath   = winrmCertDir + "/client_key.pem"
)

// EnsureLudusAuthMaterial generates SSH keys and WinRM client certificates
// if they do not already exist. This is idempotent.
func EnsureLudusAuthMaterial() error {
	if err := ensureSSHKey(); err != nil {
		return fmt.Errorf("failed to ensure SSH key: %w", err)
	}
	if err := ensureWinRMClientCert(); err != nil {
		return fmt.Errorf("failed to ensure WinRM client cert: %w", err)
	}
	return nil
}

// ensureSSHKey generates an Ed25519 SSH keypair if it doesn't already exist.
func ensureSSHKey() error {
	if _, err := os.Stat(sshKeyPath); err == nil {
		// Key already exists
		return nil
	}

	if err := os.MkdirAll(sshKeyDir, 0700); err != nil {
		return fmt.Errorf("failed to create SSH key directory: %w", err)
	}

	// Generate Ed25519 key pair
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate Ed25519 key: %w", err)
	}

	// Marshal private key to OpenSSH format
	sshPrivKey, err := ssh.MarshalPrivateKey(privKey, "ludus")
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	// Write private key
	if err := os.WriteFile(sshKeyPath, pem.EncodeToMemory(sshPrivKey), 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Generate SSH public key
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return fmt.Errorf("failed to create SSH public key: %w", err)
	}

	pubKeyBytes := ssh.MarshalAuthorizedKey(sshPubKey)
	// The authorized key line already ends with a newline; replace it with the comment
	pubKeyLine := fmt.Sprintf("%s ludus\n", string(pubKeyBytes[:len(pubKeyBytes)-1]))
	if err := os.WriteFile(sshPubKeyPath, []byte(pubKeyLine), 0644); err != nil {
		return fmt.Errorf("failed to write public key: %w", err)
	}

	// Fix ownership so Ansible (running as ludus user) can read the files
	changeFileOwner(sshKeyDir, "ludus")
	changeFileOwner(sshKeyPath, "ludus")
	changeFileOwner(sshPubKeyPath, "ludus")

	logger.Info("Generated new SSH Ed25519 keypair at " + sshKeyPath)
	return nil
}

// ensureWinRMClientCert generates a self-signed client certificate for WinRM
// certificate-based authentication if it doesn't already exist.
// The certificate includes the UPN (User Principal Name) in the SAN,
// which is required for WinRM certificate mapping on Windows.
func ensureWinRMClientCert() error {
	if _, err := os.Stat(winrmCertPath); err == nil {
		// Cert already exists
		return nil
	}

	if err := os.MkdirAll(winrmCertDir, 0700); err != nil {
		return fmt.Errorf("failed to create WinRM cert directory: %w", err)
	}

	// Generate ECDSA P-256 key for the client certificate
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("failed to generate ECDSA key: %w", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Build the UPN SAN as an otherName in the SAN extension
	// OID 1.3.6.1.4.1.311.20.2.3 is the Microsoft UPN OID
	upnOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3}
	upnValue := "localuser@localhost"

	// Encode the UTF8String value
	utf8Value, err := asn1.Marshal(upnValue)
	if err != nil {
		return fmt.Errorf("failed to marshal UPN value: %w", err)
	}

	// Wrap in otherName: SEQUENCE { OID, [0] EXPLICIT value }
	otherName := struct {
		OID   asn1.ObjectIdentifier
		Value asn1.RawValue
	}{
		OID: upnOID,
		Value: asn1.RawValue{
			Tag:        0,
			Class:      asn1.ClassContextSpecific,
			IsCompound: true,
			Bytes:      utf8Value,
		},
	}

	otherNameBytes, err := asn1.Marshal(otherName)
	if err != nil {
		return fmt.Errorf("failed to marshal otherName: %w", err)
	}

	// Build the SubjectAlternativeName extension with the otherName
	// GeneralName otherName has tag [0] IMPLICIT
	sanValue := asn1.RawValue{
		Tag:        0,
		Class:      asn1.ClassContextSpecific,
		IsCompound: true,
		Bytes:      otherNameBytes,
	}

	sanExtValue, err := asn1.Marshal([]asn1.RawValue{sanValue})
	if err != nil {
		return fmt.Errorf("failed to marshal SAN extension: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName: "ludus",
		},
		NotBefore: time.Now().Add(-24 * time.Hour),
		NotAfter:  time.Now().Add(3650 * 24 * time.Hour), // 10 years
		KeyUsage:  x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
		BasicConstraintsValid: true,
		ExtraExtensions: []pkix.Extension{
			{
				Id:    asn1.ObjectIdentifier{2, 5, 29, 17}, // subjectAltName
				Value: sanExtValue,
			},
		},
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Write certificate PEM
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(winrmCertPath, certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Write private key PEM
	privKeyBytes, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privKeyBytes})
	if err := os.WriteFile(winrmKeyPath, keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	logger.Info("Generated new WinRM client certificate at " + winrmCertPath)

	// Fix ownership so Ansible (running as ludus user) can read the files
	changeFileOwner(winrmCertDir, "ludus")
	changeFileOwner(winrmCertPath, "ludus")
	changeFileOwner(winrmKeyPath, "ludus")

	return nil
}
