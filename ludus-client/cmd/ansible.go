package cmd

import (
	"encoding/json"
	"fmt"
	logger "ludus/logger"
	"ludus/rest"
	"os"
	"path/filepath"
	"strconv"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	roleDirectory  string
	ansibleForce   bool
	ansibleVersion string
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

func formatAnsibleResponse(ansibleItems []AnsibleItem, ansibleType string) {
	// Create table
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetHeader([]string{"Name", "Version"})

	for _, item := range ansibleItems {
		if item.Type == ansibleType {
			table.Append([]string{item.Name, item.Version})
		}
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

		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/ansible?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/ansible")
		}

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
			if use == "add" {
				action = "install"
			} else {
				action = "remove"
			}

			// Given a local dir, tar it up and ship it!
			if len(args) == 0 && roleDirectory != "" {
				roleTar, err := tarDirectoryInMemory(roleDirectory)
				if err != nil {
					logger.Logger.Fatalf("Could not tar directory: %s, error: %s\n", roleDirectory, err.Error())
				}
				filename := filepath.Base(roleDirectory)
				responseJSON, success = rest.PostFileAndForce(client, "/ansible/role/fromtar", roleTar.Bytes(), filename, ansibleForce)

				if didFailOrWantJSON(success, responseJSON) {
					return
				}
				handleGenericResult(responseJSON)

			} else if len(args) == 1 && roleDirectory == "" { // install from galaxy/URL
				requestBody := fmt.Sprintf(`{
				"role": "%s",
				"force": %s,
				"version": "%s",
				"action": "%s"
			  }`, args[0], strconv.FormatBool(ansibleForce), ansibleVersion, action)

				if userID != "" {
					responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/ansible/role?userID=%s", userID), requestBody)
				} else {
					responseJSON, success = rest.GenericJSONPost(client, "/ansible/role", requestBody)
				}

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

var roleAddCmd = genericRoleCmd("add", "Add an ansible role to the ludus host", "Specify a role name (to pull from galaxy.ansible.com), a URL, or a local path to a role directory", []string{})
var roleRmCmd = genericRoleCmd("rm", "Remove an ansible role from the ludus host", "Specify a role name to remove from the ludus host", []string{"remove", "del"})

func setupRoleCmd(command *cobra.Command) {
	command.Flags().StringVarP(&roleDirectory, "directory", "d", "", "the path to the local directory of the role to install")
	command.Flags().BoolVarP(&ansibleForce, "force", "f", false, "force the role to be added")
	command.Flags().StringVar(&ansibleVersion, "version", "", "the role version to install")
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

		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/ansible/collection?userID=%s", userID), requestBody)
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/ansible/collection", requestBody)
		}

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

		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/ansible?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/ansible")
		}

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

func init() {
	collectionCmd.AddCommand(collectionsListCmd)
	setupCollectionAddCmd(collectionAddCmd)
	collectionCmd.AddCommand(collectionAddCmd)
	roleCmd.AddCommand(rolesListCmd)
	setupRoleCmd(roleAddCmd)
	setupRoleCmd(roleRmCmd)
	roleCmd.AddCommand(roleAddCmd)
	roleCmd.AddCommand(roleRmCmd)
	ansibleCmd.AddCommand(roleCmd)
	ansibleCmd.AddCommand(collectionCmd)
	rootCmd.AddCommand(ansibleCmd)
}
