package ludusapi

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

const (
	winrmClientUPN = "localuser@localhost"
)

type ludusAuthMaterialPaths struct {
	RangesDir       string
	MachineCredDir  string
	SSHKeyDir       string
	SSHKeyPath      string
	SSHPubKeyPath   string
	WinRMCertDir    string
	WinRMCertPath   string
	WinRMKeyPath    string
	WinRMCACertPath string
}

var authMaterialMu sync.Mutex

// ensureLudusAuthMaterial publishes a complete credential set atomically. Existing
// material is never regenerated: a failed deployment must not rotate VM identities.
func ensureLudusAuthMaterial(rangeID string) error {
	paths, err := authMaterialPathsForRange(rangeID)
	if err != nil {
		return err
	}
	return ensureAuthMaterial(paths)
}

func ensureAuthMaterial(paths ludusAuthMaterialPaths) error {
	authMaterialMu.Lock()
	defer authMaterialMu.Unlock()
	if _, err := os.Lstat(paths.MachineCredDir); err == nil {
		if err := validateAuthMaterial(paths, time.Now()); err != nil {
			return err
		}
		if err := setAuthMaterialOwner(paths); err != nil {
			return err
		}
		return markAuthMaterialInitialized(paths)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := validateAuthParents(paths); err != nil {
		return err
	}
	if _, err := os.Lstat(authMaterialMarkerPath(paths)); err == nil {
		return fmt.Errorf("previously provisioned machine credentials are missing; restore the original material")
	} else if !os.IsNotExist(err) {
		return err
	}

	stagingDir, err := os.MkdirTemp(filepath.Dir(paths.MachineCredDir), ".machine-credentials-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)
	stagingPaths := authMaterialPaths(paths.RangesDir, stagingDir)
	if err := generateAuthMaterial(stagingPaths, time.Now()); err != nil {
		return err
	}
	if err := validateAuthMaterial(stagingPaths, time.Now()); err != nil {
		return err
	}
	if err := setAuthMaterialOwner(stagingPaths); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, paths.MachineCredDir); err != nil {
		// Another server process may have won publication. Never replace its keys.
		if validationErr := validateAuthMaterial(paths, time.Now()); validationErr != nil {
			return fmt.Errorf("failed to publish machine credentials: %w", err)
		}
	}
	return markAuthMaterialInitialized(paths)
}

func authMaterialMarkerPath(paths ludusAuthMaterialPaths) string {
	return filepath.Join(filepath.Dir(paths.MachineCredDir), ".machine-credentials-initialized")
}

func markAuthMaterialInitialized(paths ludusAuthMaterialPaths) error {
	marker, err := os.OpenFile(authMaterialMarkerPath(paths), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	syncErr := marker.Sync()
	closeErr := marker.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(filepath.Dir(paths.MachineCredDir))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func authMaterialPathsForRange(rangeID string) (ludusAuthMaterialPaths, error) {
	if !regexp.MustCompile(ProxmoxPoolNameRegexString).MatchString(rangeID) {
		return ludusAuthMaterialPaths{}, fmt.Errorf("invalid range ID %q", rangeID)
	}

	rangesDir := filepath.Join(ludusInstallPath, "ranges")
	machineCredDir := filepath.Join(rangesDir, filepath.FromSlash(rangeID), "machine-credentials")
	return authMaterialPaths(rangesDir, machineCredDir), nil
}

func authMaterialPaths(rangesDir, machineCredDir string) ludusAuthMaterialPaths {
	sshKeyDir := filepath.Join(machineCredDir, "ssh")
	winrmCertDir := filepath.Join(machineCredDir, "winrm")
	return ludusAuthMaterialPaths{
		RangesDir:       rangesDir,
		MachineCredDir:  machineCredDir,
		SSHKeyDir:       sshKeyDir,
		SSHKeyPath:      filepath.Join(sshKeyDir, "ludus_ed25519"),
		SSHPubKeyPath:   filepath.Join(sshKeyDir, "ludus_ed25519.pub"),
		WinRMCertDir:    winrmCertDir,
		WinRMCertPath:   filepath.Join(winrmCertDir, "client_cert.pem"),
		WinRMKeyPath:    filepath.Join(winrmCertDir, "client_key.pem"),
		WinRMCACertPath: filepath.Join(winrmCertDir, "ca_cert.pem"),
	}
}

// MachineCredentialsDirForRange returns only complete, valid existing material.
// It never generates keys, changes permissions, or renews certificates.
func MachineCredentialsDirForRange(rangeID string) (string, error) {
	paths, err := authMaterialPathsForRange(rangeID)
	if err != nil {
		return "", err
	}
	if err := validateAuthMaterial(paths, time.Now()); err != nil {
		return "", err
	}
	return paths.MachineCredDir, nil
}

func generateAuthMaterial(paths ludusAuthMaterialPaths, now time.Time) error {
	for _, dir := range []string{paths.SSHKeyDir, paths.WinRMCertDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			return err
		}
	}
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	sshPrivKey, err := ssh.MarshalPrivateKey(privKey, "ludus")
	if err != nil {
		return err
	}
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return err
	}
	caPEM, certPEM, keyPEM, err := generateWinRMClientCertMaterial(now)
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{paths.SSHKeyPath, pem.EncodeToMemory(sshPrivKey), 0600},
		{paths.SSHPubKeyPath, ssh.MarshalAuthorizedKey(sshPubKey), 0644},
		{paths.WinRMCACertPath, caPEM, 0644},
		{paths.WinRMCertPath, certPEM, 0644},
		{paths.WinRMKeyPath, keyPEM, 0600},
	}
	for _, file := range files {
		f, err := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(file.data)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func validateAuthParents(paths ludusAuthMaterialPaths) error {
	// Reject symlinked range directories as well as symlinked credential entries.
	base := paths.RangesDir
	relative, err := filepath.Rel(base, filepath.Dir(paths.MachineCredDir))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("machine credentials are outside the ranges directory")
	}
	dir := base
	for _, part := range append([]string{"."}, strings.Split(relative, string(filepath.Separator))...) {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("credential parent %s is not a directory", dir)
		}
	}
	return nil
}

