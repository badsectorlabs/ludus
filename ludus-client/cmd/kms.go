package cmd

import (
	"fmt"
	"ludus/rest"

	"github.com/spf13/cobra"
)

var kmsCmd = &cobra.Command{
	Use:   "kms",
	Short: "Manage Windows license tasks (enterprise only)",
	Long:  ``,
}

var installKMSCmd = &cobra.Command{
	Use:   "install",
	Short: "Install a Key Management Service (KMS) server on the Ludus host at 192.0.2.1",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericJSONPost(client, "/kms/install", "")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

var licenseWindowsCmd = &cobra.Command{
	Use:   "license",
	Short: "License Windows VMs using KMS",
	Long:  `License one or more Windows VMs using the KMS server. Provide VM IDs as a comma-separated list.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		requestBody := map[string]interface{}{
			"vmIDs":      VMIDs,
			"productKey": productKey,
		}

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/kms/license?userID=%s", userID), requestBody)
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/kms/license", requestBody)
		}

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleSuccessErrorArrayResult(responseJSON, "license")
	},
}

var productKey string

func setupLicenseWindowsCmd(command *cobra.Command) {
	command.Flags().StringVarP(&VMIDs, "vmids", "n", "", "A VM ID (104) or multiple VM IDs (104,105) to license")
	command.Flags().StringVarP(&productKey, "product-key", "p", "", "The volume license product key to license the VMs with (default: determine from Windows version)")
	_ = command.MarkFlagRequired("vmids")
}

func init() {
	kmsCmd.AddCommand(installKMSCmd)
	setupLicenseWindowsCmd(licenseWindowsCmd)
	kmsCmd.AddCommand(licenseWindowsCmd)
	rootCmd.AddCommand(kmsCmd)
}
