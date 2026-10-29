package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const guestInstaller = "/opt/ludus/install/ludus-installer"

var airgappedISOFilenames = []string{
	"debian-13.7.0-amd64-netinst.iso",
	"debian-11.7.0-amd64-netinst.iso",
	"debian-12.14.0-amd64-netinst.iso",
	"kali-linux-2026.1-installer-netinst-amd64.iso",
	"22621.525.220925-0207.ni_release_svc_refresh_CLIENTENTERPRISEEVAL_OEMRET_x64FRE_en-us.iso",
	"SERVER_EVAL_x64FRE_en-us.iso",
	"virtio-win-0.1.240.iso",
	"virtio-win-0.1.229.iso",
}

type checksumManifest map[string][][sha256.Size]byte

// A manifest is an index, never a list of paths to open or extract. Index by
// basename, but retain every match so duplicate/ambiguous entries fail closed.
func parseChecksumManifest(data []byte) (checksumManifest, error) {
	result := make(checksumManifest)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, errors.New("invalid SHA-256 manifest line")
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" || path.IsAbs(name) || strings.ContainsAny(name, "\\\x00") {
			return nil, errors.New("unsafe artifact path in checksum manifest")
		}
		for _, component := range strings.Split(name, "/") {
			if component == ".." {
				return nil, errors.New("unsafe artifact path in checksum manifest")
			}
		}
		if strings.HasSuffix(name, "/") || path.Base(name) == "." {
			return nil, errors.New("invalid artifact name in checksum manifest")
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 entry for %s", path.Base(name))
		}
		var sum [sha256.Size]byte
		copy(sum[:], digest)
		result[path.Base(name)] = append(result[path.Base(name)], sum)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksum manifest: %w", err)
	}
	if len(result) == 0 {
		return nil, errors.New("checksum manifest contains no artifacts")
	}
	return result, nil
}

func (m checksumManifest) digest(name string) ([sha256.Size]byte, error) {
	matches := m[path.Base(name)]
	if len(matches) != 1 {
		return [sha256.Size]byte{}, fmt.Errorf("expected one checksum entry for %s, found %d", path.Base(name), len(matches))
	}
	return matches[0], nil
}

func hashArtifact(filename string) ([sha256.Size]byte, error) {
	var sum [sha256.Size]byte
	file, err := os.Open(filename)
	if err != nil {
		return sum, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return sum, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return sum, fmt.Errorf("local input must be a readable, non-empty regular file: %s", filename)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return sum, err
	}
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

func (m checksumManifest) verify(filename, name string) error {
	expected, err := m.digest(name)
	if err != nil {
		return err
	}
	actual, err := hashArtifact(filename)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		return fmt.Errorf("checksum mismatch for %s", path.Base(name))
	}
	return nil
}

func readArtifactLimited(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > limit {
		return nil, fmt.Errorf("invalid local input size or type: %s", filename)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("local input exceeds size limit")
	}
	return data, nil
}

// Match openssl dgst -sha256 for the RSA and ECDSA public keys used by signed
// offline distributions. Reject unsupported algorithms instead of weakening the
// signature requirement or mistaking a certificate for a trust decision.
func verifyManifestSignature(manifest, signature, publicKey []byte) error {
	block, rest := pem.Decode(publicKey)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return errors.New("checksum public key must contain one PEM public key")
	}
	var key any
	var err error
	switch block.Type {
	case "PUBLIC KEY":
		key, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		key, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return errors.New("unsupported checksum public key PEM type")
	}
	if err != nil {
		return errors.New("invalid checksum public key")
	}
	digest := sha256.Sum256(manifest)
	switch value := key.(type) {
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(value, crypto.SHA256, digest[:], signature); err != nil {
			return errors.New("artifact checksum manifest signature is invalid")
		}
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(value, digest[:], signature) {
			return errors.New("artifact checksum manifest signature is invalid")
		}
	default:
		return errors.New("unsupported checksum public key algorithm")
	}
	return nil
}

func loadLocalManifest(opts *Options) (checksumManifest, error) {
	if opts.SkipVerification {
		logWarning("Signed release artifact verification is disabled")
		return nil, nil
	}
	if opts.ChecksumFile == "" {
		if opts.ChecksumSignature != "" || opts.ChecksumPublicKey != "" {
			return nil, errors.New("--checksum-signature and --checksum-public-key require --checksum-file")
		}
		if opts.Offline {
			return nil, errors.New("offline installation requires --checksum-file, --checksum-signature, and --checksum-public-key")
		}
		return nil, nil
	}
	if opts.ChecksumSignature == "" || opts.ChecksumPublicKey == "" {
		return nil, errors.New("signed verification requires a checksum manifest, signature, and trusted public key")
	}
	manifest, err := readArtifactLimited(opts.ChecksumFile, 32<<20)
	if err != nil {
		return nil, err
	}
	signature, err := readArtifactLimited(opts.ChecksumSignature, 1<<20)
	if err != nil {
		return nil, err
	}
	key, err := readArtifactLimited(opts.ChecksumPublicKey, 1<<20)
	if err != nil {
		return nil, err
	}
	if err := verifyManifestSignature(manifest, signature, key); err != nil {
		return nil, err
	}
	return parseChecksumManifest(manifest)
}

