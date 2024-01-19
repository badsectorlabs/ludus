package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	userName        string
	newUserID       string
	userIsAdmin     bool
	proxmoxPassword string
)

// usersCmd represents the users command
var usersCmd = &cobra.Command{
	Use:     "users",
	Short:   "Perform actions related to users",
	Long:    ``,
	Aliases: []string{"user"},
}

var usersListCmd = &cobra.Command{
	Use:   "list [all]",
	Short: "List information about a user (alias: status)",
	Long: `Optionally supply the value "all" to retrieve
	information about all users.`,
	Args:    cobra.RangeArgs(0, 1),
	Aliases: []string{"status"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if len(args) == 1 && args[0] == "all" {
			responseJSON, success = rest.GenericGet(client, "/user/all")
		} else if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/user?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/user")
		}
		if !success {
			return
		}
		var userObjectArray []UserObject
		err := json.Unmarshal(responseJSON, &userObjectArray)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Name", "userID", "Created", "Last Active", "Admin"})

		// Add data to table
		for _, item := range userObjectArray {
			created := formatTimeObject(item.DateCreated)
			active := formatTimeObject(item.DateLastActive)
			table.Append([]string{item.Name,
				item.UserID,
				created,
				active,
				strconv.FormatBool(item.IsAdmin)})
		}

		// Print table
		table.Render()

	},
}

var credsCmd = &cobra.Command{
	Use:   "creds",
	Short: "Perform actions related to Proxmox credentials",
	Long:  ``,
}

var usersCredsGetsCmd = &cobra.Command{
	Use:   "get",
	Short: "Get Proxmox credentials for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/user/credentials?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/user/credentials")
		}
		if !success {
			return
		}

		type Data struct {
			Result struct {
				ProxmoxPassword string `json:"proxmoxPassword"`
				ProxmoxUsername string `json:"proxmoxUsername"`
			} `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Proxmox Username", "Proxmox Password"})

		// Add data to table
		table.Append([]string{data.Result.ProxmoxUsername,
			data.Result.ProxmoxPassword,
		})

		// Print table
		table.Render()

	},
}

var usersAPIKeyCmd = &cobra.Command{
	Use:   "apikey",
	Short: "Get a new Ludus apikey for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/user/apikey?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/user/apikey")
		}
		if !success {
			return
		}

		type Data struct {
			Result struct {
				ApiKey          string `json:"apiKey"`
				DateCreated     string `json:"dateCreated"`
				DateLastActive  string `json:"dateLastActive"`
				HashedAPIKey    string `json:"hashedAPIKey"`
				IsAdmin         bool   `json:"isAdmin"`
				Name            string `json:"name"`
				ProxmoxUsername string `json:"proxmoxUsername"`
				UserID          string `json:"userID"`
			} `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "API Key"})

		// Add data to table
		table.Append([]string{data.Result.UserID,
			data.Result.ApiKey,
		})

		// Print table
		table.Render()

	},
}

var usersWireguardCmd = &cobra.Command{
	Use:   "wireguard",
	Short: "Get the Ludus wireguard configuration for a user",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/user/wireguard?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/user/wireguard")
		}
		if !success {
			return
		}

		type Data struct {
			Result struct {
				WireGuardConfig string `json:"wireGuardConfig"`
			} `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(data.Result.WireGuardConfig)

	},
}

var usersAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a user to Ludus",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		requestBody := fmt.Sprintf(`{
			"name": "%s",
			"userID": "%s",
			"isAdmin": %s
		  }`, userName, newUserID, strconv.FormatBool(userIsAdmin))
		responseJSON, success = rest.GenericJSONPost(client, "/user", requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		type Data struct {
			Result struct {
				ApiKey          string `json:"apiKey"`
				DateCreated     string `json:"dateCreated"`
				DateLastActive  string `json:"dateLastActive"`
				IsAdmin         bool   `json:"isAdmin"`
				Name            string `json:"name"`
				ProxmoxUsername string `json:"proxmoxUsername"`
				UserID          string `json:"userID"`
			} `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Proxmox Username", "Admin", "API Key"})

		table.Append([]string{data.Result.UserID,
			data.Result.ProxmoxUsername,
			strconv.FormatBool(data.Result.IsAdmin),
			data.Result.ApiKey,
		})

		table.Render()

	},
}

func setupUsersAddCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the new user (2-4 chars)")
	command.Flags().StringVarP(&userName, "name", "n", "", "the name of the user (typically 'first last')")
	command.Flags().BoolVarP(&userIsAdmin, "admin", "a", false, "set this flag to make the user an admin of Ludus")

	_ = command.MarkFlagRequired("userid")
	_ = command.MarkFlagRequired("name")
}

var usersDeleteCmd = &cobra.Command{
	Use:     "rm",
	Short:   "Remove a user from Ludus",
	Long:    ``,
	Args:    cobra.ExactArgs(0),
	Aliases: []string{"remove", "delete"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/user/%s", newUserID))

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupUsersDeleteCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user to remove")
	_ = command.MarkFlagRequired("userid")
}

var usersCredsSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the proxmox password for a Ludus user",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		requestBody := fmt.Sprintf(`{
			"userID": "%s",
			"proxmoxPassword": "%s"
		  }`, newUserID, proxmoxPassword)
		responseJSON, success := rest.GenericJSONPost(client, "/user/credentials", requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupUsersCredsSetCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user")
	command.Flags().StringVarP(&proxmoxPassword, "password", "p", "", "the proxmox password of the user")

	_ = command.MarkFlagRequired("password")
}

func init() {
	usersCmd.AddCommand(usersListCmd)
	usersCmd.AddCommand(usersAPIKeyCmd)
	usersCmd.AddCommand(usersWireguardCmd)
	setupUsersAddCmd(usersAddCmd)
	usersCmd.AddCommand(usersAddCmd)
	setupUsersDeleteCmd(usersDeleteCmd)
	usersCmd.AddCommand(usersDeleteCmd)
	credsCmd.AddCommand(usersCredsGetsCmd)
	setupUsersCredsSetCmd(usersCredsSetCmd)
	credsCmd.AddCommand(usersCredsSetCmd)
	usersCmd.AddCommand(credsCmd)
	rootCmd.AddCommand(usersCmd)
}
