package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"ludusapi/dto"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

// gzipBytes wraps a tar buffer in gzip so the resulting payload matches the
// .tar.gz filename the server expects. tarDirectoryInMemory itself produces
// uncompressed tar, which other commands accept directly but the source
// upload path requires gzipped.
func gzipBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(in); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// syncResultPayload mirrors the synchronous response shape from
// POST /sources and POST /sources/{id}/sync.
type syncResultPayload struct {
	SourceID         string                  `json:"sourceID"`
	TemplateResults  []artifactResultPayload `json:"templateResults"`
	LocalRoleResults []artifactResultPayload `json:"localRoleResults"`
	RoleResults      []roleResultPayload     `json:"roleResults"`
}

type artifactResultPayload struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

type roleResultPayload struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// collectArtifactFailureLines returns one "<kind> <name>: <reason>" line per
// failed artifact across the three result slices.
func collectArtifactFailureLines(templates, localRoles []artifactResultPayload, roles []roleResultPayload) []string {
	var failures []string
	for _, r := range templates {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("template %s: %s", r.Name, r.Message))
		}
	}
	for _, r := range localRoles {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("local_role %s: %s", r.Name, r.Message))
		}
	}
	for _, r := range roles {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("role %s: %s", r.Name, r.Error))
		}
	}
	return failures
}

// printArtifactOutcome emits one INFO line on success or a WARN block listing
// each failure. successPhrase and failurePhrase are appended to label, e.g.
// `printArtifactOutcome("Source 'X'", "synced successfully", "synced with errors", failures)`.
func printArtifactOutcome(label, successPhrase, failurePhrase string, failures []string) {
	if len(failures) == 0 {
		logger.Logger.Infof("%s %s.", label, successPhrase)
		return
	}
	logger.Logger.Warnf("%s %s:", label, failurePhrase)
	for _, line := range failures {
		logger.Logger.Warnf("  - %s", line)
	}
}

// printSyncFailures emits one log line per failed artifact so callers see exactly
// which template/role didn't register or download.
func printSyncFailures(label string, p syncResultPayload) {
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalRoleResults, p.RoleResults)
	printArtifactOutcome(label, "synced successfully", "synced with errors", failures)
}

// Source-related flag vars; reused across subcommands.
var (
	sourceFlagID          string
	sourceFlagRef         string
	sourceFlagGlobalRoles bool
	sourceFlagForce       bool
	sourceFlagDryRun      bool
	sourceFlagPurge       bool
	sourceFlagNoPrompt    bool
)

var sourceCmd = &cobra.Command{
	Use:     "source",
	Short:   "Manage sources",
	Aliases: []string{"sources"},
}

var sourceAddCmd = &cobra.Command{
	Use:   "add <url | tarball | directory>",
	Short: "Register a source",
	Long: `Register an external source of blueprints.

The argument is auto-detected:
  ludus source add https://github.com/foo/bar    # git URL
  ludus source add ./source.tar.gz               # uploaded tarball/zip
  ludus source add ./local-source-dir            # tar + upload from local dir`,
	Args: cobra.ExactArgs(1),
	Run:  runSourceAdd,
}

func runSourceAdd(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

	formData := map[string]string{}
	var fileField, fileName string
	var fileBytes []byte

	arg := args[0]
	switch detectSourceArg(arg) {
	case sourceArgGit:
		formData["type"] = "git"
		formData["url"] = arg
		if sourceFlagRef != "" {
			formData["ref"] = sourceFlagRef
		}
	case sourceArgArchive:
		data, err := os.ReadFile(arg)
		if err != nil {
			logger.Logger.Fatal(err)
		}
		fileField = "archive"
		fileBytes = data
		fileName = filepath.Base(arg)
		formData["type"] = "upload"
	case sourceArgDirectory:
		roleTar, err := tarDirectoryInMemory(arg)
		if err != nil {
			logger.Logger.Fatalf("tar %s: %s", arg, err)
		}
		gz, gzErr := gzipBytes(roleTar.Bytes())
		if gzErr != nil {
			logger.Logger.Fatalf("gzip tar: %s", gzErr)
		}
		fileField = "archive"
		fileBytes = gz
		fileName = filepath.Base(strings.TrimSuffix(arg, string(os.PathSeparator))) + ".tar.gz"
		formData["type"] = "upload"
	default:
		logger.Logger.Fatalf("could not interpret %q: expected a git URL, tarball/zip path, or local directory", arg)
	}

	if sourceFlagID != "" {
		formData["id"] = sourceFlagID
	}

	endpoint := buildURLWithRangeAndUserID("/sources")
	var responseJSON []byte
	var success bool

	if fileField != "" {
		if sourceFlagGlobalRoles {
			formData["globalRoles"] = "true"
		}
		if sourceFlagForce {
			formData["force"] = "true"
		}
		if sourceFlagDryRun {
			formData["dryRun"] = "true"
		}
		responseJSON, success = rest.FileUpload(client, "POST", endpoint, fileField, fileName, fileBytes, formData)
	} else {
		jsonBody := map[string]any{}
		for k, v := range formData {
			jsonBody[k] = v
		}
		jsonBody["globalRoles"] = sourceFlagGlobalRoles
		jsonBody["force"] = sourceFlagForce
		jsonBody["dryRun"] = sourceFlagDryRun
		responseJSON, success = rest.GenericJSONPost(client, endpoint, jsonBody)
	}

	if didFailOrWantJSON(success, responseJSON) {
		return
	}

	if sourceFlagDryRun {
		printDryRunPlan(responseJSON)
		return
	}
	var resp syncResultPayload
	if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.SourceID == "" {
		logger.Logger.Info(string(responseJSON))
		return
	}
	printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), resp)
}

