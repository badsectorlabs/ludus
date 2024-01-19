package cmd

import (
	"fmt"
	"runtime/debug"

	logger "ludus/logger"
	"ludus/rest"

	"github.com/spf13/cobra"
)

// configCmd represents the config command
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Prints the version of this ludus binary",
	Long: `Prints the version of this ludus binary.
The version includes the SemVer version and the first
8 character of the git commit hash of the commit
this binary was built from.
	`,
	Run: func(cmd *cobra.Command, args []string) {
		logger.Logger.Info(fmt.Sprintf("Ludus client v%s", LudusVersion))

		if verbose {
			info, ok := debug.ReadBuildInfo()
			if ok {
				logger.Logger.Debug(info)
			} else {
				logger.Logger.Debug("No build info included in the binary it was likely compiled with '-buildinfo=false'")
			}
		}
		if len(apiKey) == 0 {
			logger.Logger.Fatal("No API key. Cannot query server version.")
			return
		}
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		responseJSON, success := rest.GenericGet(client, "/")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)

		//  TODO check for a more recent version via Gitlab release URL
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
