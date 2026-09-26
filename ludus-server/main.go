/*
 * Ludus - Automated test range deployments made simple.
 */

package main

import (
	"context"
	"crypto/tls"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	ludusapi "ludusapi"
	"ludusapi/scheduler"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const ludusInstallPath string = "/opt/ludus"

var ludusPath string

var GitCommitHash string
var VersionString string
var LudusVersion string = VersionString + "+" + GitCommitHash
var config ludusapi.Configuration
var logger *slog.Logger

// Embed the ansible directory into the binary for simple distribution
//
//go:embed all:ansible
var embeddedAnsbileDir embed.FS

// Keep direct ludus-server builds from silently omitting the generated
// dynamic-inventory binary. Build ../dynamic-inventory before building server.
//
//go:embed ansible/range-management/dynamic-inventory
var embeddedDynamicInventory []byte

//go:embed all:packer
var embeddedPackerDir embed.FS

//go:embed all:ci
var embeddedCIDir embed.FS

func pluginAPIPort(euid int, configuration ludusapi.Configuration) int {
	if euid == 0 {
		return configuration.AdminPort
	}
	return configuration.Port
}

func serve() {

	server := &ludusapi.Server{
		Version:          LudusVersion,
		VersionString:    VersionString,
		LudusInstallPath: ludusInstallPath,
		Logger:           logger,
		Scheduler:        scheduler.New(logger),
	}

	// Setup PocketBase app
	app := ludusapi.NewRouter(LudusVersion, server)

	certPath, keyPath := serverCertificatePaths()
	certificateFingerprint, err := certificateSHA256(certPath)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to fingerprint server certificate: %v", err))
		return
	}
	server.PluginAPIURL = fmt.Sprintf("https://127.0.0.1:%d", pluginAPIPort(os.Geteuid(), config))
	server.PluginAPICertificateSHA256 = certificateFingerprint

	if len(server.Entitlements) == 0 {
		logger.Info("LICENSE: Community (no entitlements)")
	}

	// Load plugins
	var pluginsDir string
	if os.Geteuid() == 0 {
		pluginsDir = fmt.Sprintf("%s/plugins/community/admin", ludusInstallPath)
	} else {
		pluginsDir = fmt.Sprintf("%s/plugins/community/", ludusInstallPath)
	}

	// Check if plugins directory exists and is a directory, if so load the plugins from it
	if info, err := os.Stat(pluginsDir); err == nil && info.IsDir() {
		entries, err := os.ReadDir(pluginsDir)
		if err != nil {
			logger.Error(fmt.Sprintf("Error reading plugins directory: %v", err))
		}

		for _, entry := range entries {
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".plugin" {
				path := filepath.Join(pluginsDir, entry.Name())
				if err := server.LoadPlugin(path); err != nil {
					logger.Error(fmt.Sprintf("Error loading plugin %s: %v", path, err))
				}
			}
		}
	}

	// Initialize plugins; failures are soft — keep serving without the broken plugin.
	if err := server.InitializePlugins(); err != nil {
		logger.Error(fmt.Sprintf("Error initializing plugins: %v", err))
	}
	if err := server.StartVMHookService(); err != nil {
		log.Fatalf("Error starting VM hook service: %v", err)
	}

	// Register plugin routes
	server.RegisterPluginRoutes(app)
	// Setup the server to use the certificate/key found above
	serveConfig := apis.ServeConfig{
		ShowStartBanner: false,
		AllowedOrigins:  []string{"*"},
	}
	ludusApp := *app
	ludusApp.OnServe().BindFunc(func(e *core.ServeEvent) error {
		certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return err
		}
		e.Server.TLSConfig = &tls.Config{
			Certificates: []tls.Certificate{certificate},
		}
		// PocketBase defaults to 5 min Read/WriteTimeout; extend for long-running requests (e.g. antisandbox enable)
		e.Server.ReadTimeout = 30 * time.Minute
		e.Server.WriteTimeout = 30 * time.Minute
		// PocketBase builds the router and binds its listener in e.Next().
		// Plugin jobs need that listener for their first record queries.
		if err := e.Next(); err != nil {
			return err
		}
		server.Scheduler.Start()
		return nil
	})

	// If we're running as a non-root user, bind to all interfaces, else (running as root) bind to localhost unless the user has opted to expose the admin API globally
	if os.Geteuid() != 0 {
		serveConfig.HttpsAddr = fmt.Sprintf("0.0.0.0:%d", config.Port)
		logger.Debug("Starting server on " + serveConfig.HttpsAddr)
		if err := apis.Serve(ludusApp, serveConfig); err != nil {
			logger.Error(fmt.Sprintf("Failed to start the server: %v", err))
		}
	} else {
		if config.ExposeAdminPort {
			serveConfig.HttpsAddr = fmt.Sprintf("0.0.0.0:%d", config.AdminPort)
		} else {
			serveConfig.HttpsAddr = fmt.Sprintf("127.0.0.1:%d", config.AdminPort)
		}
		logger.Debug("Starting server on " + serveConfig.HttpsAddr)
		if err := apis.Serve(ludusApp, serveConfig); err != nil {
			logger.Error(fmt.Sprintf("Failed to start the server: %v", err))
		}
	}
	server.Scheduler.Stop()
	if err := server.StopVMHookService(); err != nil {
		logger.Error(fmt.Sprintf("Error stopping VM hook service: %v", err))
	}
	server.ShutdownPlugins()

}

// runBootstrapOnly runs the API bootstrap (config load, PocketBase init, migrations, InitDb)
// without starting the HTTP server. Used after install playbook to create ROOT and initial admin.
func runBootstrapOnly() {
	server := &ludusapi.Server{
		Version:          LudusVersion,
		VersionString:    VersionString,
		LudusInstallPath: ludusInstallPath,
		Logger:           logger,
	}
	_ = ludusapi.NewRouter(LudusVersion, server)
}

func main() {

	// Remove date and time from log output
	log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))

	// Get the path of the executable to ensure correct install during serve
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	ludusPath = filepath.Dir(ex)

	checkArgs()
	checkConfig()

	log.Printf("Ludus server %s", LudusVersion)

	// First boot inside the LXC: provision Proxmox objects + local state via the
	// Go bootstrap path (replaces the old ansible proxmox-install playbooks).
	if !fileExists(bootstrapMarker) {
		checkRoot()
		ctx := context.Background()
		if err := bootstrap(ctx, config); err != nil {
			log.Fatalf("bootstrap failed: %v", err)
		}
		runBootstrapOnly() // create ROOT user + admin key
	}
	serve()
}
