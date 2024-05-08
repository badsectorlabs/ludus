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
	table.SetHeader([]string{"User ID", "Range Network", "Last Deployment", "Number of VMs", "Deployment Status", "Testing Enabled"})
	lastDeployment := formatTimeObject(data.LastDeployment)

	table.Append([]string{data.UserID, fmt.Sprintf("10.%d.0.0/16", data.RangeNumber), lastDeployment, fmt.Sprint(data.NumberOfVMs), data.RangeState, strings.ToUpper(strconv.FormatBool(data.TestingEnabled))})

	if data.TestingEnabled {
		table.SetColumnColor(nil, nil, nil, nil, getRangeStateColor(data), tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor})
	} else {
		table.SetColumnColor(nil, nil, nil, nil, getRangeStateColor(data), tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor})
	}
	table.Render()

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
		} else if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/range?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/range")
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
			table.SetHeader([]string{"User ID", "Range Network", "Last Deployment", "VM Count", "Deployment Status", "Testing Enabled"})
			for _, rangeObject := range data {
				lastDeployment := formatTimeObject(rangeObject.LastDeployment)

				rowValues := []string{rangeObject.UserID, fmt.Sprintf("10.%d.0.0/16", rangeObject.RangeNumber), lastDeployment, fmt.Sprint(rangeObject.NumberOfVMs), rangeObject.RangeState, strings.ToUpper(strconv.FormatBool(rangeObject.TestingEnabled))}

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
					[]tablewriter.Colors{nil, nil, dateColor, nil, getRangeStateColor(rangeObject), testingColor},
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
		} else if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/range/config?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/range/config")
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
		if userID != "" {
			responseJSON, success = rest.PostFileAndForce(client, fmt.Sprintf("/range/config?userID=%s", userID), configFileContent, "file", force)
		} else {
			responseJSON, success = rest.PostFileAndForce(client, "/range/config", configFileContent, "file", force)
		}

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

		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/range/deploy?userID=%s", userID), deployBody)
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/range/deploy", deployBody)
		}

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
	command.Flags().StringVarP(&limit, "limit", "l", "", "limit the deploy to VM that match the specified pattern (must include localhost or no plays will run)")
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
				if userID != "" {
					apiStringWithCursor = fmt.Sprintf("/range/logs?userID=%s&cursor=%d", userID, cursor)
				} else {
					apiStringWithCursor = fmt.Sprintf("/range/logs?cursor=%d", cursor)
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
			if userID != "" && tail > 0 {
				apiString = fmt.Sprintf("/range/logs?userID=%s&tail=%d", userID, tail)
			} else if tail > 0 {
				apiString = fmt.Sprintf("/range/logs?tail=%d", tail)
			} else if userID != "" {
				apiString = fmt.Sprintf("/range/logs?userID=%s", userID)
			} else {
				apiString = "/range/logs"
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

		var apiString string

		if userID != "" {
			apiString = fmt.Sprintf("/range/logs?userID=%s", userID)
		} else {
			apiString = "/range/logs"
		}
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
	Short:   "Delete your range (all VMs will be destroyed)",
	Long:    ``,
	Aliases: []string{"destroy"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var responseJSON []byte
		var success bool
		if userID == "" {
			userID = strings.Split(apiKey, ".")[0]
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will destroy all VMs for the range of user ID: %s !!!
 
Do you want to continue? (y/N): `, userID)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		responseJSON, success = rest.GenericDelete(client, fmt.Sprintf("/range?userID=%s", userID))
		if !success {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupDeleteCmd(command *cobra.Command) {
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
		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/range/ansibleinventory?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/range/ansibleinventory")
		}
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

		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/range/abort?userID=%s", userID), "")
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/range/abort", "")
		}

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

		if userID != "" {
			rest.FileGet(client, fmt.Sprintf("/range/rdpconfigs?userID=%s", userID), outputPath)
		} else {
			rest.FileGet(client, "/range/rdpconfigs", outputPath)
		}
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
		if userID != "" {
			responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/range/etchosts?userID=%s", userID))
		} else {
			responseJSON, success = rest.GenericGet(client, "/range/etchosts")
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
	rangeCmd.AddCommand(rangeConfigCmd)
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
	rootCmd.AddCommand(rangeCmd)

}
