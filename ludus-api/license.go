package ludusapi

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"ludusapi/dto"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/denisbrodbeck/machineid"
	"github.com/keygen-sh/keygen-go/v3"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/mod/semver"
)

const (
	LicenseURL                              = "https://license.ludus.cloud"
	LicenseAPIVersion                       = "1.7"
	LicenseAPIPrefix                        = "v1"
	LicenseProductLudus                     = "5722ca04-715d-4969-9130-a051532b7579"
	LicenseProductSubscriptionRolesMetadata = "7b75d702-0448-4d82-9963-2b1f1f460022"
	LicensePackageLudusEnterprisePlugin     = "a8ecdfa4-6cf7-4a7c-93cc-95fe44c94d14"
	LicensePackageLudusAntisandboxPlugin    = "a335f37d-e603-405c-8c99-0bb3185a87e8"
	LicenseAccount                          = "baaa4d02-5c5e-413d-8af1-f7846db1a838"
	LicensePublicKey                        = "70cb26141f38840b8f3f499d4875a829a9d251bd3337278995832b9ea4e39d12"
	BinaryPublicKey                         = "7990d22676174928335ce3b5eb96dd294b970fdb1427f9e4c0b84e9f8f9a9c50"
	EnterprisePluginFilename                = "ludus-enterprise.plugin"
	EnterprisePluginName                    = "Ludus Enterprise"
)

type licensedPluginSpec struct {
	name         string
	artifactBase string
	filename     string
	targetDir    string
	packageUUID  string
}

type pluginReleaseLookupFunc func(context.Context, string, string) (*keygen.Release, error)
type pluginReleaseDownloadFunc func(*keygen.Release, licensedPluginSpec) error

type licensedPluginReleases []keygen.Release

// SetData implements the JSON:API collection decoder required by the Keygen client.
func (releases *licensedPluginReleases) SetData(to func(interface{}) error) error {
	return to(releases)
}

