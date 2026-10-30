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
	"slices"
	"strings"

	"github.com/denisbrodbeck/machineid"
	"github.com/keygen-sh/keygen-go/v3"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
	"golang.org/x/mod/semver"
)

const (
	LicenseURL          = "https://license.ludus.cloud"
	LicenseAPIVersion   = "1.7"
	LicenseAPIPrefix    = "v1"
	LicenseProductLudus = "5722ca04-715d-4969-9130-a051532b7579"
	// LicenseProductMetadata is the OPEN metadata product: roles.json, the
	// plugins.json catalog, and the free official plugins, downloadable
	// without a license.
	LicenseProductMetadata              = "7b75d702-0448-4d82-9963-2b1f1f460022"
	LicensePackageLudusEnterprisePlugin = "a8ecdfa4-6cf7-4a7c-93cc-95fe44c94d14"
	LicenseAccount                      = "baaa4d02-5c5e-413d-8af1-f7846db1a838"
	LicensePublicKey                    = "70cb26141f38840b8f3f499d4875a829a9d251bd3337278995832b9ea4e39d12"
	BinaryPublicKey                     = "7990d22676174928335ce3b5eb96dd294b970fdb1427f9e4c0b84e9f8f9a9c50"
)

const (
	enterprisePluginID    = "ludus-enterprise"
	enterpriseEntitlement = "ENTERPRISE_PLUGIN"
)

// licensedPluginSpec is a package the license flow installs into
// <install>/plugins/<id>/ from signature-verified <id>_<version>.zip releases
// of a Keygen package.
type licensedPluginSpec struct {
	id          string
	productID   string
	packageUUID string
	// entitlements are any-of; without any the package is free and its
	// releases are downloaded anonymously from the OPEN product.
	entitlements []string
}

// enterprisePluginSpec is built in: the license flow installs Enterprise once,
// when the license grants ENTERPRISE_PLUGIN and it is not installed.
var enterprisePluginSpec = licensedPluginSpec{id: enterprisePluginID, productID: LicenseProductLudus, packageUUID: LicensePackageLudusEnterprisePlugin, entitlements: []string{enterpriseEntitlement}}

// licenseKey returns the key a release download authenticates with: none for
// free packages.
func (spec licensedPluginSpec) licenseKey(key string) string {
	if len(spec.entitlements) == 0 {
		return ""
	}
	return key
}

type pluginReleaseLookupFunc func(ctx context.Context, spec licensedPluginSpec, currentVersion string) (*keygen.Release, error)
type pluginReleaseDownloadFunc func(*keygen.Release, licensedPluginSpec) error

type licensedPluginReleases []keygen.Release

// SetData implements the JSON:API collection decoder required by the Keygen client.
func (releases *licensedPluginReleases) SetData(to func(interface{}) error) error {
	return to(releases)
}

func (s *Server) checkLicense() {
	if s.Entitlements == nil {
		s.Entitlements = []string{}
	}

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

	enterpriseManifest := filepath.Join(s.licensedPackageDir(enterprisePluginID), protocol.ManifestFile)

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
			case errors.Is(err, keygen.ErrMachineAlreadyActivated):
				log.Println("LICENSE: machine is already activated; continuing license check")
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
				// Without the Enterprise package the license is not valid. With it,
				// Enterprise supplies the license state from its on-disk fallback:
				// directly in the admin service, through license-state.json here.
				if !FileExists(enterpriseManifest) {
					s.LicenseValid = false
					s.LicenseMessage = "Unable to connect to license server"
					return
				}
				log.Println("LICENSE: the Enterprise package is installed; it supplies the license state")
			} else {
				s.LicenseValid = false
				s.LicenseMessage = err.Error()
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

	// Only the admin service runs the license flow; it installs root-owned packages.
	if pluginServiceRunAs() == protocol.RunAsRoot {
		if err := s.installMissingEnterprise(ctx); err != nil {
			log.Printf("LICENSE: could not install %s: %v", enterprisePluginID, err)
		}
	}
}

func (s *Server) licensedPackageDir(id string) string {
	return filepath.Join(s.pluginsRoot(), id)
}

