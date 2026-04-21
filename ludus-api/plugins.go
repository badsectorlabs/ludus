package ludusapi

import (
	"fmt"
	"io/fs"
	"log/slog"
	"ludusapi/scheduler"
	"os"
	"path/filepath"
	"plugin"
	"slices"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

type LudusPlugin interface {
	Name() string
	Initialize(server *Server) error
	RegisterRoutes(app *core.App)
	GetEmbeddedFSs() []fs.FS
	Shutdown() error
	Initialized() bool
	RoutesRegistered() bool
}

type Server struct {
	plugins          []LudusPlugin
	Version          string
	VersionString    string
	LudusInstallPath string
	Entitlements     []string
	LicenseMessage   string
	LicenseValid     bool
	LicenseKey       string
	LicenseName      string
	LicenseExpiry    *time.Time
	Logger           *slog.Logger
	Scheduler        *scheduler.Scheduler
}

func (s *Server) LoadPlugin(path string) error {
	p, err := plugin.Open(path)
	if err != nil {
		return err
	}

	symPlugin, err := p.Lookup("Plugin")
	if err != nil {
		return err
	}

	var ludusPlugin LudusPlugin
	ludusPlugin, ok := symPlugin.(LudusPlugin)
	if !ok {
		return fmt.Errorf("unexpected type from module symbol")
	}

	// Check if a plugin with the same name is already loaded
	for _, existingPlugin := range s.plugins {
		if existingPlugin.Name() == ludusPlugin.Name() {
			logger.Info(fmt.Sprintf("Plugin %s is already loaded, skipping", ludusPlugin.Name()))
			return nil
		}
	}

	s.plugins = append(s.plugins, ludusPlugin)
	logger.Info(fmt.Sprintf("Loaded plugin: %s", ludusPlugin.Name()))
	return nil
}

func (s *Server) RegisterPlugin(p LudusPlugin) {
	s.plugins = append(s.plugins, p)
}

func (s *Server) InitializePlugins() {
	for _, p := range s.plugins {
		if p.Initialized() {
			continue
		}

		if err := p.Initialize(s); err != nil {
			logger.Error(fmt.Sprintf("Failed to initialize plugin %s: %v", p.Name(), err))
		}

		embeddedFSsFromPlugin := p.GetEmbeddedFSs()
		for index, embeddedFSFromPlugin := range embeddedFSsFromPlugin {
			if embeddedFSFromPlugin != nil {
				if p.Name() == "Ludus Enterprise" && os.Geteuid() == 0 {
					logger.Info(fmt.Sprintf("Not dropping files for plugin: %s (root)", p.Name()))
					continue
				}
				logger.Info(fmt.Sprintf("Dropping embedded filesystem %d for plugin: %s", index+1, p.Name()))
				// Write out any files from the plugin FS to the host filesystem
				err := fs.WalkDir(embeddedFSFromPlugin, ".", func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					destPath := filepath.Join(ludusInstallPath, path)
					if d.IsDir() {
						return os.MkdirAll(destPath, 0755)
					}
					data, err := fs.ReadFile(embeddedFSFromPlugin, path)
					if err != nil {
						return err
					}
					return os.WriteFile(destPath, data, 0644)
				})
				if err != nil {
					logger.Error(fmt.Sprintf("Error writing out plugin files: %v", err))
				}
			}
		}
	}

}

func (s *Server) RegisterPluginRoutes(app *core.App) {
	for _, p := range s.plugins {
		if !p.RoutesRegistered() {
			logger.Info(fmt.Sprintf("Registering routes for plugin: %s", p.Name()))
			p.RegisterRoutes(app)
		}

	}
}

func (s *Server) ShutdownPlugins() {
	for _, p := range s.plugins {
		if err := p.Shutdown(); err != nil {
			logger.Info(fmt.Sprintf("Error shutting down plugin %s: %v", p.Name(), err))
		}
	}
}

// HasEntitlement checks if the server license has a specific entitlement
func (s *Server) HasEntitlement(code string) bool {
	return slices.Contains(s.Entitlements, code)
}