func (s *Server) checkLicense() {
	keygen.Account = LicenseAccount
	keygen.Product = LicenseProductLudus
	keygen.LicenseKey = s.LicenseKey
	keygen.APIURL = LicenseURL
	keygen.UserAgent = "Ludus-Server/" + s.Version

	if os.Getenv("LUDUS_DEBUG_LICENSE") == "1" {
		keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)
	}

	fingerprint, err := machineid.ProtectedID(keygen.Product)
	if err != nil {
		log.Println("LICENSE: unable to get machine fingerprint:", err)
		s.LicenseValid = false
		s.LicenseMessage = "Unable to get machine fingerprint"
		return
	}
	ctx := context.Background()

	pluginsDir := s.enterprisePluginsDir()

	var license *keygen.License
	var entitlements keygen.Entitlements
	// If a license file exists, use it instead of the network license
	if FileExists(ludusInstallPath + "/license.lic") {
		log.Println("LICENSE: using license file instead of network license")
		keygen.PublicKey = LicensePublicKey
		licenseFile, err := os.ReadFile(ludusInstallPath + "/license.lic")
		if err != nil {
			log.Println("LICENSE: unable to read license file:", err)
			return
		}
		// Verify the license file's signature
		lic := &keygen.LicenseFile{Certificate: string(licenseFile)}
		err = lic.Verify()
		switch {
		case err == keygen.ErrLicenseFileNotGenuine:
			log.Println("LICENSE: license file is not genuine!")
			s.LicenseValid = false
			s.LicenseMessage = "license file is not genuine!"
		case err != nil:
			log.Println("LICENSE: unable to verify license file:", err)
			s.LicenseValid = false
			s.LicenseMessage = err.Error()
		}
		// Use the license key to decrypt the license file
		dataset, err := lic.Decrypt(s.LicenseKey)
		switch {
		case err == keygen.ErrSystemClockUnsynced:
			log.Println("LICENSE: system clock tampering detected!")
			s.LicenseValid = false
			s.LicenseMessage = "System clock tampering detected"
			return
		case err == keygen.ErrLicenseFileExpired:
			log.Println("LICENSE: license file is expired!")
			s.LicenseValid = false
			s.LicenseMessage = "License file is expired"
			return
		case err != nil:
			log.Println("LICENSE: unable to decrypt license file:", err)
			s.LicenseValid = false
			s.LicenseMessage = err.Error()
			return
		}

		license = &dataset.License
		entitlements = dataset.Entitlements
		if len(entitlements) == 0 {
			log.Println("LICENSE: no entitlements found in license file")
			s.Entitlements = []string{}
		}
	} else {
		// Validate the license for the current fingerprint
		license, err = keygen.Validate(ctx, fingerprint)
		switch {
		case err == keygen.ErrLicenseNotActivated:
			// Activate the current fingerprint
			_, err := license.Activate(ctx, fingerprint)
			switch {
			case err == keygen.ErrMachineLimitExceeded:
				log.Println("LICENSE: machine limit has been exceeded!")
				s.LicenseValid = false
				s.LicenseMessage = "Machine limit has been exceeded"
				return
			case err != nil:
				log.Printf("LICENSE: machine activation failed: %v\n", err)
				s.LicenseValid = false
				s.LicenseMessage = "Machine activation failed"
				return
			}
		case err == keygen.ErrLicenseExpired:
			log.Println("LICENSE: license is expired!")
			s.LicenseValid = false
			s.LicenseMessage = "License is expired"
			return
		case err != nil:
			var urlErr *url.Error
			if errors.As(err, &urlErr) || strings.Contains(err.Error(), "an error occurred") {
				log.Println("LICENSE: unable to connect to license server:", err)
				// If the enterprise plugin is not installed mark the license is not valid
				// The enterprise plugin can use a fallback on disk license if the network license fails
				if !FileExists(filepath.Join(pluginsDir, EnterprisePluginFilename)) {
					s.LicenseValid = false
					s.LicenseMessage = "Unable to connect to license server"
					return
				} else {
					log.Println("LICENSE: enterprise plugin is present, attempting to load it")
					err = os.Chmod(filepath.Join(pluginsDir, EnterprisePluginFilename), 0755)
					if err == nil {
						err = s.LoadPlugin(filepath.Join(pluginsDir, EnterprisePluginFilename))
					}
					if err != nil {
						log.Printf("LICENSE: error loading enterprise plugin as part of network fallback: %v", err)
					}
				}
			}
			log.Printf("LICENSE: %v\n", err)
			return
		}
		// Extract entitlements from license
		entitlements, err = license.Entitlements(ctx)
		if err != nil {
			log.Printf("LICENSE: unable to get entitlements: %v", err)
			s.Entitlements = []string{}
		}
	}

	s.LicenseValid = true
	if license.Expiry != nil {
		log.Printf("LICENSE: active, expires: %s, licensed to %s\n", license.Expiry.Format("2006-01-02 15:04:05"), license.Name)
		s.LicenseMessage = fmt.Sprintf("License active, expires: %s, licensed to %s", license.Expiry.Format("2006-01-02 15:04:05"), license.Name)
		s.LicenseName = license.Name
		s.LicenseExpiry = license.Expiry
	} else {
		log.Println("LICENSE: active, does not expire, licensed to", license.Name)
		s.LicenseMessage = fmt.Sprintf("License active, does not expire, licensed to %s", license.Name)
		s.LicenseName = license.Name
		s.LicenseExpiry = nil
	}

	s.Entitlements = make([]string, len(entitlements))
	for i, entitlement := range entitlements {
		s.Entitlements[i] = string(entitlement.Code)
	}
	log.Printf("LICENSE: found entitlements: %s", strings.Join(s.Entitlements, ", "))

	if err := s.refreshLicensedPlugins(ctx); err != nil {
		log.Printf("LICENSE: error refreshing licensed plugins: %v", err)
	}

	// The server will initialize plugins in the main function
	// s.InitializePlugins()
}