func validateCACertificate(data []byte) error {
	certificates := 0
	for len(bytes.TrimSpace(data)) != 0 {
		block, rest := pem.Decode(data)
		if block == nil || block.Type != "CERTIFICATE" {
			return errors.New("CA certificate must contain PEM X.509 certificates only")
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return errors.New("CA certificate is not a valid PEM X.509 certificate")
		}
		certificates++
		data = rest
	}
	if certificates == 0 {
		return errors.New("CA certificate is empty")
	}
	return nil
}

func releaseArtifactURL(base, version, filename string) (string, error) {
	if version == "" || version == "." || version == ".." || strings.ContainsAny(version, "/\\\x00\r\n\t ") {
		return "", errors.New("invalid artifact release version")
	}
	if filename == "" || path.Base(filename) != filename || strings.ContainsAny(filename, "\\\x00\r\n") {
		return "", errors.New("invalid artifact filename")
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("invalid release artifact base URL")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + version + "/" + filename
	return u.String(), nil
}

func artifactHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Hour,
		Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 60 * time.Second},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many artifact download redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return errors.New("invalid artifact redirect protocol")
			}
			if via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("refusing insecure artifact redirect")
			}
			return nil
		},
	}
}

func downloadArtifact(ctx context.Context, opts *Options, base, filename, destination string) error {
	if opts.Offline {
		return errors.New("network artifact downloads are disabled for offline installation")
	}
	location, err := releaseArtifactURL(base, opts.Version, filename)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return err
	}
	client := artifactHTTPClient()
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s failed: %w", filename, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s returned HTTP %d", filename, response.StatusCode)
	}
	return writeAtomic(destination, response.Body, 0600)
}

func fetchReleaseManifest(ctx context.Context, opts *Options, base, name, directory string) (checksumManifest, error) {
	filename := filepath.Join(directory, name)
	if err := downloadArtifact(ctx, opts, base, name, filename); err != nil {
		return nil, err
	}
	data, err := readArtifactLimited(filename, 32<<20)
	if err != nil {
		return nil, err
	}
	return parseChecksumManifest(data)
}

type installArtifacts struct {
	directory  string
	template   string
	installer  string
	plugin     string
	ca         string
	license    string
	bginfo     string
	state      string
	isoDigests map[string][sha256.Size]byte
}

func (a *installArtifacts) close() {
	if a != nil {
		_ = os.RemoveAll(a.directory)
	}
}

func stageLocalArtifact(directory, source, name string, manifest checksumManifest) (string, error) {
	if source == "" {
		return "", nil
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "", fmt.Errorf("local input must be a readable, non-empty regular file: %s", source)
	}
	destination := filepath.Join(directory, name)
	if err := copyFile(source, destination, 0600); err != nil {
		return "", err
	}
	if manifest != nil {
		if err := manifest.verify(destination, filepath.Base(source)); err != nil {
			return "", err
		}
	}
	return destination, nil
}

func trustedDevelopmentExecutable(executable string) bool {
	trusted := os.Getenv("LUDUS_INSTALLER_BINARY")
	if trusted == "" {
		return false
	}
	a, err := os.Stat(trusted)
	if err != nil {
		return false
	}
	b, err := os.Stat(executable)
	return err == nil && os.SameFile(a, b)
}

