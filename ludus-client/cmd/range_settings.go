package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var autoShutdownTimeout string

// rangeSettingsNames is the catalogue of per-range settings known to the CLI.
// When a new setting is added, extend this list — it drives validation for
// `ludus range settings reset <name>` and can feed tab completion later.
var rangeSettingsNames = []string{
	"auto-shutdown-timeout",
}

// rangeSettingsResetPayload builds the request body for a `reset` call.
// - An empty names slice means "reset everything" → every known setting is cleared.
// - A non-empty slice clears only the requested settings.
func rangeSettingsResetPayload(names []string) map[string]string {
	payload := map[string]string{}
	if len(names) == 0 {
		for _, n := range rangeSettingsNames {
			payload[settingAPIKey(n)] = ""
		}
		return payload
	}
	for _, n := range names {
		payload[settingAPIKey(n)] = ""
	}
	return payload
}

// settingAPIKey translates the CLI-style setting name ("auto-shutdown-timeout")
// to the wire-level JSON key ("autoShutdownTimeout").
func settingAPIKey(name string) string {
	switch name {
	case "auto-shutdown-timeout":
		return "autoShutdownTimeout"
	}
	return name
}

type RangeSettingsResponse struct {
	AutoShutdownTimeout struct {
		ServerDefault string `json:"serverDefault"`
		RangeOverride string `json:"rangeOverride"`
		Effective     string `json:"effective"`
	} `json:"autoShutdownTimeout"`
}

func displaySettings(data RangeSettingsResponse) {
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

var settingsCmd = &cobra.Command{
	Use:   "settings",
	Short: "Manage range settings",
	Long:  ``,
}

var settingsGetCmd = &cobra.Command{
	Use:   "get",
	Short: "Get current range settings",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/range/settings"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data RangeSettingsResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		displaySettings(data)
	},
}

var settingsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set range settings",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		type SettingsPayload struct {
			AutoShutdownTimeout string `json:"autoShutdownTimeout"`
		}
		payload, _ := json.Marshal(SettingsPayload{AutoShutdownTimeout: autoShutdownTimeout})

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/range/settings"), string(payload))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data RangeSettingsResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Println("Range settings updated successfully")
		displaySettings(data)
	},
}

var settingsResetCmd = &cobra.Command{
	Use:   "reset [setting...]",
	Short: "Reset range setting overrides to server defaults",
	Long: `Reset per-range setting overrides so the range falls back to the server defaults.

With no arguments, resets every setting. Pass specific setting names to reset only those:

  ludus range settings reset
  ludus range settings reset auto-shutdown-timeout`,
	Args:      cobra.ArbitraryArgs,
	ValidArgs: rangeSettingsNames,
	Run: func(cmd *cobra.Command, args []string) {
		// Validate arg names against the known catalogue
		for _, a := range args {
			valid := false
			for _, known := range rangeSettingsNames {
				if a == known {
					valid = true
					break
				}
			}
			if !valid {
				logger.Logger.Fatalf("unknown setting %q; valid names: %s", a, strings.Join(rangeSettingsNames, ", "))
			}
		}

		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		payload, _ := json.Marshal(rangeSettingsResetPayload(args))

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/range/settings"), string(payload))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data RangeSettingsResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if len(args) == 0 {
			fmt.Println("All range setting overrides reset")
		} else {
			fmt.Printf("Reset: %s\n", strings.Join(args, ", "))
		}
		displaySettings(data)
	},
}

func init() {
	settingsSetCmd.Flags().StringVar(&autoShutdownTimeout, "auto-shutdown-timeout", "", "Inactivity timeout duration (e.g., '4h', '30m', '0' to disable)")
	_ = settingsSetCmd.MarkFlagRequired("auto-shutdown-timeout")

	settingsCmd.AddCommand(settingsGetCmd)
	settingsCmd.AddCommand(settingsSetCmd)
	settingsCmd.AddCommand(settingsResetCmd)
	rangeCmd.AddCommand(settingsCmd)
}