func validateAuthMaterial(paths ludusAuthMaterialPaths, now time.Time) error {
	if err := validateAuthParents(paths); err != nil {
		return err
	}
	for _, dir := range []string{paths.MachineCredDir, paths.SSHKeyDir, paths.WinRMCertDir} {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("credential directory %s must be a private directory (0700)", dir)
		}
	}
	for _, path := range []string{paths.SSHKeyPath, paths.SSHPubKeyPath, paths.WinRMCACertPath, paths.WinRMCertPath, paths.WinRMKeyPath} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 64*1024 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("invalid credential file %s", path)
		}
		if (path == paths.SSHKeyPath || path == paths.WinRMKeyPath) && info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("private key %s must not be accessible by group or other users", path)
		}
	}
	privatePEM, err := os.ReadFile(paths.SSHKeyPath)
	if err != nil {
		return err
	}
	privateKey, err := ssh.ParseRawPrivateKey(privatePEM)
	if err != nil {
		return fmt.Errorf("invalid SSH private key: %w", err)
	}
	if _, ok := privateKey.(*ed25519.PrivateKey); !ok {
		return fmt.Errorf("SSH private key must be Ed25519")
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return err
	}
	publicBytes, err := os.ReadFile(paths.SSHPubKeyPath)
	if err != nil {
		return err
	}
	publicKey, _, options, rest, err := ssh.ParseAuthorizedKey(publicBytes)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.Equal(publicKey.Marshal(), signer.PublicKey().Marshal()) {
		return fmt.Errorf("SSH public and private keys do not match")
	}
	if !winRMClientCertMaterialValid(paths, now) {
		return fmt.Errorf("WinRM credentials are incomplete, invalid, or expired; restore the original material instead of rotating deployed VM identities")
	}
	return nil
}

func setAuthMaterialOwner(paths ludusAuthMaterialPaths) error {
	if os.Geteuid() != 0 {
		return nil
	}
	for _, path := range []string{paths.MachineCredDir, paths.SSHKeyDir, paths.WinRMCertDir, paths.SSHKeyPath, paths.SSHPubKeyPath, paths.WinRMCACertPath, paths.WinRMCertPath, paths.WinRMKeyPath} {
		if err := changeFileOwner(path, "ludus"); err != nil {
			return fmt.Errorf("failed to make machine credentials readable by ludus: %w", err)
		}
	}
	return nil
}

func generateWinRMClientCertMaterial(now time.Time) ([]byte, []byte, []byte, error) {
	caKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate WinRM CA key: %w", err)
	}

	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate WinRM client key: %w", err)
	}

	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate WinRM CA serial number: %w", err)
	}

	clientSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate WinRM client serial number: %w", err)
	}

	caSubjectKeyID, err := subjectKeyID(&caKey.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to build WinRM CA subject key ID: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial,
		Subject: pkix.Name{
			CommonName:   "Ludus WinRM Client Auth CA",
			Organization: []string{"Ludus"},
		},
		NotBefore:             now.Add(-24 * time.Hour),
		NotAfter:              now.Add(3650 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          caSubjectKeyID,
	}

	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create WinRM CA certificate: %w", err)
	}

	upnSAN, err := winRMUPNSANExtension(winrmClientUPN)
	if err != nil {
		return nil, nil, nil, err
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: clientSerial,
		Subject: pkix.Name{
			CommonName: "ludus-winrm-client",
		},
		NotBefore: now.Add(-24 * time.Hour),
		NotAfter:  now.Add(3650 * 24 * time.Hour), // 10 years
		KeyUsage:  x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
		},
		BasicConstraintsValid: true,
		AuthorityKeyId:        caTemplate.SubjectKeyId,
		ExtraExtensions:       []pkix.Extension{upnSAN},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create WinRM client certificate: %w", err)
	}

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)})
	return caPEM, certPEM, keyPEM, nil
}

