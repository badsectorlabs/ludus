package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	configFilePath string
	tags           string
	force          bool
	rangeVerbose   bool
	outputPath     string
	noPrompt       bool
	onlyRoles      string
	limit          string
	targetUserID   string
	sourceUserID   string
	allRanges      bool
	rangeID        string
	description    string
	purpose        string
	userIDForRange string
	rangeNumber    int32
)

var rangeCmd = &cobra.Command{
	Use:   "range",
	Short: "Perform actions on your range",
	Long:  ``,
}

func getRangeStateColor(data RangeObject) tablewriter.Colors {
	if data.RangeState == "DEPLOYING" || data.RangeState == "DESTROYING" {
		return tablewriter.Colors{tablewriter.FgYellowColor, tablewriter.Bold, tablewriter.BgBlackColor}
	} else if data.RangeState == "ERROR" || data.RangeState == "ABORTED" {
		return tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor}
	} else if data.RangeState == "SUCCESS" {
		return tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor}
	} else if data.RangeState == "DESTROYED" {
		return tablewriter.Colors{tablewriter.FgGreenColor, tablewriter.Bold, tablewriter.BgBlackColor}
	} else {
		// Default to normal formatting for "NEVER DEPLOYED"
		return nil
	}
}

func formatRangeResponse(data RangeObject, withVMs bool) {
	// Create table
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAlignment(tablewriter.ALIGN_CENTER)
	table.SetHeader([]string{"Range ID", "Name", "Range Network", "Last Deployment", "Number of VMs", "Deployment Status", "Testing Enabled"})
	lastDeployment := formatTimeObject(data.LastDeployment, "2006-01-02 15:04")

	table.Append([]string{data.RangeID, data.Name, fmt.Sprintf("10.%d.0.0/16", data.RangeNumber), lastDeployment, fmt.Sprint(data.NumberOfVMs), data.RangeState, strings.ToUpper(strconv.FormatBool(data.TestingEnabled))})

	if data.TestingEnabled {
		table.SetColumnColor(nil, nil, nil, nil, nil, getRangeStateColor(data), tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor})
	} else {
		table.SetColumnColor(nil, nil, nil, nil, nil, getRangeStateColor(data), tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor})
	}
	table.Render()

	// Display description and purpose if available
	if data.Description != "" || data.Purpose != "" {
		fmt.Println()
		if data.Description != "" {
			fmt.Printf("Description: %s\n", data.Description)
		}
		if data.Purpose != "" {
			fmt.Printf("Purpose: %s\n", data.Purpose)
		}
		fmt.Println()
	}

	if withVMs {
		vmTable := tablewriter.NewWriter(os.Stdout)
		vmTable.SetColumnAlignment([]int{tablewriter.ALIGN_CENTER, tablewriter.ALIGN_LEFT, tablewriter.ALIGN_CENTER, tablewriter.ALIGN_LEFT})
		vmTable.SetHeader([]string{"Proxmox ID", "VM Name", "Power", "IP"})

		for _, vm := range data.VMs {
			var powerString string
			if vm.PoweredOn {
				powerString = "On"
			} else {
				powerString = "Off"
			}
			vmTable.Append([]string{fmt.Sprint(vm.ProxmoxID), vm.Name, powerString, vm.Ip})
		}

		vmTable.Render()
	}
}

