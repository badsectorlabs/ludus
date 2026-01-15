package cmd

import (
	"encoding/json"
	"fmt"
	logger "ludus/logger"
	"ludus/rest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ludusapi/dto"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	roleDirectory          string
	ansibleForce           bool
	ansibleVersion         string
	ansibleGlobal          bool
	subscriptionRoleNames  string
	subscriptionRoleForce  bool
	subscriptionRoleGlobal bool
)

var ansibleCmd = &cobra.Command{
	Use:   "ansible",
	Short: "Perform actions related to ansible roles and collections",
	Long:  ``,
}

var roleCmd = &cobra.Command{
	Use:     "role",
	Short:   "Perform actions related to ansible roles",
	Long:    ``,
	Aliases: []string{"roles"},
}

var collectionCmd = &cobra.Command{
	Use:     "collection",
	Short:   "Perform actions related to ansible collections",
	Long:    ``,
	Aliases: []string{"collections"},
}

var subscriptionRolesCmd = &cobra.Command{
	Use:     "subscription-roles",
	Short:   "Perform actions related to subscription Ansible roles",
	Long:    ``,
	Aliases: []string{"subscription-role", "sub-roles", "sub-role"},
}

func formatAnsibleResponse(ansibleItems []AnsibleItem, ansibleType string) {
	// Create table
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetHeader([]string{"Name", "Version", "Global"})

	for _, item := range ansibleItems {
		if item.Type == ansibleType {
			table.Append([]string{item.Name, item.Version, strconv.FormatBool(item.Global)})
		}
	}

	table.Render()
}

func formatSubscriptionRolesResponse(subscriptionRoles []dto.GetSubscriptionRolesResponseItem) {
	// Sort by last modified time (most recent first)
	sort.Slice(subscriptionRoles, func(i, j int) bool {
		timeI, errI := strconv.ParseInt(subscriptionRoles[i].LastModifiedUnix, 10, 64)
		timeJ, errJ := strconv.ParseInt(subscriptionRoles[j].LastModifiedUnix, 10, 64)
		if errI != nil || errJ != nil {
			return false
		}
		return timeI > timeJ // Descending order (most recent first)
	})

	// Create table
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetHeader([]string{"Role", "Version", "Last Modified", "Description", "License"})
	table.SetColWidth(60)       // Set wider column width to allow description to be more readable
	table.SetAutoWrapText(true) // Enable text wrapping for longer descriptions

	for _, item := range subscriptionRoles {
		lastModifiedUnix, err := strconv.ParseInt(item.LastModifiedUnix, 10, 64)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		license := "Unknown"
		if strings.Contains(item.Entitlements, "ENTERPRISE_ROLES") {
			license = "Enterprise"
		} else if strings.Contains(item.Entitlements, "PRO_ROLES") {
			license = "Pro"
		}

		table.Append([]string{
			item.Role,
			item.Version,
			formatTimeObject(time.Unix(lastModifiedUnix, 0), "2006-01-02 15:04"),
			item.Description,
			license,
		})
	}

	table.Render()
}

var rolesListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List available Ansible roles on the Ludus host",
	Long:    `Get the name and version of available ansible roles on the Ludus host`,
	Args:    cobra.NoArgs,
	Aliases: []string{"status", "get"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/ansible"))

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal JSON data
		var ansibleItems []AnsibleItem
		err := json.Unmarshal([]byte(responseJSON), &ansibleItems)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}
		formatAnsibleResponse(ansibleItems, "role")
	},
}

func genericRoleCmd(use, short, long string, aliases []string) *cobra.Command {
	return &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    long,
		Args:    cobra.RangeArgs(0, 1),
		Aliases: aliases,
		Run: func(cmd *cobra.Command, args []string) {
			var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

			var responseJSON []byte
			var success bool

			if len(args) == 0 && roleDirectory == "" {
				logger.Logger.Fatalf("No role name or role directory specified")
			}

			var action string
			if use == "rm <rolename>" {
				action = "remove"
			} else {
				action = "install"
			}

			// Given a local dir, tar it up and ship it!
			if len(args) == 0 && roleDirectory != "" && action == "install" {
				roleTar, err := tarDirectoryInMemory(roleDirectory)
				if err != nil {
					logger.Logger.Fatalf("Could not tar directory: %s, error: %s\n", roleDirectory, err.Error())
				}
				filename := filepath.Base(roleDirectory)
				responseJSON, success = rest.PostFileAndForceAndGlobal(client, buildURLWithRangeAndUserID("/ansible/role/fromtar"), roleTar.Bytes(), filename, ansibleForce, ansibleGlobal)

				if didFailOrWantJSON(success, responseJSON) {
					return
				}
				handleGenericResult(responseJSON)

			} else if len(args) == 1 && roleDirectory == "" { // install from galaxy/URL
				requestBody := fmt.Sprintf(`{
				"role": "%s",
				"force": %s,
				"version": "%s",
				"action": "%s",
				"global": %s
			  }`, args[0], strconv.FormatBool(ansibleForce), ansibleVersion, action, strconv.FormatBool(ansibleGlobal))

				responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/ansible/role"), requestBody)
				if didFailOrWantJSON(success, responseJSON) {
					return
				}
				handleGenericResult(responseJSON)
			} else {
				logger.Logger.Fatalf("You cannot specify a role name and a directory at the same time")
			}
		},
	}
}

var roleAddCmd = genericRoleCmd("add <rolename | roleurl | -d directory>", "Add an ansible role to the ludus host", "Specify a role name (to pull from galaxy.ansible.com), a URL, or a local path to a role directory", []string{})
var roleRmCmd = genericRoleCmd("rm <rolename>", "Remove an ansible role from the ludus host", "Specify a role name to remove from the ludus host", []string{"remove", "del"})

