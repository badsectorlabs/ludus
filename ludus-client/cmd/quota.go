package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"strconv"

	"github.com/go-resty/resty/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	quotaRAM    int
	quotaCPU    int
	quotaVMs    int
	quotaRanges int
)

// quotasCmd represents the quotas parent command
var quotasCmd = &cobra.Command{
	Use:     "quotas",
	Short:   "Perform actions related to quotas",
	Long:    ``,
	Aliases: []string{"quota"},
}

func formatLimit(limit int) string {
	if limit == 0 {
		return "unlimited"
	}
	return strconv.Itoa(limit)
}

func formatUsedOfLimit(used, limit int) string {
	if limit == 0 {
		return strconv.Itoa(used)
	}
	return fmt.Sprintf("%d/%d", used, limit)
}

// quotasViewCmd shows the calling user's effective quotas and current usage
var quotasViewCmd = &cobra.Command{
	Use:   "view [users|groups|defaults]",
	Short: "View effective quotas and current usage",
	Long: `Display effective quota limits and current resource usage.

  ludus quotas view            View your own quotas
  ludus quotas view users      View all users' quotas (admin only)
  ludus quotas view groups     View all groups' default quotas (admin only)
  ludus quotas view defaults   View system-wide default quotas (admin only)`,
	Args:    cobra.RangeArgs(0, 1),
	Aliases: []string{"status", "show"},
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		if len(args) == 1 {
			switch args[0] {
			case "users":
				viewAllUsers(client)
			case "groups":
				viewAllGroups(client)
			case "defaults":
				viewDefaults(client)
			default:
				logger.Logger.Fatalf("Unknown subcommand %q. Use: users, groups, or defaults", args[0])
			}
			return
		}

		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/user/quotas"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}

		var data QuotaStatusObject
		err := json.Unmarshal(responseJSON, &data)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		available := func(used, limit int) string {
			if limit == 0 {
				return "unlimited"
			}
			remaining := limit - used
			if remaining < 0 {
				return fmt.Sprintf("over by %d", -remaining)
			}
			return strconv.Itoa(remaining)
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Resource", "Used", "Limit", "Available"})

		table.Append([]string{"RAM (GB)", strconv.Itoa(data.UsedRAM), formatLimit(data.LimitRAM), available(data.UsedRAM, data.LimitRAM)})
		table.Append([]string{"CPU (cores)", strconv.Itoa(data.UsedCPU), formatLimit(data.LimitCPU), available(data.UsedCPU, data.LimitCPU)})
		table.Append([]string{"VMs", strconv.Itoa(data.UsedVMs), formatLimit(data.LimitVMs), available(data.UsedVMs, data.LimitVMs)})
		table.Append([]string{"Ranges", strconv.Itoa(data.UsedRanges), formatLimit(data.LimitRanges), available(data.UsedRanges, data.LimitRanges)})

		table.Render()
	},
}

func formatCellWithSource(used, limit int, src string) string {
	if limit == 0 {
		if used == 0 {
			return "-"
		}
		return strconv.Itoa(used)
	}
	cell := fmt.Sprintf("%d/%d", used, limit)
	if src != "" {
		cell += " (" + src + ")"
	}
	return cell
}

func viewAllUsers(client *resty.Client) {
	responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/user/quotas/all"))
	if didFailOrWantJSON(success, responseJSON) {
		return
	}

	var data []AllQuotaStatusObject
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"User", "Name", "RAM (GB)", "CPU", "VMs", "Ranges"})

	for _, u := range data {
		table.Append([]string{
			u.UserID,
			u.Name,
			formatCellWithSource(u.UsedRAM, u.LimitRAM, u.SourceRAM),
			formatCellWithSource(u.UsedCPU, u.LimitCPU, u.SourceCPU),
			formatCellWithSource(u.UsedVMs, u.LimitVMs, u.SourceVMs),
			formatCellWithSource(u.UsedRanges, u.LimitRanges, u.SourceRanges),
		})
	}

	table.Render()
	fmt.Println("(U) = user quota, (G) = group default, (S) = system default")
}

func viewAllGroups(client *resty.Client) {
	responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/groups/quotas"))
	if didFailOrWantJSON(success, responseJSON) {
		return
	}

	var data []GroupQuotaObject
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Group", "RAM (GB)", "CPU", "VMs", "Ranges", "Members"})

	for _, g := range data {
		table.Append([]string{
			g.Name,
			formatLimit(g.DefaultQuotaRAM),
			formatLimit(g.DefaultQuotaCPU),
			formatLimit(g.DefaultQuotaVMs),
			formatLimit(g.DefaultQuotaRanges),
			strconv.Itoa(g.MemberCount),
		})
	}

	table.Render()
}