var rangeListCmd = &cobra.Command{
	Use:     "list [all]",
	Short:   "List details about your range (alias: status)",
	Args:    cobra.RangeArgs(0, 1),
	Aliases: []string{"status", "get"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		all := false
		if len(args) == 1 && args[0] == "all" {
			responseJSON, success = rest.GenericGet(client, "/range/all")
			all = true
		} else if len(args) == 1 {
			logger.Logger.Fatal("Unknown argument:", args[0])
		} else {
			responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/range"))
		}
		if !success {
			return
		}

		if !all {
			var data RangeObject
			err := json.Unmarshal(responseJSON, &data)
			if err != nil {
				logger.Logger.Fatal(err.Error())
			}
			if jsonFormat {
				fmt.Printf("%s\n", responseJSON)
				return
			}
			formatRangeResponse(data, true)
		} else {
			var data []RangeObject
			err := json.Unmarshal(responseJSON, &data)
			if err != nil {
				logger.Logger.Fatal(err.Error())
			}
			if jsonFormat {
				fmt.Printf("%s\n", responseJSON)
				return
			}
			table := tablewriter.NewWriter(os.Stdout)
			table.SetAlignment(tablewriter.ALIGN_CENTER)
			table.SetHeader([]string{"Range ID", "Name", "Range Network", "Last Deployment", "VM Count", "Deployment Status", "Testing Enabled"})
			for _, rangeObject := range data {
				lastDeployment := formatTimeObject(rangeObject.LastDeployment, "2006-01-02 15:04")

				rowValues := []string{rangeObject.RangeID, rangeObject.Name, fmt.Sprintf("10.%d.0.0/16", rangeObject.RangeNumber), lastDeployment, fmt.Sprint(rangeObject.NumberOfVMs), rangeObject.RangeState, strings.ToUpper(strconv.FormatBool(rangeObject.TestingEnabled))}

				var testingColor tablewriter.Colors
				if rangeObject.TestingEnabled {
					testingColor = tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor}
				} else {
					testingColor = tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor}
				}

				var dateColor tablewriter.Colors
				// 60+ days since last deployment => red
				if rangeObject.LastDeployment.Before(time.Now().Add(-60 * 24 * time.Hour)) {
					dateColor = tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor}
					// 30+ days since last deployment => yellow
				} else if rangeObject.LastDeployment.Before(time.Now().Add(-30 * 24 * time.Hour)) {
					dateColor = tablewriter.Colors{tablewriter.FgYellowColor, tablewriter.Bold, tablewriter.BgBlackColor}
				} else {
					dateColor = tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor}
				}

				table.Rich(rowValues,
					[]tablewriter.Colors{nil, nil, nil, dateColor, nil, getRangeStateColor(rangeObject), testingColor},
				)
			}
			table.Render()

		}

	},
}

var rangeConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Get or set a range configuration",
	Long:  ``,
}

var rangeConfigGet = &cobra.Command{
	Use:   "get [example]",
	Short: "Get the current Ludus range configuration for a user",
	Long:  `Provide the 'example' argument to get an example range configuration`,
	Args:  cobra.RangeArgs(0, 1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if len(args) == 1 && args[0] != "example" {
			logger.Logger.Fatal("Unknown argument:", args[0])
		} else if len(args) == 1 && args[0] == "example" {
			responseJSON, success = rest.GenericGet(client, "/range/config/example")
		} else {
			responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/range/config"))
		}
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		type Result struct {
			RangeConfig string `json:"result"`
		}

		// Unmarshal JSON data
		var result Result
		err := json.Unmarshal([]byte(responseJSON), &result)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(result.RangeConfig)

	},
}

