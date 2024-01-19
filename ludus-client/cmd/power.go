package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/rest"
	"strings"

	"github.com/spf13/cobra"
)

var (
	powerCmdNames string
)

var powerCmd = &cobra.Command{
	Use:   "power",
	Short: "Control the power state of range VMs",
	Long:  ``,
}

func genericPowerCmd(value string) *cobra.Command {

	return &cobra.Command{
		Use:   value,
		Short: fmt.Sprintf("Power %s all range VMs", value),
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

			var machineNameArray []string
			if strings.Contains(powerCmdNames, ",") {
				machineNameArray = strings.Split(powerCmdNames, ",")
			} else {
				machineNameArray = []string{powerCmdNames}
			}

			type PowerBody struct {
				Machines []string `json:"machines"`
			}
			var powerPayload PowerBody
			powerPayload.Machines = machineNameArray

			payload, _ := json.Marshal(powerPayload)

			responseJSON, success := rest.GenericJSONPut(client, "/range/power"+value, string(payload))
			if didFailOrWantJSON(success, responseJSON) {
				return
			}
			handleGenericResult(responseJSON)

		},
	}
}

var powerOffCmd = genericPowerCmd("off")
var powerOnCmd = genericPowerCmd("on")

func setupPowerCmd(command *cobra.Command) {
	command.Flags().StringVarP(&powerCmdNames, "name", "n", "", "A VM name (JE-win10-21h2-enterprise-x64-1) or names separated by commas or 'all'")
	_ = command.MarkFlagRequired("name")
}

func init() {
	setupPowerCmd(powerOnCmd)
	setupPowerCmd(powerOffCmd)
	powerCmd.AddCommand(powerOnCmd)
	powerCmd.AddCommand(powerOffCmd)
	rootCmd.AddCommand(powerCmd)
}