// installMissingEnterprise installs Enterprise when the license grants
// ENTERPRISE_PLUGIN and no Enterprise package is installed. It never updates
// an installed package: plugins are only updated by an administrator, from
// the official plugin catalog.
func (s *Server) installMissingEnterprise(ctx context.Context) error {
	if !s.HasEntitlement(enterpriseEntitlement) {
		return nil
	}
	_, err := s.ensureLicensedPlugin(ctx, enterprisePluginSpec, false)
	return err
}

// ensureLicensedPlugin installs the newest release of a licensed package
// that is not installed, or, with update, also updates the installed one. A
// .local-testing marker in the package directory disables both. It reports
// whether it wrote a new version.
func (s *Server) ensureLicensedPlugin(ctx context.Context, spec licensedPluginSpec, update bool) (bool, error) {
	s.licensedInstallMu.Lock()
	defer s.licensedInstallMu.Unlock()
	dir := s.licensedPackageDir(spec.id)
	if isLocalPlugin(dir) {
		return false, nil
	}
	currentVersion := ""
	manifest, err := readPackageManifest(dir)
	switch {
	case err == nil && manifest.ID == spec.id:
		currentVersion = manifest.Version
	case err == nil:
		log.Printf("LICENSE: installed %s package declares ID %q; reinstalling", spec.id, manifest.ID)
	case !errors.Is(err, os.ErrNotExist):
		log.Printf("LICENSE: installed %s package is invalid; reinstalling: %v", spec.id, err)
	}
	if currentVersion != "" && !update {
		return false, nil
	}
	installed, err := s.installPluginUpdate(ctx, spec, currentVersion)
	if err != nil {
		return false, err
	}
	if !installed && currentVersion == "" {
		return false, fmt.Errorf("no published release found for %s", spec.id)
	}
	return installed, nil
}

// reconcileLicensedPackages aligns the licensed plugins this service hosts
// with the installed packages and the license: it starts installed, entitled
// packages that are not running, restarts those whose installed declaration
// changed, and stops those that were removed or are no longer entitled.
// Enterprise is never stopped, as it supplies the license itself; it is only
// restarted to run an updated package. The new process starts with the
// current license state and re-applies it from its license check. Callers
// refresh plugin declarations first.
func (s *Server) reconcileLicensedPackages(packages []installedPackage) {
	s.licensedReconcileMu.Lock()
	defer s.licensedReconcileMu.Unlock()
	installed := map[string]installedPackage{}
	for _, pkg := range packages {
		if pkg.source == protocol.SourceLicensed && pkg.manifest.Executable != "" && pkg.manifest.RunAs == pluginServiceRunAs() {
			installed[pkg.manifest.ID] = pkg
		}
	}
	for _, plugin := range s.pluginSnapshot() {
		if plugin.source != protocol.SourceLicensed {
			continue
		}
		pkg, ok := installed[plugin.manifest.ID]
		if !ok && plugin.manifest.ID == enterprisePluginID {
			continue
		}
		block := s.licensedPackageBlock(plugin.manifest.ID)
		if ok && block == "" && sameManifest(plugin.manifest, pkg.manifest) {
			continue
		}
		switch {
		case !ok:
			s.logger().Info("Stopping removed licensed plugin", "plugin", plugin.manifest.ID)
		case block != "":
			s.logger().Info("Stopping licensed plugin", "plugin", plugin.manifest.ID, "reason", block)
		default:
			s.logger().Info("Restarting updated licensed plugin", "plugin", plugin.manifest.ID, "version", pkg.manifest.Version)
		}
		s.stopPlugin(plugin, true)
	}
	for _, pkg := range installed {
		if s.pluginByKey(pluginKey(pkg.source, pkg.manifest.ID, "")) != nil {
			continue
		}
		if block := s.licensedPackageBlock(pkg.manifest.ID); block != "" {
			s.logger().Info("Not starting licensed plugin", "plugin", pkg.manifest.ID, "reason", block)
			continue
		}
		s.startInstalledPackage(pkg)
	}
}