func viewDefaults(client *resty.Client) {
	responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/user/quotas/defaults"))
	if didFailOrWantJSON(success, responseJSON) {
		return
	}

	var data QuotaStatusObject
	err := json.Unmarshal(responseJSON, &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Resource", "System Default"})

	table.Append([]string{"RAM (GB)", formatLimit(data.LimitRAM)})
	table.Append([]string{"CPU (cores)", formatLimit(data.LimitCPU)})
	table.Append([]string{"VMs", formatLimit(data.LimitVMs)})
	table.Append([]string{"Ranges", formatLimit(data.LimitRanges)})

	table.Render()
	fmt.Println("Set in /opt/ludus/config.yml (default_quota_ram, etc.)")
}

// quotasUserCmd is the parent command for user-specific quota operations
var quotasUserCmd = &cobra.Command{
	Use:   "user",
	Short: "Perform quota actions for a specific user (admin only)",
	Long:  ``,
}

// quotasUserSetCmd sets per-user quota overrides (admin only)
var quotasUserSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set per-user quota overrides (admin only)",
	Long: `Set resource quota limits for one or more users. For multiple users,
provide comma-separated userIDs (e.g., -i "su,eh,nu"). Only provide flags
for the quotas you want to change. Use "ludus quotas user reset" to remove limits.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		body := map[string]any{}
		if cmd.Flags().Changed("ram") {
			body["quotaRAM"] = quotaRAM
		}
		if cmd.Flags().Changed("cpu") {
			body["quotaCPU"] = quotaCPU
		}
		if cmd.Flags().Changed("vms") {
			body["quotaVMs"] = quotaVMs
		}
		if cmd.Flags().Changed("ranges") {
			body["quotaRanges"] = quotaRanges
		}

		if len(body) == 0 {
			logger.Logger.Fatal("No quota flags provided. Use --ram, --cpu, --vms, or --ranges to set quotas.")
		}

		body["userIDs"] = splitAndTrimIDs(newUserID)

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/user/quotas"), string(bodyJSON))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupQuotasUserSetCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user to set quotas for")
	command.Flags().IntVar(&quotaRAM, "ram", 0, "RAM limit in GB")
	command.Flags().IntVar(&quotaCPU, "cpu", 0, "CPU core limit")
	command.Flags().IntVar(&quotaVMs, "vms", 0, "VM count limit")
	command.Flags().IntVar(&quotaRanges, "ranges", 0, "range count limit")
	_ = command.MarkFlagRequired("userid")
}

// quotasUserResetCmd removes per-user quota overrides (admin only)
var quotasUserResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Remove per-user quota overrides (admin only)",
	Long: `Remove resource quota limits for one or more users, returning them to
group or system defaults. Specify which quotas to reset with flags, or omit
flags to reset all quotas. For multiple users, provide comma-separated userIDs.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		body := map[string]any{}
		// If specific flags are provided, only reset those
		anyFlagSet := cmd.Flags().Changed("ram") || cmd.Flags().Changed("cpu") ||
			cmd.Flags().Changed("vms") || cmd.Flags().Changed("ranges")

		if anyFlagSet {
			if cmd.Flags().Changed("ram") {
				body["quotaRAM"] = 0
			}
			if cmd.Flags().Changed("cpu") {
				body["quotaCPU"] = 0
			}
			if cmd.Flags().Changed("vms") {
				body["quotaVMs"] = 0
			}
			if cmd.Flags().Changed("ranges") {
				body["quotaRanges"] = 0
			}
		} else {
			// No flags = reset all
			body["quotaRAM"] = 0
			body["quotaCPU"] = 0
			body["quotaVMs"] = 0
			body["quotaRanges"] = 0
		}

		body["userIDs"] = splitAndTrimIDs(newUserID)

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/user/quotas"), string(bodyJSON))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupQuotasUserResetCmd(command *cobra.Command) {
	command.Flags().StringVarP(&newUserID, "userid", "i", "", "the UserID of the user to reset quotas for")
	command.Flags().Bool("ram", false, "reset RAM quota")
	command.Flags().Bool("cpu", false, "reset CPU quota")
	command.Flags().Bool("vms", false, "reset VMs quota")
	command.Flags().Bool("ranges", false, "reset ranges quota")
	_ = command.MarkFlagRequired("userid")
}

// quotasGroupCmd is the parent command for group quota operations
var quotasGroupCmd = &cobra.Command{
	Use:   "group",
	Short: "Perform quota actions for a group (admin only)",
	Long:  ``,
}

