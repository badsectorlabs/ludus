package ludusapi

import (
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// pluginRootStateBase holds the state directories of root-run plugins. It lies
// outside the data directory, which the admin service chowns to ludus.
var pluginRootStateBase = "/var/lib/ludus/plugin-state"

// pluginStateDir returns the plugin's StateDir without creating it.
func pluginStateDir(plugin *managedPlugin) (string, error) {
	id := plugin.manifest.ID
	if !protocol.ValidPluginID(id) {
		return "", fmt.Errorf("invalid plugin ID %q", id)
	}
	if plugin.manifest.RunAs == protocol.RunAsRoot {
		return filepath.Join(pluginRootStateBase, id), nil
	}
	ConfigMu.RLock()
	dataDirectory := filepath.Clean(ServerConfiguration.DataDirectory)
	ConfigMu.RUnlock()
	if !filepath.IsAbs(dataDirectory) || dataDirectory == string(filepath.Separator) {
		return "", fmt.Errorf("data_directory must be an absolute non-root path")
	}
	return filepath.Join(dataDirectory, "plugin-state", id), nil
}

// ensurePluginStateDir creates the StateDir 0700, owned by the run-as user.
func ensurePluginStateDir(plugin *managedPlugin) (string, error) {
	stateDir, err := pluginStateDir(plugin)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(stateDir), 0755); err != nil {
		return "", fmt.Errorf("create plugin state parent: %w", err)
	}
	if err := os.Mkdir(stateDir, 0700); err != nil && !os.IsExist(err) {
		return "", fmt.Errorf("create plugin state directory: %w", err)
	}
	info, err := os.Lstat(stateDir)
	if err != nil {
		return "", fmt.Errorf("inspect plugin state directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("plugin state path %s is not a directory", stateDir)
	}
	if err := os.Chmod(stateDir, 0700); err != nil {
		return "", fmt.Errorf("restrict plugin state directory: %w", err)
	}
	if os.Geteuid() == 0 {
		uid, gid := 0, 0
		if plugin.manifest.RunAs != protocol.RunAsRoot {
			if uid, gid, err = getUIDandGIDFromUsername("ludus"); err != nil {
				return "", err
			}
		}
		if err := os.Lchown(stateDir, uid, gid); err != nil {
			return "", fmt.Errorf("set plugin state directory owner: %w", err)
		}
	}
	return stateDir, nil
}

// pluginEnvironment is the host context sent with Initialize. It carries
// explicit non-secret settings only; the license key goes to Enterprise alone.
func (s *Server) pluginEnvironment(plugin *managedPlugin) (protocol.Environment, error) {
	stateDir, err := ensurePluginStateDir(plugin)
	if err != nil {
		return protocol.Environment{}, err
	}

	ConfigMu.RLock()
	settings := protocol.Settings{
		ProxmoxNode:               ServerConfiguration.ProxmoxNode,
		WireguardEndpoint:         ServerConfiguration.WireguardEndpoint,
		InactivityShutdownTimeout: ServerConfiguration.InactivityShutdownTimeout,
		DefaultQuotaRAM:           ServerConfiguration.DefaultQuotaRAM,
		DefaultQuotaCPU:           ServerConfiguration.DefaultQuotaCPU,
		DefaultQuotaVMs:           ServerConfiguration.DefaultQuotaVMs,
		DefaultQuotaRanges:        ServerConfiguration.DefaultQuotaRanges,
		DataDirectory:             ServerConfiguration.DataDirectory,
	}
	ConfigMu.RUnlock()

	env := protocol.Environment{
		LudusVersion: s.Version,
		ResourceID:   plugin.resourceID,
		Level:        plugin.manifest.Level,
		RunAs:        plugin.manifest.RunAs,
		Source:       plugin.source,
		InstallPath:  s.LudusInstallPath,
		StateDir:     stateDir,
		License:      s.licenseSnapshot(),
		Settings:     settings,
		API: protocol.APIEndpoint{
			URL:               s.PluginAPIURL,
			CertificateSHA256: s.PluginAPICertificateSHA256,
		},
	}
	if isEnterprisePlugin(plugin) {
		s.licenseMu.RLock()
		env.LicenseKey = s.LicenseKey
		s.licenseMu.RUnlock()
	}
	return env, nil
}
