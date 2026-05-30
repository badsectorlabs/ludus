package ludusapi

/*
Network Management for Ludus Ranges

Ludus runs inside a Proxmox LXC container and manages all range networking
via Proxmox SDN VNets. Direct editing of /etc/network/interfaces on the host
is not supported.

  - Range networks use VNets named 'r{N}' (e.g., 'r1', 'r2') in the configured SDN zone
  - NAT network uses a VNet named 'ludusnat'
  - Network configuration is managed via Proxmox API
*/

import (
	"fmt"
)

// manageVmbrInterfaceLocally manages network interfaces for a range via SDN.
// Delegates to manageRangeVNet for SDN-based networking.
func manageVmbrInterfaceLocally(rangeNumber int, present bool) error {
	return manageRangeVNet(fmt.Sprintf("r%d", rangeNumber), rangeNumber, present)
}

// manageRangeNetwork manages network resources for a range via SDN VNets.
// This is the primary entry point for range network management.
func manageRangeNetwork(rangeID string, rangeNumber int, present bool) error {
	return manageRangeVNet(rangeID, rangeNumber, present)
}
