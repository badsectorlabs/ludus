package ludusapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/spf13/viper"
)

// SDN Zone types
const (
	SDNZoneTypeSimple = "simple" // For standalone (local OVS)
	SDNZoneTypeVXLAN  = "vxlan"  // For cluster mode
	NATVNetName       = "ludusnat"
)

// IsClusterMode checks if this Proxmox instance is part of a cluster.
// First checks if the user has explicitly set cluster_mode in config.
// If not set, falls back to API detection by checking if there are multiple nodes.
func IsClusterMode() (bool, error) {
	// Check if user has explicitly set cluster_mode in config
	if viper.IsSet("cluster_mode") {
		logger.Debug(fmt.Sprintf("Cluster mode explicitly set in config to: %t", ServerConfiguration.ClusterMode))
		return ServerConfiguration.ClusterMode, nil
	}

	client, err := getRootGoProxmoxClient()
	if err != nil {
		return false, fmt.Errorf("failed to get proxmox client: %w", err)
	}

	// Fall back to API detection
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster client: %w", err)
	}
	// Get cluster status - if we have multiple nodes, we're in cluster mode
	nodes, err := cluster.Resources(ctx, "node")
	if err != nil {
		return false, fmt.Errorf("failed to get cluster resources: %w", err)
	}
	return len(nodes) > 1, nil
}

// GetClusterNodes returns all nodes in the Proxmox cluster as NodeStatuses
func GetClusterNodes(client *goproxmox.Client) (goproxmox.NodeStatuses, error) {
	ctx := context.Background()
	return client.Nodes(ctx)
}

// GetNodeResourceUsage returns CPU and memory usage percentage for a node
// Uses the NodeStatus which already has CPU and Mem info from the nodes list
func GetNodeResourceUsage(client *goproxmox.Client, nodeName string) (cpuPercent float64, memPercent float64, err error) {
	ctx := context.Background()
	nodes, err := client.Nodes(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get nodes: %w", err)
	}

	for _, nodeStatus := range nodes {
		if nodeStatus.Node == nodeName {
			// CPU usage is already a percentage (0-1)
			cpuPercent = nodeStatus.CPU * 100

			// Memory usage as percentage
			if nodeStatus.MaxMem > 0 {
				memPercent = float64(nodeStatus.Mem) / float64(nodeStatus.MaxMem) * 100
			}

			return cpuPercent, memPercent, nil
		}
	}

	return 0, 0, fmt.Errorf("node %s not found", nodeName)
}

// SelectOptimalNode selects the best node for deployment
// Uses 80% RAM weight, 20% CPU weight to favor nodes with more available memory
func SelectOptimalNode(client *goproxmox.Client) (string, error) {
	nodes, err := GetClusterNodes(client)
	if err != nil {
		return "", fmt.Errorf("failed to get nodes: %w", err)
	}

	if len(nodes) == 0 {
		return "", fmt.Errorf("no nodes found in cluster")
	}

	// If only one node, return it
	if len(nodes) == 1 {
		return nodes[0].Node, nil
	}

	var bestNode string
	var bestScore float64 = 1000.0 // Start with a high score (lower is better)

	for _, nodeStatus := range nodes {
		// Skip offline nodes
		if nodeStatus.Status != "online" {
			continue
		}

		cpu, mem, err := GetNodeResourceUsage(client, nodeStatus.Node)
		if err != nil {
			logger.Debug(fmt.Sprintf("Failed to get resource usage for node %s: %v", nodeStatus.Node, err))
			continue
		}

		// Lower score = better (more available resources)
		// Weight memory more heavily (80%) as VMs typically need more RAM
		score := (mem * 0.8) + (cpu * 0.2)
		logger.Debug(fmt.Sprintf("Node %s: CPU=%.1f%%, MEM=%.1f%%, Score=%.2f", nodeStatus.Node, cpu, mem, score))

		if score < bestScore {
			bestScore = score
			bestNode = nodeStatus.Node
		}
	}

	if bestNode == "" {
		return "", fmt.Errorf("no suitable node found")
	}

	logger.Debug(fmt.Sprintf("Selected optimal node: %s (score: %.2f)", bestNode, bestScore))
	return bestNode, nil
}

