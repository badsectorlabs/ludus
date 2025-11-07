package ludusapi

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// manageVmbrInterfaceLocally directly edits /etc/network/interfaces
// This function MUST be run as root on the Proxmox host.
func manageVmbrInterfaceLocally(rangeNumber int, present bool) error {
	interfacesPath := "/etc/network/interfaces"
	ifaceName := fmt.Sprintf("vmbr1%03d", rangeNumber)

	// We have to use the term "USER" instead of "RANGE" because ludus 1.x used it
	marker := fmt.Sprintf("# LUDUS MANAGED INTERFACE FOR USER %d {mark}", rangeNumber)

	block := fmt.Sprintf(`auto %s
iface %s inet manual
    bridge-ports none
    bridge-stp off
    bridge-fd 0
    bridge-vlan-aware yes
    bridge-vids 2-4094
    post-up ip route add 10.%d.0.0/16 via 192.0.2.%d
    post-down ip route del 10.%d.0.0/16 via 192.0.2.%d`,
		ifaceName, ifaceName, rangeNumber, 100+rangeNumber, rangeNumber, 100+rangeNumber)

	originalContent, err := os.ReadFile(interfacesPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", interfacesPath, err)
	}

	newContent, contentChanged := applyBlockInFile(string(originalContent), marker, block, present)

	if contentChanged {
		logger.Debug(fmt.Sprintf("Configuration in %s has changed. Applying...", interfacesPath))
		if err := os.WriteFile(interfacesPath, []byte(newContent), 0644); err != nil {
			return fmt.Errorf("failed to write changes to %s: %w", interfacesPath, err)
		}

		// Apply network changes based on the desired state.
		switch present {
		case true:
			runNetworkCommand("/usr/sbin/ifup", ifaceName)
		case false:
			runNetworkCommand("/usr/sbin/ifdown", ifaceName)
		}
		logger.Debug("Network configuration applied.")
	} else {
		logger.Debug(fmt.Sprintf("Configuration in %s is already in the desired state. No action taken.", interfacesPath))
	}

	return nil
}

// runNetworkCommand executes a network command (like ifup/ifdown), ignoring errors
// the command is run with the ADDRFAM environment variable set to "inet"
func runNetworkCommand(command string, args ...string) {
	cmd := exec.Command(command, args...)
	cmd.Env = append(os.Environ(), "ADDRFAM=inet")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	logger.Debug(fmt.Sprintf("Executing: %s\n", cmd.String()))
	err := cmd.Run()
	if err != nil {
		logger.Debug(fmt.Sprintf("Warning: Command '%s' failed (ignoring error): %v\n", cmd.String(), err))
		logger.Debug(fmt.Sprintf("Stderr: %s\n", stderr.String()))
	}
}
