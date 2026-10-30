package ludusapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"ludusapi/dto"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
	"golang.org/x/mod/semver"
)

// The official plugin catalog is plugins.json, a signed artifact of the OPEN
// metadata product, fetched like roles.json. Anyone may fetch it without a
// license key. Each entry names the Keygen product and package its releases
// come from and the entitlements that unlock it.

const (
	pluginCatalogSchemaVersion = 1
	pluginCatalogArtifact      = "plugins.json"
	pluginCatalogTTL           = 5 * time.Minute
	// pluginCatalogRetryDelay is how long a cached copy is served after a
	// failed fetch before Keygen is asked again.
	pluginCatalogRetryDelay   = time.Minute
	pluginCatalogFetchTimeout = 30 * time.Second

	proPluginsEntitlement        = "PRO_PLUGINS"
	enterprisePluginsEntitlement = "ENTERPRISE_PLUGINS"
)

// Reasons a catalog entry cannot be installed (unavailableReason).
const (
	catalogUnavailableLicense      = "license"
	catalogUnavailableEntitlement  = "entitlement"
	catalogUnavailableLudusVersion = "ludusVersion"
	catalogUnavailablePermission   = "permission"
)

type pluginCatalogEntry struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Author          string         `json:"author"`
	Version         string         `json:"version"`
	ProductID       string         `json:"productID"`
	PackageID       string         `json:"packageID"`
	Entitlements    []string       `json:"entitlements"`
	Level           protocol.Level `json:"level"`
	RunAs           protocol.RunAs `json:"runAs"`
	HasUI           bool           `json:"hasUI"`
	DocsURL         string         `json:"docsURL"`
	MinLudusVersion string         `json:"minLudusVersion"`
}

type pluginCatalog struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Plugins       []pluginCatalogEntry `json:"plugins"`
}

// pluginCatalogSnapshot is an immutable verified catalog and when it was
// fetched from Keygen.
type pluginCatalogSnapshot struct {
	catalog   *pluginCatalog
	fetchedAt time.Time
}

// parsePluginCatalog decodes and validates plugins.json. Unknown fields are
// ignored; another schema version is rejected.
func parsePluginCatalog(data []byte) (*pluginCatalog, error) {
	var catalog pluginCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode plugin catalog: %w", err)
	}
	if catalog.SchemaVersion != pluginCatalogSchemaVersion {
		return nil, fmt.Errorf("unsupported plugin catalog schema version %d", catalog.SchemaVersion)
	}
	seen := map[string]bool{}
	for i := range catalog.Plugins {
		entry := &catalog.Plugins[i]
		if entry.Entitlements == nil {
			entry.Entitlements = []string{}
		}
		if err := entry.validate(); err != nil {
			return nil, err
		}
		if seen[entry.ID] {
			return nil, fmt.Errorf("plugin catalog lists %q twice", entry.ID)
		}
		seen[entry.ID] = true
	}
	if catalog.Plugins == nil {
		catalog.Plugins = []pluginCatalogEntry{}
	}
	return &catalog, nil
}

func (entry pluginCatalogEntry) validate() error {
	var problem string
	switch {
	case !protocol.ValidPluginID(entry.ID):
		problem = "invalid id"
	case strings.TrimSpace(entry.Name) == "":
		problem = "missing name"
	case normalizedSemver(entry.Version) == "":
		problem = "invalid version"
	case entry.ProductID == "" || entry.PackageID == "":
		problem = "missing productID or packageID"
	case entry.Level != protocol.LevelStandard && entry.Level != protocol.LevelSystem:
		problem = "invalid level"
	case entry.RunAs != protocol.RunAsLudus && entry.RunAs != protocol.RunAsRoot:
		problem = "invalid runAs"
	case entry.MinLudusVersion != "" && normalizedSemver(entry.MinLudusVersion) == "":
		problem = "invalid minLudusVersion"
	case slices.Contains(entry.Entitlements, ""):
		problem = "empty entitlement code"
	default:
		return nil
	}
	return fmt.Errorf("plugin catalog entry %q: %s", entry.ID, problem)
}

func (catalog *pluginCatalog) entry(id string) *pluginCatalogEntry {
	for i := range catalog.Plugins {
		if catalog.Plugins[i].ID == id {
			return &catalog.Plugins[i]
		}
	}
	return nil
}

// spec is the licensed package the entry installs.
func (entry pluginCatalogEntry) spec() licensedPluginSpec {
	return licensedPluginSpec{id: entry.ID, productID: entry.ProductID, packageUUID: entry.PackageID, entitlements: entry.Entitlements}
}

