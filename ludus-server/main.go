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

	ludusapi "ludus-server/src"
)

const ludusInstallPath string = "/opt/ludus"

var ludusPath string

var config ludusapi.Configuration = ludusapi.Configuration{}

var interactiveInstall bool
var autoGenerateConfig bool = true

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
	log.Printf("Starting Ludus API server v%s\n", LudusVersion)
	router := ludusapi.NewRouter(LudusVersion)
	// If we're running as a non-root user, bind to the wireguard IP, else bind to localhost
	if os.Geteuid() != 0 {
		// This is safer (must have a WG connection, but is harder for local deployments and CI/CD)
		// log.Fatal(router.RunTLS("198.51.100.1:8080", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.pem", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.key"))
		log.Fatal(router.RunTLS("0.0.0.0:8080", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.pem", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.key"))
	} else {
		log.Fatal(router.RunTLS("127.0.0.1:8081", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.pem", "/etc/pve/nodes/"+config.ProxmoxNode+"/pve-ssl.key"))
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
	checkDebian12()
	checkForVirtualizationSupport()
	checkArgs()
	checkConfig()

	// If we're done installing, serve the API
	if fileExists(fmt.Sprintf("%s/install/.stage-3-complete", ludusInstallPath)) && !fileExists("/etc/systemd/system/ludus-install.service") {
		serve()
	}

	log.Printf("Ludus server v%s", LudusVersion)

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
