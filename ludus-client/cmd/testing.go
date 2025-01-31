package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	domain                 string
	ip                     string
	allowFilePath          string
	name                   string
	forceStop              bool
	VMIDs                  string
	RegisteredOwner        string
	RegisteredOrganization string
	Vendor                 string
	dropFiles              bool
)

var testingCmd = &cobra.Command{
	Use:   "testing",
	Short: "Control the testing state of the range",
	Long:  ``,
}

var testingStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Get the current testing status as well as any allowed domains and IPs (alias: list)",
	Args:    cobra.NoArgs,
	Aliases: []string{"list"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if userID == "" {
			userID = strings.Split(apiKey, ".")[0]
		}

		responseJSON, success := rest.GenericGet(client, fmt.Sprintf("/range?userID=%s", userID))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var rangeObject RangeObject
		err := json.Unmarshal(responseJSON, &rangeObject)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Testing Enabled", "Allowed IPs", "Allowed Domains"})
		table.SetHeaderAlignment(tablewriter.ALIGN_CENTER)
		table.SetAlignment(tablewriter.ALIGN_CENTER)
		table.SetAutoWrapText(false)
		var allowedIPsString string
		var allowedDomainsString string
		rangeObject.AllowedIPs = removeEmptyStrings(rangeObject.AllowedIPs)
		rangeObject.AllowedDomains = removeEmptyStrings(rangeObject.AllowedDomains)
		if len(rangeObject.AllowedIPs) == 0 || (len(rangeObject.AllowedIPs) == 1 && rangeObject.AllowedIPs[0] == "") {
			allowedIPsString = "No IPs are allowed"
		} else {
			allowedIPsString = rangeObject.AllowedIPs[0]
		}
		if len(rangeObject.AllowedDomains) == 0 || (len(rangeObject.AllowedDomains) == 1 && rangeObject.AllowedDomains[0] == "") {
			allowedDomainsString = "No domains are allowed"
		} else {
			allowedDomainsString = rangeObject.AllowedDomains[0]
		}
		if rangeObject.TestingEnabled {
			table.Rich([]string{strings.ToUpper(strconv.FormatBool(rangeObject.TestingEnabled)), allowedIPsString, allowedDomainsString}, []tablewriter.Colors{tablewriter.Colors{tablewriter.FgBlackColor, tablewriter.Bold, tablewriter.BgGreenColor}, tablewriter.Colors{}, tablewriter.Colors{}})
		} else {
			table.Rich([]string{strings.ToUpper(strconv.FormatBool(rangeObject.TestingEnabled)), allowedIPsString, allowedDomainsString}, []tablewriter.Colors{tablewriter.Colors{tablewriter.FgHiRedColor, tablewriter.Bold, tablewriter.BgBlackColor}, tablewriter.Colors{}, tablewriter.Colors{}})
		}
		// Loop through the arrays and add elements to the table, using blank strings if the end of either array is reached
		for i := 1; ; i++ {
			var allowedIP, allowedDomain string

			if i < len(rangeObject.AllowedIPs) {
				allowedIP = rangeObject.AllowedIPs[i]
			} else {
				allowedIP = ""
			}

			if i < len(rangeObject.AllowedDomains) {
				allowedDomain = rangeObject.AllowedDomains[i]
			} else {
				allowedDomain = ""
			}

			// Break the loop if both arrays are exhausted
			if allowedIP == "" && allowedDomain == "" {
				break
			}

			table.Append([]string{"", allowedIP, allowedDomain})
		}
		table.Render()

	},
}

var testingStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Snapshot all testing VMs and block all outbound connections and DNS from testing VMs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if userID == "" {
			userID = strings.Split(apiKey, ".")[0]
		}

		responseJSON, success := rest.GenericJSONPut(client, fmt.Sprintf("/testing/start?userID=%s", userID), "")
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)

	},
}

var testingStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Revert all testing VMs and enable all outbound connections and DNS from testing VMs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if userID == "" {
			userID = strings.Split(apiKey, ".")[0]
		}

		putBody := fmt.Sprintf(`{
			"force": %s
		}`, strconv.FormatBool(forceStop))

		if forceStop {
			var choice string
			logger.Logger.Warn(`
!!! This may leak telemetry/signatures if your VMs are dirty !!!

Do you want to continue? (y/N): `)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		responseJSON, success := rest.GenericJSONPut(client, fmt.Sprintf("/testing/stop?userID=%s", userID), putBody)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)

	},
}

func setupTestingStopCmd(command *cobra.Command) {
	command.Flags().BoolVar(&forceStop, "force", false, "force ludus to exit testing mode even if one or more snapshot reverts fails")
}

func handleAllowDenyResult(responseJSON []byte) {
	type errorStruct struct {
		Item   string `json:"item"`
		Reason string `json:"reason"`
	}

	type Data struct {
		Allowed []string      `json:"allowed"`
		Denied  []string      `json:"denied"`
		Errors  []errorStruct `json:"errors"`
	}

	// Unmarshal JSON data
	var data Data
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	logger.Logger.Debugf("%v", data)

	if len(data.Errors) > 0 {
		for _, error := range data.Errors {
			logger.Logger.Error(error.Item + ": " + error.Reason)
		}
	}
	if len(data.Allowed) > 0 {
		for _, allowed := range data.Allowed {
			logger.Logger.Info("Allowed: " + allowed)
		}
	}
	if len(data.Denied) > 0 {
		for _, denied := range data.Denied {
			logger.Logger.Info("Denied: " + denied)
		}
	}
}

