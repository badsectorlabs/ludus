package ludusapi

/*
Network Management for Ludus Ranges

Ludus uses two different networking approaches depending on the deployment mode:

# Cluster Mode (SDN)

When Ludus detects that the Proxmox host is part of a cluster (more than one node),
it uses Proxmox SDN (Software-Defined Networking) for range network management:

  - Range networks use VNets named 'r{N}' (e.g., 'r1', 'r2') in the configured SDN zone
  - NAT network uses a VNet named 'ludusnat'
  - VXLAN overlay networking allows VMs to communicate across cluster nodes
  - Network configuration is managed via Proxmox API

# Non-Cluster Mode

When Ludus is running on a standalone Proxmox host (single node), it uses vmbr management:

  - Range networks use bridges named 'vmbr{1000+N}' (e.g., 'vmbr1001', 'vmbr1002')
  - NAT network uses the bridge configured as 'ludus_nat_interface' (default: vmbr1000)
  - Network configuration is managed by editing /etc/network/interfaces directly

The mode is automatically detected by checking if the Proxmox host has more than
one node in its cluster. The UseSDNNetworking() function in sdn.go provides this check.
*/

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// manageVmbrInterfaceLocally manages network interfaces for a range.
// In cluster mode, this delegates to manageRangeVNet for SDN-based networking.
// In non-cluster mode, it directly edits /etc/network/interfaces.
// This function MUST be run as root on the Proxmox host.
func manageVmbrInterfaceLocally(rangeNumber int, present bool) error {
	// Check if we're in cluster mode (SDN networking)
	if UseSDN {
		// Use SDN VNet management for cluster mode
		rangeID := fmt.Sprintf("r%d", rangeNumber) // Generate a placeholder range ID
		return manageRangeVNet(rangeID, rangeNumber, present)
	}

	// Standalone mode: directly edit /etc/network/interfaces for non-cluster hosts
	return manageVmbrInterfaceStandalone(rangeNumber, present)
}

// manageVmbrInterfaceStandalone directly edits /etc/network/interfaces
// This is the standalone approach for backward compatibility with existing installations.
func manageVmbrInterfaceStandalone(rangeNumber int, present bool) error {
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

// manageRangeNetwork manages network resources for a range.
// This is the primary entry point for range network management.
// In cluster mode, it uses SDN VNets.
// In non-cluster mode, it falls back to /etc/network/interfaces editing.
func manageRangeNetwork(rangeID string, rangeNumber int, present bool) error {
	// Check if we're in cluster mode (SDN networking)
	if UseSDN {
		// Use SDN VNet management for cluster mode
		return manageRangeVNet(rangeID, rangeNumber, present)
	}

	// Standalone mode: directly edit /etc/network/interfaces for non-cluster hosts
	return manageVmbrInterfaceStandalone(rangeNumber, present)
}
