package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"ludusapi/dto"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	resty "github.com/go-resty/resty/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// blueprintInstallPayload is the synchronous response shape from
// POST /blueprints/{id}/install (and the install portion of create/import).
// Same shape as syncResultPayload but with a blueprintID instead of sourceID.
type blueprintInstallPayload struct {
	BlueprintID      string                  `json:"blueprintID"`
	TemplateResults  []artifactResultPayload `json:"templateResults"`
	LocalRoleResults []artifactResultPayload `json:"localRoleResults"`
	RoleResults      []roleResultPayload     `json:"roleResults"`
}

// printBlueprintInstallFailures emits one log line per failed artifact for a
// blueprint install/create/import. label is what to print when there are no
// failures (e.g. "Blueprint 'goad'").
func printBlueprintInstallFailures(label string, p blueprintInstallPayload) {
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalRoleResults, p.RoleResults)
	printArtifactOutcome(label, "dependencies installed", "install completed with errors", failures)
}

var (
	blueprintID          string
	blueprintName        string
	blueprintDescription string
	blueprintFromRange  string
	blueprintFromBP     string
	blueprintFromImport string
	blueprintConfigFile string
	blueprintTargetRange string
	blueprintForce       bool
	blueprintNoPrompt    bool

	listFlagFilterTag string
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

		endpoint := buildURLWithRangeAndUserID("/blueprints")
		if listFlagFilterTag != "" {
			endpoint += "?tag=" + neturl.QueryEscape(listFlagFilterTag)
		}

		responseJSON, success := rest.GenericGet(client, endpoint)
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
		table.SetHeader([]string{"Blueprint ID", "Name", "Owner", "Access", "Shared Users", "Shared Groups", "Tags", "Updated"})

		for _, blueprint := range blueprints {
			table.Append([]string{
				blueprint.BlueprintID,
				blueprint.Name,
				blueprint.OwnerUserID,
				blueprint.AccessType,
				fmt.Sprintf("%d", len(removeEmptyStrings(blueprint.SharedUsers))),
				fmt.Sprintf("%d", len(removeEmptyStrings(blueprint.SharedGroups))),
				strings.Join(blueprint.Tags, ", "),
				formatTimeObject(blueprint.Updated, "2006-01-02 15:04"),
			})
		}

		table.Render()
	},
}