// normalizedSemver returns version as a valid "v"-prefixed semantic version,
// or "" when it is not one.
func normalizedSemver(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return ""
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	if !semver.IsValid(version) {
		return ""
	}
	return version
}

// ludusVersionAtLeast reports whether the server version satisfies a
// minLudusVersion. Prereleases and builds of a version count as that version;
// development builds without a semantic version satisfy every minimum.
func ludusVersionAtLeast(serverVersion, minimum string) bool {
	required := normalizedSemver(minimum)
	current := normalizedSemver(serverVersion)
	if required == "" || current == "" {
		return true
	}
	release := func(version string) string {
		canonical := semver.Canonical(version)
		return strings.TrimSuffix(canonical, semver.Prerelease(canonical))
	}
	return semver.Compare(release(current), release(required)) >= 0
}

// newerVersion reports whether candidate is a newer semantic version than installed.
func newerVersion(candidate, installed string) bool {
	candidate, installed = normalizedSemver(candidate), normalizedSemver(installed)
	return candidate != "" && installed != "" && semver.Compare(candidate, installed) > 0
}

// entitlementShortfall reports why a license does not satisfy any-of
// entitlement codes: "" when it does (always when none are required),
// "license" when the license is not valid, "entitlement" when it holds none of
// the codes. ENTERPRISE_PLUGINS also satisfies PRO_PLUGINS.
func entitlementShortfall(required []string, license protocol.License) string {
	if len(required) == 0 {
		return ""
	}
	if !license.Valid {
		return catalogUnavailableLicense
	}
	for _, code := range required {
		if slices.Contains(license.Entitlements, code) ||
			(code == proPluginsEntitlement && slices.Contains(license.Entitlements, enterprisePluginsEntitlement)) {
			return ""
		}
	}
	return catalogUnavailableEntitlement
}

func requiresEntitlementMessage(required []string) string {
	return "Requires the " + strings.Join(required, " or ") + " entitlement"
}

// catalogEntryUnavailable reports why this server cannot install or update an
// entry, whoever asks: "license", "entitlement", "ludusVersion", or "".
func (s *Server) catalogEntryUnavailable(entry pluginCatalogEntry, license protocol.License) string {
	if reason := entitlementShortfall(entry.Entitlements, license); reason != "" {
		return reason
	}
	if !ludusVersionAtLeast(s.Version, entry.MinLudusVersion) {
		return catalogUnavailableLudusVersion
	}
	return ""
}

// canInstallCatalogPlugin decides who may install official plugins:
// administrators today. A nil entry asks whether the caller may install
// official plugins at all.
func canInstallCatalogPlugin(auth *core.Record, entry *pluginCatalogEntry) bool {
	return pluginAdmin(auth)
}

// licensedPackageBlock explains why an installed licensed package must not
// run: the license no longer satisfies its catalog entitlements. Enterprise
// supplies the license state itself and is never blocked. Without a known
// catalog entry the entitlements cannot be checked, so the package is blocked.
// It uses the cached catalog only and never contacts Keygen.
func (s *Server) licensedPackageBlock(id string) string {
	if id == enterprisePluginID {
		return ""
	}
	snapshot := s.cachedPluginCatalog()
	if snapshot == nil {
		return "The plugin catalog is unavailable, so its entitlements cannot be checked"
	}
	entry := snapshot.catalog.entry(id)
	if entry == nil {
		return "Not listed in the official plugin catalog"
	}
	if entitlementShortfall(entry.Entitlements, s.licenseSnapshot()) != "" {
		return requiresEntitlementMessage(entry.Entitlements)
	}
	return ""
}

func (s *Server) pluginCatalogPath() string {
	return filepath.Join(s.installPath(), "install", "plugin-catalog.json")
}

// readPluginCatalogFile loads the on-disk copy; its modification time is
// when it was fetched.
func (s *Server) readPluginCatalogFile() (*pluginCatalogSnapshot, error) {
	path := s.pluginCatalogPath()
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	catalog, err := parsePluginCatalog(data)
	if err != nil {
		return nil, err
	}
	return &pluginCatalogSnapshot{catalog: catalog, fetchedAt: info.ModTime()}, nil
}

