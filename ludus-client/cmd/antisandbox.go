package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"

	"github.com/spf13/cobra"
)

var (
	VMIDs                  string
	RegisteredOwner        string
	RegisteredOrganization string
	Vendor                 string
	dropFiles              bool
)

var antiSandboxCmd = &cobra.Command{
	Use:   "antisandbox",
	Short: "Install and enable anti-sandbox for VMs (enterprise)",
	Long:  ``,
}

var antiSandboxInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the custom QEMU and OVMF packages for anti-sandbox features (enterprise)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericJSONPost(client, "/antisandbox/install", "")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

var antiSandboxEnableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable anti-sandbox for a VM or multiple VMs (enterprise)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		type AntiSandboxPayload struct {
			VMIDs     string `json:"vmIDs"`
			Owner     string `json:"registeredOwner,omitempty"`
			Org       string `json:"registeredOrganization,omitempty"`
			Vendor    string `json:"vendor,omitempty"`
			DropFiles bool   `json:"dropFiles,omitempty"`
		}
		var antiSandboxPayload AntiSandboxPayload
		antiSandboxPayload.VMIDs = VMIDs
		antiSandboxPayload.Owner = RegisteredOwner
		antiSandboxPayload.Org = RegisteredOrganization
		antiSandboxPayload.Vendor = Vendor
		antiSandboxPayload.DropFiles = dropFiles
		if antiSandboxPayload.Vendor != "" && antiSandboxPayload.Vendor != "Dell" {
			logger.Logger.Fatal("The only supported vendor at this time is Dell")
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will enable anti-sandbox settings for VMs: %s !!!
    which will have performance penalties. This should
    be the last step once a VM is fully configured!
    The VM(s) will be rebooted during this process.

Do you want to continue? (y/N): `, VMIDs)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		logger.Logger.Info("Enabling Anti-Sandbox settings for VM(s), this can take some time. Please wait.")

		payload, _ := json.Marshal(antiSandboxPayload)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/antisandbox/enable?userID=%s", userID), string(payload))
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/antisandbox/enable", string(payload))
		}
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleAntiSandboxResult(responseJSON)
	},
}

func setupAntiSandboxEnableCmd(command *cobra.Command) {
	command.Flags().StringVarP(&VMIDs, "vmids", "n", "", "A VM ID or name (104) or multiple VM IDs or names (104,105) to enable anti-sandbox on")
	command.Flags().StringVar(&RegisteredOwner, "owner", "", "The RegisteredOwner value to use for the VMs")
	command.Flags().StringVar(&RegisteredOrganization, "org", "", "The RegisteredOrganization value to use for the VMs")
	command.Flags().StringVar(&Vendor, "vendor", "", "The Vendor value to use for the MAC address of the VMs")
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
	command.Flags().BoolVar(&dropFiles, "drop-files", false, "drop random pdf, doc, ppt, and xlsx files on the desktop and downloads folder of the VMs")

	_ = command.MarkFlagRequired("vmids")
}

func handleAntiSandboxResult(responseJSON []byte) {
	type errorStruct struct {
		Item   string `json:"item"`
		Reason string `json:"reason"`
	}

	type Data struct {
		Success []string      `json:"success"`
		Errors  []errorStruct `json:"errors"`
	}

	// Unmarshal JSON data
	var data Data
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	logger.Logger.Debugf("%v", data)

	if len(data.Errors) > 0 {
		for _, error := range data.Errors {
			logger.Logger.Error(error.Item + ": " + error.Reason)
		}
	}
	if len(data.Success) > 0 {
		for _, allowed := range data.Success {
			logger.Logger.Info("Successfully enabled anti-sandbox for VM(s): " + allowed)
		}
	}
}

func init() {
	antiSandboxCmd.AddCommand(antiSandboxInstallCmd)
	setupAntiSandboxEnableCmd(antiSandboxEnableCmd)
	antiSandboxCmd.AddCommand(antiSandboxEnableCmd)
	rootCmd.AddCommand(antiSandboxCmd)
}