var blueprintCreateCmd = &cobra.Command{
	Use:     "create",
	Short:   "Create a new blueprint (empty, from a range, copied, or imported)",
	Aliases: []string{"save"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		normalizedBlueprintID := strings.TrimSpace(blueprintID)
		fromBlueprintID := strings.TrimSpace(blueprintFromBP)
		fromImport := strings.TrimSpace(blueprintFromImport)
		seedConfigPath := strings.TrimSpace(blueprintConfigFile)
		fromRangeSet := cmd.Flags().Changed("from-range")
		fromRangeID := strings.TrimSpace(blueprintFromRange)

		sources := 0
		if fromBlueprintID != "" {
			sources++
		}
		if fromRangeSet {
			sources++
		}
		if fromImport != "" {
			sources++
		}
		if sources > 1 {
			logger.Logger.Fatal("Specify only one of --from-blueprint, --from-range, --import.")
		}
		if seedConfigPath != "" && sources > 0 {
			logger.Logger.Fatal("--config can only be used when creating a new blueprint from scratch.")
		}

		if fromImport != "" {
			runBlueprintImport(client, fromImport)
			return
		}

		var responseJSON []byte
		var success bool
		fromScratch := !fromRangeSet && fromBlueprintID == ""

		if fromScratch {
			if normalizedBlueprintID == "" {
				logger.Logger.Fatal("Blueprint ID is required. Use --id to specify it.")
			}
			var configBytes []byte
			if seedConfigPath != "" {
				data, err := os.ReadFile(seedConfigPath)
				if err != nil {
					logger.Logger.Fatalf("read --config %s: %s", seedConfigPath, err)
				}
				configBytes = data
			}
			payload := dto.CreateBlueprintRequest{
				BlueprintID:     normalizedBlueprintID,
				Name:            strings.TrimSpace(blueprintName),
				Description:     strings.TrimSpace(blueprintDescription),
				Version:         updateFlagVersion,
				Tags:            updateFlagTags,
				MinLudusVersion: updateFlagMinLudusVer,
				Config:          string(configBytes),
			}
			responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/blueprints"), payload)
		} else if fromBlueprintID != "" {
			payload := dto.CopyBlueprintRequest{
				BlueprintID: normalizedBlueprintID,
				Name:        strings.TrimSpace(blueprintName),
				Description: strings.TrimSpace(blueprintDescription),
			}
			responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/copy", neturl.PathEscape(fromBlueprintID))), payload)
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

		createdID := data.BlueprintID
		if createdID == "" {
			// Fallback: use the requested ID (copy from blueprint may not echo it back).
			createdID = normalizedBlueprintID
		}

		// from-scratch carries metadata in the create call. Other modes don't, so
		// apply via PATCH if any metadata flags were given.
		metaBody := map[string]any{}
		if !fromScratch {
			if updateFlagVersion != "" {
				metaBody["version"] = updateFlagVersion
			}
			if len(updateFlagTags) > 0 {
				metaBody["tags"] = updateFlagTags
			}
			if updateFlagMinLudusVer != "" {
				metaBody["min_ludus_version"] = updateFlagMinLudusVer
			}
		}
		if len(metaBody) > 0 && createdID != "" {
			jsonBody, _ := json.Marshal(metaBody)
			path := fmt.Sprintf("/blueprints/%s", neturl.PathEscape(createdID))
			patchResp, patchOK := rest.GenericJSONPatch(client, buildURLWithRangeAndUserID(path), string(jsonBody))
			if !patchOK {
				logger.Logger.Warnf("Blueprint created but metadata update failed: %s", string(patchResp))
			}
		}

		if data.BlueprintID != "" {
			logger.Logger.Infof("%s (ID: %s)", data.Result, data.BlueprintID)
			// Same response body also carries install results; reparse and surface failures.
			var install blueprintInstallPayload
			if err := json.Unmarshal(responseJSON, &install); err == nil {
				install.BlueprintID = data.BlueprintID
				printBlueprintInstallFailures(fmt.Sprintf("Blueprint '%s'", data.BlueprintID), install)
			}
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupBlueprintCreateCmd(command *cobra.Command) {
	command.Flags().StringVar(&blueprintID, "id", "", "blueprint ID for the new blueprint")
	command.Flags().StringVarP(&blueprintName, "name", "n", "", "blueprint display name")
	command.Flags().StringVarP(&blueprintDescription, "description", "d", "", "blueprint description")
	command.Flags().StringVarP(&blueprintFromRange, "from-range", "s", "", "source rangeID to create from")
	command.Flags().StringVarP(&blueprintFromBP, "from-blueprint", "b", "", "source blueprintID to copy from")
	command.Flags().StringVar(&blueprintFromImport, "import", "", "path to an exported blueprint tarball")
	command.Flags().StringVar(&blueprintConfigFile, "config", "", "seed range-config.yml from this file (from-scratch only)")
	command.Flags().StringVar(&updateFlagVersion, "version", "", "semver version")
	command.Flags().StringSliceVar(&updateFlagTags, "tag", nil, "tag (repeatable, or comma-separated: --tag a,b,c)")
	command.Flags().StringVar(&updateFlagMinLudusVer, "min-ludus-version", "", "minimum Ludus version")
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

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/apply", neturl.PathEscape(args[0]))), payload)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupBlueprintApplyCmd(command *cobra.Command) {
	command.Flags().StringVarP(&blueprintTargetRange, "target-range", "t", "", "target rangeID")
	command.Flags().BoolVar(&blueprintForce, "force", false, "force apply even when testing is enabled on the target range")
}

var blueprintConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Get or set blueprint configuration content",
}

var blueprintConfigGetCmd = &cobra.Command{
	Use:   "get [blueprintID]",
	Short: "Get the raw configuration for a blueprint",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/config", neturl.PathEscape(args[0]))))
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

var blueprintConfigSetFile string

var blueprintConfigSetCmd = &cobra.Command{
	Use:   "set <blueprintID>",
	Short: "Replace a blueprint's range config from a file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		configBytes, err := os.ReadFile(blueprintConfigSetFile)
		if err != nil {
			logger.Logger.Fatalf("Could not read %s: %v", blueprintConfigSetFile, err)
		}
		body, _ := json.Marshal(map[string]string{"config": string(configBytes)})
		path := fmt.Sprintf("/blueprints/%s/config", neturl.PathEscape(args[0]))
		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID(path), string(body))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

var blueprintAccessCmd = &cobra.Command{
	Use:   "access [blueprintID]",
	Short: "Inspect blueprint access (users + groups)",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			_ = cmd.Help()
			return
		}
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		bpID := args[0]

		usersJSON, uOK := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/users", neturl.PathEscape(bpID))))
		if !uOK {
			return
		}
		groupsJSON, gOK := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/groups", neturl.PathEscape(bpID))))
		if !gOK {
			return
		}

		if jsonFormat {
			combined := map[string]json.RawMessage{
				"users":  json.RawMessage(usersJSON),
				"groups": json.RawMessage(groupsJSON),
			}
			out, _ := json.Marshal(combined)
			fmt.Println(string(out))
			return
		}

		var users []dto.ListBlueprintAccessUsersResponseItem
		_ = json.Unmarshal(usersJSON, &users)
		var groups []dto.ListBlueprintAccessGroupsResponseItem
		_ = json.Unmarshal(groupsJSON, &groups)

		fmt.Println("Users:")
		if len(users) == 0 {
			fmt.Println("  (none)")
		} else {
			t := tablewriter.NewWriter(os.Stdout)
			t.SetHeader([]string{"UserID", "Name", "Access", "Groups"})
			for _, u := range users {
				t.Append([]string{u.UserID, u.Name, joinOrDash(u.Access), joinOrDash(u.Groups)})
			}
			t.Render()
		}

		fmt.Println()
		fmt.Println("Groups:")
		if len(groups) == 0 {
			fmt.Println("  (none)")
		} else {
			t := tablewriter.NewWriter(os.Stdout)
			t.SetHeader([]string{"Group", "Managers", "Members"})
			for _, g := range groups {
				t.Append([]string{g.GroupName, joinOrDash(g.Managers), joinOrDash(g.Members)})
			}
			t.Render()
		}
	},
}

var blueprintAccessUsersCmd = &cobra.Command{
	Use:     "users [blueprintID]",
	Aliases: []string{"user"},
	Short:   "List users with access to a blueprint",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/users", neturl.PathEscape(args[0]))))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var users []dto.ListBlueprintAccessUsersResponseItem
		if err := json.Unmarshal(responseJSON, &users); err != nil {
			logger.Logger.Fatal(err)
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

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/access/groups", neturl.PathEscape(args[0]))))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var groups []dto.ListBlueprintAccessGroupsResponseItem
		if err := json.Unmarshal(responseJSON, &groups); err != nil {
			logger.Logger.Fatal(err)
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

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/groups", neturl.PathEscape(args[0]))), payload)
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

		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/groups", neturl.PathEscape(args[0]))), payload)
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

		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/users", neturl.PathEscape(args[0]))), payload)
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

		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s/share/users", neturl.PathEscape(args[0]))), payload)
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

		responseJSON, success := rest.GenericDelete(client, buildURLWithRangeAndUserID(fmt.Sprintf("/blueprints/%s", neturl.PathEscape(blueprintID))))
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
	blueprintListCmd.Flags().StringVar(&listFlagFilterTag, "tag", "", "filter blueprints by tag")
	blueprintCmd.AddCommand(blueprintListCmd)

	setupBlueprintCreateCmd(blueprintCreateCmd)
	blueprintCmd.AddCommand(blueprintCreateCmd)

	setupBlueprintApplyCmd(blueprintApplyCmd)
	blueprintCmd.AddCommand(blueprintApplyCmd)

	blueprintConfigCmd.AddCommand(blueprintConfigGetCmd)
	blueprintConfigSetCmd.Flags().StringVarP(&blueprintConfigSetFile, "file", "f", "", "path to the YAML config file to upload")
	_ = blueprintConfigSetCmd.MarkFlagRequired("file")
	blueprintConfigCmd.AddCommand(blueprintConfigSetCmd)
	blueprintConfigEditCmd.Flags().StringVarP(&blueprintEditEditor, "editor", "e", "", "external editor to use (e.g., vim, nano, code); overrides $LUDUS_EDITOR")
	blueprintConfigCmd.AddCommand(blueprintConfigEditCmd)
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

	blueprintCmd.AddCommand(blueprintInfoCmd)

	blueprintInstallCmd.Flags().BoolVar(&installFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide")
	blueprintInstallCmd.Flags().BoolVar(&installFlagForceRoles, "force-roles", false, "overwrite already-installed roles")
	blueprintCmd.AddCommand(blueprintInstallCmd)

	blueprintUpdateCmd.Flags().StringVar(&blueprintName, "name", "", "blueprint display name")
	blueprintUpdateCmd.Flags().StringVar(&blueprintDescription, "description", "", "blueprint description")
	blueprintUpdateCmd.Flags().StringVar(&updateFlagVersion, "version", "", "semver version")
	blueprintUpdateCmd.Flags().StringSliceVar(&updateFlagTags, "tag", nil, "tag (repeatable, or comma-separated: --tag a,b,c)")
	blueprintUpdateCmd.Flags().BoolVar(&updateFlagClearTags, "clear-tags", false, "remove all tags")
	blueprintUpdateCmd.Flags().StringVar(&updateFlagMinLudusVer, "min-ludus-version", "", "minimum Ludus version")
	blueprintCmd.AddCommand(blueprintUpdateCmd)

	blueprintExportCmd.Flags().StringVarP(&blueprintExportOut, "output", "o", "", "Output path (default <id>.tar.gz)")
	blueprintCmd.AddCommand(blueprintExportCmd)

	blueprintCmd.AddCommand(blueprintImportCmd)

	blueprintEditCmd.Flags().StringVarP(&blueprintEditEditor, "editor", "e", "", "external editor to use (e.g., vim, nano, code); overrides $LUDUS_EDITOR")
	blueprintCmd.AddCommand(blueprintEditCmd)

	rootCmd.AddCommand(blueprintCmd)
}


var blueprintInfoCmd = &cobra.Command{
	Use:     "info <id>",
	Short:   "Show blueprint metadata and dependency status",
	Aliases: []string{"describe"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		path := fmt.Sprintf("/blueprints/%s", neturl.PathEscape(args[0]))
		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID(path))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var detail map[string]any
		if err := json.Unmarshal(responseJSON, &detail); err != nil {
			logger.Logger.Fatal(err)
		}
		printBlueprintInfo(detail)
	},
}

func printBlueprintInfo(d map[string]any) {
	fmt.Printf("ID:          %v\n", d["id"])
	fmt.Printf("Name:        %v\n", d["name"])
	fmt.Printf("Version:     %v\n", d["version"])
	if v, ok := d["description"]; ok && v != nil && v != "" {
		fmt.Printf("Description: %v\n", v)
	}
	if authors, ok := d["authors"].([]any); ok && len(authors) > 0 {
		fmt.Printf("Authors:     %v\n", authors)
	}
	if v, ok := d["homepage"]; ok && v != nil && v != "" {
		fmt.Printf("Homepage:    %v\n", v)
	}
	if v, ok := d["license"]; ok && v != nil && v != "" {
		fmt.Printf("License:     %v\n", v)
	}
	if tags, ok := d["tags"].([]any); ok && len(tags) > 0 {
		fmt.Printf("Tags:        %v\n", tags)
	}
	if v, ok := d["min_ludus_version"]; ok && v != nil && v != "" {
		fmt.Printf("Min Ludus:   %v\n", v)
	}

	if templates, ok := d["templateStatus"].([]any); ok && len(templates) > 0 {
		fmt.Println()
		fmt.Println("Templates:")
		t := tablewriter.NewWriter(os.Stdout)
		t.SetHeader([]string{"Name", "Built"})
		for _, raw := range templates {
			m := raw.(map[string]any)
			built := "no"
			if b, ok := m["built"].(bool); ok && b {
				built = "yes"
			}
			t.Append([]string{fmt.Sprintf("%v", m["name"]), built})
		}
		t.Render()
	}

	if roles, ok := d["roleStatus"].([]any); ok && len(roles) > 0 {
		fmt.Println()
		fmt.Println("Roles:")
		t := tablewriter.NewWriter(os.Stdout)
		t.SetHeader([]string{"Name", "Installed", "Subscription"})
		for _, raw := range roles {
			m := raw.(map[string]any)
			installed := "no"
			if b, ok := m["installed"].(bool); ok && b {
				installed = "yes"
			}
			sub := "no"
			if b, ok := m["subscription"].(bool); ok && b {
				sub = "yes"
			}
			t.Append([]string{fmt.Sprintf("%v", m["name"]), installed, sub})
		}
		t.Render()
	}
}


var (
	installFlagGlobalRoles bool
	installFlagForceRoles  bool
)

var blueprintInstallCmd = &cobra.Command{
	Use:   "install <id>",
	Short: "Install galaxy/git role dependencies for a blueprint",
	Long: `Install the galaxy/git role dependencies a blueprint declares.

Idempotent — re-running on a fully-installed blueprint is fast.

Works on local blueprints (e.g. 'my-lab') OR slug-prefixed source-blueprints (e.g. 'bsl/goad').`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		body, _ := json.Marshal(dto.InstallBlueprintDepsRequest{
			GlobalRoles: installFlagGlobalRoles,
			ForceRoles:  installFlagForceRoles,
		})
		path := fmt.Sprintf("/blueprints/%s/install", neturl.PathEscape(args[0]))
		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(path), string(body))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var resp blueprintInstallPayload
		if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.BlueprintID == "" {
			logger.Logger.Info(string(responseJSON))
			return
		}
		printBlueprintInstallFailures(fmt.Sprintf("Blueprint '%s'", resp.BlueprintID), resp)
	},
}


var (
	updateFlagVersion     string
	updateFlagTags        []string
	updateFlagClearTags   bool
	updateFlagMinLudusVer string
)

var blueprintUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "Update fields on a local blueprint",
	Long: `Update fields on a local blueprint. Pass an empty string to clear a
field. For interactive editing of the YAML config, use 'ludus blueprint config edit'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		body := map[string]any{}
		if cmd.Flags().Changed("name") {
			body["name"] = blueprintName
		}
		if cmd.Flags().Changed("description") {
			body["description"] = blueprintDescription
		}
		if cmd.Flags().Changed("version") {
			body["version"] = updateFlagVersion
		}
		if cmd.Flags().Changed("tag") {
			body["tags"] = updateFlagTags
		}
		if updateFlagClearTags {
			body["tags"] = []string{}
		}
		if cmd.Flags().Changed("min-ludus-version") {
			body["min_ludus_version"] = updateFlagMinLudusVer
		}
		if len(body) == 0 {
			logger.Logger.Fatal("at least one field flag is required")
		}

		recordID, err := rest.PBLookupRecordID(client, "blueprints", "blueprintID", args[0])
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}
		jsonBody, _ := json.Marshal(body)
		path := fmt.Sprintf("/api/collections/blueprints/records/%s", neturl.PathEscape(recordID))
		responseJSON, success := rest.GenericJSONPatch(client, path, string(jsonBody))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Infof("Blueprint '%s' updated", args[0])
	},
}


var blueprintExportOut string

var blueprintExportCmd = &cobra.Command{
	Use:   "export <id>",
	Short: "Export a blueprint bundle to a gzipped tarball",
	Long: `Export a self-contained blueprint bundle as a gzipped tarball.

The bundle includes the range config, pinned requirements.yml, copies of every
local role, copies of every template's HCL build dir, and any subscription
references. Subscription role bytes are NOT included.

The exported tarball can be imported on another Ludus instance via
'ludus blueprint import'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		out := blueprintExportOut
		if out == "" {
			out = args[0] + ".tar.gz"
		}
		path := fmt.Sprintf("/blueprints/%s/export", neturl.PathEscape(args[0]))
		_, success := rest.FileGet(client, path, out)
		if !success {
			return
		}
		if jsonFormat {
			payload, _ := json.Marshal(map[string]string{
				"blueprintID": args[0],
				"path":        out,
				"result":      "exported",
			})
			fmt.Println(string(payload))
			return
		}
		logger.Logger.Infof("wrote %s", out)
	},
}

