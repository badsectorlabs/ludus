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

var (
	blueprintID          string
	blueprintName        string
	blueprintDescription string
	blueprintFromRange   string
	blueprintFromBP      string
	blueprintTargetRange string
	blueprintForce       bool
	blueprintNoPrompt    bool
)

func printBlueprintBulkOperationResponse(responseJSON []byte, action string, itemType string) {
	var bulkResponse dto.BulkBlueprintOperationResponse
	if err := json.Unmarshal(responseJSON, &bulkResponse); err != nil {
		logger.Logger.Info("Operation completed successfully")
		return
	}

	errors := make([]bulkOperationError, 0, len(bulkResponse.Errors))
	for _, err := range bulkResponse.Errors {
		errors = append(errors, bulkOperationError{
			Item:   err.Item,
			Reason: err.Reason,
		})
	}

	printBulkOperationResult(action, itemType, bulkResponse.Success, errors)
}

var blueprintCmd = &cobra.Command{
	Use:     "blueprint",
	Short:   "Perform actions related to blueprints",
	Long:    ``,
	Aliases: []string{"blueprints"},
}

var blueprintListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all blueprints you can access",
	Aliases: []string{"status", "get", "ls"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/blueprints"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var blueprints []dto.ListBlueprintsResponseItem
		if err := json.Unmarshal(responseJSON, &blueprints); err != nil {
			logger.Logger.Fatal(err)
		}

		if len(blueprints) == 0 {
			logger.Logger.Info("No blueprints found")
			return
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Blueprint ID", "Name", "Owner", "Access", "Shared Users", "Shared Groups", "Updated"})

		for _, blueprint := range blueprints {
			table.Append([]string{
				blueprint.BlueprintID,
				blueprint.Name,
				blueprint.OwnerUserID,
				blueprint.AccessType,
				fmt.Sprintf("%d", len(removeEmptyStrings(blueprint.SharedUsers))),
				fmt.Sprintf("%d", len(removeEmptyStrings(blueprint.SharedGroups))),
				formatTimeObject(blueprint.Updated, "2006-01-02 15:04"),
			})
		}

		table.Render()
	},
}