var rangeConfigSet = &cobra.Command{
	Use:   "set",
	Short: "Set the configuration for a range",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		configFileContent, err := os.ReadFile(configFilePath)
		if err != nil {
			logger.Logger.Fatalf("Could not read: %s, error: %s\n", configFilePath, err.Error())
		}
		responseJSON, success = rest.PostFileAndForce(client, buildURLWithRangeAndUserID("/range/config"), configFileContent, "file", force)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupRangeConfigSet(command *cobra.Command) {
	command.Flags().StringVarP(&configFilePath, "file", "f", "", "the range configuration file")
	_ = command.MarkFlagRequired("file")
	command.Flags().BoolVar(&force, "force", false, "force the configuration to be updated, even with testing enabled")
}

type DeployBody struct {
	Tags      string   `json:"tags"`
	Force     bool     `json:"force"`
	Verbose   bool     `json:"verbose"`
	OnlyRoles []string `json:"only_roles"`
	Limit     string   `json:"limit"`
}

var rangeDeployCmd = &cobra.Command{
	Use:     "deploy",
	Short:   "Deploy a range, running specific tags if specified",
	Long:    ``,
	Args:    cobra.ExactArgs(0),
	Aliases: []string{"build"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		deployBody := DeployBody{
			Tags:      tags,
			Force:     force,
			Verbose:   rangeVerbose,
			OnlyRoles: strings.Split(onlyRoles, ","),
			Limit:     limit,
		}

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/range/deploy"), deployBody)

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupRangeDeployCmd(command *cobra.Command) {
	command.Flags().StringVarP(&tags, "tags", "t", "", "the ansible tags to run for this deploy (default: all)")
	command.Flags().BoolVar(&force, "force", false, "force the deployment if testing is enabled (default: false)")
	command.Flags().BoolVarP(&rangeVerbose, "verbose-ansible", "v", false, "enable verbose output from ansible during the deploy (default: false)")
	command.Flags().StringVar(&onlyRoles, "only-roles", "", "limit the user defined roles to be run to this comma separated list of roles")
	command.Flags().StringVarP(&limit, "limit", "l", "", "limit the deploy to VM that match the specified pattern")
}

var rangeLogsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Get the latest deploy logs from your range",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var apiString, apiStringWithCursor string
		if follow {
			var newLogs string
			var cursor int = 0
			for {
				// Build base URL with range/user parameters
				baseURL := buildURLWithRangeAndUserID("/range/logs")
				if strings.Contains(baseURL, "?") {
					apiStringWithCursor = fmt.Sprintf("%s&cursor=%d", baseURL, cursor)
				} else {
					apiStringWithCursor = fmt.Sprintf("%s?cursor=%d", baseURL, cursor)
				}
				responseJSON, success := rest.GenericGet(client, apiStringWithCursor)
				if didFailOrWantJSON(success, responseJSON) {
					return
				}
				newLogs, cursor = stringAndCursorFromResult(responseJSON)
				if len(newLogs) > 0 {
					fmt.Print(newLogs)
				}
				time.Sleep(2 * time.Second)
			}
		} else {
			// Build base URL with range/user parameters
			baseURL := buildURLWithRangeAndUserID("/range/logs")
			if tail > 0 {
				if strings.Contains(baseURL, "?") {
					apiString = fmt.Sprintf("%s&tail=%d", baseURL, tail)
				} else {
					apiString = fmt.Sprintf("%s?tail=%d", baseURL, tail)
				}
			} else {
				apiString = baseURL
			}
			responseJSON, success := rest.GenericGet(client, apiString)
			if didFailOrWantJSON(success, responseJSON) {
				return
			}
			newLogs, _ := stringAndCursorFromResult(responseJSON)
			fmt.Print(newLogs)
		}
	},
}

func setupRangeLogsCmd(command *cobra.Command) {
	command.Flags().BoolVarP(&follow, "follow", "f", false, "continuously poll the log and print new lines as they are written")
	command.Flags().IntVarP(&tail, "tail", "t", 0, "number of lines of the log from the end to print")
}

var rangeErrorsCmd = &cobra.Command{
	Use:     "errors",
	Short:   "Parse the latest deploy logs from your range and print any non-ignored fatal errors",
	Args:    cobra.NoArgs,
	Aliases: []string{"error"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		apiString := buildURLWithRangeAndUserID("/range/logs")
		responseJSON, success := rest.GenericGet(client, apiString)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		rangeLogs, _ := stringAndCursorFromResult(responseJSON)
		printFatalErrorsFromString(rangeLogs)

	},
}

var rangeDeleteCmd = &cobra.Command{
	Use:     "rm",
	Short:   "Delete your range object from database and optionally destroy all VMs",
	Long:    `Delete your range object from the database and destroy all VMs. Use --force to delete all VMs.`,
	Aliases: []string{"destroy"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		var rangeIDString string
		if rangeID == "" {
			if userID == "" {
				rangeIDString = strings.Split(apiKey, ".")[0]
			} else {
				rangeIDString = userID
			}
		} else {
			rangeIDString = rangeID
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will delete the range: %s !!!
This action cannot be undone.

Do you want to continue? (y/N): `, rangeIDString)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		// Build URL with force parameter if specified
		deleteURL := buildURLWithRangeAndUserID("/range")
		if force {
			deleteURL += "?force=true"
		}

		responseJSON, success = rest.GenericDelete(client, deleteURL)
		if !success {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupDeleteCmd(command *cobra.Command) {
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
	command.Flags().BoolVar(&force, "force", false, "force deletion of range even if it has VMs")
}

var rangeDestroyVmsCmd = &cobra.Command{
	Use:     "destroy-vms",
	Short:   "Destroy all VMs in your range (keeps range)",
	Long:    `Destroy all VMs in your range but keep the range object in the database. Use this to start fresh with your range configuration.`,
	Aliases: []string{"rm-vms"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		var rangeIDString string
		if rangeID == "" {
			if userID == "" {
				rangeIDString = strings.Split(apiKey, ".")[0]
			} else {
				rangeIDString = userID
			}
		} else {
			rangeIDString = rangeID
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will destroy all VMs for the range: %s !!!
The range object will be kept in the database.

Do you want to continue? (y/N): `, rangeIDString)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		// Build URL for VM destruction endpoint
		destroyVmsURL := buildURLWithRangeAndUserID(fmt.Sprintf("/range/%s/vms", rangeID))

		responseJSON, success = rest.GenericDelete(client, destroyVmsURL)
		if !success {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupDestroyVmsCmd(command *cobra.Command) {
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
}

var rangeAnsibleInventoryCmd = &cobra.Command{
	Use:   "inventory",
	Short: "Get the ansible inventory file for a range",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		baseURL := buildURLWithRangeAndUserID("/range/ansibleinventory")

		// Add allranges parameter
		if allRanges {
			if strings.Contains(baseURL, "?") {
				baseURL += "&allranges=true"
			} else {
				baseURL += "?allranges=true"
			}
		}

		responseJSON, success = rest.GenericGet(client, baseURL)
		if !success {
			return
		}

		type Data struct {
			Result string `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(data.Result)

	},
}

func setupRangeAnsibleInventoryCmd(command *cobra.Command) {
	command.Flags().BoolVar(&allRanges, "all", false, "return inventory for all ranges this user has access to (useful for admin users)")
}

var rangeGetTags = &cobra.Command{
	Use:   "gettags",
	Short: "Get the ansible tags available for use with deploy",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, "/range/tags")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		type Data struct {
			Result string `json:"result"`
		}

		// Unmarshal JSON data
		var data Data
		err := json.Unmarshal([]byte(responseJSON), &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(data.Result)

	},
}

var rangeAbortCmd = &cobra.Command{
	Use:   "abort",
	Short: "Kill the ansible process deploying a range",
	Long:  ``,
	Args:  cobra.ExactArgs(0),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool

		responseJSON, success = rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/range/abort"), "")

		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

var rangeRDPGET = &cobra.Command{
	Use:   "rdp",
	Short: "Get a zip of RDP configuration files for all Windows hosts in a range",
	Long: `The RDP zip file will contain two configs for each Windows box:
one for the domainadmin user, and another for the domainuser user`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rest.FileGet(client, buildURLWithRangeAndUserID("/range/rdpconfigs"), outputPath)
	},
}

func setupRangeRDPGET(command *cobra.Command) {
	command.Flags().StringVarP(&outputPath, "output", "o", "rdp.zip", "the output file path")
}

var rangeEtcHostsGET = &cobra.Command{
	Use:   "etc-hosts",
	Short: "Get an /etc/hosts formatted file for all hosts in the range",
	Long:  ``,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/range/etchosts"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		type Result struct {
			RangeConfig string `json:"result"`
		}

		// Unmarshal JSON data
		var result Result
		err := json.Unmarshal([]byte(responseJSON), &result)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		fmt.Print(result.RangeConfig)
	},
}

var rangeAccessCmd = &cobra.Command{
	Use:   "access",
	Short: "Grant or revoke access to a range",
	Long:  ``,
}

type RangeAccessActionPayload struct {
	AccessActionVerb string `json:"action"`
	TargetUserID     string `json:"targetUserID"`
	SourceUserID     string `json:"sourceUserID"`
	Force            bool   `json:"force"`
}

func genericRangeActionCmd(use string, short string, aliases []string) *cobra.Command {

	return &cobra.Command{
		Use:     use,
		Short:   short,
		Long:    ``,
		Args:    cobra.ExactArgs(0),
		Aliases: aliases,
		Run: func(cmd *cobra.Command, args []string) {
			var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

			var responseJSON []byte
			var success bool

			accessBody := RangeAccessActionPayload{
				AccessActionVerb: use,
				TargetUserID:     targetUserID,
				SourceUserID:     sourceUserID,
				Force:            force,
			}

			responseJSON, success = rest.GenericJSONPost(client, "/range/access", accessBody)

			if didFailOrWantJSON(success, responseJSON) {
				return
			}
			handleGenericResult(responseJSON)
		},
	}
}

var accessGrantCmd = genericRangeActionCmd("grant", "grant access to a target range from a source user", []string{"share"})
var accessRevokeCmd = genericRangeActionCmd("revoke", "revoke access to a target range from a source user", []string{"unshare"})

func setupGenericRangeActionCmd(command *cobra.Command) {
	command.Flags().StringVarP(&targetUserID, "target", "t", "", "the userID of the range to grant/revoke access to/from")
	command.Flags().StringVarP(&sourceUserID, "source", "s", "", "the userID of the user to gaining or losing access")
	command.Flags().BoolVar(&force, "force", false, "force the access action even if the target router is inaccessible")
}

var accessListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List the status of all active cross-range accesses",
	Args:    cobra.ExactArgs(0),
	Aliases: []string{"status", "get"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, "/range/access")
		if !success {
			return
		}
		var rangeAccessObjects []RangeAccessObject
		err := json.Unmarshal(responseJSON, &rangeAccessObjects)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}
		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
			return
		}
		// Create table
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAlignment(tablewriter.ALIGN_CENTER)
		table.SetHeader([]string{"Target Range User ID", "Source User IDs"})

		for _, item := range rangeAccessObjects {
			table.Append([]string{item.TargetUserID, strings.Join(item.SourceUserIDs, ",")})
		}

		table.Render()

	},
}

var rangeTaskOutputCmd = &cobra.Command{
	Use:   "taskoutput",
	Short: "Get the output of a task by name from the latest deploy logs",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		apiString := buildURLWithRangeAndUserID("/range/logs")
		responseJSON, success := rest.GenericGet(client, apiString)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		rangeLogs, _ := stringAndCursorFromResult(responseJSON)
		printTaskOutputFromString(rangeLogs, args[0])

	},
}

type RangeCreatePayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Purpose     string `json:"purpose"`
	UserID      string `json:"userID"`
	RangeNumber int32  `json:"rangeNumber"`
	RangeID     string `json:"rangeID"`
}

// Commands for range management
var rangeCreateCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Create a new range",
	Long:  `Create a new range with a name and pool name. Description, purpose, and userID are optional.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		name := args[0]

		if rangeID == "" {
			logger.Logger.Fatal("Range ID is required. Use --range-id or -r to specify the range ID.")
		}

		payload := RangeCreatePayload{
			Name:        name,
			RangeID:     rangeID,
			Description: description,
			Purpose:     purpose,
			UserID:      userIDForRange,
			RangeNumber: rangeNumber,
		}

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, "/ranges/create", payload)
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Range '%s' created successfully\n", rangeID)
		}
	},
}

func setupRangeCreateCmd(command *cobra.Command) {
	command.Flags().StringVarP(&description, "description", "d", "", "Description of the range")
	command.Flags().StringVarP(&purpose, "purpose", "o", "", "Purpose of the range")
	command.Flags().StringVar(&userIDForRange, "user", "", "User ID to assign the range to (optional)")
	command.Flags().Int32VarP(&rangeNumber, "range-number", "n", 0, "Specific range number to assign (optional)")
}

var rangeAssignCmd = &cobra.Command{
	Use:   "assign [userID] [rangeID]",
	Short: "Assign a range to a user (admin only)",
	Long:  `Assign an existing range to a user, granting them direct access. Admin privileges required.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userID := args[0]
		rangeID := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/ranges/assign/%s/%s", userID, rangeID), nil)
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Range %s assigned to user %s successfully\n", rangeID, userID)
		}
	},
}

var rangeRevokeCmd = &cobra.Command{
	Use:   "revoke [userID] [rangeID]",
	Short: "Revoke range access from a user (admin only)",
	Long:  `Revoke a user's direct access to a range. Admin privileges required.`,
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		userID := args[0]
		rangeID := args[1]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/ranges/revoke/%s/%s?force=%t", userID, rangeID, force))
		if !success {
			return
		}

		if jsonFormat {
			fmt.Printf("%s\n", responseJSON)
		} else {
			fmt.Printf("Range %s access revoked from user %s successfully\n", rangeID, userID)
		}
	},
}

func setupRangeRevokeCmd(command *cobra.Command) {
	command.Flags().BoolVar(&force, "force", false, "force the access action even if the target router is inaccessible")
}

var rangeUsersCmd = &cobra.Command{
	Use:   "users [rangeID]",
	Short: "List users with access to a range (admin only)",
	Long:  `List all users who have access to a specific range, including direct and group-based access. Admin privileges required.`,
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		rangeID := args[0]

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/ranges/%s/users", rangeID))
		if !success {
			return
		}

		type Data struct {
			Result []struct {
				UserID string `json:"userID"`
				Name   string `json:"name"`
				Type   string `json:"type"`
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
		table.SetHeader([]string{"UserID", "Name", "Access Type"})

		// Add data to table
		for _, user := range data.Result {
			table.Append([]string{user.UserID, user.Name, user.Type})
		}

		// Print table
		table.Render()
	},
}

var rangeAccessibleCmd = &cobra.Command{
	Use:   "accessible",
	Short: "List all ranges accessible to the current user",
	Long:  `List all ranges that the current user can access, including direct assignments and group-based access.`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		responseJSON, success = rest.GenericGet(client, buildURLWithRangeAndUserID("/ranges/accessible"))
		if !success {
			return
		}

		type Data struct {
			Result []struct {
				RangeNumber int32  `json:"rangeNumber"`
				RangeID     string `json:"rangeID"`
				AccessType  string `json:"accessType"`
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
		table.SetHeader([]string{"Range Network", "Range ID", "Access Type"})

		// Add data to table
		for _, rangeObj := range data.Result {
			table.Append([]string{
				fmt.Sprintf("10.%d.0.0/16", rangeObj.RangeNumber),
				rangeObj.RangeID,
				rangeObj.AccessType,
			})
		}

		// Print table
		table.Render()
	},
}

func init() {
	rangeConfigCmd.AddCommand(rangeConfigGet)
	setupRangeConfigSet(rangeConfigSet)
	rangeConfigCmd.AddCommand(rangeConfigSet)
	setupRangeDeployCmd(rangeDeployCmd)
	rangeCmd.AddCommand(rangeDeployCmd)
	setupRangeLogsCmd(rangeLogsCmd)
	rangeCmd.AddCommand(rangeLogsCmd)
	rangeCmd.AddCommand(rangeErrorsCmd)
	rangeCmd.AddCommand(rangeListCmd)
	setupDeleteCmd(rangeDeleteCmd)
	rangeCmd.AddCommand(rangeDeleteCmd)
	setupDestroyVmsCmd(rangeDestroyVmsCmd)
	rangeCmd.AddCommand(rangeDestroyVmsCmd)
	rangeCmd.AddCommand(rangeConfigCmd)
	setupRangeAnsibleInventoryCmd(rangeAnsibleInventoryCmd)
	rangeCmd.AddCommand(rangeAnsibleInventoryCmd)
	rangeCmd.AddCommand(rangeGetTags)
	rangeCmd.AddCommand(rangeAbortCmd)
	setupRangeRDPGET(rangeRDPGET)
	rangeCmd.AddCommand(rangeRDPGET)
	rangeCmd.AddCommand(rangeEtcHostsGET)
	rangeAccessCmd.AddCommand(accessListCmd)
	rangeAccessCmd.AddCommand(accessGrantCmd)
	rangeAccessCmd.AddCommand(accessRevokeCmd)
	setupGenericRangeActionCmd(accessGrantCmd)
	setupGenericRangeActionCmd(accessRevokeCmd)
	rangeCmd.AddCommand(rangeAccessCmd)
	rangeCmd.AddCommand(rangeTaskOutputCmd)

	// Add admin range management commands
	setupRangeCreateCmd(rangeCreateCmd)
	rangeCmd.AddCommand(rangeCreateCmd)
	rangeCmd.AddCommand(rangeAssignCmd)
	setupRangeRevokeCmd(rangeRevokeCmd)
	rangeCmd.AddCommand(rangeRevokeCmd)
	rangeCmd.AddCommand(rangeUsersCmd)
	rangeCmd.AddCommand(rangeAccessibleCmd)

	rootCmd.AddCommand(rangeCmd)

}