func (s *Server) refreshLicensedPlugins(ctx context.Context) error {
	var refreshErrors []error
	if s.HasEntitlement("ENTERPRISE_PLUGIN") {
		err := s.ensureLicensedPlugin(ctx, licensedPluginSpec{
			name:         EnterprisePluginName,
			artifactBase: "ludus-enterprise",
			filename:     EnterprisePluginFilename,
			targetDir:    s.enterprisePluginsDir(),
			packageUUID:  LicensePackageLudusEnterprisePlugin,
		})
		if err != nil {
			refreshErrors = append(refreshErrors, fmt.Errorf("refresh enterprise plugin: %w", err))
		}
	}
	return errors.Join(refreshErrors...)
}

func (s *Server) enterprisePluginsDir() string {
	if os.Geteuid() == 0 {
		return filepath.Join(ludusInstallPath, "plugins", "enterprise", "admin")
	}
	return filepath.Join(ludusInstallPath, "plugins", "enterprise")
}

func (s *Server) ensureLicensedPlugin(ctx context.Context, spec licensedPluginSpec) error {
	pluginPath := filepath.Join(spec.targetDir, spec.filename)
	if metadata, loaded := s.loadedPluginMetadata(spec.name); loaded {
		if isLocalPlugin(spec.targetDir) {
			return nil
		}
		currentVersion := metadata.Version
		if diskMetadata, err := readPluginMetadata(pluginPath, s.Logger); err == nil &&
			diskMetadata.Name == spec.name && diskMetadata.Version != "" {
			currentVersion = diskMetadata.Version
		}
		installed, err := s.installPluginUpdate(ctx, spec, currentVersion)
		if err != nil {
			return err
		}
		if installed {
			log.Printf("LICENSE: installed an updated %s plugin; restart Ludus to activate it", spec.name)
		}
		return nil
	}

	if FileExists(pluginPath) {
		if err := os.Chmod(pluginPath, 0755); err != nil {
			return fmt.Errorf("make plugin executable: %w", err)
		}
		metadata, probeErr := readPluginMetadata(pluginPath, s.Logger)
		if probeErr == nil && metadata.Name == spec.name {
			if !isLocalPlugin(spec.targetDir) {
				if installed, updateErr := s.installPluginUpdate(ctx, spec, metadata.Version); updateErr != nil {
					log.Printf("LICENSE: unable to check %s for updates; using installed version %s: %v", spec.name, metadata.Version, updateErr)
				} else if installed {
					log.Printf("LICENSE: updated %s before loading", spec.name)
				}
			}
			return s.LoadPlugin(pluginPath)
		}
		if probeErr == nil {
			probeErr = fmt.Errorf("plugin reported name %q, want %q", metadata.Name, spec.name)
		}
		log.Printf("LICENSE: installed %s plugin failed validation: %v", spec.name, probeErr)
		if isLocalPlugin(spec.targetDir) {
			return probeErr
		}
	}

	installed, err := s.installPluginUpdate(ctx, spec, "")
	if err != nil {
		return err
	}
	if !installed {
		return fmt.Errorf("no published release found for %s", spec.name)
	}
	if err := os.Chmod(pluginPath, 0755); err != nil {
		return fmt.Errorf("make downloaded plugin executable: %w", err)
	}
	return s.LoadPlugin(pluginPath)
}

