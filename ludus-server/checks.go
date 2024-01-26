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

// check the configuration file to ensure it exists and that it does not have default values
func checkConfig() {
	configPath := fmt.Sprintf("%s/config.yml", ludusPath)

	// First run, no config
	if ludusPath != fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) {
		// If we are running without prompts, generate a config automatically
		if autoGenerateConfig {
			log.Printf("No config.yml found. Generating a config at %s/config.yml. Please check that it contains the correct values.", ludusPath)
			automatedConfigGenerator()
		} else {
			log.Printf("No config.yml found. Generating an example config at %s/config.yml. Please edit it and re-run the install.", ludusPath)

			fileContent, err := embeddedAnsbileDir.ReadFile("ansible/config.yml.example")
			if err != nil {
				log.Fatal(err.Error())
			}

			if err := os.WriteFile(configPath, fileContent, 0644); err != nil {
				log.Printf("error os.WriteFile error: %v", err)
				log.Fatal(err.Error())
			}
			os.Exit(1)
		}

	} else if ludusPath != fmt.Sprintf("%s/ludus-server", ludusInstallPath) { // First run, example config provided
		configContents, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatal(err.Error())
		}
		if strings.Contains(string(configContents), "192.168.0.0") {
			log.Fatalf("The config file (%s) contains example values. Edit it to reflect this machine.\n", configPath)
		}

	} else if ludusPath == fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) { // Installed, but config missing
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

// check /etc/os-release for Debian 11, throw a fatal error /etc/os-release does not exist or does not contain the Debian 12 string
func checkDebian12() {
	if fileExists("/etc/os-release") {
		osReleaseContents, err := os.ReadFile("/etc/os-release")
		if err != nil {
			log.Fatal(err.Error())
		}
		if !strings.Contains(string(osReleaseContents), "Debian GNU/Linux 12 (bookworm)") {
			log.Fatal("/etc/os-release did not indicate this is Debian 12. Ludus only supports Debian 12.")
		}
	} else {
		log.Fatal("Could not read /etc/os-release to check for Debian 12. Ludus only supports Debian 12.")
	}
}
