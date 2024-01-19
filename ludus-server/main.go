/*
 * Ludus - Automated test range deployments made simple.
 */

package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	ludusapi "ludus-server/src"

	"github.com/apenella/go-ansible/pkg/execute"
	"github.com/apenella/go-ansible/pkg/options"
	"github.com/apenella/go-ansible/pkg/playbook"
)

const ludusInstallPath string = "/opt/ludus"

var ludusPath string

var config ludusapi.Configuration = ludusapi.Configuration{}

var interactiveInstall bool
var autoGenerateConfig bool = true

var GitCommitHash string
var LudusVersion string = "1.0.0+" + GitCommitHash

// Embed the ansible directory into the binary for simple distribution
//
//go:embed all:ansible
var embeddedAnsbileDir embed.FS

//go:embed all:packer
var embeddedPackerDir embed.FS

//go:embed all:ci
var embeddedCIDir embed.FS

// Return nil if the WireGuard IP is found on the local machine, otherwise return an error
func checkForWGIP() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			return err
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip.Equal(net.ParseIP("198.51.100.1")) {
				return nil
			}
		}
	}

	return errors.New("wireguard IP not found. Has the ludus install finished?")
}

func runInstallPlaybook() {
	// Check if we are in a tty (console) or pts (ssh) to know if we can kick over the TTY service during install
	inTTY := Run("tty | grep tty > /dev/null && echo true || echo false", false, false)
	inTTYBool, err := strconv.ParseBool(strings.Trim(inTTY, "\n"))
	if err != nil {
		panic(err)
	}
	extraVars := map[string]interface{}{"in_tty": inTTYBool}

	log.Println("Running ludus install playbook")
	ansiblePlaybookConnectionOptions := &options.AnsibleConnectionOptions{
		Connection: "local",
	}

	ansiblePlaybookOptions := &playbook.AnsiblePlaybookOptions{
		Inventory:     "127.0.0.1,",
		ExtraVarsFile: []string{fmt.Sprintf("@%s/config.yml", ludusInstallPath)},
		ExtraVars:     extraVars,
	}
	ansibleLogFile, err := os.OpenFile(fmt.Sprintf("%s/install/install.log", ludusInstallPath), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		log.Fatal(err.Error())
	}
	execute := execute.NewDefaultExecute(
		// Write to a log file and stdout
		execute.WithWrite(io.MultiWriter(os.Stdout, ansibleLogFile)),
	)

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         []string{fmt.Sprintf("%s/ansible/proxmox-install/main.yml", ludusInstallPath)},
		Exec:              execute,
		ConnectionOptions: ansiblePlaybookConnectionOptions,
		Options:           ansiblePlaybookOptions,
	}

	err = playbook.Run(context.TODO())
	if err != nil {
		panic(err)
	}
}

func installAnsibleWithPip() {
	if strings.Contains(Run("ansible --version", false, false), "command not found") {
		log.Println("Installing ansible with pip")
		log.Println("  Updating apt cache...")
		Run("apt update", false, true)
		log.Println("  Installing python3-pip...")
		Run("DEBIAN_FRONTEND=noninteractive apt-get -qqy install python3-pip", false, true)
		log.Println("  Installing ansible with pip...")
		Run("python3 -m pip install ansible==8.4.0 netaddr==0.9.0 --break-system-packages", false, true)
		log.Println("  Printing ansible version...")
		Run("ansible --version", true, true)
	}
}

func installAnsibleRequirements() {
	_, err := os.Stat(fmt.Sprintf("%s/install/.ansible-requirements-installed", ludusInstallPath))
	if err != nil {
		log.Println("Installing ansible requirements")
		Run(fmt.Sprintf("ansible-galaxy install -r %s/ansible/requirements.yml", ludusInstallPath), true, true)
		os.Create(fmt.Sprintf("%s/install/.ansible-requirements-installed", ludusInstallPath))
	}
	// Temporary to support debian 12/proxmox 8 until the pull request is merged in
	// https://github.com/lae/ansible-role-proxmox/pull/230
	log.Print("Running a one-off ansible-galaxy command to support debian 12 and proxmox 8")
	Run("ansible-galaxy install https://github.com/lexxxel/ansible-role-proxmox/archive/feature/add_bookworm_and_debian_12_compatibility.tar.gz,pr-230,lae.proxmox --force", true, true)
}