// CreateSimpleSDNZone creates the Ludus SDN zone using go-proxmox library
// Uses "simple" type for standalone
func CreateSimpleSDNZone(client *goproxmox.Client, zoneName string) error {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	zoneType := SDNZoneTypeSimple

	// Use go-proxmox's SDNZoneOptions struct
	zoneOpts := &goproxmox.SDNZoneOptions{
		Name: zoneName,
		Type: zoneType,
	}

	// Use library's NewSDNZone function
	err = cluster.NewSDNZone(ctx, zoneOpts)
	if err != nil {
		return fmt.Errorf("failed to create SDN zone %s: %w", zoneName, err)
	}

	logger.Debug(fmt.Sprintf("Created SDN zone %s (type: %s)", zoneName, zoneType))
	return nil
}

// CreateVNet creates an SDN VNet using go-proxmox library
func CreateVNet(client *goproxmox.Client, vnetName string, zoneName string, tag uint32) error {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use go-proxmox's VNetOptions struct
	vnetOpts := &goproxmox.VNetOptions{
		Name: vnetName,
		Zone: zoneName,
	}
	if tag > 0 {
		vnetOpts.Tag = tag
	}

	// Use library's NewSDNVNet function
	err = cluster.NewSDNVNet(ctx, vnetOpts)
	if err != nil {
		return fmt.Errorf("failed to create VNet %s: %w", vnetName, err)
	}

	logger.Debug(fmt.Sprintf("Created VNet %s in zone %s", vnetName, zoneName))
	return nil
}

// DeleteVNet removes an SDN VNet using go-proxmox library
func DeleteVNet(client *goproxmox.Client, vnetName string) error {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's DeleteSDNVNet function
	err = cluster.DeleteSDNVNet(ctx, vnetName)
	if err != nil {
		return fmt.Errorf("failed to delete VNet %s: %w", vnetName, err)
	}

	logger.Debug(fmt.Sprintf("Deleted VNet %s", vnetName))
	return nil
}

// DeleteSDNZone removes an SDN zone using go-proxmox library
func DeleteSDNZone(client *goproxmox.Client, zoneName string) error {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's DeleteSDNZone function
	err = cluster.DeleteSDNZone(ctx, zoneName)
	if err != nil {
		return fmt.Errorf("failed to delete SDN zone %s: %w", zoneName, err)
	}

	logger.Debug(fmt.Sprintf("Deleted SDN zone %s", zoneName))
	return nil
}

// ApplySDNChanges applies pending SDN configuration using go-proxmox library
func ApplySDNChanges(client *goproxmox.Client) error {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNApply function - returns a Task
	task, err := cluster.SDNApply(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply SDN changes: %w", err)
	}

	// Wait for task to complete
	err = task.Wait(ctx, 2*time.Second, 60*time.Second)
	if err != nil {
		return fmt.Errorf("SDN apply task failed: %w", err)
	}

	logger.Debug("Applied SDN changes successfully")
	return nil
}

// VNetExists checks if a VNet already exists using go-proxmox library
func VNetExists(client *goproxmox.Client, vnetName string) (bool, error) {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNVNet function - returns error if not found
	_, err = cluster.SDNVNet(ctx, vnetName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check VNet existence: %w", err)
	}
	return true, nil
}

// ZoneExists checks if an SDN zone already exists using go-proxmox library
func ZoneExists(client *goproxmox.Client, zoneName string) (bool, error) {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNZone function - returns error if not found
	_, err = cluster.SDNZone(ctx, zoneName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such") {
			return false, nil
		}
		return false, fmt.Errorf("failed to check SDN zone existence: %w", err)
	}
	return true, nil
}

// GetVNetSubnets returns all subnets for a VNet using go-proxmox library
func GetVNetSubnets(client *goproxmox.Client, vnetName string) ([]*goproxmox.VNetSubnet, error) {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNSubnets function
	return cluster.SDNSubnets(ctx, vnetName)
}

// GetAllVNets returns all VNets using go-proxmox library
func GetAllVNets(client *goproxmox.Client) ([]*goproxmox.VNet, error) {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNVNets function
	return cluster.SDNVNets(ctx)
}

