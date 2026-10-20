package ludusapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"ludusapi/scheduler"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	hcplugin "github.com/hashicorp/go-plugin"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// managedPlugin is one plugin process hosted by this service.
type managedPlugin struct {
	rpc            protocol.Plugin
	client         *hcplugin.Client
	manifest       protocol.Manifest // approved declarations
	metadata       protocol.Metadata
	source         protocol.Source
	resourceID     string // plugin_resources record ID ("" until mirrored)
	dir            string // package directory the executable runs from
	host           *HostService
	webApplication *protocol.WebApplication

	mu          sync.Mutex
	initialized bool
	jobs        []string // scheduler job names, set before registration
}

type Server struct {
	PluginResources *PluginResources
	pluginMu        sync.Mutex
	// plugins is the registry of every plugin process this service hosts,
	// whatever its source.
	plugins []*managedPlugin
	// remotePluginRoutes and remoteVMHooks describe the active plugins the
	// other service hosts. The regular service proxies the API routes of
	// root-run plugins and selects their VM hooks through the VM hook socket;
	// both services check new routes against them.
	remotePluginRoutes []pluginAPIRoute
	remoteVMHooks      protocol.VMHookCapabilities
	// rejectedPlugins are active plugins refused because an API route they
	// declare is already claimed, by plugin key.
	rejectedPlugins map[string]error

	pluginTasksOnce     sync.Once
	pluginTasks         chan func()
	licensedRefreshMu   sync.Mutex
	lastLicensedRefresh time.Time

	licenseMu                  sync.RWMutex
	Version                    string
	VersionString              string
	LudusInstallPath           string
	Entitlements               []string
	LicenseMessage             string
	LicenseValid               bool
	LicenseKey                 string
	LicenseName                string
	LicenseExpiry              *time.Time
	Logger                     *slog.Logger
	Scheduler                  *scheduler.Scheduler
	PluginAPIURL               string
	PluginAPICertificateSHA256 string
	pluginReleaseLookup        pluginReleaseLookupFunc
	pluginReleaseDownload      pluginReleaseDownloadFunc
}

// pluginServiceRunAs is the run-as user of the plugins this service hosts:
// ludus-admin.service (root) hosts root-run plugins, ludus.service the rest.
var pluginServiceRunAs = func() protocol.RunAs {
	if os.Geteuid() == 0 {
		return protocol.RunAsRoot
	}
	return protocol.RunAsLudus
}

// rootPluginOwnerUID is the owner every path component of a root-run package
// must have (D8). Tests substitute their own UID.
var rootPluginOwnerUID uint32 = 0

func (s *Server) installPath() string {
	if s.LudusInstallPath != "" {
		return s.LudusInstallPath
	}
	return ludusInstallPath
}

// pluginsRoot holds root-managed packages: <root>/<id>/ for server and
// licensed packages, <root>/uploaded/<resourceID>/ for approved root-run uploads.
func (s *Server) pluginsRoot() string {
	return filepath.Join(s.installPath(), "plugins")
}

func (s *Server) uploadedRootPackageDir(resourceID string) string {
	return filepath.Join(s.pluginsRoot(), "uploaded", resourceID)
}

func (s *Server) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// pluginKey identifies a plugin across services: uploaded packages by their
// resource record, installed (server or licensed) packages by their ID.
func pluginKey(source protocol.Source, pluginID, resourceID string) string {
	if source == protocol.SourceUploaded {
		return "uploaded/" + resourceID
	}
	return "installed/" + pluginID
}

func (p *managedPlugin) key() string {
	return pluginKey(p.source, p.manifest.ID, p.resourceID)
}

func (p *managedPlugin) exited() bool {
	return p.client != nil && p.client.Exited()
}

