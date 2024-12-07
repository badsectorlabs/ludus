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
	GetEmbeddedFS() fs.FS
	Shutdown() error
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

	s.plugins = append(s.plugins, ludusPlugin)
	log.Println("Loaded plugin: ", ludusPlugin.Name())
	return nil
}

func (s *Server) RegisterPlugin(p LudusPlugin) {
	s.plugins = append(s.plugins, p)
}

func (s *Server) InitializePlugins() {
	for _, p := range s.plugins {
		if err := p.Initialize(s); err != nil {
			log.Printf("Failed to initialize plugin %s: %v", p.Name(), err)
		}
		// If we're not running as root, drop the files from the plugin FS to the host filesystem
		if os.Geteuid() != 0 {
			embeddedFSFromPlugin := p.GetEmbeddedFS()
			if embeddedFSFromPlugin != nil {
				log.Println("Dropping files for plugin: ", p.Name())
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
		p.RegisterRoutes(router)
	}
}

func (s *Server) ShutdownPlugins() {
	for _, p := range s.plugins {
		if err := p.Shutdown(); err != nil {
			log.Printf("Error shutting down plugin %s: %v", p.Name(), err)
		}
	}
}