// GetAllSDNZones returns all SDN zones using go-proxmox library
func GetAllSDNZones(client *goproxmox.Client, typeFilter ...string) ([]*goproxmox.SDNZone, error) {
	ctx := context.Background()
	cluster, err := client.Cluster(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Use library's SDNZones function with optional type filter
	return cluster.SDNZones(ctx, typeFilter...)
}

// manageRangeVNet creates or deletes a VNet for a range using go-proxmox library
// This replaces the old manageVmbrInterfaceLocally function that edited /etc/network/interfaces
func manageRangeVNet(rangeID string, rangeNumber int, present bool) error {
	vnetName := fmt.Sprintf("r%d", rangeNumber) // e.g., "r1", "r2"
	ctx := context.Background()

	// Get root proxmox client for SDN operations
	client, err := getRootGoProxmoxClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Get configured zone name with fallback to default
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	if present {
		// Check if VNet already exists using library's SDNVNet function
		_, err := cluster.SDNVNet(ctx, vnetName)
		if err == nil {
			logger.Debug(fmt.Sprintf("VNet %s already exists, skipping creation", vnetName))
			return nil
		}

		// Create VNet for range using go-proxmox VNetOptions struct
		// In cluster mode, VNets are VLAN aware and no subnet is needed
		// Tag is required for VXLAN zones and must be unique per range
		// Use vxlan_tag_base + rangeNumber to allow coexistence with pre-existing VXLAN VNets
		vxlanTag := uint32(ServerConfiguration.VXLANTagBase + rangeNumber)
		vnetOpts := &goproxmox.VNetOptions{
			Name:      vnetName,
			Zone:      zoneName,
			Tag:       vxlanTag,
			VlanAware: true,
		}
		err = cluster.NewSDNVNet(ctx, vnetOpts)
		if err != nil {
			return fmt.Errorf("failed to create VNet %s: %w", vnetName, err)
		}

		// Apply SDN changes using library's SDNApply (returns Task)
		task, err := cluster.SDNApply(ctx)
		if err != nil {
			return fmt.Errorf("failed to apply SDN changes: %w", err)
		}
		err = task.Wait(ctx, 2*time.Second, 60*time.Second)
		if err != nil {
			return fmt.Errorf("SDN apply task failed: %w", err)
		}

		// Add the route through the range router for this range network
		addRouteForRangeNetworkInVNet(rangeNumber)

		logger.Debug(fmt.Sprintf("Created VLAN-aware VNet %s (tag: %d) for range %s", vnetName, vxlanTag, rangeID))

	} else {
		// Delete VNet using library's DeleteSDNVNet function
		err = cluster.DeleteSDNVNet(ctx, vnetName)
		if err != nil {
			// If VNet doesn't exist, that's OK
			if !strings.Contains(err.Error(), "does not exist") && !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("failed to delete VNet %s: %w", vnetName, err)
			}
			logger.Debug(fmt.Sprintf("VNet %s does not exist, skipping deletion", vnetName))
			return nil
		}

		// Apply SDN changes
		task, err := cluster.SDNApply(ctx)
		if err != nil {
			return fmt.Errorf("failed to apply SDN changes after deletion: %w", err)
		}
		err = task.Wait(ctx, 2*time.Second, 60*time.Second)
		if err != nil {
			return fmt.Errorf("SDN apply task failed: %w", err)
		}

		// Remove the route through the range router for this range network
		removeRouteForRangeNetworkInVNet(rangeNumber)

		logger.Debug(fmt.Sprintf("Deleted VNet %s for range %s", vnetName, rangeID))
	}

	return nil
}

