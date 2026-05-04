package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var autoShutdownTimeout string

type AutoShutdownResponse struct {
	AutoShutdownTimeout struct {
		ServerDefault string `json:"serverDefault"`
		RangeOverride string `json:"rangeOverride"`
		Effective     string `json:"effective"`
	} `json:"autoShutdownTimeout"`
}

func displayAutoShutdown(data AutoShutdownResponse) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetHeader([]string{"Setting", "Server Default", "Range Override", "Effective"})
	table.Append([]string{
		"Auto Shutdown Timeout",
		data.AutoShutdownTimeout.ServerDefault,
		data.AutoShutdownTimeout.RangeOverride,
		data.AutoShutdownTimeout.Effective,
	})
	table.Render()
}

var autoShutdownCmd = &cobra.Command{
	Use:   "auto-shutdown",
	Short: "Manage range auto-shutdown settings",
	Long:  ``,
}

var autoShutdownGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get current range auto-shutdown settings",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/range/auto-shutdown"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data AutoShutdownResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		displayAutoShutdown(data)
	},
}

var autoShutdownSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set range auto-shutdown timeout",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		type AutoShutdownPayload struct {
			AutoShutdownTimeout string `json:"autoShutdownTimeout"`
		}
		payload, _ := json.Marshal(AutoShutdownPayload{AutoShutdownTimeout: autoShutdownTimeout})

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/range/auto-shutdown"), string(payload))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data AutoShutdownResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Println("Range auto-shutdown settings updated successfully")
		displayAutoShutdown(data)
	},
}

var autoShutdownResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset range auto-shutdown timeout to server default",
	Long: `Reset the per-range auto-shutdown timeout override so the range falls back to the server default.

  ludus range auto-shutdown reset`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		payload, _ := json.Marshal(map[string]string{"autoShutdownTimeout": ""})

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/range/auto-shutdown"), string(payload))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data AutoShutdownResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Println("Auto-shutdown timeout reset to server default")
		displayAutoShutdown(data)
	},
}

func init() {
	autoShutdownSetCmd.Flags().StringVarP(&autoShutdownTimeout, "timeout", "t", "", "Inactivity timeout duration (e.g., '4h', '30m', '0' to disable)")
	_ = autoShutdownSetCmd.MarkFlagRequired("timeout")

	autoShutdownCmd.AddCommand(autoShutdownGetCmd)
	autoShutdownCmd.AddCommand(autoShutdownSetCmd)
	autoShutdownCmd.AddCommand(autoShutdownResetCmd)
	rangeCmd.AddCommand(autoShutdownCmd)
}
