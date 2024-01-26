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
