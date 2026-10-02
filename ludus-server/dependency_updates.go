package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-version"
	"gopkg.in/yaml.v2"
)

const (
	// Minimum required versions
	minPackerVersion              = "1.9.4" // No v in the version string
	minPackerProxmoxPluginVersion = "1.2.4" // No v in the version string - Note we are using the badsectorlabs/proxmox plugin instead of hashicorp/proxmox
	minPackerAnsiblePluginVersion = "1.1.1" // No v in the version string
	ansibleCoreVersion            = "2.21.4"
	// Define the ansible roles and collection versions in the requirements.yml file (ludus-server/ansible/requirements.yml)
)

//go:embed lxc/python-requirements.txt
var controllerRequirements []byte

// checkAndUpdateDependencies installs the release's controller dependencies.
func checkAndUpdateDependencies() error {
	if err := updateControllerPython(ludusInstallPath); err != nil {
		return err
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
				// LXC deps are baked at image-build time and the design floor is
				// Proxmox >= 9.1, so we no longer shell out to pveversion to pick
				// a legacy plugin pin.
				Run("PACKER_PLUGIN_PATH="+ludusInstallPath+"/resources/packer/plugins packer plugins install github.com/badsectorlabs/proxmox v"+minVersion, false, true)
			}
		} else {
			log.Printf("Packer plugin %s version %s meets minimum required version %s", pluginName, pluginVer, minVersion)
		}
	}

	return nil
}

func runDependencyCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dependency command %s failed: %w", name, err)
	}
	return nil
}

func updateControllerPython(installPath string) error {
	python := filepath.Join(installPath, "venv/bin/python3")
	if err := runDependencyCommand(python, "-c",
		"import sys; sys.exit(0 if (3, 12) <= sys.version_info[:2] <= (3, 14) else 'ansible-core 2.21 requires controller Python 3.12 through 3.14')"); err != nil {
		return fmt.Errorf("managed controller Python is unavailable or unsupported: %w", err)
	}
	args := []string{"-m", "pip", "install", "--disable-pip-version-check"}
	requirements, err := os.CreateTemp("", "ludus-python-requirements-")
	if err != nil {
		return err
	}
	defer os.Remove(requirements.Name())
	if _, err := requirements.Write(controllerRequirements); err != nil {
		requirements.Close()
		return err
	}
	if err := requirements.Close(); err != nil {
		return err
	}
	args = append(args, "-r", requirements.Name())
	if err := runDependencyCommand(python, args...); err != nil {
		return err
	}
	if err := runDependencyCommand(python, "-m", "pip", "check"); err != nil {
		return err
	}
	return runDependencyCommand(python, "-c",
		"import ansible.release,sys; sys.exit(0 if ansible.release.__version__ == sys.argv[1] else 'unexpected ansible-core version')", ansibleCoreVersion)
}

type shippedAnsibleRequirement struct {
	Name    string `yaml:"name"`
	Version string `yaml:"version"`
}