// printDryRunPlan renders the JSON returned by `source add --dry-run` as a
// human-readable summary. The shape mirrors ludus-api's DryRunPlan.
func printDryRunPlan(body []byte) {
	var plan struct {
		SourceName        string   `json:"sourceName"`
		BlueprintIDs      []string `json:"blueprintIDs"`
		Templates         []string `json:"templates"`
		LocalRoles        []string `json:"localRoles"`
		GalaxyRoles       []string `json:"galaxyRoles"`
		SubscriptionRoles []string `json:"subscriptionRoles"`
	}
	if err := json.Unmarshal(body, &plan); err != nil {
		logger.Logger.Info(string(body))
		return
	}
	fmt.Println("Dry run — nothing was persisted or installed.")
	if plan.SourceName != "" {
		fmt.Printf("\nSource: %s\n", plan.SourceName)
	}
	rows := [][2]any{
		{"Blueprints", plan.BlueprintIDs},
		{"Templates", plan.Templates},
		{"Local roles", plan.LocalRoles},
		{"Galaxy roles", plan.GalaxyRoles},
		{"Subscription roles", plan.SubscriptionRoles},
	}
	for _, row := range rows {
		items := row[1].([]string)
		if len(items) == 0 {
			fmt.Printf("  %-19s (none)\n", row[0])
			continue
		}
		fmt.Printf("  %-19s %s\n", row[0], strings.Join(items, ", "))
	}
}

func isSourceURL(s string) bool {
	if u, err := neturl.Parse(s); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		return true
	}
	return strings.HasPrefix(s, "git@")
}

func isSourceArchivePath(s string) bool {
	low := strings.ToLower(s)
	return strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz") || strings.HasSuffix(low, ".zip")
}

type sourceArgKind int

const (
	sourceArgUnknown sourceArgKind = iota
	sourceArgGit
	sourceArgArchive
	sourceArgDirectory
)

// detectSourceArg classifies a positional source argument. URL wins outright;
// otherwise stat the path: a directory always tars+uploads, and a regular file
// is treated as an archive when the suffix matches.
func detectSourceArg(arg string) sourceArgKind {
	if isSourceURL(arg) {
		return sourceArgGit
	}
	info, err := os.Stat(arg)
	if err != nil {
		return sourceArgUnknown
	}
	if info.IsDir() {
		return sourceArgDirectory
	}
	if isSourceArchivePath(arg) {
		return sourceArgArchive
	}
	return sourceArgUnknown
}


var sourceListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List registered sources",
	Aliases: []string{"ls", "status"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources"))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var sources []dto.SourceResponse
		if err := json.Unmarshal(responseJSON, &sources); err != nil {
			logger.Logger.Fatal(err)
		}
		if len(sources) == 0 {
			logger.Logger.Info("No sources registered.")
			return
		}
		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"Source ID", "Name", "Type", "Authors", "Last Synced", "Status"})
		for _, s := range sources {
			table.Append([]string{
				s.SourceID,
				s.Name,
				s.Type,
				strings.Join(s.Authors, ", "),
				s.LastSyncedAt,
				s.LastSyncStatus,
			})
		}
		table.Render()
	},
}


var sourceSyncCmd = &cobra.Command{
	Use:   "sync [<sourceID>]",
	Short: "Re-pull a git source",
	Long:  `Re-pull a git source and re-register its content. Upload sources don't sync — push a new tarball with 'ludus source update' instead.`,
	Args:  cobra.MaximumNArgs(1),
	Run:   runSourceSync,
}

