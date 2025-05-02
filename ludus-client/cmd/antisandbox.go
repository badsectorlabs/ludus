package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"strings"

	"github.com/spf13/cobra"
)

var (
	VMIDs                  string
	RegisteredOwner        string
	RegisteredOrganization string
	Vendor                 string
	dropFiles              bool
	processorName          string
	processorVendor        string
	processorSpeed         string
	processorIdentifier    string
)

func isUsingAdminPort() bool {
	return strings.Contains(url, ":8081")
}

var antiSandboxCmd = &cobra.Command{
	Use:   "antisandbox",
	Short: "Install and enable anti-sandbox for VMs (enterprise)",
	Long:  ``,
}

var antiSandboxInstallCustomCmd = &cobra.Command{
	Use:   "install-custom",
	Short: "Install the custom QEMU and OVMF packages for anti-sandbox features (enterprise)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if !isUsingAdminPort() {
			logger.Logger.Fatal("Anti-Sandbox is only available on the admin port (:8081)")
		}

		responseJSON, success := rest.GenericJSONPost(client, "/antisandbox/install-custom", "")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

var antiSandboxInstallStandardCmd = &cobra.Command{
	Use:   "install-standard",
	Short: "Install the standard QEMU and OVMF packages (enterprise)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if !isUsingAdminPort() {
			logger.Logger.Fatal("Anti-Sandbox is only available on the admin port (:8081)")
		}

		responseJSON, success := rest.GenericJSONPost(client, "/antisandbox/install-standard", "")
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

		if !isUsingAdminPort() {
			logger.Logger.Fatal("Anti-Sandbox is only available on the admin port (:8081)")
		}

		type AntiSandboxPayload struct {
			VMIDs               string `json:"vmIDs"`
			Owner               string `json:"registeredOwner,omitempty"`
			Org                 string `json:"registeredOrganization,omitempty"`
			Vendor              string `json:"vendor,omitempty"`
			DropFiles           bool   `json:"dropFiles,omitempty"`
			ProcessorName       string `json:"processorName,omitempty"`
			ProcessorVendor     string `json:"processorVendor,omitempty"`
			ProcessorSpeed      string `json:"processorSpeed,omitempty"`
			ProcessorIdentifier string `json:"processorIdentifier,omitempty"`
		}
		var antiSandboxPayload AntiSandboxPayload
		antiSandboxPayload.VMIDs = VMIDs
		antiSandboxPayload.Owner = RegisteredOwner
		antiSandboxPayload.Org = RegisteredOrganization
		antiSandboxPayload.Vendor = Vendor
		antiSandboxPayload.DropFiles = dropFiles
		antiSandboxPayload.ProcessorName = processorName
		antiSandboxPayload.ProcessorVendor = processorVendor
		antiSandboxPayload.ProcessorSpeed = processorSpeed
		antiSandboxPayload.ProcessorIdentifier = processorIdentifier
		if antiSandboxPayload.Vendor != "" && antiSandboxPayload.Vendor != "Dell" {
			logger.Logger.Fatal("The only supported vendor at this time is Dell")
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will enable anti-sandbox settings for VMs: %s !!!
    which will have performance penalties. This should
    be the last step once a VM is fully configured!
    The VM(s) will be hard rebooted during this process.

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
		handleSuccessErrorArrayResult(responseJSON, "anti-sandbox")
	},
}

func setupAntiSandboxEnableCmd(command *cobra.Command) {
	command.Flags().StringVarP(&VMIDs, "vmids", "n", "", "A VM ID or name (104) or multiple VM IDs or names (104,105) to enable anti-sandbox on")
	command.Flags().StringVar(&RegisteredOwner, "owner", "", "The RegisteredOwner value to use for the VMs")
	command.Flags().StringVar(&RegisteredOrganization, "org", "", "The RegisteredOrganization value to use for the VMs")
	command.Flags().StringVar(&Vendor, "vendor", "", "The Vendor value to use for the MAC address of the VMs")
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
	command.Flags().BoolVar(&dropFiles, "drop-files", false, "drop random pdf, doc, ppt, and xlsx files on the desktop and downloads folder of the VMs")
	command.Flags().StringVar(&processorName, "processor-name", "", "The ProcessorNameString value to use for the VMs (e.g. Intel(R) Core(TM) i7-9750H CPU @ 2.60GHz)")
	command.Flags().StringVar(&processorVendor, "processor-vendor", "", "The VendorIdentifier value to use for the VMs (e.g. GenuineIntel or AuthenticAMD)")
	command.Flags().StringVar(&processorSpeed, "processor-speed", "", "The ~Mhz value to use for the VMs in MHz (e.g. 2600)")
	command.Flags().StringVar(&processorIdentifier, "processor-identifier", "", "The Identifier value to use for the VMs (e.g. Intel64 Family 6 Model 142 Stepping 10)")
	_ = command.MarkFlagRequired("vmids")
}

func handleSuccessErrorArrayResult(responseJSON []byte, feature string) {
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
			logger.Logger.Info("Successfully enabled " + feature + " for VM(s): " + allowed)
		}
	}
}

func init() {
	antiSandboxCmd.AddCommand(antiSandboxInstallCustomCmd)
	antiSandboxCmd.AddCommand(antiSandboxInstallStandardCmd)
	setupAntiSandboxEnableCmd(antiSandboxEnableCmd)
	antiSandboxCmd.AddCommand(antiSandboxEnableCmd)
	rootCmd.AddCommand(antiSandboxCmd)
}