func winRMClientCertMaterialValid(paths ludusAuthMaterialPaths, now time.Time) bool {
	return winRMClientCertMaterialValidAt(paths.WinRMCertPath, paths.WinRMKeyPath, paths.WinRMCACertPath, now)
}

func winRMClientCertMaterialValidAt(certPath, keyPath, caPath string, now time.Time) bool {
	clientCert, err := readPEMCertificate(certPath)
	if err != nil {
		return false
	}
	caCert, err := readPEMCertificate(caPath)
	if err != nil {
		return false
	}

	if clientCert.IsCA || !caCert.IsCA || clientCert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return false
	}
	if now.Before(clientCert.NotBefore) || now.After(clientCert.NotAfter) || now.Before(caCert.NotBefore) || now.After(caCert.NotAfter) {
		return false
	}
	if !hasExtKeyUsage(clientCert, x509.ExtKeyUsageClientAuth) {
		return false
	}

	expectedUPN, err := winRMUPNSANExtension(winrmClientUPN)
	if err != nil || !hasExtension(clientCert.Extensions, expectedUPN.Id, expectedUPN.Value) {
		return false
	}

	clientKey, err := readPEMRSAPrivateKey(keyPath)
	if err != nil || clientKey.N.BitLen() < 2048 || clientKey.Validate() != nil || !rsaPublicKeysEqual(clientCert.PublicKey, &clientKey.PublicKey) {
		return false
	}

	if caCert.CheckSignatureFrom(caCert) != nil {
		return false
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = clientCert.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	return err == nil
}

func readPEMCertificate(path string) (*x509.Certificate, error) {
	certPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("missing certificate PEM block in %s", path)
	}
	return x509.ParseCertificate(block.Bytes)
}

func hasExtKeyUsage(cert *x509.Certificate, usage x509.ExtKeyUsage) bool {
	for _, certUsage := range cert.ExtKeyUsage {
		if certUsage == usage {
			return true
		}
	}
	return false
}

func hasExtension(extensions []pkix.Extension, oid asn1.ObjectIdentifier, value []byte) bool {
	for _, extension := range extensions {
		if extension.Id.Equal(oid) && bytes.Equal(extension.Value, value) {
			return true
		}
	}
	return false
}

func readPEMRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	keyPEM, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, rest := pem.Decode(keyPEM)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("missing private key PEM block in %s", path)
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key in %s is not RSA", path)
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported private key type %q in %s", block.Type, path)
	}
}

func rsaPublicKeysEqual(certPublicKey interface{}, privatePublicKey *rsa.PublicKey) bool {
	certRSAKey, ok := certPublicKey.(*rsa.PublicKey)
	if !ok || certRSAKey == nil || privatePublicKey == nil {
		return false
	}
	return certRSAKey.E == privatePublicKey.E && certRSAKey.N.Cmp(privatePublicKey.N) == 0
}

func subjectKeyID(pub *rsa.PublicKey) ([]byte, error) {
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum(publicKeyBytes)
	return sum[:], nil
}

func winRMUPNSANExtension(upn string) (pkix.Extension, error) {
	// OID 1.3.6.1.4.1.311.20.2.3 is the Microsoft UPN otherName OID.
	upnOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3}

	utf8Value, err := asn1.Marshal(upn)
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("failed to marshal UPN value: %w", err)
	}

	oidValue, err := asn1.Marshal(upnOID)
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("failed to marshal UPN OID: %w", err)
	}

	explicitValue, err := asn1.Marshal(asn1.RawValue{
		Tag:        0,
		Class:      asn1.ClassContextSpecific,
		IsCompound: true,
		Bytes:      utf8Value,
	})
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("failed to marshal UPN explicit value: %w", err)
	}

	otherNameValue := append(oidValue, explicitValue...)
	sanValue := asn1.RawValue{
		Tag:        0,
		Class:      asn1.ClassContextSpecific,
		IsCompound: true,
		Bytes:      otherNameValue,
	}

	sanExtValue, err := asn1.Marshal([]asn1.RawValue{sanValue})
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("failed to marshal UPN SAN extension: %w", err)
	}

	return pkix.Extension{
		Id:    asn1.ObjectIdentifier{2, 5, 29, 17}, // subjectAltName
		Value: sanExtValue,
	}, nil
}
