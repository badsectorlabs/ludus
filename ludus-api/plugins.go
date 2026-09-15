package ludusapi

import (
	"context"
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

// StartVMHook is an optional plugin capability. Plugins that do not implement
// it continue to use the normal Proxmox start path without any changes.
//
// Hooks run in plugin load order. Returning StartVMHandled stops dispatch and
// tells Ludus that the plugin completed the start. Returning an error fails the
// request closed; Ludus will not fall back to an ordinary Proxmox start.
type StartVMHook interface {
	StartVM(context.Context, StartVMHookRequest) (StartVMHookResult, error)
}

type VMHookRequest struct {
	Source  string
	RangeID string
	VMID    int
	VMName  string
	Node    string
	Pool    string
	Status  string
}

type StartVMHookRequest = VMHookRequest

type StartVMHookResult uint8

const (
	StartVMContinue StartVMHookResult = iota
	StartVMHandled
)

const StartVMSourceAPI = "api"

const StartVMSourceDeployment = "deployment"

const StartVMSourceDeploymentFirstBoot = "deployment-first-boot"

// StopVMHook is the stop-side equivalent of StartVMHook. A handled result
// means the plugin has stopped the VM and Ludus must not call Proxmox Stop.
type StopVMHook interface {
	StopVM(context.Context, VMHookRequest) (StartVMHookResult, error)
}

// VMStatusHook lets a provider replace Proxmox's reported runtime status.
// This is necessary when the provider owns a QEMU process that is launched
// through a private runtime rather than the ordinary API start operation.
type VMStatusHook interface {
	VMStatus(context.Context, VMHookRequest) (VMStatusHookResult, error)
}

type VMStatus string

const (
	VMStatusRunning  VMStatus = "running"
	VMStatusStopped  VMStatus = "stopped"
	VMStatusStarting VMStatus = "starting"
	VMStatusStopping VMStatus = "stopping"
)

type VMStatusHookResult struct {
	Decision StartVMHookResult
	Status   VMStatus
}

// BeforeDeleteVMHook prepares plugin-owned runtime state for deletion. Every
// registered hook runs; returning an error vetoes the Proxmox delete.
type BeforeDeleteVMHook interface {
	BeforeDeleteVM(context.Context, VMHookRequest) error
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

func (s *Server) hasStartVMHooks() bool {
	for _, plugin := range s.plugins {
		if _, ok := plugin.(StartVMHook); ok {
			return true
		}
	}
	return false
}

func (s *Server) hasStopVMHooks() bool {
	for _, plugin := range s.plugins {
		if _, ok := plugin.(StopVMHook); ok {
			return true
		}
	}
	return false
}

func (s *Server) hasVMStatusHooks() bool {
	for _, plugin := range s.plugins {
		if _, ok := plugin.(VMStatusHook); ok {
			return true
		}
	}
	return false
}

func (s *Server) hasBeforeDeleteVMHooks() bool {
	for _, plugin := range s.plugins {
		if _, ok := plugin.(BeforeDeleteVMHook); ok {
			return true
		}
	}
	return false
}

func (s *Server) hasVMLifecycleHooks() bool {
	return s.hasStartVMHooks() || s.hasStopVMHooks() || s.hasVMStatusHooks() || s.hasBeforeDeleteVMHooks() || s.hasVMAddressHooks()
}

func (s *Server) runStartVMHooks(ctx context.Context, request StartVMHookRequest) (bool, error) {
	for _, plugin := range s.plugins {
		handler, ok := plugin.(StartVMHook)
		if !ok {
			continue
		}
		if !plugin.Initialized() {
			return false, fmt.Errorf("plugin %s registered a start VM hook but is not initialized", plugin.Name())
		}

		result, err := handler.StartVM(ctx, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected start of VMID %d: %w", plugin.Name(), request.VMID, err)
		}
		switch result {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled start of VMID %d", plugin.Name(), request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid start VM hook result %d for VMID %d", plugin.Name(), result, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runStopVMHooks(ctx context.Context, request VMHookRequest) (bool, error) {
	for _, plugin := range s.plugins {
		handler, ok := plugin.(StopVMHook)
		if !ok {
			continue
		}
		if !plugin.Initialized() {
			return false, fmt.Errorf("plugin %s registered a stop VM hook but is not initialized", plugin.Name())
		}

		result, err := handler.StopVM(ctx, request)
		if err != nil {
			return false, fmt.Errorf("plugin %s rejected stop of VMID %d: %w", plugin.Name(), request.VMID, err)
		}
		switch result {
		case StartVMContinue:
			continue
		case StartVMHandled:
			logger.Debug(fmt.Sprintf("Plugin %s handled stop of VMID %d", plugin.Name(), request.VMID))
			return true, nil
		default:
			return false, fmt.Errorf("plugin %s returned invalid stop VM hook result %d for VMID %d", plugin.Name(), result, request.VMID)
		}
	}
	return false, nil
}

func (s *Server) runVMStatusHooks(ctx context.Context, request VMHookRequest) (VMStatus, bool, error) {
	for _, plugin := range s.plugins {
		handler, ok := plugin.(VMStatusHook)
		if !ok {
			continue
		}
		if !plugin.Initialized() {
			return "", false, fmt.Errorf("plugin %s registered a VM status hook but is not initialized", plugin.Name())
		}

		result, err := handler.VMStatus(ctx, request)
		if err != nil {
			return "", false, fmt.Errorf("plugin %s failed status for VMID %d: %w", plugin.Name(), request.VMID, err)
		}
		switch result.Decision {
		case StartVMContinue:
			continue
		case StartVMHandled:
			switch result.Status {
			case VMStatusRunning, VMStatusStopped, VMStatusStarting, VMStatusStopping:
				return result.Status, true, nil
			default:
				return "", false, fmt.Errorf("plugin %s returned invalid status %q for VMID %d", plugin.Name(), result.Status, request.VMID)
			}
		default:
			return "", false, fmt.Errorf("plugin %s returned invalid VM status hook result %d for VMID %d", plugin.Name(), result.Decision, request.VMID)
		}
	}
	return "", false, nil
}

func (s *Server) runBeforeDeleteVMHooks(ctx context.Context, request VMHookRequest) error {
	for _, plugin := range s.plugins {
		handler, ok := plugin.(BeforeDeleteVMHook)
		if !ok {
			continue
		}
		if !plugin.Initialized() {
			return fmt.Errorf("plugin %s registered a before-delete VM hook but is not initialized", plugin.Name())
		}
		if err := handler.BeforeDeleteVM(ctx, request); err != nil {
			return fmt.Errorf("plugin %s rejected deletion of VMID %d: %w", plugin.Name(), request.VMID, err)
		}
	}
	return nil
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