// blueprintImportCmd is hidden — preferred surface is `blueprint create --import <path>`.
// Kept as a thin alias so existing scripts don't break in the rename window.
var blueprintImportCmd = &cobra.Command{
	Use:    "import <tar-path>",
	Short:  "Deprecated alias for `blueprint create --import <tar-path>`",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		runBlueprintImport(client, args[0])
	},
}

// runBlueprintImport reads tarPath and POSTs it to /blueprints/import. Shared
// by `blueprint create --import` and the hidden `blueprint import` alias.
func runBlueprintImport(client *resty.Client, tarPath string) {
	data, err := os.ReadFile(tarPath)
	if err != nil {
		logger.Logger.Fatalf("read tarball: %v", err)
		return
	}
	responseJSON, success := rest.FileUpload(
		client,
		"POST",
		"/blueprints/import",
		"archive",
		filepath.Base(tarPath),
		data,
		nil,
	)
	if didFailOrWantJSON(success, responseJSON) {
		return
	}
	var resp struct {
		ID          string `json:"id"`
		BlueprintID string `json:"blueprintID"`
	}
	_ = json.Unmarshal(responseJSON, &resp)
	logger.Logger.Infof("imported as blueprint id: %s", resp.BlueprintID)
	if resp.BlueprintID != "" {
		var install blueprintInstallPayload
		if err := json.Unmarshal(responseJSON, &install); err == nil {
			install.BlueprintID = resp.BlueprintID
			printBlueprintInstallFailures(fmt.Sprintf("Blueprint '%s'", resp.BlueprintID), install)
		}
	}
}