func runSourceSync(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

	var targets []string
	if len(args) == 1 {
		targets = []string{args[0]}
	} else {
		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources"))
		if !success {
			logger.Logger.Fatal(string(responseJSON))
		}
		var sources []dto.SourceResponse
		_ = json.Unmarshal(responseJSON, &sources)
		for _, s := range sources {
			if s.Type == "git" {
				targets = append(targets, s.SourceID)
			}
		}
	}

	formData := map[string]any{}
	if sourceFlagGlobalRoles {
		formData["globalRoles"] = true
	}
	if sourceFlagForce {
		formData["force"] = true
	}
	if sourceFlagDryRun {
		formData["dryRun"] = true
	}
	body, _ := json.Marshal(formData)

	for _, sid := range targets {
		path := fmt.Sprintf("/sources/%s/sync", sid)
		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(path), string(body))
		if !success {
			if msg := strings.TrimSpace(string(responseJSON)); msg != "" {
				logger.Logger.Errorf("sync %s: %s", sid, msg)
			}
			continue
		}
		var resp syncResultPayload
		if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.SourceID == "" {
			logger.Logger.Info(string(responseJSON))
			continue
		}
		printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), resp)
	}
}


var sourceUpdateCmd = &cobra.Command{
	Use:   "update <sourceID> [<tarball-or-directory>]",
	Short: "Change a source's tracked ref or content",
	Long: `Update a source.

  ludus source update <id> --ref main           # git: change tracked ref
  ludus source update <id> ./new-source.tar.gz  # upload: replace content
  ludus source update <id> ./source-dir         # upload: tar a dir and replace`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSourceUpdate,
}

func runSourceUpdate(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
	sid := args[0]
	path := fmt.Sprintf("/sources/%s", sid)

	var fileField, fileName string
	var fileBytes []byte
	if len(args) == 2 {
		switch detectSourceArg(args[1]) {
		case sourceArgArchive:
			data, err := os.ReadFile(args[1])
			if err != nil {
				logger.Logger.Fatal(err)
			}
			fileField = "archive"
			fileBytes = data
			fileName = filepath.Base(args[1])
		case sourceArgDirectory:
			roleTar, err := tarDirectoryInMemory(args[1])
			if err != nil {
				logger.Logger.Fatal(err)
			}
			gz, gzErr := gzipBytes(roleTar.Bytes())
			if gzErr != nil {
				logger.Logger.Fatal(gzErr)
			}
			fileField = "archive"
			fileBytes = gz
			fileName = filepath.Base(strings.TrimSuffix(args[1], string(os.PathSeparator))) + ".tar.gz"
		default:
			logger.Logger.Fatalf("could not interpret %q: expected a tarball/zip path or local directory", args[1])
		}
	}

	if fileField == "" && sourceFlagRef == "" {
		logger.Logger.Fatal("provide --ref (git) or a tarball/directory path (upload)")
	}

	if fileField != "" {
		formData := map[string]string{}
		if sourceFlagGlobalRoles {
			formData["globalRoles"] = "true"
		}
		if sourceFlagForce {
			formData["force"] = "true"
		}
		responseJSON, success := rest.FileUpload(client, "PATCH",
			buildURLWithRangeAndUserID(path), fileField, fileName, fileBytes, formData)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		var resp syncResultPayload
		if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.SourceID == "" {
			logger.Logger.Info(string(responseJSON))
			return
		}
		printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), resp)
		return
	}

	body, _ := json.Marshal(map[string]string{"ref": sourceFlagRef})
	responseJSON, success := rest.GenericJSONPatch(client, buildURLWithRangeAndUserID(path), string(body))
	if didFailOrWantJSON(success, responseJSON) {
		return
	}
	logger.Logger.Infof("Source '%s' ref updated. Run `ludus source sync %s` to apply.", sid, sid)
}


var sourceRmCmd = &cobra.Command{
	Use:     "rm <sourceID>",
	Short:   "Remove a source",
	Aliases: []string{"delete", "del"},
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		// Skip the prompt automatically when stdin is piped/non-TTY (CI, scripts).
		// Callers can also force-skip with --no-prompt.
		if !sourceFlagNoPrompt && stdinIsTerminal() {
			extra := ""
			if sourceFlagPurge {
				extra = " and purge its templates/roles"
			}
			fmt.Printf("Remove source '%s'%s? [y/N]: ", args[0], extra)
			var resp string
			_, _ = fmt.Scanln(&resp)
			if !strings.EqualFold(strings.TrimSpace(resp), "y") {
				logger.Logger.Info("Aborted.")
				return
			}
		}
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		path := fmt.Sprintf("/sources/%s", args[0])
		body, _ := json.Marshal(map[string]bool{"purge": sourceFlagPurge})
		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(path), body)
		didFailOrWantJSON(success, responseJSON)
	},
}


