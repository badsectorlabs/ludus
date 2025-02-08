/*
 * Ludus - Automated test range deployments made simple.
 */

package main

import (
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"

	ludusapi "ludusapi"
)

const ludusInstallPath string = "/opt/ludus"

var ludusPath string

var GitCommitHash string
var VersionString string
var LudusVersion string = VersionString + "+" + GitCommitHash

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
	}

	// Setup Gin router
	router := ludusapi.NewRouter(LudusVersion, server)

	if server.LicenseType == "community" {
		fmt.Println("LICENSE: Community Edition")
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
			log.Printf("Error reading plugins directory: %v", err)
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
	server.RegisterPluginRoutes(router)

	certPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.pem"
	keyPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.key"

	// Check if the pve-ssl.pem and pve-ssl.key files exist and we can read them
	if !fileExists(certPath) || !fileExists(keyPath) {
		log.Println("Could not find/read " + certPath + " or " + keyPath)
		generateSelfSignedCert()
		certPath = "/opt/ludus/cert.pem"
		keyPath = "/opt/ludus/key.pem"
	}
	// If we're running as a non-root user, bind to all interfaces, else (running as root) bind to localhost unless the user has opted to expose the admin API globally
	var err error
	if os.Geteuid() != 0 {
		err = router.RunTLS("0.0.0.0:8080", certPath, keyPath)
	} else {
		if config.ExposeAdminPort {
			err = router.RunTLS("0.0.0.0:8081", certPath, keyPath)
		} else {
			err = router.RunTLS("127.0.0.1:8081", certPath, keyPath)
		}
	}
	server.ShutdownPlugins()
	if err != nil {
		log.Fatalf("Error in Ludus API server: %v", err)
	}

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
	checkDebian12()
	checkForVirtualizationSupport()
	generateConfigIfAutomatedInstall()

	// If we're done installing, serve the API
	if fileExists(fmt.Sprintf("%s/install/.stage-3-complete", ludusInstallPath)) && !fileExists("/etc/systemd/system/ludus-install.service") {
		checkConfig()
		serve()
	}

	log.Printf("Ludus server %s", LudusVersion)

	// The install hasn't finished, so make sure we're root, then run through the install
	checkRoot()

	// If this is a proxmox 8 machine, print some warnings and set the bool
	existingProxmox := checkForProxmox8()

	getInstallStep(existingProxmox)
	// Use pip to install ansible because Debian's ansible apt package is 4 versions out of date (2.10, current is 2.14)
	installAnsibleWithPip()
	// Make sure we have the ansible galaxy package required for Ludus
	installAnsibleRequirements()
	// Run the install playbooks with ansible now that it is installed
	runInstallPlaybook(existingProxmox)

}