// cachedPluginCatalog returns the newest verified catalog this service knows,
// from memory or the on-disk copy either service wrote, without contacting
// Keygen. It returns nil when there is none.
func (s *Server) cachedPluginCatalog() *pluginCatalogSnapshot {
	s.pluginCatalogMu.Lock()
	defer s.pluginCatalogMu.Unlock()
	now := time.Now()
	if s.pluginCatalog != nil && (now.Sub(s.pluginCatalog.fetchedAt) < pluginCatalogTTL || now.Sub(s.pluginCatalogDiskChecked) < pluginCatalogRetryDelay) {
		return s.pluginCatalog
	}
	s.pluginCatalogDiskChecked = now
	disk, err := s.readPluginCatalogFile()
	switch {
	case err == nil:
		if s.pluginCatalog == nil || disk.fetchedAt.After(s.pluginCatalog.fetchedAt) {
			s.pluginCatalog = disk
		}
	case !errors.Is(err, os.ErrNotExist):
		s.logger().Error("Could not read the cached plugin catalog", "path", s.pluginCatalogPath(), "error", err)
	}
	return s.pluginCatalog
}

// loadPluginCatalog returns a catalog at most pluginCatalogTTL old, fetching
// it from Keygen when needed. When the fetch fails it falls back to the cached
// copy and reports it stale; without one it returns the error.
func (s *Server) loadPluginCatalog(ctx context.Context) (*pluginCatalogSnapshot, bool, error) {
	s.pluginCatalogFetchMu.Lock()
	defer s.pluginCatalogFetchMu.Unlock()
	cached := s.cachedPluginCatalog()
	if cached != nil && time.Since(cached.fetchedAt) < pluginCatalogTTL {
		return cached, false, nil
	}
	s.pluginCatalogMu.Lock()
	recentFailure := time.Since(s.pluginCatalogFailedAt) < pluginCatalogRetryDelay
	s.pluginCatalogMu.Unlock()
	if cached != nil && recentFailure {
		return cached, true, nil
	}

	fetch := s.pluginCatalogFetch
	if fetch == nil {
		fetch = s.fetchPluginCatalog
	}
	data, err := fetch(ctx)
	var catalog *pluginCatalog
	if err == nil {
		catalog, err = parsePluginCatalog(data)
	}
	if err != nil {
		s.pluginCatalogMu.Lock()
		s.pluginCatalogFailedAt = time.Now()
		s.pluginCatalogMu.Unlock()
		if cached != nil {
			s.logger().Error("Could not fetch the plugin catalog; using the cached copy", "fetchedAt", cached.fetchedAt, "error", err)
			return cached, true, nil
		}
		return nil, false, err
	}
	snapshot := &pluginCatalogSnapshot{catalog: catalog, fetchedAt: time.Now()}
	if err := writeSharedInstallFile(s.pluginCatalogPath(), data); err != nil {
		s.logger().Error("Could not cache the plugin catalog", "path", s.pluginCatalogPath(), "error", err)
	}
	s.pluginCatalogMu.Lock()
	s.pluginCatalog = snapshot
	s.pluginCatalogMu.Unlock()
	return snapshot, false, nil
}

// fetchPluginCatalog downloads plugins.json from the metadata product
// anonymously and verifies its signature.
func (s *Server) fetchPluginCatalog(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, pluginCatalogFetchTimeout)
	defer cancel()
	dir, err := os.MkdirTemp("", "ludus-plugin-catalog-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err := DownloadFileUsingLicenseKey(ctx, pluginCatalogArtifact, pluginCatalogArtifact, dir, s.Version, "", LicenseProductMetadata); err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, pluginCatalogArtifact))
}

// pluginCatalogResponse describes the catalog for the caller: what is
// installed, what can be updated, and why an entry cannot be installed.
func (s *Server) pluginCatalogResponse(snapshot *pluginCatalogSnapshot, stale bool, auth *core.Record) dto.GetPluginCatalogResponse {
	license := s.licenseSnapshot()
	installed := map[string]string{}
	for _, pkg := range s.installedPackages() {
		installed[pkg.manifest.ID] = pkg.manifest.Version
	}
	response := dto.GetPluginCatalogResponse{
		Plugins:    make([]dto.GetPluginCatalogResponsePluginsItem, 0, len(snapshot.catalog.Plugins)),
		CanInstall: canInstallCatalogPlugin(auth, nil),
		Stale:      stale,
		FetchedAt:  snapshot.fetchedAt.UTC().Truncate(time.Second),
	}
	for _, entry := range snapshot.catalog.Plugins {
		reason := s.catalogEntryUnavailable(entry, license)
		if reason == "" && !canInstallCatalogPlugin(auth, &entry) {
			reason = catalogUnavailablePermission
		}
		response.Plugins = append(response.Plugins, dto.GetPluginCatalogResponsePluginsItem{
			ID:                entry.ID,
			Name:              entry.Name,
			Description:       entry.Description,
			Author:            entry.Author,
			Version:           entry.Version,
			Entitlements:      slices.Clone(entry.Entitlements),
			Level:             string(entry.Level),
			RunAs:             string(entry.RunAs),
			HasUI:             entry.HasUI,
			DocsURL:           entry.DocsURL,
			MinLudusVersion:   entry.MinLudusVersion,
			InstalledVersion:  installed[entry.ID],
			UpdateAvailable:   newerVersion(entry.Version, installed[entry.ID]),
			Entitled:          entitlementShortfall(entry.Entitlements, license) == "",
			AutoInstalled:     entry.ID == enterprisePluginID,
			CanInstall:        reason == "",
			UnavailableReason: reason,
		})
	}
	return response
}