var sourceShareCmd = &cobra.Command{
	Use:   "share",
	Short: "Share a source with users or groups",
}

var sourceShareUserCmd = &cobra.Command{
	Use:   "user <sourceID> <userID...>",
	Short: "Share a source with one or more users",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		shareSource(args[0], "share/users", args[1:])
	},
}

var sourceShareGroupCmd = &cobra.Command{
	Use:   "group <sourceID> <groupName...>",
	Short: "Share a source with one or more groups",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		shareSource(args[0], "share/groups", args[1:])
	},
}

var sourceUnshareCmd = &cobra.Command{
	Use:   "unshare",
	Short: "Unshare a source",
}

var sourceUnshareUserCmd = &cobra.Command{
	Use:   "user <sourceID> <userID...>",
	Short: "Remove user share grants",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		shareSource(args[0], "unshare/users", args[1:])
	},
}

var sourceUnshareGroupCmd = &cobra.Command{
	Use:   "group <sourceID> <groupName...>",
	Short: "Remove group share grants",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		shareSource(args[0], "unshare/groups", args[1:])
	},
}

func shareSource(sourceID, op string, ids []string) {
	flat := []string{}
	for _, s := range ids {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				flat = append(flat, p)
			}
		}
	}
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
	path := fmt.Sprintf("/sources/%s/%s", sourceID, op)

	subjectKind := "user(s)"
	bodyKey := "userIDs"
	if strings.HasSuffix(op, "/groups") {
		bodyKey = "groupNames"
		subjectKind = "group(s)"
	}
	body, _ := json.Marshal(map[string][]string{bodyKey: flat})
	responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(path), string(body))
	if didFailOrWantJSON(success, responseJSON) {
		return
	}
	verb := "shared with"
	if strings.HasPrefix(op, "unshare/") {
		verb = "unshared from"
	}
	var resp dto.BulkBlueprintOperationResponse
	_ = json.Unmarshal(responseJSON, &resp)
	if len(resp.Success) > 0 {
		logger.Logger.Infof("Source '%s' %s %d %s: %v", sourceID, verb, len(resp.Success), subjectKind, resp.Success)
	}
	for _, e := range resp.Errors {
		logger.Logger.Errorf("%s: %s", e.Item, e.Reason)
	}
	if len(resp.Success) == 0 && len(resp.Errors) == 0 {
		logger.Logger.Infof("Source '%s' %s %d %s: %v", sourceID, verb, len(flat), subjectKind, flat)
	}
}

func init() {
	sourceAddCmd.Flags().StringVar(&sourceFlagID, "id", "", "explicit sourceID; overrides auto-derived slug")
	sourceAddCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "git branch/tag/commit (git sources only)")
	sourceAddCmd.Flags().BoolVar(&sourceFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide")
	sourceAddCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "overwrite already-installed templates and galaxy/local roles")
	sourceAddCmd.Flags().BoolVar(&sourceFlagDryRun, "dry-run", false, "preview without persisting or installing")

	// Sync flags (reuses sourceFlagRef, etc. from add).
	sourceSyncCmd.Flags().BoolVar(&sourceFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide")
	sourceSyncCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "overwrite already-installed templates and galaxy/local roles")
	sourceSyncCmd.Flags().BoolVar(&sourceFlagDryRun, "dry-run", false, "preview without persisting or installing")

	// Update flags.
	sourceUpdateCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "new git branch/tag/commit (git sources)")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide (upload only)")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "overwrite already-installed templates and galaxy/local roles (upload only)")

	// Rm flags.
	sourceRmCmd.Flags().BoolVar(&sourceFlagPurge, "purge", false, "remove templates/roles registered only by this source")
	sourceRmCmd.Flags().BoolVar(&sourceFlagNoPrompt, "no-prompt", false, "skip confirmation prompt")

	// Compose share subcommands.
	sourceShareCmd.AddCommand(sourceShareUserCmd, sourceShareGroupCmd)
	sourceUnshareCmd.AddCommand(sourceUnshareUserCmd, sourceUnshareGroupCmd)

	sourceCmd.AddCommand(sourceAddCmd)
	sourceCmd.AddCommand(
		sourceListCmd,
		sourceSyncCmd,
		sourceUpdateCmd,
		sourceRmCmd,
		sourceShareCmd,
		sourceUnshareCmd,
	)
	rootCmd.AddCommand(sourceCmd)
}

// stdinIsTerminal reports whether stdin is connected to an interactive TTY.
// Returns false when stdin is a pipe/redirect (e.g. CI), so prompts can be skipped.
func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