// startPlugin starts, initializes, and registers the package in dir. The
// manifest is the approved declaration; the process must report matching
// metadata.
func (s *Server) startPlugin(dir string, manifest protocol.Manifest, source protocol.Source, resourceID string) (*managedPlugin, error) {
	if manifest.ID == "ludus-guacamole" && source == protocol.SourceUploaded {
		if s.PluginResources == nil {
			return nil, errors.New("Guacamole resource approval is unavailable")
		}
		record, err := s.PluginResources.app.FindRecordById("plugin_resources", resourceID)
		if err != nil || !trustedGuacamoleResource(s.PluginResources.app, record) {
			return nil, errors.New("only administrator-owned Guacamole plugins may be started")
		}
	}
	if manifest.Executable == "" {
		return nil, fmt.Errorf("plugin %s has no executable", manifest.ID)
	}
	if err := checkHostDeclarations(manifest); err != nil {
		return nil, err
	}
	if manifest.RunAs != pluginServiceRunAs() {
		return nil, fmt.Errorf("plugin %s runs as %s; this service hosts %s plugins", manifest.ID, manifest.RunAs, pluginServiceRunAs())
	}
	executable := filepath.Join(dir, protocol.ExecutableFile)
	if manifest.RunAs == protocol.RunAsRoot {
		if err := s.verifyRootPackage(dir, manifest, source, resourceID); err != nil {
			return nil, fmt.Errorf("refusing to start root-run plugin %s: %w", manifest.ID, err)
		}
	} else {
		info, err := os.Stat(executable)
		if err != nil {
			return nil, fmt.Errorf("stat plugin executable: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0100 == 0 {
			return nil, fmt.Errorf("plugin executable is not a regular executable file: %s", executable)
		}
	}
	if source != protocol.SourceUploaded && resourceID == "" && s.PluginResources != nil {
		resourceID = s.PluginResources.installedRecordID(manifest.ID)
	}
	key := pluginKey(source, manifest.ID, resourceID)
	if err := s.checkRunningPluginRoutes(manifest, key); err != nil {
		return nil, err
	}
	s.pluginMu.Lock()
	rejection := s.rejectedPlugins[key]
	s.pluginMu.Unlock()
	if rejection != nil {
		return nil, rejection
	}

	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  protocol.Handshake,
		Plugins:          protocol.PluginMap(nil),
		Cmd:              exec.Command(executable),
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolNetRPC},
		SyncStdout:       os.Stdout,
		SyncStderr:       os.Stderr,
	})
	keep := false
	defer func() {
		if !keep {
			client.Kill()
		}
	}()
	rpcClient, err := client.Client()
	if err != nil {
		return nil, fmt.Errorf("start plugin process: %w", err)
	}
	raw, err := rpcClient.Dispense(protocol.PluginName)
	if err != nil {
		return nil, fmt.Errorf("dispense plugin: %w", err)
	}
	rpcPlugin, ok := raw.(*protocol.RPCClient)
	if !ok {
		return nil, fmt.Errorf("unexpected plugin RPC client type %T", raw)
	}
	metadata, err := rpcPlugin.Metadata()
	if err != nil {
		return nil, fmt.Errorf("read plugin metadata: %w", err)
	}
	if err := manifest.CheckMetadata(metadata); err != nil {
		return nil, err
	}
	plugin := &managedPlugin{
		rpc:        rpcPlugin,
		client:     client,
		manifest:   manifest,
		metadata:   metadata,
		source:     source,
		resourceID: resourceID,
		dir:        dir,
	}
	plugin.host = newHostService(s, plugin)
	jobs, err := s.initializePlugin(plugin, rpcPlugin.Broker())
	if err != nil {
		return nil, err
	}
	if err := s.registerPlugin(plugin, jobs); err != nil {
		return nil, err
	}
	keep = true
	s.logger().Info("Started plugin", "plugin", manifest.ID, "version", manifest.Version, "source", source, "level", manifest.Level, "runAs", manifest.RunAs)
	if manifest.VMHooks.Any() {
		if err := s.StartVMHookService(); err != nil {
			s.logger().Error("Could not start the VM hook service", "plugin", manifest.ID, "error", err)
		}
	}
	return plugin, nil
}