func testingAllowDenyCmd(use, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {

			var domainArray []string
			var ipArray []string
			// This is not a perfect regex, and I'm not sure one even exists. It is however, good enough(tm) (modified from https://regexr.com/3au3g)
			domainRegex, _ := regexp.Compile(`(?m)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9][a-z0-9-]{0,61}[a-z]`)
			// IP regex from https://regexr.com/39hqf
			ipRegex, _ := regexp.Compile(`(?m)^((25[0-5]|(2[0-4]|1\d|[1-9]|)\d)\.?\b){4}$`)

			if domain == "" && ip == "" && allowFilePath == "" {
				logger.Logger.Fatal("You must specify at least one flag for this command")
			}

			// Parse domains
			if len(domain) > 0 {
				if strings.Contains(domain, ",") {
					domainArray = strings.Split(domain, ",")
					for _, domain := range domainArray {
						if !domainRegex.MatchString(domain) {
							logger.Logger.Fatalf("%s is not a valid domain", domain)
						}
					}
				} else {
					if !domainRegex.MatchString(domain) {
						logger.Logger.Fatalf("%s is not a valid domain", domain)
					}
					domainArray = []string{domain}
				}
			}

			// Parse IPs
			if len(ip) > 0 {
				if strings.Contains(ip, ",") {
					ipArray = strings.Split(ip, ",")
					for _, ip := range ipArray {
						if !ipRegex.MatchString(ip) {
							logger.Logger.Fatalf("%s is not a valid IP", ip)
						}
					}
				} else {
					if !ipRegex.MatchString(ip) {
						logger.Logger.Fatalf("%s is not a valid IP", ip)
					}
					ipArray = []string{ip}
				}
			}

			// Parse file
			if allowFilePath != "" {
				logger.Logger.Debugf("Checking file %s\n", allowFilePath)
				fileBytes, err := os.ReadFile(allowFilePath)
				if err != nil {
					logger.Logger.Fatal(err.Error())
				}
				fileString := string(fileBytes)
				ipsFromFile := ipRegex.FindAllString(fileString, -1)
				if ipsFromFile != nil {
					logger.Logger.Debugf("Found ip in file: %v\n", ipsFromFile)
					ipArray = append(ipArray, ipsFromFile...)
				}
				domainsFromFile := domainRegex.FindAllString(fileString, -1)
				if domainsFromFile != nil {
					logger.Logger.Debugf("Found domains in file: %v\n", domainsFromFile)
					domainArray = append(domainArray, domainsFromFile...)
				}
			}

			var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

			type AllowPayload struct {
				Domains []string `json:"domains"`
				IPs     []string `json:"ips"`
			}
			var allowPayload AllowPayload
			allowPayload.Domains = domainArray
			allowPayload.IPs = ipArray

			payload, _ := json.Marshal(allowPayload)

			var responseJSON []byte
			var success bool
			if userID != "" {
				responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/testing/%s?userID=%s", use, userID), string(payload))
			} else {
				responseJSON, success = rest.GenericJSONPost(client, "/testing/"+use, string(payload))
			}

			if didFailOrWantJSON(success, responseJSON) {
				return
			}
			handleAllowDenyResult(responseJSON)
		},
	}
}

var testingAllowCmd = testingAllowDenyCmd("allow", "allow a domain, IP, or file containing domains and IPs during testing", `If providing a file, domains and IPs will be extracted with 
regex that require them to start at the beginning of a line.`)

func setupTestingAllowCmd(command *cobra.Command) {
	command.Flags().StringVarP(&domain, "domain", "d", "", "A domain or comma separated list of domains (and HTTPS certificate CRL domains) to allow. Resolved on Ludus.")
	command.Flags().StringVarP(&ip, "ip", "i", "", "An IP or comma separated list of IPs to allow")
	command.Flags().StringVarP(&allowFilePath, "file", "f", "", "A file containing domains and/or IPs to allow")
}

var testingDenyCmd = testingAllowDenyCmd("deny", "deny a previously allowed domain, IP, or file containing domains and IPs during testing", `If providing a file, domains and IPs will be extracted with 
regex that require them to start at the beginning of a line.`)

func setupTestingDenyCmd(command *cobra.Command) {
	command.Flags().StringVarP(&domain, "domain", "d", "", "A domain or comma separated list of domains to deny.")
	command.Flags().StringVarP(&ip, "ip", "i", "", "An IP or comma separated list of IPs to deny")
	command.Flags().StringVarP(&allowFilePath, "file", "f", "", "A file containing domains and/or IPs to deny")
}

var testingUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Perform a Windows update on a VM or group of VMs",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		type UpdatePayload struct {
			Name string `json:"name"`
		}
		var updatePayload UpdatePayload
		updatePayload.Name = name

		payload, _ := json.Marshal(updatePayload)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/testing/update?userID=%s", userID), string(payload))
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/testing/update", string(payload))
		}
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupTestingUpdateCmd(command *cobra.Command) {
	command.Flags().StringVarP(&name, "name", "n", "", "A VM name (JD-win10-21h2-enterprise-x64-1) or group name (JD_windows_endpoints) to update with Windows Update")
	_ = command.MarkFlagRequired("name")
}

var testingEnableAntiSandboxCmd = &cobra.Command{
	Use:   "enable-anti-sandbox",
	Short: "Enable anti-sandbox for a VM or multiple VMs (enterprise)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		type AntiSandboxPayload struct {
			VMIDs     string `json:"vmIDs"`
			Owner     string `json:"registeredOwner,omitempty"`
			Org       string `json:"registeredOrganization,omitempty"`
			Vendor    string `json:"vendor,omitempty"`
			DropFiles bool   `json:"dropFiles,omitempty"`
		}
		var antiSandboxPayload AntiSandboxPayload
		antiSandboxPayload.VMIDs = VMIDs
		antiSandboxPayload.Owner = RegisteredOwner
		antiSandboxPayload.Org = RegisteredOrganization
		antiSandboxPayload.Vendor = Vendor
		antiSandboxPayload.DropFiles = dropFiles
		if antiSandboxPayload.Vendor != "" && antiSandboxPayload.Vendor != "Dell" {
			logger.Logger.Fatal("The only supported vendor at this time is Dell")
		}

		if !noPrompt {
			var choice string
			logger.Logger.Warnf(`
!!! This will enable anti-sandbox settings for VMs: %s !!!
    which will have performance penalties. This should
    be the last step once a VM is fully configured!
    The VM(s) will be rebooted during this process.

Do you want to continue? (y/N): `, VMIDs)
			fmt.Scanln(&choice)
			if choice != "Y" && choice != "y" {
				logger.Logger.Fatal("Bailing!")
			}
		}

		logger.Logger.Info("Enabling Anti-Sandbox settings for VM(s), this can take some time. Please wait.")

		payload, _ := json.Marshal(antiSandboxPayload)

		var responseJSON []byte
		var success bool
		if userID != "" {
			responseJSON, success = rest.GenericJSONPost(client, fmt.Sprintf("/testing/antisandbox?userID=%s", userID), string(payload))
		} else {
			responseJSON, success = rest.GenericJSONPost(client, "/testing/antisandbox", string(payload))
		}
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleAntiSandboxResult(responseJSON)
	},
}

func setupEnableAntiSandboxCmd(command *cobra.Command) {
	command.Flags().StringVarP(&VMIDs, "vmids", "n", "", "A VM ID or name (104) or multiple VM IDs or names (104,105) to enable anti-sandbox on")
	command.Flags().StringVar(&RegisteredOwner, "owner", "", "The RegisteredOwner value to use for the VMs")
	command.Flags().StringVar(&RegisteredOrganization, "org", "", "The RegisteredOrganization value to use for the VMs")
	command.Flags().StringVar(&Vendor, "vendor", "", "The Vendor value to use for the MAC address of the VMs")
	command.Flags().BoolVar(&noPrompt, "no-prompt", false, "skip the confirmation prompt")
	command.Flags().BoolVar(&dropFiles, "drop-files", false, "drop random pdf, doc, ppt, and xlsx files on the desktop and downloads folder of the VMs")

	_ = command.MarkFlagRequired("vmids")
}

func handleAntiSandboxResult(responseJSON []byte) {
	type errorStruct struct {
		Item   string `json:"item"`
		Reason string `json:"reason"`
	}

	type Data struct {
		Success []string      `json:"success"`
		Errors  []errorStruct `json:"errors"`
	}

	// Unmarshal JSON data
	var data Data
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	logger.Logger.Debugf("%v", data)

	if len(data.Errors) > 0 {
		for _, error := range data.Errors {
			logger.Logger.Error(error.Item + ": " + error.Reason)
		}
	}
	if len(data.Success) > 0 {
		for _, allowed := range data.Success {
			logger.Logger.Info("Successfully enabled anti-sandbox for VM(s): " + allowed)
		}
	}
}

func init() {
	testingCmd.AddCommand(testingStatusCmd)
	testingCmd.AddCommand(testingStartCmd)
	setupTestingStopCmd(testingStopCmd)
	testingCmd.AddCommand(testingStopCmd)
	setupTestingAllowCmd(testingAllowCmd)
	testingCmd.AddCommand(testingAllowCmd)
	setupTestingDenyCmd(testingDenyCmd)
	testingCmd.AddCommand(testingDenyCmd)
	setupTestingUpdateCmd(testingUpdateCmd)
	testingCmd.AddCommand(testingUpdateCmd)
	setupEnableAntiSandboxCmd(testingEnableAntiSandboxCmd)
	testingCmd.AddCommand(testingEnableAntiSandboxCmd)
	rootCmd.AddCommand(testingCmd)
}