// Verify all release pins before replacing any active dependency.
func validateAnsiblePayload(requirements []byte, resources string) error {
	var pins struct {
		Collections []shippedAnsibleRequirement `yaml:"collections"`
		Roles       []shippedAnsibleRequirement `yaml:"roles"`
	}
	if err := yaml.Unmarshal(requirements, &pins); err != nil {
		return err
	}
	for _, pin := range pins.Collections {
		parts := strings.Split(pin.Name, ".")
		if len(parts) != 2 {
			return fmt.Errorf("invalid shipped collection name %q", pin.Name)
		}
		data, err := os.ReadFile(filepath.Join(resources, "global-collections/ansible_collections", parts[0], parts[1], "MANIFEST.json"))
		if err != nil {
			return err
		}
		var manifest struct {
			Info struct {
				Version string `json:"version"`
			} `json:"collection_info"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return err
		}
		if manifest.Info.Version != pin.Version {
			return fmt.Errorf("collection %s has version %s, want %s", pin.Name, manifest.Info.Version, pin.Version)
		}
	}
	for _, pin := range pins.Roles {
		data, err := os.ReadFile(filepath.Join(resources, "global-roles", pin.Name, "meta/.galaxy_install_info"))
		if err != nil {
			return err
		}
		var info struct {
			Version string `yaml:"version"`
		}
		if err := yaml.Unmarshal(data, &info); err != nil {
			return err
		}
		if info.Version != pin.Version {
			return fmt.Errorf("role %s has version %s, want %s", pin.Name, info.Version, pin.Version)
		}
	}
	return nil
}

// Refuse symlinked parents rather than deleting through a user-controlled path.
func removeShippedAnsiblePath(root, relative string) error {
	parent := root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		parent = filepath.Join(parent, part)
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("refusing to remove shipped Ansible content through non-directory %s", parent)
		}
	}
	return os.RemoveAll(filepath.Join(root, relative))
}

// Only the release's staged identities are replaced. Unrelated global and
// per-user installations survive; old user copies cannot shadow the new pins.
func installAnsiblePayload(installPath, resources string) error {
	users, err := os.ReadDir(filepath.Join(installPath, "users"))
	if err != nil {
		return err
	}
	type resource struct {
		source, global, user string
	}
	var replacements []resource
	roles, err := os.ReadDir(filepath.Join(resources, "global-roles"))
	if err != nil {
		return err
	}
	for _, role := range roles {
		if role.IsDir() {
			replacements = append(replacements, resource{
				filepath.Join(resources, "global-roles", role.Name()),
				filepath.Join(installPath, "resources/global-roles", role.Name()),
				filepath.Join(".ansible/roles", role.Name()),
			})
		}
	}
	collections := filepath.Join(resources, "global-collections/ansible_collections")
	namespaces, err := os.ReadDir(collections)
	if err != nil {
		return err
	}
	for _, namespace := range namespaces {
		if !namespace.IsDir() || strings.HasSuffix(namespace.Name(), ".info") {
			continue
		}
		names, err := os.ReadDir(filepath.Join(collections, namespace.Name()))
		if err != nil {
			return err
		}
		for _, name := range names {
			if !name.IsDir() {
				continue
			}
			identity := filepath.Join(namespace.Name(), name.Name())
			replacements = append(replacements, resource{
				filepath.Join(collections, identity),
				filepath.Join(installPath, "resources/global-collections/ansible_collections", identity),
				filepath.Join(".ansible/collections/ansible_collections", identity),
			})
		}
	}
	for _, replacement := range replacements {
		relative, err := filepath.Rel(installPath, replacement.global)
		if err != nil {
			return err
		}
		if err := removeShippedAnsiblePath(installPath, relative); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(replacement.global), 0755); err != nil {
			return err
		}
		if err := runDependencyCommand("cp", "-a", replacement.source, replacement.global); err != nil {
			return err
		}
		for _, user := range users {
			if user.IsDir() {
				if err := removeShippedAnsiblePath(installPath, filepath.Join("users", user.Name(), replacement.user)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func updateAnsibleRoles() error {
	requirementsPath := filepath.Join(ludusInstallPath, "ansible/requirements.yml")
	requirements, err := os.ReadFile(requirementsPath)
	if err != nil {
		return err
	}
	resources, err := os.MkdirTemp("", "ludus-ansible-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(resources)
	galaxy := filepath.Join(ludusInstallPath, "venv/bin/ansible-galaxy")
	for _, item := range []struct{ kind, directory string }{
		{"collection", "global-collections"}, {"role", "global-roles"},
	} {
		if err := runDependencyCommand(galaxy, item.kind, "install", "-r", requirementsPath,
			"-p", filepath.Join(resources, item.directory), "--force"); err != nil {
			return err
		}
	}
	if err := validateAnsiblePayload(requirements, resources); err != nil {
		return fmt.Errorf("invalid shipped Ansible payload: %w", err)
	}
	if err := installAnsiblePayload(ludusInstallPath, resources); err != nil {
		return err
	}
	return runDependencyCommand("chown", "-R", "ludus:ludus",
		filepath.Join(ludusInstallPath, "resources/global-roles"),
		filepath.Join(ludusInstallPath, "resources/global-collections"))
}
