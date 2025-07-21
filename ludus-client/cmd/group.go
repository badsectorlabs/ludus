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

// groupsCmd represents the groups command
var groupsCmd = &cobra.Command{
	Use:     "groups",
	Short:   "Perform actions related to groups",
	Long:    ``,
	Aliases: []string{"group"},
}

var groupsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all groups",
	Long:  `List all groups in the system.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, "/groups")
		if !success {
			return
		}

		type Data struct {
			Result []struct {
				ID          int    `json:"id"`
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"result"`
		}

		var data Data
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Name", "Description"})

		// Add data to table
		for _, group := range data.Result {
			table.Append([]string{
				fmt.Sprintf("%d", group.ID),
				group.Name,
				group.Description,
			})
		}

		// Print table
		table.Render()
	},
}

var groupsCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new group",
	Long:  `Create a new group with the specified name.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		description, _ := cmd.Flags().GetString("description")

		payload := map[string]interface{}{
			"name":        args[0],
			"description": description,
		}

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, "/groups", payload)
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Group '%s' created successfully\n", args[0])
		}
	},
}

var groupsDeleteCmd = &cobra.Command{
	Use:   "delete [groupID]",
	Short: "Delete a group",
	Long:  `Delete a group and clean up all memberships and range access.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/groups/%s", groupID))
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Group %s deleted successfully\n", groupID)
		}
	},
}

var groupsAddUserCmd = &cobra.Command{
	Use:   "add user [groupID] [userID]",
	Short: "Add a user to a group",
	Long:  `Add a user to a group to grant them access to ranges assigned to that group.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]
		userID := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/groups/%s/users/%s", groupID, userID), nil)
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("User %s added to group %s successfully\n", userID, groupID)
		}
	},
}

var groupsRemoveUserCmd = &cobra.Command{
	Use:   "remove user [groupID] [userID]",
	Short: "Remove a user from a group",
	Long:  `Remove a user from a group to revoke their access to ranges assigned to that group.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]
		userID := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/groups/%s/users/%s", groupID, userID))
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("User %s removed from group %s successfully\n", userID, groupID)
		}
	},
}

var groupsAddRangeCmd = &cobra.Command{
	Use:   "add range [groupID] [rangeNumber]",
	Short: "Grant group access to a range",
	Long:  `Grant a group access to a specific range, allowing all group members to access that range.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]
		rangeNumber := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/groups/%s/ranges/%s", groupID, rangeNumber), nil)
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Group %s granted access to range %s successfully\n", groupID, rangeNumber)
		}
	},
}

var groupsRemoveRangeCmd = &cobra.Command{
	Use:   "remove range [groupID] [rangeNumber]",
	Short: "Revoke group access from a range",
	Long:  `Revoke a group's access to a specific range, removing access for all group members.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]
		rangeNumber := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/groups/%s/ranges/%s", groupID, rangeNumber))
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Group %s access to range %s revoked successfully\n", groupID, rangeNumber)
		}
	},
}

var groupsMembersCmd = &cobra.Command{
	Use:   "members [groupID]",
	Short: "List group members",
	Long:  `List all users who are members of the specified group.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/groups/%s/users", groupID))
		if !success {
			return
		}

		type Data struct {
			Result []struct {
				UserID string `json:"userID"`
				Name   string `json:"name"`
			} `json:"result"`
		}

		var data Data
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Name"})

		// Add data to table
		for _, user := range data.Result {
			table.Append([]string{user.UserID, user.Name})
		}

		// Print table
		table.Render()
	},
}

var groupsRangesCmd = &cobra.Command{
	Use:   "ranges [groupID]",
	Short: "List group accessible ranges",
	Long:  `List all ranges that the specified group has access to.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupID := args[0]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/groups/%s/ranges", groupID))
		if !success {
			return
		}

		type Data struct {
			Result []struct {
				RangeNumber int32  `json:"rangeNumber"`
				UserID      string `json:"userID"`
				RangeState  string `json:"rangeState"`
			} `json:"result"`
		}

		var data Data
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Range Number", "UserID", "State"})

		// Add data to table
		for _, rangeObj := range data.Result {
			table.Append([]string{
				fmt.Sprintf("%d", rangeObj.RangeNumber),
				rangeObj.UserID,
				rangeObj.RangeState,
			})
		}

		// Print table
		table.Render()
	},
}

func init() {
	// Add flags to group create command
	groupsCreateCmd.Flags().String("description", "", "Description of the group")

	// Add subcommands to groups command
	groupsCmd.AddCommand(groupsListCmd)
	groupsCmd.AddCommand(groupsCreateCmd)
	groupsCmd.AddCommand(groupsDeleteCmd)
	groupsCmd.AddCommand(groupsAddUserCmd)
	groupsCmd.AddCommand(groupsRemoveUserCmd)
	groupsCmd.AddCommand(groupsAddRangeCmd)
	groupsCmd.AddCommand(groupsRemoveRangeCmd)
	groupsCmd.AddCommand(groupsMembersCmd)
	groupsCmd.AddCommand(groupsRangesCmd)

	// Register the groups command with the root command
	rootCmd.AddCommand(groupsCmd)
}