func (s *Server) installPluginUpdate(ctx context.Context, spec licensedPluginSpec, currentVersion string) (bool, error) {
	lookup := s.pluginReleaseLookup
	if lookup == nil {
		lookup = s.lookupLicensedPluginRelease
	}
	release, err := lookup(ctx, currentVersion, spec.packageUUID)
	if err != nil {
		return false, fmt.Errorf("check %s release: %w", spec.name, err)
	}
	if release == nil {
		return false, nil
	}
	if release.ID == "" || release.Version == "" {
		return false, fmt.Errorf("Keygen returned an incomplete release for %s", spec.name)
	}

	download := s.pluginReleaseDownload
	if download == nil {
		download = s.downloadPluginRelease
	}
	log.Printf("LICENSE: downloading %s plugin release %s", spec.name, release.Version)
	if err := download(release, spec); err != nil {
		return false, fmt.Errorf("download %s release %s: %w", spec.name, release.Version, err)
	}
	return true, nil
}

func (s *Server) lookupLicensedPluginRelease(ctx context.Context, currentVersion, packageUUID string) (*keygen.Release, error) {
	client := newLicenseClient(s.Version, s.LicenseKey)
	return lookupLicensedPluginRelease(ctx, client, currentVersion, packageUUID)
}

func lookupLicensedPluginRelease(ctx context.Context, client *keygen.Client, currentVersion, packageUUID string) (*keygen.Release, error) {
	query := url.Values{
		"package": {packageUUID},
		"product": {LicenseProductLudus},
		"channel": {"stable"},
	}
	releaseVersion := strings.TrimPrefix(strings.TrimSpace(currentVersion), "v")
	var discovered *keygen.Release
	if !semver.IsValid("v" + releaseVersion) {
		query.Set("status", "PUBLISHED")
		query.Set("limit", "1")
		var releases licensedPluginReleases
		if _, err := client.Get(ctx, "releases?"+query.Encode(), nil, &releases); err != nil {
			return nil, err
		}
		if len(releases) == 0 {
			return nil, nil
		}
		discovered = &releases[0]
		if discovered.ID == "" || discovered.Version == "" {
			return nil, fmt.Errorf("Keygen returned an incomplete release for package %s", packageUUID)
		}
		// Listing is creation-date ordered. Upgrade from a real release to find
		// the semantic latest, or keep that release if no upgrade exists.
		releaseVersion = discovered.ID
		query.Del("status")
		query.Del("limit")
	}
	path := fmt.Sprintf("releases/%s/upgrade?%s", url.PathEscape(releaseVersion), query.Encode())

	release := &keygen.Release{}
	if _, err := client.Get(ctx, path, nil, release); err != nil {
		var notFound *keygen.NotFoundError
		if errors.As(err, &notFound) {
			return discovered, nil
		}
		return nil, err
	}
	return release, nil
}

func (s *Server) downloadPluginRelease(release *keygen.Release, spec licensedPluginSpec) error {
	artifact := fmt.Sprintf("%s_%s.plugin", spec.artifactBase, release.Version)
	path := fmt.Sprintf("releases/%s/artifacts/%s", url.PathEscape(release.ID), url.PathEscape(artifact))
	candidateName := "." + spec.filename + ".update"
	candidatePath := filepath.Join(spec.targetDir, candidateName)
	defer os.Remove(candidatePath)

	if err := DownloadFileUsingLicenseKey(path, candidateName, spec.targetDir, s.Version, s.LicenseKey, spec.packageUUID); err != nil {
		return err
	}
	if err := os.Chmod(candidatePath, 0755); err != nil {
		return fmt.Errorf("make plugin update executable: %w", err)
	}
	return s.activatePluginUpdate(candidatePath, filepath.Join(spec.targetDir, spec.filename), release.Version, spec.name)
}

func (s *Server) activatePluginUpdate(candidatePath, targetPath, expectedVersion, expectedName string) error {
	metadata, err := readPluginMetadata(candidatePath, s.Logger)
	if err != nil {
		return fmt.Errorf("validate plugin update protocol: %w", err)
	}
	if metadata.Name != expectedName {
		return fmt.Errorf("plugin update reported name %q, want %q", metadata.Name, expectedName)
	}
	if !samePluginVersion(metadata.Version, expectedVersion) {
		return fmt.Errorf("plugin update reported version %q, want %q", metadata.Version, expectedVersion)
	}
	if err := os.Rename(candidatePath, targetPath); err != nil {
		return fmt.Errorf("activate plugin update: %w", err)
	}
	return nil
}

