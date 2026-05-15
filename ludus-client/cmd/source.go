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

	"github.com/go-resty/resty/v2"
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
	SourceID               string                        `json:"sourceID"`
	TemplateResults        []artifactResultPayload       `json:"templateResults"`
	LocalRoleResults       []artifactResultPayload       `json:"localRoleResults"`
	RoleResults            []roleResultPayload           `json:"roleResults"`
	UndeclaredDependencies []undeclaredDependencyPayload `json:"undeclaredDependencies,omitempty"`
}

type undeclaredDependencyPayload struct {
	BlueprintID string `json:"blueprintID"`
	Role        string `json:"role"`
	Hint        string `json:"hint,omitempty"`
}

type artifactResultPayload struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

func updateSourceRequestToForm(req dto.UpdateSourceRequest) map[string]string {
	form := map[string]string{}
	if req.Ref != "" {
		form["ref"] = req.Ref
	}
	if req.GlobalRoles {
		form["globalRoles"] = "true"
	}
	if req.Force {
		form["force"] = "true"
	}
	return form
}

// createSourceRequestToForm omits zero-valued fields so server defaults still apply.
func createSourceRequestToForm(req dto.CreateSourceRequest) map[string]string {
	form := map[string]string{}
	if req.ID != "" {
		form["id"] = req.ID
	}
	if req.Type != "" {
		form["type"] = req.Type
	}
	if req.URL != "" {
		form["url"] = req.URL
	}
	if req.Ref != "" {
		form["ref"] = req.Ref
	}
	if req.GlobalRoles {
		form["globalRoles"] = "true"
	}
	if req.Force {
		form["force"] = "true"
	}
	if req.DryRun {
		form["dryRun"] = "true"
	}
	return form
}

type roleResultPayload struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

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

func printSyncFailures(label string, p syncResultPayload) {
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalRoleResults, p.RoleResults)
	printArtifactOutcome(label, "synced successfully", "synced with errors", failures)
	printUndeclaredDependencies(p.UndeclaredDependencies)
}

// printUndeclaredDependencies surfaces range-config role references that
// aren't covered by requirements.yml. Non-fatal — the source still synced —
// but the user needs to fix these before deploy or the role install will be
// silently skipped.
func printUndeclaredDependencies(deps []undeclaredDependencyPayload) {
	if len(deps) == 0 {
		return
	}
	logger.Logger.Warnf("%d undeclared dependency reference(s) — install will be skipped at deploy:", len(deps))
	for _, d := range deps {
		logger.Logger.Warnf("  - blueprint %q role %q", d.BlueprintID, d.Role)
		if d.Hint != "" {
			logger.Logger.Warnf("      %s", d.Hint)
		}
	}
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

	req := dto.CreateSourceRequest{
		ID:          sourceFlagID,
		GlobalRoles: sourceFlagGlobalRoles,
		Force:       sourceFlagForce,
		DryRun:      sourceFlagDryRun,
	}
	var fileField, fileName string
	var fileBytes []byte

	arg := args[0]
	switch detectSourceArg(arg) {
	case sourceArgGit:
		req.Type = "git"
		req.URL = arg
		req.Ref = sourceFlagRef
	case sourceArgArchive:
		data, err := os.ReadFile(arg)
		if err != nil {
			logger.Logger.Fatal(err)
		}
		fileField = "archive"
		fileBytes = data
		fileName = filepath.Base(arg)
		req.Type = "upload"
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
		req.Type = "upload"
	default:
		logger.Logger.Fatalf("could not interpret %q: expected a git URL, tarball/zip path, or local directory", arg)
	}

	endpoint := buildURLWithRangeAndUserID("/sources")
	var responseJSON []byte
	var success bool

	if fileField != "" {
		responseJSON, success = rest.FileUpload(client, "POST", endpoint, fileField, fileName, fileBytes, createSourceRequestToForm(req))
	} else {
		responseJSON, success = rest.GenericJSONPost(client, endpoint, req)
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
		SourceName             string                        `json:"sourceName"`
		BlueprintIDs           []string                      `json:"blueprintIDs"`
		Templates              []string                      `json:"templates"`
		LocalRoles             []string                      `json:"localRoles"`
		GalaxyRoles            []string                      `json:"galaxyRoles"`
		SubscriptionRoles      []string                      `json:"subscriptionRoles"`
		UndeclaredDependencies []undeclaredDependencyPayload `json:"undeclaredDependencies"`
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
	printUndeclaredDependencies(plan.UndeclaredDependencies)
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
	Use:     "list [<sourceID>]",
	Short:   "List registered sources, or show details for one",
	Aliases: []string{"ls", "status"},
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		if len(args) == 1 {
			runSourceDetail(client, args[0])
			return
		}
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

// runSourceDetail prints metadata + per-artifact tables for one source.
// Each sub-resource fetch is independent: a failure on one (e.g. perms) does
// not suppress the rest. Empty sections render as "(none)".
func runSourceDetail(client *resty.Client, sourceID string) {
	srcJSON, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID))
	if didFailOrWantJSON(ok, srcJSON) {
		return
	}
	var src dto.SourceResponse
	if err := json.Unmarshal(srcJSON, &src); err != nil {
		logger.Logger.Fatal(err)
	}

	fmt.Printf("Source: %s\n", src.SourceID)
	printField("Name", src.Name)
	printField("Description", src.Description)
	printField("Type", src.Type)
	if src.Type == "git" {
		printField("URL", src.URL)
		printField("Ref", src.Ref)
	}
	if len(src.Authors) > 0 {
		printField("Authors", strings.Join(src.Authors, ", "))
	}
	printField("License", src.License)
	printField("Homepage", src.Homepage)
	printField("Owner", src.OwnerUserID)
	printField("Kind", src.Kind)
	printField("Last synced", src.LastSyncedAt)
	printField("Status", src.LastSyncStatus)
	if src.LastSyncError != "" {
		printField("Error", src.LastSyncError)
	}
	fmt.Println()

	printSourceBlueprints(client, sourceID)
	printSourceTemplates(client, sourceID)
	printSourceRoles(client, sourceID)
	printSourceCollections(client, sourceID)
}

