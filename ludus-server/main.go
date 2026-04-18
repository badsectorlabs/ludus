/*
 * Ludus - Automated test range deployments made simple.
 */

package main

import (
	"crypto/tls"
	"embed"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	ludusapi "ludusapi"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const ludusInstallPath string = "/opt/ludus"

var ludusPath string

var GitCommitHash string
var VersionString string
var LudusVersion string = VersionString + "+" + GitCommitHash
var existingProxmox bool
var logger *slog.Logger

// Embed the ansible directory into the binary for simple distribution
//
//go:embed all:ansible
var embeddedAnsbileDir embed.FS

//go:embed all:packer
var embeddedPackerDir embed.FS

//go:embed all:ci
var embeddedCIDir embed.FS

func serve() {

	server := &ludusapi.Server{
		Version:          LudusVersion,
		VersionString:    VersionString,
		LudusInstallPath: ludusInstallPath,
		Logger:           logger,
	}

	// Setup PocketBase app
	app := ludusapi.NewRouter(LudusVersion, server)

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
			if !entry.IsDir() && filepath.Ext(entry.Name()) == ".so" {
				path := filepath.Join(pluginsDir, entry.Name())
				if err := server.LoadPlugin(path); err != nil {
					log.Fatalf("Error loading plugin %s: %v", path, err)
				}
			}
		}
	}

	// Initialize plugins
	server.InitializePlugins()

	// Register plugin routes
	server.RegisterPluginRoutes(app)

	// When a user uploads their own certificate to proxmox, it gets saved as pveproxy-ssl.pem and pveproxy-ssl.key in the /etc/pve/nodes/<node>/ directory.
	// If these files exist, use them instead of the proxmox CA signed certs (pve-ssl.pem and pve-ssl.key), and only fall back to our own self signed certs if both are missing.
	certPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pveproxy-ssl.pem"
	keyPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pveproxy-ssl.key"

	// Check if the pveproxy-ssl.pem and pveproxy-ssl.key files exist and we can read them. If not, use the proxmox CA signed certs.
	if !fileExists(certPath) || !fileExists(keyPath) {
		certPath = "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.pem"
		keyPath = "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.key"
	}

	// Check if the pve-ssl.pem and pve-ssl.key files exist and we can read them. If not, generate our own self signed certs.
	if !fileExists(certPath) || !fileExists(keyPath) {
		log.Println("Could not find/read " + certPath + " or " + keyPath)
		generateSelfSignedCert()
		certPath = "/opt/ludus/cert.pem"
		keyPath = "/opt/ludus/key.pem"
	}

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
		return e.Next()
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

	// Sanity checks
	checkArgs()
	checkDebian12or13()
	checkForVirtualizationSupport()
	generateConfigIfAutomatedInstall()
	inCluster = isInCluster()

	// If we're done installing, serve the API
	if fileExists(fmt.Sprintf("%s/install/.stage-3-complete", ludusInstallPath)) && !fileExists("/etc/systemd/system/ludus-install.service") {
		checkConfig()
		serve()
	}

	log.Printf("Ludus server %s", LudusVersion)

	// The install hasn't finished, so make sure we're root, then run through the install
	checkRoot()

	// If this is a proxmox 8 machine, print some warnings and set the bool
	existingProxmox = checkForProxmox8or9()

	getInstallStep(existingProxmox)
	// Use pip to install ansible because Debian's ansible apt package is 4 versions out of date (2.10, current is 2.14)
	installAnsibleWithPip()
	// Make sure we have the ansible galaxy package required for Ludus
	installAnsibleRequirements()
	// Run the install playbooks with ansible now that it is installed
	runInstallPlaybook(existingProxmox)
	// If initial-admin.yml exists, run bootstrap to create ROOT + initial admin.
	time.Sleep(3 * time.Second)
	if interactiveInstall {
		initialAdminPath := fmt.Sprintf("%s/install/initial-admin.yml", ludusInstallPath)
		if fileExists(initialAdminPath) {
			runBootstrapOnly()
		}
	}
}