func samePluginVersion(pluginVersion, releaseVersion string) bool {
	normalize := func(version string) string {
		if version != "" && !strings.HasPrefix(version, "v") {
			return "v" + version
		}
		return version
	}
	pluginVersion = normalize(pluginVersion)
	releaseVersion = normalize(releaseVersion)
	if semver.IsValid(pluginVersion) && semver.IsValid(releaseVersion) {
		return semver.Compare(pluginVersion, releaseVersion) == 0
	}
	return pluginVersion == releaseVersion
}

func isLocalPlugin(targetDir string) bool {
	_, err := os.Stat(filepath.Join(targetDir, ".local-testing"))
	return err == nil
}

func GetSubscriptionRolesMetadata(e *core.RequestEvent) ([]dto.GetSubscriptionRolesResponseItem, error) {

	// Check the request cache first
	if e != nil {
		subscriptionRolesFromCache := e.Get("rolesJSON")
		if subscriptionRolesFromCache != nil {
			return subscriptionRolesFromCache.([]dto.GetSubscriptionRolesResponseItem), nil
		}
	}

	// First get the version of the role from latest
	err := DownloadFileUsingLicenseKey("roles.json", "roles.json", "/tmp", server.Version, server.LicenseKey, LicenseProductSubscriptionRolesMetadata)
	if err != nil {
		return nil, err
	}
	rolesJSON, err := os.ReadFile("/tmp/roles.json")
	if err != nil {
		return nil, err
	}
	// Read the json into an array of GetSubscriptionRolesResponseItem
	var subscriptionRoles []dto.GetSubscriptionRolesResponseItem
	err = json.Unmarshal(rolesJSON, &subscriptionRoles)
	if err != nil {
		return nil, err
	}
	if e != nil {
		e.Set("rolesJSON", subscriptionRoles)
	}
	return subscriptionRoles, nil
}

func DownloadRoleUsingLicenseKey(e *core.RequestEvent, roleName string, targetDir string) (string, error) {

	// Get the subscription roles metadata
	subscriptionRoles, err := GetSubscriptionRolesMetadata(e)
	if err != nil {
		return "", err
	}

	// Find the version of the role
	var roleVersion string
	var rolePackageUUID string
	for _, role := range subscriptionRoles {
		if role.Role == roleName {
			roleVersion = role.Version
			rolePackageUUID = role.PackageUUID
			break
		}
	}
	if roleVersion == "" || rolePackageUUID == "" {
		return "", errors.New("role " + roleName + " not found")
	}

	roleFileName := fmt.Sprintf("%s_v%s.tar.gz", roleName, roleVersion)
	err = DownloadFileUsingLicenseKey(roleFileName, roleFileName, targetDir, server.Version, server.LicenseKey, rolePackageUUID)
	if err != nil {
		return "", err
	}
	return roleFileName, nil
}

