package cmd

import (
	"fmt"

	logger "ludus/logger"

	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

// configCmd represents the config command
var apiKeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Store your Ludus API key in the system keyring",
	Long: `This command stores the Ludus API key in the system
keyring. This is implemented differently on different OS's but
is more secure than writing unencrypted to a file.`,
	Run: func(cmd *cobra.Command, args []string) {

		logger.Logger.Info(fmt.Sprintf("Enter your Ludus API Key for %s: ", url))
		fmt.Scanln(&apiKey)

		// set API key in the system keyring
		err := keyring.Set(keyringService, url, apiKey)
		if err != nil {
			logger.Logger.Fatalf("Failed to set the api key in the in system keyring." +
				"\nYou can set the LUDUS_API_KEY env variable if you are on a headless system.\n\n" + err.Error())
		}
		logger.Logger.Info("Ludus API key set successfully")
	},
}

func init() {
	rootCmd.AddCommand(apiKeyCmd)
}