func setupRoleCmd(command *cobra.Command) {
	command.Flags().StringVarP(&roleDirectory, "directory", "d", "", "the path to the local directory of the role to install")
	command.Flags().BoolVarP(&ansibleForce, "force", "f", false, "force the role to be added")
	command.Flags().StringVar(&ansibleVersion, "version", "", "the role version to install")
	command.Flags().BoolVarP(&ansibleGlobal, "global", "g", false, "install the role for all users")
}

var collectionAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add an ansible collection to the ludus host",
	Long:  `Specify a collection name (to pull from galaxy.ansible.com), or a URL to a tar.gz collection artifact`,
	Args:  cobra.RangeArgs(0, 1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		if len(args) == 0 {
			logger.Logger.Fatalf("No collection name specified")
		}

		requestBody := fmt.Sprintf(`{
				"collection": "%s",
				"force": %s,
				"version": "%s"
			  }`, args[0], strconv.FormatBool(ansibleForce), ansibleVersion)

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/ansible/collection"), requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupCollectionAddCmd(command *cobra.Command) {
	command.Flags().BoolVarP(&ansibleForce, "force", "f", false, "force the collection to be added")
	command.Flags().StringVar(&ansibleVersion, "version", "", "the collection version to install")
}

var collectionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available user Ansible collections on the Ludus host",
	Long:  `Get the name and version of available ansible collections on the Ludus host installed by the user (default Ansible collections are not shown)`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/ansible"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		// Unmarshal JSON data
		var ansibleItems []AnsibleItem
		err := json.Unmarshal([]byte(responseJSON), &ansibleItems)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}
		formatAnsibleResponse(ansibleItems, "collection")
	},
}

var subscriptionRolesListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List available subscription Ansible roles",
	Long:    `Get the list of available subscription Ansible roles from the Ludus subscription service`,
	Args:    cobra.NoArgs,
	Aliases: []string{"status", "get"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/ansible/subscription-roles"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal JSON data
		var subscriptionRoles []dto.GetSubscriptionRolesResponseItem
		err := json.Unmarshal([]byte(responseJSON), &subscriptionRoles)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}
		formatSubscriptionRolesResponse(subscriptionRoles)
	},
}

var subscriptionRolesInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install subscription Ansible roles",
	Long:  `Install one or more subscription Ansible roles using a comma-separated list of role names`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if subscriptionRoleNames == "" {
			logger.Logger.Fatalf("No role names specified. Use -n to specify comma-separated role names")
		}

		// Parse comma-separated role names
		roleNames := strings.Split(subscriptionRoleNames, ",")
		// Trim whitespace from each role name
		for i, role := range roleNames {
			roleNames[i] = strings.TrimSpace(role)
		}

		// Create request body using dto
		requestBody := dto.InstallSubscriptionRolesRequest{
			Roles:  roleNames,
			Global: subscriptionRoleGlobal,
			Force:  subscriptionRoleForce,
		}

		// Marshal to JSON
		requestJSON, err := json.Marshal(requestBody)
		if err != nil {
			logger.Logger.Fatalf("Failed to marshal request: %s", err.Error())
		}

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/ansible/subscription-roles"), string(requestJSON))

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		// Unmarshal response
		var response dto.InstallSubscriptionRolesResponse
		err = json.Unmarshal(responseJSON, &response)
		if err != nil {
			logger.Logger.Fatalf("Failed to parse response: %s", err.Error())
		}

		// Display results
		if len(response.Success) > 0 {
			logger.Logger.Infof("Successfully installed roles: %s", strings.Join(response.Success, ", "))
		}

		if len(response.Errors) > 0 {
			logger.Logger.Warnf("Failed to install %d role(s):", len(response.Errors))
			for _, errItem := range response.Errors {
				logger.Logger.Warnf("  - %s: %s", errItem.Role, errItem.Reason)
			}
		}

		if len(response.Success) == 0 && len(response.Errors) == 0 {
			logger.Logger.Info("No roles were processed")
		}
	},
}

func setupSubscriptionRolesInstallCmd(command *cobra.Command) {
	command.Flags().StringVarP(&subscriptionRoleNames, "names", "n", "", "comma-separated list of subscription role names to install (required)")
	command.MarkFlagRequired("names")
	command.Flags().BoolVarP(&subscriptionRoleGlobal, "global", "g", false, "install the roles globally for all users")
	command.Flags().BoolVarP(&subscriptionRoleForce, "force", "f", false, "force installation even if role already exists")
}

func init() {
	collectionCmd.AddCommand(collectionsListCmd)
	setupCollectionAddCmd(collectionAddCmd)
	collectionCmd.AddCommand(collectionAddCmd)
	roleCmd.AddCommand(rolesListCmd)
	setupRoleCmd(roleAddCmd)
	setupRoleCmd(roleRmCmd)
	roleCmd.AddCommand(roleAddCmd)
	roleCmd.AddCommand(roleRmCmd)
	subscriptionRolesCmd.AddCommand(subscriptionRolesListCmd)
	setupSubscriptionRolesInstallCmd(subscriptionRolesInstallCmd)
	subscriptionRolesCmd.AddCommand(subscriptionRolesInstallCmd)
	ansibleCmd.AddCommand(roleCmd)
	ansibleCmd.AddCommand(collectionCmd)
	ansibleCmd.AddCommand(subscriptionRolesCmd)
	rootCmd.AddCommand(ansibleCmd)
}
