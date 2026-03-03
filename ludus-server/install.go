package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apenella/go-ansible/pkg/execute"
	"github.com/apenella/go-ansible/pkg/options"
	"github.com/apenella/go-ansible/pkg/playbook"
)

func getInstallStep(existingProxmox bool) {
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
		// If the config file exists (was auto created or dropped by a user), copy it to the install path
		if fileExists(fmt.Sprintf("%s/config.yml", ludusPath)) {
			copy(fmt.Sprintf("%s/config.yml", ludusPath), fmt.Sprintf("%s/config.yml", ludusInstallPath))
		}
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
			runInteractiveInstall(existingProxmox)
		}
		// We need to write the canary file if not on an existing proxmox install no matter if this is interactive or not
		if !existingProxmox {
			debainInstallFile, err := os.Create(fmt.Sprintf("%s/install/.installed-on-debian", ludusInstallPath))
			if err != nil {
				log.Fatalf("Failed to create or touch the file %s: %s", fmt.Sprintf("%s/install/.installed-on-debian", ludusInstallPath), err)
			}
			debainInstallFile.Close()
		}
	}
}

func runInstallPlaybook(existingProxmox bool) {
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

	var installPlaybook []string
	if existingProxmox {
		installPlaybook = []string{fmt.Sprintf("%s/ansible/proxmox-install/existing-proxmox.yml", ludusInstallPath)}
	} else {
		installPlaybook = []string{fmt.Sprintf("%s/ansible/proxmox-install/main.yml", ludusInstallPath)}
	}

	playbook := &playbook.AnsiblePlaybookCmd{
		Playbooks:         installPlaybook,
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
	if !fileExists("/usr/local/bin/ansible") {
		log.Println("Installing ansible with pip")
		log.Println("  Updating apt cache...")
		Run("apt update", false, true)
		log.Println("  Installing python3-pip...")
		Run("DEBIAN_FRONTEND=noninteractive apt-get -qqy install python3-pip", false, true)
		log.Println("  Installing ansible with pip...")
		Run("python3 -m pip install ansible==9.3.0 netaddr==1.2.1 --break-system-packages", false, true)
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
}