func getInstallStep() {
	installedBinaryPath := fmt.Sprintf("%s/ludus-server", ludusInstallPath)
	if ludusPath != ludusInstallPath && fileExists(installedBinaryPath) { // After first install run, but not in /opt/ludus
		log.Println("Please run the installed copy of ludus-server in /opt/ludus")
		log.Printf("This binary will now delete itself. It has been copied to %s\n", ludusInstallPath)
		os.Remove(fmt.Sprintf("%s/ludus-server", ludusPath))
		os.Remove(fmt.Sprintf("%s/config.yml", ludusPath))
		os.Exit(1)
	} else if ludusPath != ludusInstallPath && !fileExists(installedBinaryPath) { // First install run
		if _, err := os.Stat(ludusInstallPath); os.IsNotExist(err) {
			os.MkdirAll(ludusInstallPath, 0755)
		}
		checkDirAndReplaceFiles()

		// Make the install directory
		os.MkdirAll(fmt.Sprintf("%s/install", ludusInstallPath), 0700)

		// Copy the server binary, chmod it correctly, and copy the config to the install path
		exePath, err := os.Executable()
		if err != nil {
			log.Fatal("Error getting executable path:", err)
			return
		}
		exeName := filepath.Base(exePath)
		copy(fmt.Sprintf("%s/%s", ludusPath, exeName), installedBinaryPath)
		chownFileToUsername(installedBinaryPath, "root")
		os.Chmod(installedBinaryPath, 0711)
		copy(fmt.Sprintf("%s/config.yml", ludusPath), fmt.Sprintf("%s/config.yml", ludusInstallPath))
	}

	if fileExists(fmt.Sprintf("%s/install/.stage-3-complete", ludusInstallPath)) {
		log.Println("Ludus install complete!")
		os.Exit(0)
	} else if fileExists(fmt.Sprintf("%s/install/.stage-2-complete", ludusInstallPath)) {
		log.Println("Step 2 complete, running step 3. There will be no reboot after this step.")
	} else if fileExists(fmt.Sprintf("%s/install/.stage-1-complete", ludusInstallPath)) {
		log.Println("Step 1 complete, running step 2. The machine will reboot at the end of this step.")
	} else {
		if interactiveInstall {
			var yes string

			configBytes, err := os.ReadFile("config.yml")
			if err != nil {
				log.Fatal(err)
			}
			configStr := string(configBytes)

			log.Printf("\n!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!\n")
			log.Println("!!! Only run Ludus install on a clean Debian 12 machine that will be dedicated to Ludus !!!")
			log.Println("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")

			log.Printf("Using the following config:\n%s\n", configStr)

			log.Print(`
Ludus install will cause the machine to reboot twice. Install will continue
automatically after each reboot. Check the progress of the install by running: 
'ludus-install-status' from a root shell.
	
Do you want to continue? (y/N): `)
			fmt.Scanln(&yes)
			if yes != "Y" && yes != "y" {
				log.Fatal("Exiting")
			}
		}
	}
}

func serve() {
	// We now serve on 0.0.0.0, so no need to check for a WG IP
	// err := checkForWGIP()
	// if err != nil {
	// 	log.Fatal(err.Error())
	// }
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

	getInstallStep()
	// Use pip to install ansible because Debian's ansible apt package is 4 versions out of date (2.10, current is 2.14)
	installAnsibleWithPip()
	// Make sure we have the ansible galaxy package required for Ludus
	installAnsibleRequirements()
	// Run the install playbooks with ansible now that it is installed
	runInstallPlaybook()

}
