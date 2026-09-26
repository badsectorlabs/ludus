package ludusapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	goproxmox "github.com/luthermonson/go-proxmox"
)

// SDN Zone types
const (
	SDNZoneTypeSimple = "simple" // Single-node SDN zone
	SDNZoneTypeVXLAN  = "vxlan"  // Multi-node cluster SDN zone
	NATVNetName       = "ludusnat"
	NATVNetVXLANTag   = 100000
)

// RangeVNetOptionsForZone returns the Proxmox VNet options Ludus should use
// for a range network in the given SDN zone type. Range VNets are VLAN-aware
// because Ludus uses VM NIC VLAN tags inside each range.
func RangeVNetOptionsForZone(zoneType string, vxlanTagBase, rangeNumber int) (tag int, vlanaware bool) {
	if strings.EqualFold(zoneType, SDNZoneTypeVXLAN) {
		return vxlanTagBase + rangeNumber, true
	}
	return 0, true
}

func NATVNetOptionsForZone(zoneType string) (tag int, vlanaware bool) {
	if strings.EqualFold(zoneType, SDNZoneTypeVXLAN) {
		return NATVNetVXLANTag, false
	}
	return 0, false
}

// IsClusterMode reports whether this Proxmox instance has more than one node.
// Ludus always uses SDN regardless of this result; it only affects whether the
// SDN zone must be a user-preconfigured VXLAN zone (cluster) vs a simple zone
// auto-created at bootstrap (single node).
func IsClusterMode() (bool, error) {
	client, err := GetRootGoProxmoxClient()
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

	return applySDNChangesAndWait(ctx, cluster)
}

