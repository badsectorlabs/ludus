package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	logger "ludus/logger"
	"ludus/rest"
	"ludusapi/dto"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// diagnosticsCmd represents the diagnostics command
var diagnosticsCmd = &cobra.Command{
	Use:   "diagnostics",
	Short: "Get system diagnostics from the Ludus server",
	Long: `Get system diagnostics from the Ludus server including:
- CPU information (model and cores)
- Storage pool information (size, used, free, percentage)
- Performance metrics from pveperf (CPU, regex, disk, network)`,
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		responseJSON, success := rest.GenericGet(client, "/diagnostics")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Parse the diagnostics response
		var diagnostics dto.GetDiagnosticsResponse

		err := json.Unmarshal(responseJSON, &diagnostics)
		if err != nil {
			logger.Logger.Fatalf("Failed to parse diagnostics response: %v", err)
			return
		}

		// Print CPU information
		fmt.Println("\n=== CPU Information ===")
		fmt.Printf("Model: %s\n", diagnostics.CPU.Model)
		fmt.Printf("Cores: %d\n", diagnostics.CPU.Cores)

		// Print Storage Pools
		if len(diagnostics.StoragePools) > 0 {
			fmt.Println("\n=== Storage Pools ===")
			table := tablewriter.NewWriter(os.Stdout)
			table.SetHeader([]string{"Name", "Type", "Size (GB)", "Used (GB)", "Free (GB)", "Free %"})
			table.SetAlignment(tablewriter.ALIGN_LEFT)

			for _, pool := range diagnostics.StoragePools {
				table.Append([]string{
					pool.Name,
					pool.Type,
					fmt.Sprintf("%.2f", pool.SizeGB),
					fmt.Sprintf("%.2f", pool.UsedGB),
					fmt.Sprintf("%.2f", pool.FreeGB),
					fmt.Sprintf("%.2f%%", pool.FreePercentage),
				})
			}
			table.Render()
		}

		// Print Performance Metrics
		fmt.Println("\n=== Performance Metrics (pveperf) ===")
		fmt.Printf("CPU BOGOMIPS:      %.2f\n", diagnostics.Pveperf.CPUBogomips)
		fmt.Printf("REGEX/SECOND:      %d\n", diagnostics.Pveperf.RegexPerSecond)
		fmt.Printf("HD SIZE:           %s\n", diagnostics.Pveperf.HdSize)
		fmt.Printf("BUFFERED READS:    %s\n", diagnostics.Pveperf.BufferedReads)
		fmt.Printf("AVERAGE SEEK TIME: %s\n", diagnostics.Pveperf.AverageSeekTime)
		fmt.Printf("FSYNCS/SECOND:     %.2f\n", diagnostics.Pveperf.FsyncsPerSecond)
		fmt.Printf("DNS EXT:           %s\n", diagnostics.Pveperf.DNSExt)
		fmt.Println()
	},
}

func init() {
	rootCmd.AddCommand(diagnosticsCmd)
}