func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Printf("  %-13s %s\n", label+":", value)
}

func printSourceBlueprints(client *resty.Client, sourceID string) {
	body, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/blueprints"))
	if !ok {
		return
	}
	var items []dto.SourceBlueprintListItem
	if err := json.Unmarshal(body, &items); err != nil {
		return
	}
	fmt.Printf("Blueprints (%d)\n", len(items))
	if len(items) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Blueprint ID", "Name", "Version", "Tags"})
	for _, b := range items {
		table.Append([]string{b.SourceBlueprintID, b.Name, b.Version, strings.Join(b.Tags, ", ")})
	}
	table.Render()
	fmt.Println()
}

func printSourceTemplates(client *resty.Client, sourceID string) {
	body, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/templates"))
	if !ok {
		return
	}
	var items []dto.ListSourceTemplatesResponseItem
	if err := json.Unmarshal(body, &items); err != nil {
		return
	}
	fmt.Printf("Templates (%d)\n", len(items))
	if len(items) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Version"})
	for _, t := range items {
		table.Append([]string{t.Name, t.Version})
	}
	table.Render()
	fmt.Println()
}

func printSourceRoles(client *resty.Client, sourceID string) {
	body, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/roles"))
	if !ok {
		return
	}
	var items []dto.ListSourceRolesResponseItem
	if err := json.Unmarshal(body, &items); err != nil {
		return
	}
	fmt.Printf("Roles (%d)\n", len(items))
	if len(items) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Scope", "Version"})
	for _, r := range items {
		table.Append([]string{r.Name, r.Scope, r.Version})
	}
	table.Render()
	fmt.Println()
}

func printSourceCollections(client *resty.Client, sourceID string) {
	body, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/collections"))
	if !ok {
		return
	}
	var items []dto.ListSourceCollectionsResponseItem
	if err := json.Unmarshal(body, &items); err != nil {
		return
	}
	fmt.Printf("Collections (%d)\n", len(items))
	if len(items) == 0 {
		fmt.Println("  (none)")
		fmt.Println()
		return
	}
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Name", "Version"})
	for _, c := range items {
		table.Append([]string{c.Name, c.Version})
	}
	table.Render()
	fmt.Println()
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

	body, _ := json.Marshal(dto.SyncSourceRequest{
		GlobalRoles: sourceFlagGlobalRoles,
		Force:       sourceFlagForce,
		DryRun:      sourceFlagDryRun,
	})

	for _, sid := range targets {
		path := fmt.Sprintf("/sources/%s/sync", sid)
		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(path), string(body))
		if didFailOrWantJSON(success, responseJSON) {
			continue
		}
		if sourceFlagDryRun {
			printDryRunPlan(responseJSON)
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
		updateReq := dto.UpdateSourceRequest{
			GlobalRoles: sourceFlagGlobalRoles,
			Force:       sourceFlagForce,
		}
		responseJSON, success := rest.FileUpload(client, "PATCH",
			buildURLWithRangeAndUserID(path), fileField, fileName, fileBytes, updateSourceRequestToForm(updateReq))
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

	body, _ := json.Marshal(dto.UpdateSourceRequest{Ref: sourceFlagRef})
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
				extra = " and uninstall its templates and roles"
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
		body, _ := json.Marshal(dto.DeleteSourceRequest{Purge: sourceFlagPurge})
		responseJSON, success := rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(path), body)
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		if !sourceFlagPurge {
			logger.Logger.Infof("Source %q removed.", args[0])
			return
		}
		var resp dto.DeleteSourceResponse
		_ = json.Unmarshal(responseJSON, &resp)
		logger.Logger.Infof("Source %q removed. Templates and roles registered only by this source were uninstalled. Collections remain installed.", args[0])
		if len(resp.AffectedSources) > 0 {
			logger.Logger.Warnf("Other sources also claimed some of these artifacts and will be missing files until re-synced:")
			for _, s := range resp.AffectedSources {
				logger.Logger.Warnf("  - %s", s)
			}
		}
		if len(resp.PurgeErrors) > 0 {
			logger.Logger.Warnf("%d artifact(s) could not be cleaned up:", len(resp.PurgeErrors))
			for _, e := range resp.PurgeErrors {
				logger.Logger.Warnf("  - %s", e)
			}
		}
	},
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
	sourceRmCmd.Flags().BoolVar(&sourceFlagPurge, "purge", false, "uninstall templates and roles registered only by this source (collections persist on disk)")
	sourceRmCmd.Flags().BoolVar(&sourceFlagNoPrompt, "no-prompt", false, "skip confirmation prompt")

	sourceCmd.AddCommand(sourceAddCmd)
	sourceCmd.AddCommand(
		sourceListCmd,
		sourceSyncCmd,
		sourceUpdateCmd,
		sourceRmCmd,
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