var blueprintCreateCmd = &cobra.Command{
	Use:     "create",
	Short:   "Create a blueprint from a range or existing blueprint",
	Aliases: []string{"save"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		normalizedBlueprintID := strings.TrimSpace(blueprintID)
		fromBlueprintID := strings.TrimSpace(blueprintFromBP)
		fromRangeID := strings.TrimSpace(blueprintFromRange)

		if fromBlueprintID != "" && fromRangeID != "" {
			logger.Logger.Fatal("Specify either --from-blueprint or --from-range, not both.")
		}

		var responseJSON []byte
		var success bool

		if fromBlueprintID != "" {
			payload := dto.CopyBlueprintRequest{
				BlueprintID: normalizedBlueprintID,
				Name:        strings.TrimSpace(blueprintName),
				Description: strings.TrimSpace(blueprintDescription),
			}
			responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/copy", fromBlueprintID)), payload)
		} else {
			if normalizedBlueprintID == "" {
				logger.Logger.Fatal("Blueprint ID is required when creating from a range. Use --id to specify the blueprint ID.")
			}

			payload := dto.CreateBlueprintFromRangeRequest{
				BlueprintID: normalizedBlueprintID,
				Name:        strings.TrimSpace(blueprintName),
				Description: strings.TrimSpace(blueprintDescription),
			}
			if fromRangeID != "" {
				payload.RangeID = fromRangeID
			} else if rangeID != "" {
				payload.RangeID = rangeID
			}
			responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/blueprints/from-range"), payload)
		}

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		type BlueprintMutationResponse struct {
			Result      string `json:"result"`
			BlueprintID string `json:"blueprintID"`
		}
		var data BlueprintMutationResponse
		if err := json.Unmarshal(responseJSON, &data); err != nil {
			logger.Logger.Fatal(err)
		}
		if data.BlueprintID != "" {
			logger.Logger.Infof("%s (ID: %s)", data.Result, data.BlueprintID)
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupBlueprintCreateCmd(command *cobra.Command) {
	command.Flags().StringVar(&blueprintID, "id", "", "blueprint ID for the new blueprint (required for --from-range, optional for --from-blueprint)")
	command.Flags().StringVarP(&blueprintName, "name", "n", "", "name for the new blueprint (optional)")
	command.Flags().StringVarP(&blueprintDescription, "description", "d", "", "description for the new blueprint (optional)")
	command.Flags().StringVarP(&blueprintFromRange, "from-range", "s", "", "source rangeID to create the blueprint from (optional)")
	command.Flags().StringVarP(&blueprintFromBP, "from-blueprint", "b", "", "source blueprintID to copy from (optional)")
}

var blueprintApplyCmd = &cobra.Command{
	Use:   "apply [blueprintID]",
	Short: "Apply a blueprint configuration to a range",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		targetRange := blueprintTargetRange
		if targetRange == "" {
			targetRange = rangeID
		}

		payload := dto.ApplyBlueprintRequest{
			RangeID: targetRange,
			Force:   blueprintForce,
		}

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/apply", args[0])), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupBlueprintApplyCmd(command *cobra.Command) {
	command.Flags().StringVarP(&blueprintTargetRange, "target-range", "t", "", "target rangeID to apply the blueprint to (optional)")
	command.Flags().BoolVar(&blueprintForce, "force", false, "force apply even when testing is enabled on the target range")
}

var blueprintConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Get blueprint configuration content",
}

var blueprintConfigGetCmd = &cobra.Command{
	Use:   "get [blueprintID]",
	Short: "Get the raw configuration for a blueprint",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/config", args[0])))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		type Result struct {
			Result string `json:"result"`
		}
		var result Result
		if err := json.Unmarshal(responseJSON, &result); err != nil {
			logger.Logger.Fatal(err)
		}

		fmt.Print(result.Result)
	},
}

var blueprintAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Inspect blueprint access by users or groups",
}

var blueprintAccessUsersCmd = &cobra.Command{
	Use:     "users [blueprintID]",
	Aliases: []string{"user"},
	Short:   "List users with access to a blueprint",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/users", args[0])))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var users []dto.ListBlueprintAccessUsersResponseItem
		if err := json.Unmarshal(responseJSON, &users); err != nil {
			logger.Logger.Fatal(err)
		}

		if len(users) == 0 {
			logger.Logger.Info("No users currently have access to this blueprint")
			return
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"UserID", "Name", "Access", "Groups"})
		for _, user := range users {
			table.Append([]string{
				user.UserID,
				user.Name,
				joinOrDash(user.Access),
				joinOrDash(user.Groups),
			})
		}
		table.Render()
	},
}

var blueprintAccessGroupsCmd = &cobra.Command{
	Use:     "groups [blueprintID]",
	Aliases: []string{"group"},
	Short:   "List shared groups and their users for a blueprint",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/groups", args[0])))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var groups []dto.ListBlueprintAccessGroupsResponseItem
		if err := json.Unmarshal(responseJSON, &groups); err != nil {
			logger.Logger.Fatal(err)
		}

		if len(groups) == 0 {
			logger.Logger.Info("This blueprint is not shared with any groups")
			return
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Group", "Managers", "Members"})
		for _, group := range groups {
			table.Append([]string{
				group.GroupName,
				joinOrDash(group.Managers),
				joinOrDash(group.Members),
			})
		}
		table.Render()
	},
}

var blueprintShareCmd = &cobra.Command{
	Use:   "share",
	Short: "Share a blueprint with groups or users",
}

var blueprintUnshareCmd = &cobra.Command{
	Use:   "unshare",
	Short: "Remove blueprint sharing from groups or users",
}

