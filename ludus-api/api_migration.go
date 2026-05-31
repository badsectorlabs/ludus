package ludusapi

import (
	"context"
	"fmt"
	"ludusapi/dto"
	"net/http"
	"os"
	"strconv"
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
	client, err := GetRootGoProxmoxClient()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get Proxmox client: "+err.Error())
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

	// System needs migration if there are ranges in the database but no range VNets for each existing range
	needsMigration := false
	ranges, err := app.FindAllRecords("ranges")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get ranges: "+err.Error())
	}
	if len(ranges) > 0 && !natVNetExists {
		for _, rangeRecord := range ranges {
			vnetName := fmt.Sprintf("r%d", rangeRecord.GetInt("rangeNumber"))
			vnetExists, _ := VNetExists(client, vnetName)
			if !vnetExists {
				needsMigration = true
			}
		}
	}

	// On multi-node clusters, users must manually create the zone with correct VXLAN peer IPs
	clusterMode, _ := IsClusterMode()
	requiresManualZone := clusterMode && !zoneExists

	status := dto.SDNStatus{
		SDNZoneExists:      zoneExists,
		NATVNetExists:      natVNetExists,
		NeedsMigration:     needsMigration,
		ClusterMode:        clusterMode,
		RequiresManualZone: requiresManualZone,
		CurrentSDNZone:     zoneName,
		LudusNATInterface:  ServerConfiguration.LudusNATInterface,
		Message:            fmt.Sprintf("SDN migration status: %t, NAT VNet exists: %t, Zone exists: %t", needsMigration, natVNetExists, zoneExists),
	}

	// Add helpful message when manual zone creation is required
	if requiresManualZone {
		status.Message = fmt.Sprintf("Multi-node cluster requires a pre-configured SDN zone. Create zone '%s' in Proxmox with correct VXLAN peer IPs before running migration.", zoneName)
	}

	return e.JSON(http.StatusOK, status)
}

// MigrateToSDN migrates existing bridge-based networking to SDN VNets.
// All operations use the Proxmox API for portability.
func MigrateToSDN(e *core.RequestEvent) error {
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("Migration must be run via ludus-admin on 127.0.0.1:%d", ServerConfiguration.AdminPort))
	}

	client, err := GetRootGoProxmoxClient()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to get Proxmox client: "+err.Error())
	}

	ctx := context.Background()

	logger.Info("Starting SDN migration")

	// 2. Check/create SDN zone
	zoneName := ServerConfiguration.SDNZone
	if zoneName == "" {
		zoneName = "ludus"
	}

	zoneExists, err := ZoneExists(client, zoneName)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to check SDN zone: "+err.Error())
	}

	if !zoneExists {
		return JSONError(e, http.StatusBadRequest,
			fmt.Sprintf("SDN zone '%s' not found. On multi-node clusters create it in Proxmox with correct VXLAN peer IPs; on single-node it should have been created at bootstrap", zoneName))
	}
	logger.Info(fmt.Sprintf("Using existing SDN zone '%s'", zoneName))

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

	return JSONResult(e, http.StatusOK, "Migration to SDN VNets completed successfully.")
}

// migrateRangeVMsToVNet updates all VM network interfaces to use the new VNet
func migrateRangeVMsToVNet(client *goproxmox.Client, ctx context.Context, rangeID string, vnetName string) error {
	// Get VMs in this range's pool
	pool, err := client.Pool(ctx, rangeID, "qemu")
	if err != nil {
		return fmt.Errorf("failed to get pool: %w", err)
	}
	parts := strings.Split(vnetName, "r")
	if len(parts) != 2 {
		return fmt.Errorf("failed to get range number: %w", err)
	}
	rangeNumber, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("failed to get range number: %w", err)
	}
	rangeBridgeName := fmt.Sprintf("vmbr%d", 1000+rangeNumber)

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

		var netOptions []goproxmox.VirtualMachineOption
		vmInterfaces := vm.VirtualMachineConfig.Nets
		if len(vmInterfaces) > 0 {
			for ifaceName, ifaceData := range vmInterfaces {
				logger.Debug(fmt.Sprintf("VM %d, interface %s: %s", member.VMID, ifaceName, ifaceData))

				// Replace the NAT bridge with the SDN NAT bridge
				ifaceData = strings.ReplaceAll(ifaceData, "bridge=vmbr1000", "bridge=ludusnat")
				// Replace the range bridge with the new VNet interface
				logger.Debug(fmt.Sprintf("Replacing bridge %s with %s", rangeBridgeName, vnetName))
				ifaceData = strings.ReplaceAll(ifaceData, fmt.Sprintf("bridge=%s", rangeBridgeName), fmt.Sprintf("bridge=%s", vnetName))

				logger.Debug(fmt.Sprintf("VM %d, updated interface %s: %s", member.VMID, ifaceName, ifaceData))

				netOptions = append(netOptions, goproxmox.VirtualMachineOption{
					Name:  ifaceName,
					Value: ifaceData,
				})
			}
		}

		_, err = vm.Config(ctx, netOptions...)
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to update VM %d network config: %v", member.VMID, err))
		} else {
			logger.Debug(fmt.Sprintf("Updated VM %d to use VNet %s", member.VMID, vnetName))
		}
	}

	return nil
}

// SetupSDNInfrastructure verifies the SDN zone and creates the NAT VNet without
// migrating existing ranges. Used during fresh installations.
func SetupSDNInfrastructure(e *core.RequestEvent) error {
	if os.Geteuid() != 0 {
		return JSONError(e, http.StatusForbidden, fmt.Sprintf("SDN setup must be run via ludus-admin on 127.0.0.1:%d", ServerConfiguration.AdminPort))
	}

	err := setupSDNZone()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to setup SDN zone: "+err.Error())
	}

	err = setupNATVNet()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Failed to setup NAT VNet: "+err.Error())
	}

	return JSONResult(e, http.StatusOK, "SDN infrastructure setup completed successfully")
}