// licenseSnapshot returns a copy of the current license state.
func (s *Server) licenseSnapshot() protocol.License {
	s.licenseMu.RLock()
	defer s.licenseMu.RUnlock()
	license := protocol.License{
		Valid:        s.LicenseValid,
		Message:      s.LicenseMessage,
		Name:         s.LicenseName,
		Entitlements: slices.Clone(s.Entitlements),
	}
	if license.Entitlements == nil {
		license.Entitlements = []string{}
	}
	if s.LicenseExpiry != nil {
		expiry := *s.LicenseExpiry
		license.Expiry = &expiry
	}
	return license
}

func (s *Server) applyLicense(state protocol.License) {
	s.licenseMu.Lock()
	defer s.licenseMu.Unlock()
	// Enterprise retains cached grants on offline fallback. An authoritative
	// empty list means the grants have been revoked.
	s.Entitlements = slices.Clone(state.Entitlements)
	if s.Entitlements == nil {
		s.Entitlements = []string{}
	}
	s.LicenseValid = state.Valid
	s.LicenseMessage = state.Message
	s.LicenseName = state.Name
	s.LicenseExpiry = nil
	if state.Expiry != nil {
		expiry := *state.Expiry
		s.LicenseExpiry = &expiry
	}
}

// setLicenseFromPlugin applies the license state Enterprise reports. The admin
// service also hands it to the regular service through license-state.json.
// Each service's minute job then starts or stops the licensed plugins it
// hosts to match.
func (s *Server) setLicenseFromPlugin(state protocol.License) {
	s.applyLicense(state)
	if pluginServiceRunAs() != protocol.RunAsRoot {
		return
	}
	if err := s.writeLicenseState(state); err != nil {
		s.logger().Error("Could not write the license state file", "error", err)
	}
}

func (s *Server) licenseStatePath() string {
	return filepath.Join(s.installPath(), "install", "license-state.json")
}

func (s *Server) writeLicenseState(state protocol.License) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return writeSharedInstallFile(s.licenseStatePath(), data)
}

// writeSharedInstallFile atomically replaces a world-readable file the other
// Ludus service reads.
func writeSharedInstallFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0644); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// loadLicenseState applies the license state the admin service wrote. The
// regular service reloads it every minute; root never reads it.
func (s *Server) loadLicenseState() error {
	data, err := os.ReadFile(s.licenseStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var state protocol.License
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("decode license state: %w", err)
	}
	s.applyLicense(state)
	return nil
}

func (s *Server) installPluginUpdate(ctx context.Context, spec licensedPluginSpec, currentVersion string) (bool, error) {
	lookup := s.pluginReleaseLookup
	if lookup == nil {
		lookup = s.lookupLicensedPluginRelease
	}
	release, err := lookup(ctx, spec, currentVersion)
	if err != nil {
		return false, fmt.Errorf("check %s release: %w", spec.id, err)
	}
	if release == nil {
		return false, nil
	}
	if release.ID == "" || release.Version == "" {
		return false, fmt.Errorf("Keygen returned an incomplete release for %s", spec.id)
	}

	download := s.pluginReleaseDownload
	if download == nil {
		download = s.downloadPluginRelease
	}
	log.Printf("LICENSE: downloading %s release %s", spec.id, release.Version)
	if err := download(release, spec); err != nil {
		return false, fmt.Errorf("download %s release %s: %w", spec.id, release.Version, err)
	}
	return true, nil
}

func (s *Server) lookupLicensedPluginRelease(ctx context.Context, spec licensedPluginSpec, currentVersion string) (*keygen.Release, error) {
	client := newLicenseClient(s.Version, spec.licenseKey(s.LicenseKey))
	return lookupLicensedPluginRelease(ctx, client, currentVersion, spec.productID, spec.packageUUID)
}