var blueprintShareGroupsCmd = &cobra.Command{
	Use:     "group [blueprintID] [groupName(s)...]",
	Aliases: []string{"groups"},
	Short:   "Share a blueprint with one or more groups",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupNames := parseBulkIdentifiers(args[1:])
		payload := dto.BulkShareBlueprintWithGroupsRequest{
			GroupNames: groupNames,
		}

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/groups", args[0])), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBlueprintBulkOperationResponse(responseJSON, "shared with", "group(s)")
	},
}

var blueprintUnshareGroupsCmd = &cobra.Command{
	Use:     "group [blueprintID] [groupName(s)...]",
	Aliases: []string{"groups"},
	Short:   "Unshare a blueprint from one or more groups",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupNames := parseBulkIdentifiers(args[1:])
		payload := dto.BulkUnshareBlueprintWithGroupsRequest{
			GroupNames: groupNames,
		}

		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/groups", args[0])), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBlueprintBulkOperationResponse(responseJSON, "unshared from", "group(s)")
	},
}

var blueprintShareUsersCmd = &cobra.Command{
	Use:     "user [blueprintID] [userID(s)...]",
	Aliases: []string{"users"},
	Short:   "Share a blueprint with one or more users",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userIDs := parseBulkIdentifiers(args[1:])
		payload := dto.BulkShareBlueprintWithUsersRequest{
			UserIDs: userIDs,
		}

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/users", args[0])), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBlueprintBulkOperationResponse(responseJSON, "shared with", "user(s)")
	},
}

var blueprintUnshareUsersCmd = &cobra.Command{
	Use:     "user [blueprintID] [userID(s)...]",
	Aliases: []string{"users"},
	Short:   "Unshare a blueprint from one or more users",
	Args:    cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userIDs := parseBulkIdentifiers(args[1:])
		payload := dto.BulkUnshareBlueprintWithUsersRequest{
			UserIDs: userIDs,
		}

		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/users", args[0])), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		printBlueprintBulkOperationResponse(responseJSON, "unshared from", "user(s)")
	},
}

var blueprintDeleteCmd = &cobra.Command{
	Use:     "rm [blueprintID]",
	Short:   "Delete a blueprint you own",
	Aliases: []string{"delete", "remove"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		blueprintID := args[0]
		if !blueprintNoPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will delete blueprint: %s !!!
This action cannot be undone.

Do you want to continue? (y/N): `, blueprintID)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		responseJSON, success := rest.GenericDelete(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s", blueprintID)))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupBlueprintDeleteCmd(command *cobra.Command) {
	command.Flags().BoolVar(&blueprintNoPrompt, "no-prompt", false, "skip the confirmation prompt")
}

func init() {
	blueprintCmd.AddCommand(blueprintListCmd)

	setupBlueprintCreateCmd(blueprintCreateCmd)
	blueprintCmd.AddCommand(blueprintCreateCmd)

	setupBlueprintApplyCmd(blueprintApplyCmd)
	blueprintCmd.AddCommand(blueprintApplyCmd)

	blueprintConfigCmd.AddCommand(blueprintConfigGetCmd)
	blueprintCmd.AddCommand(blueprintConfigCmd)

	blueprintAccessCmd.AddCommand(blueprintAccessUsersCmd)
	blueprintAccessCmd.AddCommand(blueprintAccessGroupsCmd)
	blueprintCmd.AddCommand(blueprintAccessCmd)

	blueprintShareCmd.AddCommand(blueprintShareGroupsCmd)
	blueprintShareCmd.AddCommand(blueprintShareUsersCmd)
	blueprintCmd.AddCommand(blueprintShareCmd)

	blueprintUnshareCmd.AddCommand(blueprintUnshareGroupsCmd)
	blueprintUnshareCmd.AddCommand(blueprintUnshareUsersCmd)
	blueprintCmd.AddCommand(blueprintUnshareCmd)

	setupBlueprintDeleteCmd(blueprintDeleteCmd)
	blueprintCmd.AddCommand(blueprintDeleteCmd)

	rootCmd.AddCommand(blueprintCmd)
}
