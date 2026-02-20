package ludusapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// MigrateSQLiteToPostgreSQL handles the HTTP request to migrate from SQLite to PocketBase
func MigrateSQLiteToPocketBaseHandler(e *core.RequestEvent) error {
	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not an admin and cannot migrate from SQLite to PocketBase")
	}

	if err := MigrateFromSQLiteToPocketBase(); err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	return JSONResult(e, http.StatusOK, "Migration from SQLite to PocketBase completed successfully")
}

// GetSDNMigrationStatus checks if the system needs SDN migration
func GetSDNMigrationStatus(e *core.RequestEvent) error {
	client, err := getRootGoProxmoxClient()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get Proxmox client: "+err.Error())
	}

	// Detect cluster mode
	clusterMode := UseSDN

	// Non-cluster hosts don't use SDN - they use vmbr management
	if !clusterMode {
		status := map[string]interface{}{
			"sdn_zone_exists":      false,
			"nat_vnet_exists":      false,
			"needs_migration":      false,
			"cluster_mode":         false,
			"requires_manual_zone": false,
			"current_sdn_zone":     "",
			"ludus_nat_interface":  ServerConfiguration.LudusNATInterface,
			"message":              "Not in cluster mode. Using vmbr network management - no SDN migration needed.",
		}
		return e.JSON(http.StatusOK, status)
	}

	// Get configured zone name with fallback to default
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	// Check if SDN zone exists
	zoneExists, _ := ZoneExists(client, zoneName)

	// Check if NAT VNet exists
	natVNetExists, _ := VNetExists(client, ServerConfiguration.LudusNATInterface)

	// System needs migration if SDN zone doesn't exist
	needsMigration := !zoneExists

	// In cluster mode, users must manually create the zone with correct VXLAN peer IPs
	requiresManualZone := !zoneExists

	status := map[string]interface{}{
		"sdn_zone_exists":      zoneExists,
		"nat_vnet_exists":      natVNetExists,
		"needs_migration":      needsMigration,
		"cluster_mode":         clusterMode,
		"requires_manual_zone": requiresManualZone,
		"current_sdn_zone":     zoneName,
		"ludus_nat_interface":  ServerConfiguration.LudusNATInterface,
	}

	// Add helpful message when manual zone creation is required
	if requiresManualZone {
		status["message"] = fmt.Sprintf("Cluster mode requires a pre-configured SDN zone. Create zone '%s' in Proxmox with correct VXLAN peer IPs before running migration.", zoneName)
	}

	return e.JSON(http.StatusOK, status)
}

