package cmd

import (
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"strconv"

	"github.com/spf13/cobra"
)

var vmCmd = &cobra.Command{
	Use:   "vm",
	Short: "Perform actions on VMs",
	Long:  ``,
}

var vmDestroyCmd = &cobra.Command{
	Use:     "destroy",
	Short:   "Destroy a VM",
	Long:    `Destroy a VM by its Proxmox ID. The VM will be stopped if running and then permanently deleted.`,
	Aliases: []string{"rm", "delete"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		vmIDStr := args[0]
		vmID, err := strconv.Atoi(vmIDStr)
		if err != nil {
			logger.Logger.Fatalf("Invalid VM ID: %s (must be a number)", vmIDStr)
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will permanently destroy VM %d !!!
The VM will be stopped if running and then deleted.

Do you want to continue? (y/N): `, vmID)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		// Build URL for VM destruction endpoint
		destroyVMURL := buildURLWithRangeAndUserID(fmt.Sprintf("/vm/%d", vmID))

		responseJSON, success := rest.GenericDelete(client, destroyVMURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupVmDestroyCmd(command *cobra.Command) {
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
}

func init() {
	setupVmDestroyCmd(vmDestroyCmd)
	vmCmd.AddCommand(vmDestroyCmd)
	rootCmd.AddCommand(vmCmd)
}

