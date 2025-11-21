package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"ludusapi/dto"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var manager bool

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
		groupURL := buildURLWithRangeAndUserID("/groups")
		responseJSON, success = rest.GenericGet(client, groupURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data []dto.ListGroupsResponseItem
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Name", "Description", "Managers", "Members", "Ranges"})

		// Add data to table
		for _, group := range data {
			table.Append([]string{
				group.Name,
				group.Description,
				fmt.Sprintf("%d", group.NumManagers),
				fmt.Sprintf("%d", group.NumMembers),
				fmt.Sprintf("%d", group.NumRanges),
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
		groupURL := buildURLWithRangeAndUserID("/groups")
		responseJSON, success = rest.GenericJSONPost(client, groupURL, payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		logger.Logger.Info(fmt.Sprintf("Group '%s' created successfully", args[0]))
	},
}

var groupsDeleteCmd = &cobra.Command{
	Use:   "delete [groupName]",
	Short: "Delete a group",
	Long:  `Delete a group and clean up all memberships and range access.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupName := args[0]

		var responseJSON []byte
		var success bool
		groupURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s", groupName))
		responseJSON, success = rest.GenericDelete(client, groupURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Info(fmt.Sprintf("Group %s deleted successfully", groupName))
	},
}

// Parent add command
var groupsAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add users or ranges to groups",
	Long:  `Add users to groups or grant group access to ranges.`,
}

var groupsAddUserCmd = &cobra.Command{
	Use:   "user [userID] [groupName]",
	Short: "Add a user to a group",
	Long:  `Add a user to a group to grant them access to ranges assigned to that group.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userID := args[0]
		groupName := args[1]

		var responseJSON []byte
		var success bool
		groupUserURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/users/%s", groupName, userID))
		if manager {
			groupUserURL = addQueryParameterToURL(groupUserURL, "manager", "true")
		}
		responseJSON, success = rest.GenericJSONPost(client, groupUserURL, nil)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Info(fmt.Sprintf("User %s added to group %s successfully", userID, groupName))
	},
}

func setupGroupsAddUserCmd(command *cobra.Command) {
	command.Flags().BoolVarP(&manager, "manager", "m", false, "whether the user should be a manager of the group")
}

var groupsAddRangeCmd = &cobra.Command{
	Use:   "range [rangeID] [groupName]",
	Short: "Grant group access to a range",
	Long:  `Grant a group access to a specific range, allowing all group members to access that range.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rangeID := args[0]
		groupName := args[1]

		var responseJSON []byte
		var success bool
		groupRangeURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/ranges/%s", groupName, rangeID))
		responseJSON, success = rest.GenericJSONPost(client, groupRangeURL, nil)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Info(fmt.Sprintf("Group %s granted access to range %s successfully", groupName, rangeID))
	},
}

// Parent remove command
var groupsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove users or ranges from groups",
	Long:  `Remove users from groups or revoke group access to ranges.`,
}

var groupsRemoveUserCmd = &cobra.Command{
	Use:   "user [userID] [groupName]",
	Short: "Remove a user from a group",
	Long:  `Remove a user from a group to revoke their access to ranges assigned to that group.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userID := args[0]
		groupName := args[1]

		var responseJSON []byte
		var success bool
		groupUserURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/users/%s", groupName, userID))
		responseJSON, success = rest.GenericDelete(client, groupUserURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Info(fmt.Sprintf("User %s removed from group %s successfully", userID, groupName))
	},
}

var groupsRemoveRangeCmd = &cobra.Command{
	Use:   "range [rangeID] [groupName]",
	Short: "Revoke group access from a range",
	Long:  `Revoke a group's access to a specific range, removing access for all group members.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rangeID := args[0]
		groupName := args[1]

		if rangeID == "" || groupName == "" {
			logger.Logger.Fatal("rangeID and groupName are required")
		}

		var responseJSON []byte
		var success bool
		groupRangeURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/ranges/%s", groupName, rangeID))
		responseJSON, success = rest.GenericDelete(client, groupRangeURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Info(fmt.Sprintf("Group %s access to range %s revoked successfully", groupName, rangeID))
	},
}

var groupsMembersCmd = &cobra.Command{
	Use:   "members [groupName]",
	Short: "List group members",
	Long:  `List all users who are members and managers of the specified group.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupName := args[0]

		var responseJSON []byte
		var success bool
		groupURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/users", groupName))
		responseJSON, success = rest.GenericGet(client, groupURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data dto.ListGroupMembersResponse
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Name", "Role"})

		// Add data to table
		for _, user := range data.Result {
			table.Append([]string{user.UserID, user.Name, user.Role})
		}

		// Print table
		table.Render()
	},
}

var groupsRangesCmd = &cobra.Command{
	Use:   "ranges [groupName]",
	Short: "List group accessible ranges",
	Long:  `List all ranges that the specified group has access to.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupName := args[0]

		var responseJSON []byte
		var success bool

		groupURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/ranges", groupName))
		responseJSON, success = rest.GenericGet(client, groupURL)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var data []dto.ListGroupRangesResponseItem
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Range Number", "Range ID", "Name", "Number of VMs", "Testing Enabled"})

		// Add data to table
		for _, rangeObj := range data {
			table.Append([]string{
				fmt.Sprintf("%d", rangeObj.RangeNumber),
				rangeObj.RangeID,
				rangeObj.Name,
				fmt.Sprintf("%d", rangeObj.NumberOfVMs),
				fmt.Sprintf("%t", rangeObj.TestingEnabled),
			})
		}

		// Print table
		table.Render()
	},
}

func init() {
	// Add flags to group create command
	groupsCreateCmd.Flags().String("description", "", "Description of the group")

	// Add subcommands to parent add command
	setupGroupsAddUserCmd(groupsAddUserCmd)
	groupsAddCmd.AddCommand(groupsAddUserCmd)
	groupsAddCmd.AddCommand(groupsAddRangeCmd)

	// Add subcommands to parent remove command
	groupsRemoveCmd.AddCommand(groupsRemoveUserCmd)
	groupsRemoveCmd.AddCommand(groupsRemoveRangeCmd)

	// Add subcommands to groups command
	groupsCmd.AddCommand(groupsListCmd)
	groupsCmd.AddCommand(groupsCreateCmd)
	groupsCmd.AddCommand(groupsDeleteCmd)
	groupsCmd.AddCommand(groupsAddCmd)
	groupsCmd.AddCommand(groupsRemoveCmd)
	groupsCmd.AddCommand(groupsMembersCmd)
	groupsCmd.AddCommand(groupsRangesCmd)

	// Register the groups command with the root command
	rootCmd.AddCommand(groupsCmd)
}
