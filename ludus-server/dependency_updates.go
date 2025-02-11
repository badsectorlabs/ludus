package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-version"
)

const (
	// Minimum required versions
	minPackerVersion              = "1.11.2" // No v in the version string
	minPackerProxmoxPluginVersion = "1.2.2"  // No v in the version string
	minPackerAnsiblePluginVersion = "1.1.1"  // No v in the version string
	minAnsibleCoreVersion         = "2.16.0" // 2.16.14 is the last version that supports Python 2.7 on the target hosts
	ansiblePyPiVersionToInstall   = "9.13.0" // 9.13.0 is the last version that supports Python 2.7 on the target hosts
	// Define the ansible roles and collection versions in the requirements.yml file (ludus-server/ansible/requirements.yml)
)

// checkAndUpdateDependencies ensures ansible and packer meet minimum version requirements
func checkAndUpdateDependencies() error {
	// Check ansible version
	ansibleCmd := exec.Command("ansible", "--version")
	ansibleOut, err := ansibleCmd.Output()
	if err != nil {
		return fmt.Errorf("error checking ansible version: %v", err)
	}

	// Parse ansible version from first line which looks like: "ansible [core 2.15.2]"
	ansibleVer := strings.Split(strings.Split(string(ansibleOut), "\n")[0], " ")[2]
	ansibleVer = strings.Trim(ansibleVer, "[]")

	if !versionMeetsMinimum(ansibleVer, minAnsibleCoreVersion) {
		log.Printf("Ansible version %s does not meet minimum required version %s. Updating...", ansibleVer, minAnsibleCoreVersion)
		Run("python3 -m pip install ansible=="+ansiblePyPiVersionToInstall+" --break-system-packages", false, true)
	} else {
		log.Printf("Ansible version %s meets minimum required version %s", ansibleVer, minAnsibleCoreVersion)
	}

	// Check packer version
	packerCmd := exec.Command("packer", "version")
	packerOut, err := packerCmd.Output()
	if err != nil {
		return fmt.Errorf("error checking packer version: %v", err)
	}

	// Parse packer version from output like: "Packer v1.9.2" with possible newlines
	packerVer := strings.Split(string(packerOut), " ")[1]
	packerVer = strings.TrimPrefix(packerVer, "v")
	packerVer = strings.TrimSpace(packerVer)
	packerVer = strings.Split(packerVer, "\n")[0]

	if !versionMeetsMinimum(packerVer, minPackerVersion) {
		log.Printf("Packer version %s does not meet minimum required version %s. Updating...", packerVer, minPackerVersion)
		Run("curl -o /tmp/packer.zip https://releases.hashicorp.com/packer/"+minPackerVersion+"/packer_"+minPackerVersion+"_linux_amd64.zip", false, true)
		Run("unzip -qq -o -d /tmp /tmp/packer.zip", false, true)
		Run("mv /tmp/packer /usr/local/bin/packer", false, true)
		Run("rm /tmp/packer.zip /tmp/LICENSE.txt", false, true)
	} else {
		log.Printf("Packer version %s meets minimum required version %s", packerVer, minPackerVersion)
	}

	return nil
}

// versionMeetsMinimum compares version strings and returns true if version meets or exceeds minimum
func versionMeetsMinimum(versionString, minimumString string) bool {
	currentVersion, err := version.NewVersion(versionString)
	if err != nil {
		log.Printf("Error parsing version string %s: %v", versionString, err)
		return false
	}
	minimumVersion, err := version.NewVersion(minimumString)
	if err != nil {
		log.Printf("Error parsing minimum version string %s: %v", minimumString, err)
		return false
	}
	if currentVersion.LessThan(minimumVersion) {
		return false
	} else {
		return true
	}
}

// checkPackerPluginVersions checks if packer plugins meet minimum version requirements and updates them if needed
func checkPackerPluginVersions() error {
	// Get list of installed plugins
	pluginCmd := exec.Command("packer", "plugins", "installed")
	pluginCmd.Env = os.Environ()
	pluginCmd.Env = append(pluginCmd.Env, fmt.Sprintf("PACKER_PLUGIN_PATH=%s/resources/packer/plugins", ludusInstallPath))
	pluginOut, err := pluginCmd.Output()
	if err != nil {
		return fmt.Errorf("error checking packer plugin versions: %v", err)
	}

	// Parse plugin output
	plugins := strings.Split(string(pluginOut), "\n")
	for _, plugin := range plugins {
		if plugin == "" {
			continue
		}

		// Parse plugin name and version
		// Format is: /opt/ludus/resources/packer/plugins/github.com/hashicorp/ansible/packer-plugin-ansible_v1.1.1_x5.0_linux_amd64
		parts := strings.Split(plugin, "_v")
		if len(parts) < 2 {
			continue
		}

		pluginName := filepath.Base(parts[0])
		pluginVer := strings.Split(parts[1], "_")[0]

		// Check version against minimum requirements
		var minVersion string
		switch pluginName {
		case "packer-plugin-proxmox":
			minVersion = minPackerProxmoxPluginVersion
		case "packer-plugin-ansible":
			minVersion = minPackerAnsiblePluginVersion
		default:
			continue
		}

		if !versionMeetsMinimum(pluginVer, minVersion) {
			log.Printf("Packer plugin %s version %s does not meet minimum required version %s. Updating...", pluginName, pluginVer, minVersion)

			// Remove existing plugin
			err := os.RemoveAll(filepath.Dir(plugin))
			if err != nil {
				return fmt.Errorf("error removing old plugin %s: %v", pluginName, err)
			}

			// Install latest version
			log.Printf("Updating packer plugin %s from version %s to version %s", pluginName, pluginVer, minVersion)
			if pluginName == "packer-plugin-ansible" {
				Run("PACKER_PLUGIN_PATH="+ludusInstallPath+"/resources/packer/plugins packer plugins install github.com/hashicorp/ansible v"+minVersion, false, true)
			} else if pluginName == "packer-plugin-proxmox" {
				Run("PACKER_PLUGIN_PATH="+ludusInstallPath+"/resources/packer/plugins packer plugins install github.com/hashicorp/proxmox v"+minVersion, false, true)
			}
		} else {
			log.Printf("Packer plugin %s version %s meets minimum required version %s", pluginName, pluginVer, minVersion)
		}
	}

	return nil
}

// Without the --force flag, this will only update collections, not roles listed in the requirements.yml file
func updateAnsibleRoles() error {
	// Get list of users
	users, err := os.ReadDir(ludusInstallPath + "/users")
	if err != nil {
		return fmt.Errorf("error reading users directory: %v", err)
	}

	// Update ansible roles for each user
	for _, user := range users {
		if !user.IsDir() {
			continue
		}

		username := user.Name()
		if username == "root" {
			continue
		}
		log.Printf("Updating required ansible roles for user %s\n", username)
		cmd := fmt.Sprintf("su ludus -c 'ANSIBLE_HOME=%s/users/%s/.ansible ansible-galaxy install -r %s/ansible/requirements.yml'",
			ludusInstallPath, username, ludusInstallPath)

		Run(cmd, false, true)

	}

	// Add the root user's ansible roles
	log.Println("Updating required ansible roles for user root")
	cmd := fmt.Sprintf("ANSIBLE_HOME=%s/users/root/.ansible ansible-galaxy install -r %s/ansible/requirements.yml",
		ludusInstallPath, ludusInstallPath)

	Run(cmd, false, true)

	return nil
}
