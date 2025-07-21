package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"

	"github.com/spf13/cobra"
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate data from SQLite to PostgreSQL",
	Long: `Migrate data from SQLite database to PostgreSQL database.
	
This command will migrate all data from the old SQLite database to the new PostgreSQL database
if the following conditions are met:
1. SQLite database file exists at /opt/ludus/ludus.db
2. PostgreSQL database only contains the ROOT user

The migration includes:
- Users (excluding ROOT)
- Ranges with default values for new fields (name, description, purpose)
- VMs
- Range access permissions (converted from RangeAccessObject to UserRangeAccess)

After successful migration, the SQLite database will be backed up with a timestamp.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, "/migrate/sqlite", nil)
		if !success {
			return
		}

		type Result struct {
			Result string `json:"result"`
		}

		var result Result
		err := json.Unmarshal(responseJSON, &result)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		fmt.Println(result.Result)
	},
}

func init() {
	rootCmd.AddCommand(migrateCmd)
}
