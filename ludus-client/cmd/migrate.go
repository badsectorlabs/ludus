package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migration commands",
	Long:  "Commands for migrating Ludus data and infrastructure",
}

var migrateSQLiteCmd = &cobra.Command{
	Use:   "sqlite",
	Short: "Migrate data from SQLite to PocketBase",
	Long: `Migrate data from SQLite database to PocketBase database.
	
This command will migrate all data from the old SQLite database to the new PocketBase database
if the following conditions are met:
1. SQLite database file exists at /opt/ludus/ludus.db
2. PocketBase database only contains the ROOT user

The migration includes:
- Users (excluding ROOT)
- Ranges with default values for new fields (name, description, purpose)
- VMs
- Range access permissions (converted from RangeAccessObject to UserRangeAccess)

After successful migration, the SQLite database will be backed up with a timestamp.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, "/migrate/sqlite", nil)
		if !success {
			return
		}

		type Result struct {
			Result string `json:"result"`
		}

		var result Result
		err := json.Unmarshal(responseJSON, &result)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		fmt.Println(result.Result)
	},
}

var migrateSDNStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check SDN migration status",
	Long: `Check the current status of SDN migration.
	
This command displays:
- Whether SDN zone exists
- Whether NAT VNet exists
- Cluster mode status
- Whether migration is needed
- Current SDN zone name`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, "/migrate/sdn/status")
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		type Status struct {
			SDNZoneExists      bool   `json:"sdn_zone_exists"`
			NATVNetExists      bool   `json:"nat_vnet_exists"`
			NeedsMigration     bool   `json:"needs_migration"`
			ClusterMode        bool   `json:"cluster_mode"`
			RequiresManualZone bool   `json:"requires_manual_zone"`
			CurrentSDNZone     string `json:"current_sdn_zone"`
			LudusNATInterface  string `json:"ludus_nat_interface"`
			Message            string `json:"message"`
		}

		var status Status
		err := json.Unmarshal(responseJSON, &status)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Println("================================================================")
		fmt.Println("CURRENT SDN MIGRATION STATUS:")
		fmt.Println("================================================================")
		fmt.Printf("SDN Zone Exists: %t\n", status.SDNZoneExists)
		fmt.Printf("NAT VNet Exists: %t\n", status.NATVNetExists)
		fmt.Printf("Cluster Mode: %t\n", status.ClusterMode)
		fmt.Printf("Needs Migration: %t\n", status.NeedsMigration)
		fmt.Printf("Current SDN Zone: %s\n", status.CurrentSDNZone)
		if status.LudusNATInterface != "" {
			fmt.Printf("Ludus NAT Interface: %s\n", status.LudusNATInterface)
		}
		if status.Message != "" {
			fmt.Printf("\nMessage: %s\n", status.Message)
		}
		fmt.Println("================================================================")
	},
}

var migrateSDNRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run SDN migration",
	Long: `Migrate Ludus from bridge-based networking to SDN VNets.
	
This is recommended for:
- Proxmox deployments that have joined a cluster

After migration:
- Range VNets will be created as SDN VNets (r1, r2, etc.)
- The NAT network will use the 'ludus-nat' VNet
- Old vmbr interfaces can be manually removed after verification

In cluster mode, the SDN zone must be pre-configured with correct VXLAN peer IPs.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		// First check status
		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, "/migrate/sdn/status")
		if !success {
			return
		}

		type Status struct {
			NeedsMigration     bool   `json:"needs_migration"`
			RequiresManualZone bool   `json:"requires_manual_zone"`
			Message            string `json:"message"`
		}

		var status Status
		err := json.Unmarshal(responseJSON, &status)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if !status.NeedsMigration {
			fmt.Println("================================================================")
			fmt.Println("NO MIGRATION NEEDED")
			fmt.Println("================================================================")
			fmt.Println("Your Ludus installation already has SDN infrastructure configured.")
			if status.Message != "" {
				fmt.Printf("\n%s\n", status.Message)
			}
			fmt.Println("================================================================")
			return
		}

		if status.RequiresManualZone {
			fmt.Println("================================================================")
			fmt.Println("MANUAL ZONE CREATION REQUIRED")
			fmt.Println("================================================================")
			if status.Message != "" {
				fmt.Printf("%s\n", status.Message)
			}
			fmt.Println("================================================================")
			os.Exit(1)
		}

		// Confirm migration unless --no-prompt flag is set
		if !noPrompt {
			fmt.Println("================================================================")
			fmt.Println("SDN MIGRATION")
			fmt.Println("================================================================")
			fmt.Println("This will migrate your Ludus installation from bridge-based")
			fmt.Println("networking to SDN VNets.")
			fmt.Println("")
			fmt.Println("This is recommended for:")
			fmt.Println("- Proxmox cluster deployments")
			fmt.Println("- New installations")
			fmt.Println("- Systems where you want centralized SDN management")
			fmt.Println("")
			fmt.Println("After migration:")
			fmt.Println("- Range VNets will be created as SDN VNets (r1, r2, etc.)")
			fmt.Println("- The NAT network will use the 'ludus-nat' VNet")
			fmt.Println("- Old vmbr interfaces can be manually removed after verification")
			fmt.Println("")
			fmt.Print("Press ENTER to continue or Ctrl+C to abort: ")
			var input string
			fmt.Scanln(&input)
		}

		// Run migration
		responseJSON, success = rest.GenericJSONPost(client, "/migrate/sdn", nil)
		if !success {
			return
		}

		type Result struct {
			Result string `json:"result"`
		}

		var result Result
		err = json.Unmarshal(responseJSON, &result)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		fmt.Println("================================================================")
		fmt.Println("MIGRATION COMPLETE - MANUAL STEPS REQUIRED:")
		fmt.Println("================================================================")
		fmt.Println(result.Result)
		fmt.Println("")
		fmt.Println("1. Verify all VMs are accessible on their new VNet interfaces")
		fmt.Println("2. Test range connectivity (ping router, access VMs)")
		fmt.Println("3. Once verified, remove old bridge entries from /etc/network/interfaces:")
		fmt.Println("   - Look for lines containing \"vmbr1XXX\" where XXX is 001-254")
		fmt.Println("   - Remove the associated \"auto\", \"iface\", and route entries")
		fmt.Println("4. Reboot the Proxmox host to fully apply changes")
		fmt.Println("================================================================")
	},
}

var migrateSDNCmd = &cobra.Command{
	Use:   "sdn",
	Short: "SDN migration commands",
	Long:  "Commands for migrating from bridge-based networking to SDN VNets",
}

func init() {
	migrateSDNRunCmd.Flags().BoolVar(&noPrompt, "no-prompt", false, "Skip confirmation prompt")

	migrateSDNCmd.AddCommand(migrateSDNStatusCmd)
	migrateSDNCmd.AddCommand(migrateSDNRunCmd)

	migrateCmd.AddCommand(migrateSQLiteCmd)
	migrateCmd.AddCommand(migrateSDNCmd)

	rootCmd.AddCommand(migrateCmd)
}