var blueprintEditEditor string

// blueprintEditCmd is hidden — preferred surface is `blueprint config edit`.
// Kept as a thin alias so existing scripts and muscle memory don't break in
// the rename window.
var blueprintEditCmd = &cobra.Command{
	Use:    "edit <id>",
	Short:  "Deprecated alias for `blueprint config edit <id>`",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runBlueprintConfigEdit(args[0])
	},
}

var blueprintConfigEditCmd = &cobra.Command{
	Use:   "edit <blueprintID>",
	Short: "Edit a blueprint's range config in an editor",
	Long: `Edit a blueprint's range config either in a built-in TUI editor or an
external editor specified by --editor. Mirrors 'ludus range config edit'.

Editor selection: --editor, then $LUDUS_EDITOR, then the built-in TUI editor.

For metadata edits (name, description, tags, etc.), use 'ludus blueprint update'.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		runBlueprintConfigEdit(args[0])
	},
}

func runBlueprintConfigEdit(bpID string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

	// 1. GET current config: endpoint returns {"result": "<yaml-string>"}.
	configPath := fmt.Sprintf("/blueprints/%s/config", neturl.PathEscape(bpID))
	respJSON, ok := rest.GenericGet(client, buildURLWithRangeAndUserID(configPath))
	if !ok {
		return
	}
	var wrapper struct {
		Result string `json:"result"`
	}
	if err := json.Unmarshal(respJSON, &wrapper); err != nil {
		logger.Logger.Fatalf("parse config response: %v", err)
	}
	oldContent := wrapper.Result

	// 2. Edit. Editor selection matches range config edit.
	editorCmd := blueprintEditEditor
	if editorCmd == "" {
		editorCmd = os.Getenv("LUDUS_EDITOR")
	}

	tmp, err := os.CreateTemp("", "ludus-bp-config-*.yml")
	if err != nil {
		logger.Logger.Fatal(err)
	}
	tmpName := tmp.Name()
	_, _ = tmp.WriteString(oldContent)
	_ = tmp.Close()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()

	var newContent []byte
	if editorCmd != "" {
		newContent, err = editWithExternalEditor([]byte(oldContent), editorCmd, tmpName)
		if err != nil {
			logger.Logger.Fatal(err)
		}
	} else {
		a := createBuiltinEditor(oldContent)
		if err := a.Run(); err != nil {
			logger.Logger.Fatal(err)
		}
		newContent = []byte(textArea.GetText())
		if err := os.WriteFile(tmpName, newContent, 0644); err != nil {
			logger.Logger.Fatal(err)
		}
	}

	// 3. PUT updated config.
	body, _ := json.Marshal(map[string]string{"config": string(newContent)})
	responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID(configPath), string(body))
	if didFailOrWantJSON(success, responseJSON) {
		if !success && !jsonFormat {
			removeTemp = false
			logger.Logger.Errorf("Load your edits with: ludus blueprint config set %s --file %s", bpID, tmpName)
		}
		return
	}
	handleGenericResult(responseJSON)
}