// initializePlugin sends Initialize without holding any plugin-management
// lock, so the plugin may call the host service while it initializes.
func (s *Server) initializePlugin(plugin *managedPlugin, broker *hcplugin.MuxBroker) ([]protocol.ScheduledJob, error) {
	env, err := s.pluginEnvironment(plugin)
	if err != nil {
		return nil, fmt.Errorf("prepare environment for plugin %s: %w", plugin.manifest.ID, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	call, done := plugin.host.beginPluginCall(ctx)
	response, err := plugin.rpc.Initialize(protocol.InitializeRequest{
		Call:        call,
		HostService: plugin.host.serve(broker),
		Env:         env,
	})
	done()
	if err != nil {
		return nil, fmt.Errorf("initialize plugin %s: %w", plugin.manifest.ID, err)
	}
	if err := configurePluginWebApplication(plugin, response.WebApplication); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, job := range response.Jobs {
		if job.Name == "" || names[job.Name] || job.Interval < protocol.MinJobInterval {
			return nil, fmt.Errorf("plugin %s returned an invalid scheduled job", plugin.manifest.ID)
		}
		names[job.Name] = true
	}
	if len(response.Jobs) > 0 && s.Scheduler == nil {
		return nil, fmt.Errorf("plugin %s requires scheduled jobs but the server has no scheduler", plugin.manifest.ID)
	}
	plugin.mu.Lock()
	plugin.initialized = true
	plugin.mu.Unlock()
	return response.Jobs, nil
}

// registerPlugin schedules the plugin's jobs and adds it to the registry,
// unless another instance or a conflicting API route is already registered.
func (s *Server) registerPlugin(plugin *managedPlugin, jobs []protocol.ScheduledJob) error {
	for _, job := range jobs {
		name := "plugin " + plugin.key() + ": " + job.Name
		jobName := job.Name
		s.Scheduler.Register(scheduler.Job{
			Name:     name,
			Interval: job.Interval,
			Fn: func(ctx context.Context) error {
				call, done := plugin.host.beginPluginCall(ctx)
				defer done()
				if err := plugin.rpc.RunJob(protocol.JobRequest{Call: call, Name: jobName}); err != nil {
					return fmt.Errorf("run plugin %s job %s: %w", plugin.manifest.ID, jobName, err)
				}
				return nil
			},
		})
		plugin.jobs = append(plugin.jobs, name)
	}
	s.pluginMu.Lock()
	err := s.registrationConflictLocked(plugin.manifest, plugin.key())
	if err == nil {
		s.plugins = append(s.plugins, plugin)
	}
	s.pluginMu.Unlock()
	if err != nil {
		s.removePluginJobs(plugin)
	}
	return err
}

func (s *Server) registrationConflictLocked(manifest protocol.Manifest, key string) error {
	for _, existing := range s.plugins {
		if existing.key() == key {
			return fmt.Errorf("plugin %s is already running", manifest.ID)
		}
	}
	return apiRouteConflict(manifest, key, s.runningAPIRoutesLocked())
}

func (s *Server) checkRunningPluginRoutes(manifest protocol.Manifest, key string) error {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	return s.registrationConflictLocked(manifest, key)
}

func (s *Server) removePluginJobs(plugin *managedPlugin) {
	if s.Scheduler == nil {
		return
	}
	for _, name := range plugin.jobs {
		s.Scheduler.Remove(name)
	}
}

// stopPlugin removes the plugin from the registry and ends its process.
// Killing the process also interrupts blocked requests and jobs.
func (s *Server) stopPlugin(plugin *managedPlugin, graceful bool) {
	s.pluginMu.Lock()
	s.plugins = slices.DeleteFunc(s.plugins, func(existing *managedPlugin) bool { return existing == plugin })
	s.pluginMu.Unlock()
	s.removePluginJobs(plugin)
	if graceful {
		if err := plugin.rpc.Shutdown(); err != nil {
			s.logger().Info("Error shutting down plugin", "plugin", plugin.manifest.ID, "error", err)
		}
	}
	if plugin.client != nil {
		plugin.client.Kill()
	}
}

func (s *Server) ShutdownPlugins() {
	for _, plugin := range s.pluginSnapshot() {
		s.stopPlugin(plugin, true)
	}
}

func (s *Server) pluginSnapshot() []*managedPlugin {
	if s == nil {
		return nil
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	return slices.Clone(s.plugins)
}

func (s *Server) findPlugin(match func(*managedPlugin) bool) *managedPlugin {
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	for _, plugin := range s.plugins {
		if match(plugin) {
			return plugin
		}
	}
	return nil
}

func (s *Server) pluginByKey(key string) *managedPlugin {
	return s.findPlugin(func(plugin *managedPlugin) bool { return plugin.key() == key })
}

// stopPluginByKey stops a registered plugin. It reports whether one was running.
func (s *Server) stopPluginByKey(key string) bool {
	plugin := s.pluginByKey(key)
	if plugin == nil {
		return false
	}
	s.stopPlugin(plugin, false)
	return true
}

// verifyRootPackage applies the D8 checks: every path component from the
// plugins root down to the executable, plugin.json, and approval.json is owned
// by root and not group- or world-writable, and an uploaded package's
// executable matches the SHA-256 the administrator approved.
func (s *Server) verifyRootPackage(dir string, manifest protocol.Manifest, source protocol.Source, resourceID string) error {
	root := s.pluginsRoot()
	rel, err := filepath.Rel(root, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("package %s is outside %s", dir, root)
	}
	paths := []string{root}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		paths = append(paths, current)
	}
	paths = append(paths, filepath.Join(dir, protocol.ManifestFile), filepath.Join(dir, protocol.ExecutableFile))
	approvalPath := filepath.Join(dir, pluginApprovalFile)
	if _, err := os.Lstat(approvalPath); err == nil {
		paths = append(paths, approvalPath)
	}
	for _, path := range paths {
		if err := checkRootOwned(path); err != nil {
			return err
		}
	}
	approval, approved, err := readPluginApproval(dir)
	if err != nil {
		return err
	}
	switch source {
	case protocol.SourceUploaded:
		if !approved || approval.Source != protocol.SourceUploaded || approval.ResourceID != resourceID {
			return errors.New("no administrator approval in the root-owned package")
		}
		if approval.Level != manifest.Level || approval.RunAs != manifest.RunAs {
			return errors.New("plugin.json declarations differ from the approval")
		}
		sum, err := fileSHA256(filepath.Join(dir, protocol.ExecutableFile))
		if err != nil {
			return err
		}
		if sum != approval.SHA256 {
			return errors.New("executable SHA-256 differs from the approval")
		}
	case protocol.SourceLicensed:
		if !approved || approval.Source != protocol.SourceLicensed {
			return errors.New("licensed package has no license-flow approval")
		}
	case protocol.SourceServer:
		if approved {
			return errors.New("server package carries an approval")
		}
	default:
		return fmt.Errorf("unknown package source %q", source)
	}
	return nil
}

func checkRootOwned(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read the owner of %s", path)
	}
	if stat.Uid != rootPluginOwnerUID {
		return fmt.Errorf("%s is not owned by root", path)
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s is group- or world-writable", path)
	}
	return nil
}

// installedPackage is a server or licensed package in <install>/plugins/<id>/.
type installedPackage struct {
	dir      string
	manifest protocol.Manifest
	source   protocol.Source
	sha256   string // executable SHA-256 from approval.json, if any
}

// installedPackages scans <install>/plugins/*/plugin.json. It reads
// declarations without running anything.
func (s *Server) installedPackages() []installedPackage {
	root := s.pluginsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger().Error("Could not read the plugins directory", "path", root, "error", err)
		}
		return nil
	}
	var packages []installedPackage
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "uploaded" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifest, err := readPackageManifest(dir)
		if err != nil {
			s.logger().Error("Skipping invalid plugin package", "path", dir, "error", err)
			continue
		}
		if manifest.ID != entry.Name() {
			s.logger().Error("Skipping plugin package whose ID does not match its directory", "path", dir, "id", manifest.ID)
			continue
		}
		approval, approved, err := readPluginApproval(dir)
		if err != nil {
			s.logger().Error("Skipping plugin package with an invalid approval", "path", dir, "error", err)
			continue
		}
		pkg := installedPackage{dir: dir, manifest: manifest, source: protocol.SourceServer}
		if approved {
			if approval.Source != protocol.SourceLicensed {
				s.logger().Error("Skipping plugin package with an unexpected approval source", "path", dir, "source", approval.Source)
				continue
			}
			pkg.source, pkg.sha256 = protocol.SourceLicensed, approval.SHA256
		}
		packages = append(packages, pkg)
	}
	return packages
}

