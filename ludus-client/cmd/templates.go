package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"ludusapi/dto"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	follow              bool
	tail                int
	templateName        string
	templateNames       []string
	templateParallel    int
	templateDirectory   string
	verboseTemplateLogs bool
)

var templatesCmd = &cobra.Command{
	Use:     "templates",
	Short:   "List, build, add, or get the status of templates",
	Long:    ``,
	Aliases: []string{"template"},
}

var templatesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all templates",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/templates"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var templateStatusArray []dto.GetTemplatesResponseItem
		err := json.Unmarshal(responseJSON, &templateStatusArray)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Template", "OS", "Status"})

		for _, template := range templateStatusArray {
			var statusString string
			if template.Status == "building" {
				statusString = "🚧 BUILDING"
			} else {
				if template.Built {
					statusString = "✅ BUILT"
				} else {
					statusString = "❌ NOT BUILT"
				}
			}
			table.Append([]string{template.Name, template.OS, statusString})
		}

		// Print table
		table.Render()

	},
}

var templatesBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build templates",
	Long:  "Build a template or all un-built templates",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		// If no name is provided, build all templates
		if len(templateNames) == 0 {
			templateNames = []string{"all"}
		}

		requestBody := dto.BuildTemplatesRequest{
			Templates: templateNames,
			Parallel:  templateParallel,
		}

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/templates"), requestBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		handleGenericResult(responseJSON)

	},
}

func setupTemplatesBuildCmd(command *cobra.Command) {
	command.Flags().StringSliceVarP(&templateNames, "names", "n", []string{}, "template names to build separated by commas (use 'all' to build all templates)")
	command.Flags().IntVarP(&templateParallel, "parallel", "p", 1, "build templates in parallel (speeds things up). Specify what number of templates to build at a time")
}

var templatesStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Get templates being built",
	Long:  "Show the templates currently being built by packer",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/templates/status"))

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var templatesInProgress []dto.GetTemplatesStatusResponseItem

		err := json.Unmarshal(responseJSON, &templatesInProgress)
		if err != nil {
			logger.Logger.Fatal(err)
		}
		if len(templatesInProgress) == 0 {
			logger.Logger.Info("No template builds in progress")
		} else {
			// Create table
			table := tablewriter.NewWriter(os.Stdout)
			table.SetAlignment(tablewriter.ALIGN_CENTER)
			table.SetHeader([]string{"Template Being Built", "User"})

			for _, item := range templatesInProgress {
				table.Append([]string{item.Template, item.User})

			}

			table.Render()
		}

	},
}

var templateLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Get the latest packer logs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, false, verbose, LudusVersion)

		var apiString string
		apiString = buildURLWithRangeAndUserID("/templates/logs")
		if follow {
			var newLogs string
			var cursor int = 0

			for {
				apiStringWithCursor := addQueryParameterToURL(apiString, "cursor", strconv.Itoa(cursor))
				responseJSON, success := rest.GenericGet(client, apiStringWithCursor)
				if !success {
					return
				}
				newLogs, cursor = stringAndCursorFromResult(responseJSON)
				if len(newLogs) > 0 {
					filterAndPrintTemplateLogs(newLogs, verboseTemplateLogs)
				}
				time.Sleep(2 * time.Second)
			}
		} else {
			if tail > 0 {
				apiString = addQueryParameterToURL(apiString, "tail", strconv.Itoa(tail))
			}
			responseJSON, success := rest.GenericGet(client, apiString)
			if didFailOrWantJSON(success, responseJSON) {
				return
			}
			newLogs, _ := stringAndCursorFromResult(responseJSON)
			filterAndPrintTemplateLogs(newLogs, verboseTemplateLogs)
		}

	},
}