// MigrateToSDN migrates existing bridge-based networking to SDN VNets
// All operations use the Proxmox API for portability
// This is only applicable to cluster mode - non-cluster hosts use vmbr management
func MigrateToSDN(e *core.RequestEvent) error {
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "Migration must be run via ludus-admin on 127.0.0.1:8081")
	}

	client, err := getRootGoProxmoxClient()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get Proxmox client: "+err.Error())
	}

	ctx := context.Background()

	clusterMode := UseSDN

	// Non-cluster hosts don't need SDN migration - they use vmbr management
	if !clusterMode {
		return JSONResult(e, http.StatusOK, "Not in cluster mode. Using vmbr network management - no SDN migration needed.")
	}

	logger.Info(fmt.Sprintf("Starting SDN migration (cluster mode: %t)", clusterMode))

	// 2. Check/create SDN zone
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	zoneExists, err := ZoneExists(client, zoneName)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to check SDN zone: "+err.Error())
	}

	// In cluster mode, zone must be pre-configured by user with correct VXLAN peer IPs
	if !zoneExists {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("Cluster mode requires a pre-configured SDN zone. Create zone '%s' in Proxmox with correct VXLAN peer IPs, then retry", zoneName))
	}
	logger.Info(fmt.Sprintf("Using existing SDN zone '%s' for cluster mode", zoneName))

	// 3. Create NAT VNet if not exists
	natExists, _ := VNetExists(client, NATVNetName)
	if !natExists {
		err = setupNATVNet()
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, "Failed to create NAT VNet: "+err.Error())
		}
		logger.Info(fmt.Sprintf("Created NAT VNet '%s'", NATVNetName))
	}

	// 4. Get all existing ranges from database
	ranges, err := app.FindAllRecords("ranges")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get ranges: "+err.Error())
	}

	var migrationErrors []string

	for _, rangeRecord := range ranges {
		rangeNumber := rangeRecord.GetInt("rangeNumber")
		rangeID := rangeRecord.GetString("rangeID")

		// Create VNet for this range
		err = manageRangeVNet(rangeID, rangeNumber, true)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Sprintf("Range %s: %v", rangeID, err))
			continue
		}

		// Update VM network interfaces via API
		vnetName := fmt.Sprintf("r%d", rangeNumber)
		err = migrateRangeVMsToVNet(client, ctx, rangeID, vnetName)
		if err != nil {
			migrationErrors = append(migrationErrors, fmt.Sprintf("Range %s VMs: %v", rangeID, err))
		}

		logger.Info(fmt.Sprintf("Migrated range %s to VNet %s", rangeID, vnetName))
	}

	// 5. Apply all SDN changes
	err = ApplySDNChanges(client)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to apply SDN changes: "+err.Error())
	}

	if len(migrationErrors) > 0 {
		return JSONError(e, http.StatusPartialContent,
			fmt.Sprintf("Migration completed with errors: %s. Manual cleanup of old bridge interfaces may be required.", strings.Join(migrationErrors, "; ")))
	}

	return JSONResult(e, http.StatusOK, "Migration to SDN VNets completed successfully. Reboot recommended to clean up old bridge interfaces.")
}

// migrateRangeVMsToVNet updates all VM network interfaces to use the new VNet
func migrateRangeVMsToVNet(client *goproxmox.Client, ctx context.Context, rangeID string, vnetName string) error {
	// Get VMs in this range's pool
	pool, err := client.Pool(ctx, rangeID, "qemu")
	if err != nil {
		return fmt.Errorf("failed to get pool: %w", err)
	}

	for _, member := range pool.Members {
		if member.Type != "qemu" {
			continue
		}

		// Get VM's current network config
		node, err := findNodeForVM(ctx, client, member.VMID)
		if err != nil {
			logger.Debug(fmt.Sprintf("Could not find node for VM %d: %v", member.VMID, err))
			continue
		}

		nodeClient, err := client.Node(ctx, node)
		if err != nil {
			logger.Debug(fmt.Sprintf("Could not get node client for %s: %v", node, err))
			continue
		}

		vm, err := nodeClient.VirtualMachine(ctx, int(member.VMID))
		if err != nil {
			logger.Debug(fmt.Sprintf("Could not get VM %d: %v", member.VMID, err))
			continue
		}

		// Update network interface to use VNet
		// This uses the Proxmox API to update VM config
		netOption := goproxmox.VirtualMachineOption{
			Name:  "net0",
			Value: fmt.Sprintf("virtio,bridge=%s", vnetName),
		}

		_, err = vm.Config(ctx, netOption)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to update VM %d network config: %v", member.VMID, err))
		} else {
			logger.Debug(fmt.Sprintf("Updated VM %d to use VNet %s", member.VMID, vnetName))
		}
	}

	return nil
}

// SetupSDNInfrastructure creates the SDN zone and NAT VNet without migrating existing ranges
// This is used during fresh installations in cluster mode.
// Non-cluster hosts skip this and use vmbr management.
func SetupSDNInfrastructure(e *core.RequestEvent) error {
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, "SDN setup must be run via ludus-admin on 127.0.0.1:8081")
	}

	// Check if we're in cluster mode
	if !UseSDN {
		return JSONResult(e, http.StatusOK, "Not in cluster mode. Using vmbr network management - no SDN setup needed.")
	}

	// Setup SDN zone (cluster mode only)
	err := setupSDNZone()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to setup SDN zone: "+err.Error())
	}

	// Setup NAT VNet (cluster mode only)
	err = setupNATVNet()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to setup NAT VNet: "+err.Error())
	}

	return JSONResult(e, http.StatusOK, "SDN infrastructure setup completed successfully")
}
