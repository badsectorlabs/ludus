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
		LudusInstallPath: ludusInstallPath,
	}

	// Load plugins
	pluginsDir := fmt.Sprintf("%s/plugins", ludusInstallPath)
	pluginsFound := false
	err := filepath.Walk(pluginsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) == ".so" {
			pluginsFound = true
			if err := server.LoadPlugin(path); err != nil {
				log.Printf("Error loading plugin %s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("Error walking plugins directory: %v", err)
	}
	if !pluginsFound {
		fmt.Println("LICENSE: Community Edition")
	}

	// Initialize plugins
	server.InitializePlugins()

	// Setup Gin router
	router := ludusapi.NewRouter(LudusVersion, server)

	// Register plugin routes
	server.RegisterPluginRoutes(router)

	certPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.pem"
	keyPath := "/etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.key"

	// Check if the pve-ssl.pem and pve-ssl.key files exist and we can read them
	if !fileExists("/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.pem") || !fileExists("/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.key") {
		log.Println("Could not find/read /etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.pem or /etc/pve/nodes/" + config.ProxmoxNode + "/pve-ssl.key")
		generateSelfSignedCert()
		certPath = "/opt/ludus/cert.pem"
		keyPath = "/opt/ludus/key.pem"
	}
	// If we're running as a non-root user, bind to all interfaces, else (running as root) bind to localhost
	if os.Geteuid() != 0 {
		err = router.RunTLS("0.0.0.0:8080", certPath, keyPath)
	} else {
		err = router.RunTLS("127.0.0.1:8081", certPath, keyPath)
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