func (s *Server) startInstalledPackage(pkg installedPackage) {
	if _, err := s.startPlugin(pkg.dir, pkg.manifest, pkg.source, ""); err != nil {
		s.logger().Error("Could not start plugin", "plugin", pkg.manifest.ID, "error", err)
	}
}

// StartPlugins starts every plugin this service hosts: installed packages whose
// runAs matches the service, approved uploads, and the periodic plugin jobs.
// Declarations of both services are resolved first, so a plugin whose API
// route another active plugin already claims is refused in either service.
func (s *Server) StartPlugins() {
	runAs := pluginServiceRunAs()
	packages := s.installedPackages()
	rejected := s.refreshPluginDeclarations(packages)
	if runAs == protocol.RunAsLudus && s.PluginResources != nil {
		s.PluginResources.mirrorInstalled(packages, rejected)
		s.PluginResources.flagRejectedUploads(rejected)
	}
	for _, pkg := range packages {
		if pkg.manifest.Executable != "" && pkg.manifest.RunAs == runAs {
			s.startInstalledPackage(pkg)
		}
	}
	if runAs == protocol.RunAsRoot {
		s.reconcileUploadedRootPackages(true)
		if s.Scheduler != nil {
			s.Scheduler.Register(scheduler.Job{Name: "plugin-approvals", Interval: time.Minute, Fn: func(context.Context) error {
				s.refreshPluginDeclarations(s.installedPackages())
				s.reconcileUploadedRootPackages(false)
				return nil
			}})
		}
		return
	}
	if s.PluginResources != nil {
		s.PluginResources.startUploaded()
	}
	if s.Scheduler != nil {
		s.Scheduler.Register(scheduler.Job{Name: "license-state", Interval: time.Minute, Fn: func(context.Context) error {
			return s.loadLicenseState()
		}})
		s.Scheduler.Register(scheduler.Job{Name: "plugin-packages", Interval: time.Minute, Fn: func(context.Context) error {
			s.syncInstalledPackages()
			return nil
		}})
	}
}