// setupNATVNet creates the NAT VNet (ludusnat) for the 192.0.2.0/24 network
// This is only used in cluster mode; non-cluster hosts use vmbr1000.
func setupNATVNet() error {
	ctx := context.Background()

	client, err := getRootGoProxmoxClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	// Check if we're in cluster mode - only setup SDN for clusters
	if !UseSDN {
		logger.Debug("Not in cluster mode, skipping SDN NAT VNet setup (using standalone vmbr1000)")
		return nil
	}

	cluster, err := client.Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Get configured zone name with fallback to default
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	// Check if already exists using library's SDNVNet function
	_, err = cluster.SDNVNet(ctx, NATVNetName)
	if err == nil {
		logger.Debug(fmt.Sprintf("VNet %s already exists, skipping creation", NATVNetName))
		return nil // Already exists
	}

	// Create NAT VNet using go-proxmox VNetOptions
	vnetOpts := &goproxmox.VNetOptions{
		Name:      NATVNetName,
		Zone:      zoneName,
		Tag:       16777215,
		VlanAware: true,
	}
	err = cluster.NewSDNVNet(ctx, vnetOpts)
	if err != nil {
		return fmt.Errorf("failed to create NAT VNet: %w", err)
	}

	// Apply changes using library's SDNApply
	task, err := cluster.SDNApply(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply SDN changes: %w", err)
	}
	err = task.Wait(ctx, 2*time.Second, 60*time.Second)
	if err != nil {
		return fmt.Errorf("SDN apply task failed: %w", err)
	}

	// Make sure all ludus users have SDN.Use on the ludusnat vnet
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return errors.New("unable to create proxmox client: " + err.Error())
	}

	SDNVNetACL := goproxmox.ACLOptions{
		Path:      fmt.Sprintf("/sdn/zones/%s/%s", ServerConfiguration.SDNZone, NATVNetName),
		Groups:    "ludus_users",
		Roles:     "PVESDNUser",
		Propagate: goproxmox.IntOrBool(true),
		Delete:    goproxmox.IntOrBool(false),
	}
	logger.Debug(fmt.Sprintf("Setting permissions for group 'ludus_users' to SDN VNet '%s'\n", NATVNetName))
	err = proxmoxClient.UpdateACL(context.Background(), SDNVNetACL)
	if err != nil {
		return errors.New("unable to set permissions for group: " + err.Error())
	}

	logger.Debug(fmt.Sprintf("Created NAT VNet %s", NATVNetName))
	return nil
}

// setupSDNZone creates the Ludus SDN zone for cluster mode.
// Non-cluster hosts skip this entirely and use vmbr management.
// In cluster mode, requires a pre-configured zone (user must create it with correct VXLAN peer IPs).
func setupSDNZone() error {
	client, err := getRootGoProxmoxClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	// Detect cluster mode via API - only setup SDN for clusters
	clusterMode, err := IsClusterMode()
	if err != nil {
		return fmt.Errorf("failed to detect cluster mode: %w", err)
	}

	if !clusterMode {
		logger.Debug("Not in cluster mode, skipping SDN zone setup (using vmbr management)")
		return nil
	}

	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	// Check if zone already exists
	zoneExists, err := ZoneExists(client, zoneName)
	if err != nil {
		return fmt.Errorf("failed to check SDN zone: %w", err)
	}

	// In cluster mode, zone must be pre-configured by user with correct VXLAN peer IPs
	if !zoneExists {
		return fmt.Errorf("cluster mode requires a pre-configured SDN zone. Create zone '%s' in Proxmox with correct VXLAN peer IPs, then retry", zoneName)
	}
	logger.Debug(fmt.Sprintf("Using existing SDN zone %s for cluster mode", zoneName))
	return nil
}

func addRouteForRangeNetworkInVNet(rangeNumber int) error {
	return routeForRangeNetworkInVNetAction(rangeNumber, true)
}

func removeRouteForRangeNetworkInVNet(rangeNumber int) error {
	return routeForRangeNetworkInVNetAction(rangeNumber, false)
}

func routeForRangeNetworkInVNetAction(rangeNumber int, present bool) error {

	// Edit the /etc/network/if-up.d/sdn-routes file and make sure it contains and ip route command for the range network
	sdnRoutesFile := "/etc/network/if-up.d/sdn-routes"

	// Create the file if it doesn't exist and make it executable
	if !FileExists(sdnRoutesFile) {
		touch(sdnRoutesFile)
		os.Chmod(sdnRoutesFile, 0755)
		// The file must start with a shebang or it will throw an `exec format error`
		os.WriteFile(sdnRoutesFile, []byte("#!/bin/sh\n"), 0755)
	}

	block := fmt.Sprintf(`
if [ "$IFACE" = "r%d" ]; then
	ip route add 10.%d.0.0/16 via 192.0.2.%d dev %s
fi
	`, rangeNumber, rangeNumber, 100+rangeNumber, NATVNetName)
	_, err := applyBlockInFileAtPath(sdnRoutesFile, fmt.Sprintf("# LUDUS MANAGED BLOCK FOR RANGE %d {mark}", rangeNumber), block, present)
	if err != nil {
		return fmt.Errorf("failed to apply block in file: %w", err)
	}
	if present {
		// Add the route immediately
		err = Run(fmt.Sprintf("ip route add 10.%d.0.0/16 via 192.0.2.%d dev %s", rangeNumber, 100+rangeNumber, NATVNetName), "/tmp", "/tmp/sdn-routes.log")
		if err != nil {
			return fmt.Errorf("failed to add route: %w", err)
		}
	}
	return nil
}