func prepareArtifacts(ctx context.Context, opts *Options) (_ *installArtifacts, resultErr error) {
	manifest, err := loadLocalManifest(opts)
	if err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp("", "ludus-install-")
	if err != nil {
		return nil, err
	}
	artifacts := &installArtifacts{directory: directory, isoDigests: make(map[string][sha256.Size]byte)}
	defer func() {
		if resultErr != nil {
			artifacts.close()
		}
	}()
	if opts.Offline && (opts.TemplateFile == "" || opts.ISODirectory == "" || opts.BgInfoFile == "") {
		return nil, errors.New("offline installation requires --template-file, --iso-directory and --bginfo-file")
	}
	if opts.LicenseFile != "" && (opts.EnterprisePlugin == "" || opts.License == "" || opts.License == "community") {
		return nil, errors.New("--license-file requires --enterprise-plugin and an enterprise --license key")
	}
	var templateDigest [sha256.Size]byte
	if opts.TemplateFile != "" {
		templateDigest, err = hashArtifact(opts.TemplateFile)
		if err != nil {
			return nil, err
		}
		if manifest != nil {
			expected, err := manifest.digest(filepath.Base(opts.TemplateFile))
			if err != nil {
				return nil, err
			}
			if subtle.ConstantTimeCompare(templateDigest[:], expected[:]) != 1 {
				return nil, errors.New("local appliance checksum mismatch")
			}
		}
	}
	for _, entry := range []struct {
		source, name string
		target       *string
	}{
		{opts.EnterprisePlugin, "ludus-enterprise.zip", &artifacts.plugin},
		{opts.LicenseFile, "license.lic", &artifacts.license},
		{opts.BgInfoFile, "bginfo.exe", &artifacts.bginfo},
		{opts.ImportDB, "ludus-import.tar.gz", &artifacts.state},
	} {
		*entry.target, err = stageLocalArtifact(directory, entry.source, entry.name, manifest)
		if err != nil {
			return nil, err
		}
	}
	if artifacts.plugin != "" {
		if err := validateEnterprisePackage(artifacts.plugin); err != nil {
			return nil, err
		}
	}
	artifacts.ca, err = stageLocalArtifact(directory, opts.CACertificate, "injected-ca-certificate.crt", nil)
	if err != nil {
		return nil, err
	}
	if artifacts.ca != "" {
		data, err := readArtifactLimited(artifacts.ca, 16<<20)
		if err != nil {
			return nil, err
		}
		if err := validateCACertificate(data); err != nil {
			return nil, err
		}
	}
	if opts.Offline {
		for _, name := range airgappedISOFilenames {
			filename := filepath.Join(opts.ISODirectory, name)
			if manifest != nil {
				if err := manifest.verify(filename, name); err != nil {
					return nil, err
				}
				artifacts.isoDigests[name], err = manifest.digest(name)
			} else {
				artifacts.isoDigests[name], err = hashArtifact(filename)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate executing installer: %w", err)
	}
	artifacts.installer, err = stageLocalArtifact(directory, executable, "ludus-installer", nil)
	if err != nil {
		return nil, err
	}
	if !opts.SkipVerification && !trustedDevelopmentExecutable(executable) {
		installerManifest := manifest
		if installerManifest == nil {
			base, name := opts.ApplianceBase, "checksums.txt"
			if opts.Channel == "beta" {
				base, name = opts.ReleaseBase, "ludus_"+opts.Version+"_checksums.txt"
			}
			installerManifest, err = fetchReleaseManifest(ctx, opts, base, name, directory)
			if err != nil {
				return nil, err
			}
		}
		if err := installerManifest.verify(artifacts.installer, "ludus-installer"); err != nil {
			return nil, fmt.Errorf("executing installer provenance: %w", err)
		}
	}
	if err := os.Chmod(artifacts.installer, 0700); err != nil {
		return nil, err
	}
	name := "ludus-" + opts.Version + "-debian13-amd64.tar.zst"
	// Resolve the version through the URL validator even for local media: it is
	// also used as a filesystem component and must not escape the template cache.
	if _, err := releaseArtifactURL("https://lxc.ludus.cloud", opts.Version, name); err != nil {
		return nil, err
	}
	cache := "/var/lib/vz/template/cache"
	if err := os.MkdirAll(cache, 0755); err != nil {
		return nil, err
	}
	artifacts.template = filepath.Join(cache, name)
	if opts.TemplateFile != "" {
		sourceInfo, sourceErr := os.Stat(opts.TemplateFile)
		cacheInfo, cacheErr := os.Stat(artifacts.template)
		if sourceErr != nil || cacheErr != nil || !os.SameFile(sourceInfo, cacheInfo) {
			if err := copyFile(opts.TemplateFile, artifacts.template, 0644); err != nil {
				return nil, err
			}
		}
		cachedDigest, err := hashArtifact(artifacts.template)
		if err != nil {
			return nil, err
		}
		if subtle.ConstantTimeCompare(templateDigest[:], cachedDigest[:]) != 1 {
			return nil, errors.New("local appliance changed after verification")
		}
	} else {
		if opts.Offline {
			return nil, errors.New("offline installation cannot download an appliance")
		}
		var online checksumManifest
		if !opts.SkipVerification {
			online, err = fetchReleaseManifest(ctx, opts, opts.ApplianceBase, "checksums.txt", directory)
			if err != nil {
				return nil, err
			}
		}
		if !fileExists(artifacts.template) {
			logStep("Downloading LXC template %s", name)
			if err := downloadArtifact(ctx, opts, opts.ApplianceBase, name, artifacts.template); err != nil {
				return nil, err
			}
		}
		if online != nil {
			if err := online.verify(artifacts.template, name); err != nil {
				return nil, err
			}
		} else if _, err := hashArtifact(artifacts.template); err != nil {
			return nil, err
		}
	}
	return artifacts, nil
}
