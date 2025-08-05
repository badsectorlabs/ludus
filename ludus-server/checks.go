package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

// check for vmx or svm features in /proc/cpu with egrep. Will print a message and exit if they are not found.
func checkForVirtualizationSupport() {
	cpuInfo := Run("egrep '(vmx|svm)' --color=never /proc/cpuinfo", false, false)
	if !strings.Contains(cpuInfo, "vmx") && !strings.Contains(cpuInfo, "svm") {
		log.Fatal(`This machine is not capable of virtualization. 
Ludus is requires a host with vmx or svm enabled on the CPU. 
This is usually a bare metal machine or a nested VM with virtualization support enabled.
For Proxmox, see: https://pve.proxmox.com/wiki/Nested_Virtualization`)
	}
}

func generateConfigIfAutomatedInstall() {
	configPath := fmt.Sprintf("%s/config.yml", ludusPath)
	// First run, no config
	if ludusPath != fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) {
		// If we are running without prompts, generate a config automatically
		if autoGenerateConfig {
			log.Printf("No config.yml found. Generating a config at %s/config.yml. Please check that it contains the correct values.", ludusPath)
			automatedConfigGenerator(true)
		}
	}
}

// check the configuration file to ensure it exists and that it does not have default values
func checkConfig() {
	configPath := fmt.Sprintf("%s/config.yml", ludusPath)

	if ludusPath == fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) { // Installed, but config missing
		log.Printf("Config file (%s) missing!\n", configPath)
		os.Exit(1)
	}

	// Open config file
	file, err := os.Open(configPath)
	if err != nil {
		log.Fatalf("Error opening config: %v", err)
	}
	defer file.Close()

	// Init new YAML decode
	d := yaml.NewDecoder(file)

	// Start YAML decoding from file
	if err := d.Decode(&config); err != nil {
		log.Fatalf("Error decoding config: %v", err)
	}
}

// check if the current uid is zero (root) and throw a fatal error if not
func checkRoot() {
	if os.Geteuid() != 0 {
		log.Fatal("Ludus must be run as root.")
	}
}

func checkForProxmox8or9() bool {
	if fileExists("/usr/bin/pveversion") && !fileExists(fmt.Sprintf("%s/install/.installed-on-debian", ludusInstallPath)) {
		pveVersion := Run("pveversion", false, false)
		if strings.Contains(pveVersion, "pve-manager/8") {
			return true
		} else if strings.Contains(pveVersion, "pve-manager/9") {
			return true
		} else if strings.Contains(pveVersion, "pve-manager/7") {
			log.Fatal(`This is a Proxmox host but not proxmox 8 or 9.
Upgrade to Proxmox 8 or 9 before using Ludus.
See: https://pve.proxmox.com/wiki/Upgrade_from_7_to_8
`)
		} else {
			log.Fatal("This is a Proxmox host and is not a supported version. Only Proxmox 8 or 9 are supported by Ludus.")
		}
	}
	return false
}

// check /etc/os-release for Debian 12 or 13, throw a fatal error /etc/os-release does not exist or does not contain the Debian 12 string
func checkDebian12or13() {
	if fileExists("/etc/os-release") {
		osReleaseContents, err := os.ReadFile("/etc/os-release")
		if err != nil {
			log.Fatal(err.Error())
		}
		if !strings.Contains(string(osReleaseContents), "Debian GNU/Linux 12 (bookworm)") && !strings.Contains(string(osReleaseContents), "Debian GNU/Linux 13 (trixie)") {
			log.Fatal("/etc/os-release did not indicate this is Debian 12 or 13. Ludus only supports Debian 12 or 13.")
		}
	} else {
		log.Fatal("Could not read /etc/os-release to check for Debian 12 or 13. Ludus only supports Debian 12 or 13.")
	}
}
