package ludusapi

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"plugin"

	"github.com/gin-gonic/gin"
)

type LudusPlugin interface {
	Name() string
	Initialize(server *Server) error
	RegisterRoutes(router *gin.Engine)
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
	LicenseType      string
	LicenseMessage   string
	LicenseValid     bool
	LicenseKey       string
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
			log.Printf("Plugin %s is already loaded, skipping", ludusPlugin.Name())
			return nil
		}
	}

	s.plugins = append(s.plugins, ludusPlugin)
	log.Println("Loaded plugin: ", ludusPlugin.Name())
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
			log.Printf("Failed to initialize plugin %s: %v", p.Name(), err)
		}

		embeddedFSsFromPlugin := p.GetEmbeddedFSs()
		for index, embeddedFSFromPlugin := range embeddedFSsFromPlugin {
			if embeddedFSFromPlugin != nil {
				if p.Name() == "Ludus Enterprise" && os.Geteuid() == 0 {
					log.Println("Not dropping files for plugin: ", p.Name(), " (root)")
					continue
				}
				log.Printf("Dropping embedded filesystem %d for plugin: %s\n", index+1, p.Name())
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
					log.Printf("Error writing out plugin files: %v", err)
				}
			}
		}
	}

}

func (s *Server) RegisterPluginRoutes(router *gin.Engine) {
	for _, p := range s.plugins {
		if !p.RoutesRegistered() {
			log.Printf("Registering routes for plugin: %s\n", p.Name())
			p.RegisterRoutes(router)
		}

	}
}

func (s *Server) ShutdownPlugins() {
	for _, p := range s.plugins {
		if err := p.Shutdown(); err != nil {
			log.Printf("Error shutting down plugin %s: %v", p.Name(), err)
		}
	}
}