func DownloadFileUsingLicenseKey(path string, fileName string, targetDir string, version string, licenseKey string, packageUUID string) error {
	if !strings.HasPrefix(path, "artifacts/") &&
		!strings.HasPrefix(path, "/artifacts/") &&
		!strings.HasPrefix(path, "releases/") &&
		!strings.HasPrefix(path, "/releases/") {
		path = "artifacts/" + path
	}
	path = strings.TrimPrefix(path, "/")

	if isLocalPlugin(targetDir) {
		log.Printf("LICENSE: In local-testing mode (%s/.local-testing exists), skipping file download\n", targetDir)
		return nil
	}

	client := newLicenseClient(version, licenseKey)
	if os.Getenv("LUDUS_DEBUG_LICENSE") == "1" {
		keygen.Logger = keygen.NewLogger(keygen.LogLevelDebug)
	}

	artifact := &keygen.Artifact{}
	response, err := client.Get(context.Background(), path, nil, artifact)
	if err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to download file %s: %v", fileName, err))
		return err
	}
	artifact.URL = response.Headers.Get("Location")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to create target directory: %v", err))
		return err
	}
	targetPath := filepath.Join(targetDir, fileName)
	if err := installDownloadedArtifact(http.DefaultClient, artifact, targetPath, BinaryPublicKey, packageUUID); err != nil {
		logger.Error(fmt.Sprintf("LICENSE: unable to install downloaded file %s: %v", fileName, err))
		return err
	}
	logger.Debug(fmt.Sprintf("LICENSE: successfully verified signature for %s target file", fileName))
	return nil
}

func newLicenseClient(version, licenseKey string) *keygen.Client {
	return keygen.NewClientWithOptions(&keygen.ClientOptions{
		Account:    LicenseAccount,
		APIURL:     LicenseURL,
		PublicKey:  LicensePublicKey,
		APIPrefix:  LicenseAPIPrefix,
		APIVersion: LicenseAPIVersion,
		UserAgent:  "Ludus-Server/" + version,
		LicenseKey: licenseKey,
	})
}

func installDownloadedArtifact(httpClient *http.Client, artifact *keygen.Artifact, targetPath, publicKey, signatureContext string) error {
	if artifact.URL == "" {
		return errors.New("artifact download URL is empty")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	tempFile, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".*")
	if err != nil {
		return fmt.Errorf("create temporary artifact: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)
	defer tempFile.Close()

	response, err := httpClient.Get(artifact.URL)
	if err != nil {
		return fmt.Errorf("download artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download artifact: unexpected HTTP status %s", response.Status)
	}
	if _, err := io.Copy(tempFile, response.Body); err != nil {
		return fmt.Errorf("write temporary artifact: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync temporary artifact: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close temporary artifact: %w", err)
	}
	if err := os.Chmod(tempPath, 0644); err != nil {
		return fmt.Errorf("set artifact permissions: %w", err)
	}
	if err := VerifySignature(tempPath, artifact.Signature, publicKey, signatureContext); err != nil {
		return fmt.Errorf("verify artifact signature: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	return nil
}

func VerifySignature(filePath string, signatureString string, publicKeyHex string, context string) error {

	signature, err := base64.RawStdEncoding.DecodeString(signatureString)
	if err != nil {
		return err
	}

	// Read and hash the file content
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Create SHA-512 hash of file contents
	h := crypto.SHA512.New()
	if _, err := io.Copy(h, file); err != nil {
		return err
	}
	checksum := h.Sum(nil)

	// Decode the public key from hex
	publicKey, err := hex.DecodeString(publicKeyHex)
	if err != nil {
		return errors.New("failed to decode ed25519ph public key")
	}

	// Verify public key length
	if l := len(publicKey); l != ed25519.PublicKeySize {
		return errors.New("invalid ed25519ph public key")
	}

	// Set up verification options with context
	opts := &ed25519.Options{
		Hash:    crypto.SHA512,
		Context: context,
	}

	// Verify the signature
	err = ed25519.VerifyWithOptions(publicKey, checksum, signature, opts)
	if err != nil {
		// Fall back to the Ludus product UUID
		if strings.Contains(err.Error(), "invalid signature") {
			opts := &ed25519.Options{
				Hash:    crypto.SHA512,
				Context: LicenseProductLudus,
			}
			err = ed25519.VerifyWithOptions(publicKey, checksum, signature, opts)
			if err != nil {
				return fmt.Errorf("failed to verify ed25519ph signature with package and product UUID: %v", err)
			} else {
				return nil
			}
		}
		return fmt.Errorf("failed to verify ed25519ph signature: %v", err)
	}

	return nil
}