// syncInstalledPackages mirrors installed packages, including ones the admin
// service's license flow installed, refreshes what it knows of the other
// service's plugins, and flags plugins refused for conflicting routes.
func (s *Server) syncInstalledPackages() {
	packages := s.installedPackages()
	rejected := s.refreshPluginDeclarations(packages)
	if s.PluginResources != nil {
		s.PluginResources.mirrorInstalled(packages, rejected)
		s.PluginResources.flagRejectedUploads(rejected)
	}
}

// reconcileUploadedRootPackages runs in the admin service. It stops and removes
// root-owned copies whose resource record is gone or no longer approved (for
// example after a replacement upload) and, at startup, starts the rest. The
// database can only withdraw an approval; it never grants one. A failed
// database lookup withdraws nothing.
func (s *Server) reconcileUploadedRootPackages(start bool) {
	// Serialize with Activate and Remove, which install and delete copies.
	if s.PluginResources != nil {
		s.PluginResources.mu.Lock()
		defer s.PluginResources.mu.Unlock()
	}
	uploadedRoot := filepath.Join(s.pluginsRoot(), "uploaded")
	entries, err := os.ReadDir(uploadedRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger().Error("Could not read uploaded root plugins", "path", uploadedRoot, "error", err)
		return
	}
	present := map[string]bool{}
	for _, entry := range entries {
		resourceID := entry.Name()
		if !entry.IsDir() || !pluginRecordIDPattern.MatchString(resourceID) {
			continue
		}
		present[resourceID] = true
		if s.PluginResources != nil {
			approved, err := s.PluginResources.approvedRootUpload(resourceID)
			if err != nil {
				s.logger().Error("Could not check the approval of an uploaded root plugin", "resourceID", resourceID, "error", err)
				continue
			}
			if !approved {
				s.removeUploadedRootPackage(resourceID)
				delete(present, resourceID)
				continue
			}
		}
		if start {
			if _, err := s.startUploadedRootPackage(resourceID); err != nil {
				s.logger().Error("Could not start uploaded root plugin", "resourceID", resourceID, "error", err)
			}
		}
	}
	for _, plugin := range s.pluginSnapshot() {
		if plugin.source == protocol.SourceUploaded && !present[plugin.resourceID] {
			s.stopPlugin(plugin, false)
		}
	}
}

func (s *Server) startUploadedRootPackage(resourceID string) (*managedPlugin, error) {
	dir := s.uploadedRootPackageDir(resourceID)
	manifest, err := readPackageManifest(dir)
	if err != nil {
		return nil, err
	}
	return s.startPlugin(dir, manifest, protocol.SourceUploaded, resourceID)
}

// removeUploadedRootPackage stops an uploaded root-run plugin and deletes its
// root-owned copy.
func (s *Server) removeUploadedRootPackage(resourceID string) {
	s.stopPluginByKey(pluginKey(protocol.SourceUploaded, "", resourceID))
	if err := os.RemoveAll(s.uploadedRootPackageDir(resourceID)); err != nil {
		s.logger().Error("Could not remove root-owned plugin copy", "resourceID", resourceID, "error", err)
	}
}

// queuePluginTask runs plugin-management work after the triggering plugin
// call returns, one task at a time, so host-service calls never wait on it.
func (s *Server) queuePluginTask(task func()) {
	s.pluginTasksOnce.Do(func() {
		s.pluginTasks = make(chan func(), 16)
		go func() {
			for task := range s.pluginTasks {
				task()
			}
		}()
	})
	select {
	case s.pluginTasks <- task:
	default:
		s.logger().Error("Plugin task queue is full; dropping task")
	}
}

// HasEntitlement checks if the server license has a specific entitlement
func (s *Server) HasEntitlement(code string) bool {
	s.licenseMu.RLock()
	defer s.licenseMu.RUnlock()
	return slices.Contains(s.Entitlements, code)
}

// EntitlementsSnapshot returns a copy that license refreshes cannot mutate.
func (s *Server) EntitlementsSnapshot() []string {
	s.licenseMu.RLock()
	defer s.licenseMu.RUnlock()
	return slices.Clone(s.Entitlements)
}
