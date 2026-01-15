package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"ludusapi/dto"
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var manager bool

// splitAndTrimIDs splits a comma-separated string of IDs and trims whitespace from each
func splitAndTrimIDs(idsArg string) []string {
	ids := strings.Split(idsArg, ",")
	for i, id := range ids {
		ids[i] = strings.TrimSpace(id)
	}
	return ids
}

// printBulkOperationResponse parses and prints the bulk operation response
func printBulkOperationResponse(responseJSON []byte, action string, itemType string) {
	var bulkResponse dto.BulkGroupOperationResponse
	if err := json.Unmarshal(responseJSON, &bulkResponse); err != nil {
		logger.Logger.Info("Operation completed successfully")
		return
	}

	if len(bulkResponse.Success) > 0 {
		logger.Logger.Info(fmt.Sprintf("Successfully %s %d %s: %v", action, len(bulkResponse.Success), itemType, bulkResponse.Success))
	}
	if len(bulkResponse.Errors) > 0 {
		logger.Logger.Warn(fmt.Sprintf("Failed to process %d %s:", len(bulkResponse.Errors), itemType))
		for _, err := range bulkResponse.Errors {
			logger.Logger.Warn(fmt.Sprintf("  %s: %s", err.Item, err.Reason))
		}
	}
}

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
	Use:     "user [userID(s)] [groupName]",
	Aliases: []string{"users"},
	Short:   "Add user(s) to a group",
	Long:    `Add one or more users to a group. For multiple users, provide comma-separated userIDs (e.g., "user1,user2,user3").`,
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userIDArg := args[0]
		groupName := args[1]

		userIDs := splitAndTrimIDs(userIDArg)

		var managersFlag []string
		if manager {
			managersFlag = userIDs
		}

		payload := dto.BulkAddUsersToGroupRequest{
			UserIDs:  userIDs,
			Managers: managersFlag,
		}

		groupUserURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/users", groupName))
		responseJSON, success := rest.GenericJSONPost(client, groupUserURL, payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBulkOperationResponse(responseJSON, "added", "user(s)")
	},
}

func setupGroupsAddUserCmd(command *cobra.Command) {
	command.Flags().BoolVarP(&manager, "manager", "m", false, "whether the user(s) should be manager(s) of the group")
}

var groupsAddRangeCmd = &cobra.Command{
	Use:     "range [rangeID(s)] [groupName]",
	Aliases: []string{"ranges"},
	Short:   "Grant group access to range(s)",
	Long:    `Grant a group access to one or more ranges. For multiple ranges, provide comma-separated rangeIDs (e.g., "range1,range2,range3").`,
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rangeIDArg := args[0]
		groupName := args[1]

		rangeIDs := splitAndTrimIDs(rangeIDArg)

		payload := dto.BulkAddRangesToGroupRequest{
			RangeIDs: rangeIDs,
		}

		groupRangeURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/ranges", groupName))
		responseJSON, success := rest.GenericJSONPost(client, groupRangeURL, payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBulkOperationResponse(responseJSON, "granted access to", "range(s)")
	},
}

// Parent remove command
var groupsRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove users or ranges from groups",
	Long:  `Remove users from groups or revoke group access to ranges.`,
}

var groupsRemoveUserCmd = &cobra.Command{
	Use:     "user [userID(s)] [groupName]",
	Aliases: []string{"users"},
	Short:   "Remove user(s) from a group",
	Long:    `Remove one or more users from a group. For multiple users, provide comma-separated userIDs (e.g., "user1,user2,user3").`,
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userIDArg := args[0]
		groupName := args[1]

		userIDs := splitAndTrimIDs(userIDArg)

		payload := dto.BulkRemoveUsersFromGroupRequest{
			UserIDs: userIDs,
		}

		groupUserURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/users", groupName))
		responseJSON, success := rest.GenericDeleteWithBody(client, groupUserURL, payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBulkOperationResponse(responseJSON, "removed", "user(s)")
	},
}

var groupsRemoveRangeCmd = &cobra.Command{
	Use:     "range [rangeID(s)] [groupName]",
	Aliases: []string{"ranges"},
	Short:   "Revoke group access from range(s)",
	Long:    `Revoke a group's access to one or more ranges. For multiple ranges, provide comma-separated rangeIDs (e.g., "range1,range2,range3").`,
	Args:    cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rangeIDArg := args[0]
		groupName := args[1]

		rangeIDs := splitAndTrimIDs(rangeIDArg)

		payload := dto.BulkRemoveRangesFromGroupRequest{
			RangeIDs: rangeIDs,
		}

		groupRangeURL := buildURLWithRangeAndUserID(fmt.Sprintf("/groups/%s/ranges", groupName))
		responseJSON, success := rest.GenericDeleteWithBody(client, groupRangeURL, payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBulkOperationResponse(responseJSON, "revoked access from", "range(s)")
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

		var data []dto.ListGroupMembersResponseItem
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err)
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Name", "Role"})

		// Add data to table
		for _, user := range data {
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