func applySDNChangesAndWait(ctx context.Context, cluster *goproxmox.Cluster) error {
	task, err := cluster.SDNApply(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply SDN changes: %w", err)
	}
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

// ZoneExists checks if an SDN zone already exists without unmarshalling full zone options.
// Required until https://github.com/luthermonson/go-proxmox/pull/297 is merged
func ZoneExists(client *goproxmox.Client, zoneName string) (bool, error) {
	ctx := context.Background()
	type sdnZoneSummary struct {
		Name string `json:"zone"`
	}

	var zones []sdnZoneSummary
	err := client.Get(ctx, "/cluster/sdn/zones", &zones)
	if err != nil {
		return false, fmt.Errorf("failed to list SDN zones: %w", err)
	}

	for _, zone := range zones {
		if zone.Name == zoneName {
			return true, nil
		}
	}

	return false, nil
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

	pc, err := GetRootPVEClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	// Get configured zone name with fallback to default
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	if present {
		zoneType, err := pc.SDNZoneType(ctx, zoneName)
		if err != nil {
			return fmt.Errorf("failed to get SDN zone type for %s: %w", zoneName, err)
		}
		tag, vlanaware := RangeVNetOptionsForZone(zoneType, ServerConfiguration.VXLANTagBase, rangeNumber)

		if err := pc.EnsureVNet(ctx, zoneName, vnetName, tag, vlanaware); err != nil {
			return fmt.Errorf("failed to create VNet %s: %w", vnetName, err)
		}

		cluster, err := pc.Raw().Cluster(ctx)
		if err != nil {
			return fmt.Errorf("failed to get cluster client: %w", err)
		}
		if err := applySDNChangesAndWait(ctx, cluster); err != nil {
			return err
		}

		if err := addRouteForRangeNetworkInVNet(rangeNumber); err != nil {
			return err
		}

		logger.Debug(fmt.Sprintf("Created/updated VNet %s (zone type: %s, tag: %d, vlanaware: %t) for range %s", vnetName, zoneType, tag, vlanaware, rangeID))

	} else {
		client := pc.Raw()
		cluster, err := client.Cluster(ctx)
		if err != nil {
			return fmt.Errorf("failed to get cluster client: %w", err)
		}

		// Delete VNet using library's DeleteSDNVNet function
		deleted := true
		err = cluster.DeleteSDNVNet(ctx, vnetName)
		if err != nil {
			// If VNet doesn't exist, that's OK
			if !strings.Contains(err.Error(), "does not exist") && !strings.Contains(err.Error(), "not found") {
				return fmt.Errorf("failed to delete VNet %s: %w", vnetName, err)
			}
			logger.Debug(fmt.Sprintf("VNet %s does not exist, skipping deletion", vnetName))
			deleted = false
		}

		if deleted {
			// Apply SDN changes
			if err := applySDNChangesAndWait(ctx, cluster); err != nil {
				return err
			}
		}

		// Remove the route through the range router for this range network
		if err := removeRouteForRangeNetworkInVNet(rangeNumber); err != nil {
			return err
		}

		logger.Debug(fmt.Sprintf("Deleted VNet %s for range %s", vnetName, rangeID))
	}

	return nil
}

// setupNATVNet creates the NAT VNet (ludusnat) for the 192.0.2.0/24 network
// in the configured SDN zone.
func setupNATVNet() error {
	ctx := context.Background()

	pc, err := GetRootPVEClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	// Get configured zone name with fallback to default
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	zoneType, err := pc.SDNZoneType(ctx, zoneName)
	if err != nil {
		return fmt.Errorf("failed to get SDN zone type: %w", err)
	}
	tag, vlanaware := NATVNetOptionsForZone(zoneType)
	if err := pc.EnsureVNet(ctx, zoneName, NATVNetName, tag, vlanaware); err != nil {
		return fmt.Errorf("failed to create NAT VNet: %w", err)
	}
	cluster, err := pc.Raw().Cluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	if err := applySDNChangesAndWait(ctx, cluster); err != nil {
		return err
	}

	// Make sure all ludus users have SDN.Use on the ludusnat vnet
	proxmoxClient, err := GetRootGoProxmoxClient()
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

// setupSDNZone verifies the Ludus SDN zone exists.
// On single-node installs the zone is auto-created at bootstrap; on multi-node
// clusters the user must pre-create a VXLAN zone with correct peer IPs.
func setupSDNZone() error {
	client, err := GetRootGoProxmoxClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	clusterMode, err := IsClusterMode()
	if err != nil {
		return fmt.Errorf("failed to detect cluster mode: %w", err)
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

	if !zoneExists {
		if clusterMode {
			return fmt.Errorf("multi-node cluster requires a pre-configured SDN zone. Create zone '%s' in Proxmox with correct VXLAN peer IPs, then retry", zoneName)
		}
		return fmt.Errorf("SDN zone '%s' not found; it should have been created at bootstrap", zoneName)
	}
	logger.Debug(fmt.Sprintf("Using existing SDN zone %s", zoneName))
	return nil
}

func addRouteForRangeNetworkInVNet(rangeNumber int) error {
	return routeForRangeNetworkInVNetAction(rangeNumber, true)
}

func removeRouteForRangeNetworkInVNet(rangeNumber int) error {
	return routeForRangeNetworkInVNetAction(rangeNumber, false)
}

func routeForRangeNetworkInVNetAction(rangeNumber int, present bool) error {

	// Edit the /etc/network/if-up.d/ludus-routes file and make sure it contains an ip route command for the range network.
	// Ludus runs inside an LXC: the NAT interface is eth1 (not the host's "ludusnat" bridge), and we omit `dev` so the
	// kernel selects the interface from the via address.
	sdnRoutesFile := "/etc/network/if-up.d/ludus-routes"

	// Create the file if it doesn't exist and make it executable
	if !FileExists(sdnRoutesFile) {
		touch(sdnRoutesFile)
		os.Chmod(sdnRoutesFile, 0755)
		// The file must start with a shebang or it will throw an `exec format error`
		os.WriteFile(sdnRoutesFile, []byte("#!/bin/sh\n"), 0755)
	}

	block := fmt.Sprintf(`
if [ "$IFACE" = "eth1" ]; then
	ip route replace 10.%d.0.0/16 via 192.0.2.%d
fi
	`, rangeNumber, 100+rangeNumber)
	_, err := applyBlockInFileAtPath(sdnRoutesFile, fmt.Sprintf("# LUDUS MANAGED BLOCK FOR RANGE %d {mark}", rangeNumber), block, present)
	if err != nil {
		return fmt.Errorf("failed to apply block in file: %w", err)
	}
	if present {
		// Apply the route immediately. Use replace so reruns recover cleanly from
		// stale route entries left by interrupted range cleanup.
		err = Run(fmt.Sprintf("ip route replace 10.%d.0.0/16 via 192.0.2.%d", rangeNumber, 100+rangeNumber), "/tmp", "/tmp/sdn-routes.log")
		if err != nil {
			return fmt.Errorf("failed to add route: %w", err)
		}
	} else {
		err = Run(fmt.Sprintf("ip route delete 10.%d.0.0/16 via 192.0.2.%d 2>/dev/null || true", rangeNumber, 100+rangeNumber), "/tmp", "/tmp/sdn-routes.log")
		if err != nil {
			return fmt.Errorf("failed to remove route: %w", err)
		}
	}
	return nil
}
