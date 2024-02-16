package main

import (
	"bufio"
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

			if existingProxmox {
				log.Printf("Using the following config:\n%s\n", configStr)
				log.Print(`
    ~~~ You are installing Ludus on an existing Proxmox 8 host - here be dragons ~~~
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!
!!! Ludus will install: ansible, packer, dnsmasq, sshpass, curl, jq, iptables-persistent !!!
!!!                     gpg-agent, dbus, dbus-user-session, and vim                      !!!
!!! Ludus will install python packages: proxmoxer, requests, netaddr, pywinrm,           !!!
!!!                                     dnspython, and jmespath                          !!!
!!! Ludus will create the proxmox groups ludus_users and ludus_admins                    !!!
!!! Ludus will create the proxmox pools SHARED and ADMIN                                 !!!
!!! Ludus will create a wireguard server wg0 with IP range 198.51.100.0/24               !!!
!!! Ludus will create an interface 'ludus' with IP range 192.0.2.0/24 that NATs traffic  !!!
!!! Ludus will create user ranges with IPs in the 10.0.0.0/16 network                    !!!
!!! Ludus will create user interfaces starting at vmbr1001 incrementing for each user    !!!
!!! Ludus will create the pam user 'ludus' and pam users for all Ludus users added       !!!
!!! Ludus will create the ludus-admin and ludus systemd services                         !!!
!!! Ludus will listen on 127.0.0.1:8081 and 0.0.0.0:8080                                 !!!
!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!

Carefully consider the above block. If all items are compatible with your existing setup, you
may continue. Ludus comes with NO WARRANTY and no guarantee your existing setup will continue
to function. The Ludus install process will not reboot your host.

If you wish to continue type 'I understand the risks': `)
				r := bufio.NewReader(os.Stdin)
				res, err := r.ReadString('\n')
				if err != nil {
					log.Fatal(err)
				}
				if strings.ToLower(strings.TrimSpace(res)) != "i understand the risks" {
					log.Fatal("Exiting")
				}
			} else {
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
		// We need to write the canary file if no on an existing proxmox install no matter if this is interactive or not
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