// quotasGroupSetCmd sets group-default quota values (admin only)
var quotasGroupSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set group default quotas (admin only)",
	Long: `Set default resource quota limits for a group. These apply to members
that don't have per-user overrides. Only provide flags for the quotas you
want to change. Use "ludus quotas group reset" to remove limits.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupName, _ := cmd.Flags().GetString("group")

		body := map[string]any{}
		if cmd.Flags().Changed("ram") {
			body["defaultQuotaRAM"] = quotaRAM
		}
		if cmd.Flags().Changed("cpu") {
			body["defaultQuotaCPU"] = quotaCPU
		}
		if cmd.Flags().Changed("vms") {
			body["defaultQuotaVMs"] = quotaVMs
		}
		if cmd.Flags().Changed("ranges") {
			body["defaultQuotaRanges"] = quotaRanges
		}

		if len(body) == 0 {
			logger.Logger.Fatal("No quota flags provided. Use --ram, --cpu, --vms, or --ranges to set quotas.")
		}

		body["groupNames"] = splitAndTrimIDs(groupName)

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/groups/quotas"), string(bodyJSON))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupQuotasGroupSetCmd(command *cobra.Command) {
	command.Flags().StringP("group", "g", "", "the group name(s) to set quotas for (comma-separated for multiple)")
	command.Flags().IntVar(&quotaRAM, "ram", 0, "RAM limit in GB")
	command.Flags().IntVar(&quotaCPU, "cpu", 0, "CPU core limit")
	command.Flags().IntVar(&quotaVMs, "vms", 0, "VM count limit")
	command.Flags().IntVar(&quotaRanges, "ranges", 0, "range count limit")
	_ = command.MarkFlagRequired("group")
}

// quotasGroupResetCmd removes group default quotas (admin only)
var quotasGroupResetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Remove group default quotas (admin only)",
	Long: `Remove default resource quota limits for a group, returning members to
system defaults. Specify which quotas to reset with flags, or omit flags to reset all.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		groupName, _ := cmd.Flags().GetString("group")

		body := map[string]any{}
		anyFlagSet := cmd.Flags().Changed("ram") || cmd.Flags().Changed("cpu") ||
			cmd.Flags().Changed("vms") || cmd.Flags().Changed("ranges")

		if anyFlagSet {
			if cmd.Flags().Changed("ram") {
				body["defaultQuotaRAM"] = 0
			}
			if cmd.Flags().Changed("cpu") {
				body["defaultQuotaCPU"] = 0
			}
			if cmd.Flags().Changed("vms") {
				body["defaultQuotaVMs"] = 0
			}
			if cmd.Flags().Changed("ranges") {
				body["defaultQuotaRanges"] = 0
			}
		} else {
			body["defaultQuotaRAM"] = 0
			body["defaultQuotaCPU"] = 0
			body["defaultQuotaVMs"] = 0
			body["defaultQuotaRanges"] = 0
		}

		body["groupNames"] = splitAndTrimIDs(groupName)

		bodyJSON, err := json.Marshal(body)
		if err != nil {
			logger.Logger.Fatal(err.Error())
		}

		responseJSON, success := rest.GenericJSONPut(client, buildURLWithRangeAndUserID("/groups/quotas"), string(bodyJSON))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupQuotasGroupResetCmd(command *cobra.Command) {
	command.Flags().StringP("group", "g", "", "the group name(s) to reset quotas for (comma-separated for multiple)")
	command.Flags().Bool("ram", false, "reset RAM quota")
	command.Flags().Bool("cpu", false, "reset CPU quota")
	command.Flags().Bool("vms", false, "reset VMs quota")
	command.Flags().Bool("ranges", false, "reset ranges quota")
	_ = command.MarkFlagRequired("group")
}

func init() {
	// Wire up quotasUserCmd
	setupQuotasUserSetCmd(quotasUserSetCmd)
	setupQuotasUserResetCmd(quotasUserResetCmd)
	quotasUserCmd.AddCommand(quotasUserSetCmd)
	quotasUserCmd.AddCommand(quotasUserResetCmd)

	// Wire up quotasGroupCmd
	setupQuotasGroupSetCmd(quotasGroupSetCmd)
	setupQuotasGroupResetCmd(quotasGroupResetCmd)
	quotasGroupCmd.AddCommand(quotasGroupSetCmd)
	quotasGroupCmd.AddCommand(quotasGroupResetCmd)

	// Wire up quotasCmd
	quotasCmd.AddCommand(quotasViewCmd)
	quotasCmd.AddCommand(quotasUserCmd)
	quotasCmd.AddCommand(quotasGroupCmd)

	// Register with root
	rootCmd.AddCommand(quotasCmd)
}