// Catalog lists the official plugins for any authenticated user.
func (p *PluginResources) Catalog(e *core.RequestEvent) error {
	if e.Auth == nil {
		return JSONError(e, http.StatusUnauthorized, "authentication required")
	}
	snapshot, stale, err := p.server.loadPluginCatalog(e.Request.Context())
	if err != nil {
		return JSONError(e, http.StatusBadGateway, "Unable to fetch the plugin catalog: "+err.Error())
	}
	return e.JSON(http.StatusOK, p.server.pluginCatalogResponse(snapshot, stale, e.Auth))
}

// InstallCatalogPlugin installs or updates an official plugin, Enterprise
// included, to its latest stable release, then starts it, or restarts it on
// the new version, when it runs in this service. It is the only way an
// installed plugin is updated. It runs in the admin service, which owns
// <install>/plugins; the regular service proxies the request and then starts
// or restarts ludus-run plugins itself.
func (p *PluginResources) InstallCatalogPlugin(e *core.RequestEvent) error {
	if e.Auth == nil {
		return JSONError(e, http.StatusUnauthorized, "authentication required")
	}
	if !canInstallCatalogPlugin(e.Auth, nil) {
		return JSONError(e, http.StatusForbidden, "Only administrators can install official plugins")
	}
	if pluginServiceRunAs() != protocol.RunAsRoot {
		return JSONError(e, http.StatusInternalServerError, "Official plugins are installed by the Ludus admin service")
	}
	s := p.server
	snapshot, _, err := s.loadPluginCatalog(e.Request.Context())
	if err != nil {
		return JSONError(e, http.StatusBadGateway, "Unable to fetch the plugin catalog: "+err.Error())
	}
	id := e.Request.PathValue("pluginID")
	entry := snapshot.catalog.entry(id)
	if entry == nil {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("%s is not in the official plugin catalog", id))
	}
	if !canInstallCatalogPlugin(e.Auth, entry) {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("You may not install %s", entry.Name))
	}
	switch s.catalogEntryUnavailable(*entry, s.licenseSnapshot()) {
	case catalogUnavailableLicense:
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("%s requires a valid license", entry.Name))
	case catalogUnavailableEntitlement:
		return JSONError(e, http.StatusForbidden, requiresEntitlementMessage(entry.Entitlements))
	case catalogUnavailableLudusVersion:
		return JSONError(e, http.StatusUnprocessableEntity, fmt.Sprintf("%s requires Ludus %s or newer", entry.Name, entry.MinLudusVersion))
	}

	dir := s.licensedPackageDir(entry.ID)
	previous := ""
	if manifest, err := readPackageManifest(dir); err == nil && manifest.ID == entry.ID {
		previous = manifest.Version
	}
	changed, err := s.ensureLicensedPlugin(e.Request.Context(), entry.spec(), true)
	if err != nil {
		var conflict *routeConflictError
		if errors.As(err, &conflict) {
			return JSONError(e, http.StatusUnprocessableEntity, err.Error())
		}
		log.Printf("LICENSE: could not install %s: %v", entry.ID, err)
		return JSONError(e, http.StatusBadGateway, fmt.Sprintf("Unable to install %s: %v", entry.ID, err))
	}
	manifest, err := readPackageManifest(dir)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("%s was installed but cannot be read: %v", entry.ID, err))
	}
	packages := s.installedPackages()
	s.refreshPluginDeclarations(packages)
	s.reconcileLicensedPackages(packages)

	result := "unchanged"
	switch {
	case changed && previous == "":
		result = "installed"
	case changed:
		result = "updated"
	}
	return e.JSON(http.StatusOK, dto.InstallCatalogPluginResponse{ID: entry.ID, Version: manifest.Version, Result: result})
}