func lookupLicensedPluginRelease(ctx context.Context, client *keygen.Client, currentVersion, productID, packageUUID string) (*keygen.Release, error) {
	query := url.Values{
		"package": {packageUUID},
		"product": {productID},
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
	artifact := fmt.Sprintf("%s_%s.zip", spec.id, release.Version)
	path := fmt.Sprintf("releases/%s/artifacts/%s", url.PathEscape(release.ID), url.PathEscape(artifact))
	if err := os.MkdirAll(s.pluginsRoot(), 0755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(s.pluginsRoot(), ".download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	if err := DownloadFileUsingLicenseKey(context.Background(), path, artifact, staging, s.Version, spec.licenseKey(s.LicenseKey), spec.packageUUID, spec.productID); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(staging, artifact))
	if err != nil {
		return err
	}
	return s.installLicensedPackage(data, spec, release.Version)
}

// routeConflictError marks a release refused because a core route or an
// active plugin already claims one of its API routes.
type routeConflictError struct{ err error }

func (e *routeConflictError) Error() string { return e.err.Error() }
func (e *routeConflictError) Unwrap() error { return e.err }

// installLicensedPackage writes a signature-verified release ZIP into
// <install>/plugins/<id>/ with a licensed approval. Declarations come from
// plugin.json; nothing is executed.
func (s *Server) installLicensedPackage(data []byte, spec licensedPluginSpec, expectedVersion string) error {
	pkg, err := readPluginPackage(data)
	if err != nil {
		return fmt.Errorf("validate %s release package: %w", spec.id, err)
	}
	if pkg.manifest.ID != spec.id {
		return fmt.Errorf("release package declares ID %q, want %q", pkg.manifest.ID, spec.id)
	}
	if !samePluginVersion(pkg.manifest.Version, expectedVersion) {
		return fmt.Errorf("release package declares version %q, want %q", pkg.manifest.Version, expectedVersion)
	}
	if pkg.manifest.Executable == "" {
		return fmt.Errorf("release package for %s has no executable", spec.id)
	}
	// The installed version of the same package is replaced, so its own routes
	// are not a conflict.
	if err := apiRouteConflict(pkg.manifest, pluginKey(protocol.SourceLicensed, spec.id, ""), s.declaredAPIRoutes()); err != nil {
		return &routeConflictError{err: err}
	}
	return writeRootPackage(s.licensedPackageDir(spec.id), pkg, pluginApproval{Source: protocol.SourceLicensed})
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
	err := DownloadFileUsingLicenseKey(context.Background(), "roles.json", "roles.json", "/tmp", server.Version, server.LicenseKey, LicenseProductMetadata)
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
	err = DownloadFileUsingLicenseKey(context.Background(), roleFileName, roleFileName, targetDir, server.Version, server.LicenseKey, rolePackageUUID)
	if err != nil {
		return "", err
	}
	return roleFileName, nil
}

// DownloadFileUsingLicenseKey downloads a Keygen artifact into targetDir and
// verifies its ed25519ph signature. An empty licenseKey downloads anonymously,
// as OPEN products allow. The signature may use any of signatureContexts or
// either product UUID.
func DownloadFileUsingLicenseKey(ctx context.Context, path string, fileName string, targetDir string, version string, licenseKey string, signatureContexts ...string) error {
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
	response, err := client.Get(ctx, path, nil, artifact)
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
	contexts := append(slices.Clone(signatureContexts), LicenseProductLudus, LicenseProductMetadata)
	if err := installDownloadedArtifact(http.DefaultClient, artifact, targetPath, BinaryPublicKey, contexts...); err != nil {
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

func installDownloadedArtifact(httpClient *http.Client, artifact *keygen.Artifact, targetPath, publicKey string, signatureContexts ...string) error {
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
	if err := VerifySignature(tempPath, artifact.Signature, publicKey, signatureContexts...); err != nil {
		return fmt.Errorf("verify artifact signature: %w", err)
	}
	if err := os.Rename(tempPath, targetPath); err != nil {
		return fmt.Errorf("replace artifact: %w", err)
	}
	return nil
}

// VerifySignature checks an ed25519ph signature of the file, accepting any of
// the given signing contexts (Keygen signs with the package or product UUID).
func VerifySignature(filePath string, signatureString string, publicKeyHex string, contexts ...string) error {
	if len(contexts) == 0 {
		return errors.New("no ed25519ph signature context to verify with")
	}
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

	for _, context := range contexts {
		opts := &ed25519.Options{Hash: crypto.SHA512, Context: context}
		if err = ed25519.VerifyWithOptions(publicKey, checksum, signature, opts); err == nil {
			return nil
		}
	}
	return fmt.Errorf("failed to verify ed25519ph signature with the package or product UUID: %v", err)
}