func setupTemplateLogsCmd(command *cobra.Command) {
	command.Flags().BoolVarP(&follow, "follow", "f", false, "continuously poll the log and print new lines as they are written")
	command.Flags().IntVarP(&tail, "tail", "t", 0, "number of lines of the log from the end to print")
	command.Flags().BoolVarP(&verboseTemplateLogs, "verbose-packer", "v", false, "print all lines from the packer log")
}

var templateAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a template directory to ludus",
	Long:  "Add a specified directory to ludus as a template. Windows templates should include an Autounattend.xml file in the root of their directory",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		var templateDirectoryPath string

		userProvidedTemplates, err := findFiles(templateDirectory, ".pkr.hcl", ".pkr.json")
		if err != nil {
			logger.Logger.Fatalf("Error finding .pkr.hcl or .pkr.json template files: %v", err)
		}
		if len(userProvidedTemplates) > 1 {
			logger.Logger.Fatal("Found more than one .hcl or .json template file in the provided directory. Only add one template directory at a time.")
		} else if len(userProvidedTemplates) == 0 {
			logger.Logger.Fatal("Could not find any .hcl or .json files in the provided directory")
		} else {
			templateDirectoryPath = userProvidedTemplates[0]
		}

		// findFiles returns the path to the actual .hcl or .json, but we want to tar the parent directory and all its files
		templateDirectoryPath = filepath.Dir(templateDirectoryPath)

		roleTar, err := tarDirectoryInMemory(templateDirectoryPath)
		if err != nil {
			logger.Logger.Fatalf("Could not tar directory: %s, error: %s\n", templateDirectory, err.Error())
		}
		filename := filepath.Base(templateDirectory)
		responseJSON, success = rest.PostFileAndForce(client, buildURLWithRangeAndUserID("/templates"), roleTar.Bytes(), filename, force)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)

	},
}

func setupTemplateAddCmd(command *cobra.Command) {
	command.Flags().StringVarP(&templateDirectory, "directory", "d", "", "the path to the local directory of the template to add to ludus")
	command.Flags().BoolVarP(&force, "force", "f", false, "remove the template directory if it exists on ludus before adding")
}

var templatesAbortCmd = &cobra.Command{
	Use:   "abort",
	Short: "Kill any running packer processes for the given user (default: calling user)",
	Long:  "Finds any running packer processes with the given user's username and kills them. It uses a SIGINT signal, which should cause packer to clean up the running VMs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/templates/abort"), "")

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		handleGenericResult(responseJSON)

	},
}

var templatesRemoveCmd = &cobra.Command{
	Use:     "rm",
	Short:   "Remove a template for the given user (default: calling user)",
	Long:    "Removes any built VM template for the given name as well as the template directory. Will not remove built-in template directories that ship with Ludus.",
	Args:    cobra.NoArgs,
	Aliases: []string{"remove", "delete"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		if templateName == "" {
			logger.Logger.Fatal("You must specify a template name to delete")
		}

		responseJSON, success = rest.GenericDelete(client, buildURLWithRangeAndUserID(fmt.Sprintf("/template/%s", templateName)))

		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		handleGenericResult(responseJSON)

	},
}

func setupTemplatesRemoveCmd(command *cobra.Command) {
	command.Flags().StringVarP(&templateName, "name", "n", "", "the name of the template to remove")
}

func init() {
	templatesCmd.AddCommand(templatesListCmd)
	setupTemplatesBuildCmd(templatesBuildCmd)
	templatesCmd.AddCommand(templatesBuildCmd)
	setupTemplateLogsCmd(templateLogsCmd)
	templatesCmd.AddCommand(templateLogsCmd)
	templatesCmd.AddCommand(templatesStatusCmd)
	setupTemplateAddCmd(templateAddCmd)
	templatesCmd.AddCommand(templateAddCmd)
	templatesCmd.AddCommand(templatesAbortCmd)
	setupTemplatesRemoveCmd(templatesRemoveCmd)
	templatesCmd.AddCommand(templatesRemoveCmd)
	rootCmd.AddCommand(templatesCmd)
}
