package cmd

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"ludus/internal/sourcepicker"
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
	BlueprintID      string `json:"blueprintID"`
	Role             string `json:"role"`
	Kind             string `json:"kind,omitempty"`
	ParentCollection string `json:"parentCollection,omitempty"`
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

// printSyncFailures renders the per-artifact result list returned by the
// server. verbPast is the action verb (past tense) shown in the summary —
// "installed" from source add / install, "synced" from source sync,
// "updated" from source update — so the output matches what the user
// actually invoked.
func printSyncFailures(label, verbPast string, p syncResultPayload) {
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalRoleResults, p.RoleResults)
	printArtifactOutcome(label, verbPast+" successfully", verbPast+" with errors", failures)
	printUndeclaredDependencies(p.UndeclaredDependencies)
}

// printUndeclaredDependencies surfaces range-config role references that
// aren't covered by requirements.yml. Non-fatal — the source still synced —
// but the user needs to fix these before deploy or the role install will be
// silently skipped. Items are grouped by kind (missing role vs missing
// collection) and deduped across blueprints so the user sees a compact list
// + one guidance line per kind.
func printUndeclaredDependencies(deps []undeclaredDependencyPayload) {
	if len(deps) == 0 {
		return
	}
	logger.Logger.Warnf("%d undeclared dependency reference(s) — install will be skipped at deploy:", len(deps))
	roles, collections := groupUndeclaredPayloads(deps)
	if len(roles) > 0 {
		logger.Logger.Warnf("  Missing roles:")
		for _, e := range roles {
			logger.Logger.Warnf("    %s  in: %s", e.label, strings.Join(e.blueprints, ", "))
		}
		logger.Logger.Warnf("    → declare in requirements.yml or ship as a local role under roles/")
	}
	if len(collections) > 0 {
		logger.Logger.Warnf("  Missing collections:")
		for _, e := range collections {
			logger.Logger.Warnf("    %s  in: %s", e.label, strings.Join(e.blueprints, ", "))
		}
		logger.Logger.Warnf("    → declare the parent collection in requirements.yml")
	}
}

// undeclaredGroup represents one deduped row in the rendered output: a
// missing role/collection plus the blueprints that reference it.
type undeclaredGroup struct {
	label      string
	blueprints []string
}

func groupUndeclaredPayloads(deps []undeclaredDependencyPayload) (roles, collections []undeclaredGroup) {
	rolesByKey := map[string]*undeclaredGroup{}
	colsByKey := map[string]*undeclaredGroup{}
	var rolesOrder, colsOrder []string
	for _, d := range deps {
		switch d.Kind {
		case "missing_collection":
			key := d.ParentCollection
			label := d.ParentCollection + "  (refs " + d.Role + ")"
			if g, ok := colsByKey[key]; ok {
				g.blueprints = appendUniqueStr(g.blueprints, d.BlueprintID)
			} else {
				colsByKey[key] = &undeclaredGroup{label: label, blueprints: []string{d.BlueprintID}}
				colsOrder = append(colsOrder, key)
			}
		default: // missing_role or pre-Kind payloads
			key := d.Role
			if g, ok := rolesByKey[key]; ok {
				g.blueprints = appendUniqueStr(g.blueprints, d.BlueprintID)
			} else {
				rolesByKey[key] = &undeclaredGroup{label: d.Role, blueprints: []string{d.BlueprintID}}
				rolesOrder = append(rolesOrder, key)
			}
		}
	}
	for _, k := range rolesOrder {
		roles = append(roles, *rolesByKey[k])
	}
	for _, k := range colsOrder {
		collections = append(collections, *colsByKey[k])
	}
	return roles, collections
}

func appendUniqueStr(s []string, v string) []string {
	for _, e := range s {
		if e == v {
			return s
		}
	}
	return append(s, v)
}

// Source-related flag vars; reused across subcommands.
var (
	sourceFlagID          string
	sourceFlagRef         string
	sourceFlagGlobalRoles bool
	sourceFlagForce       bool
	sourceFlagPurge       bool
	sourceFlagNoPrompt    bool

	// Selection flags for `source add`. Picker is used when none are set
	// and stdin is a TTY; otherwise these drive scripted installs.
	sourceFlagBlueprints stringSliceCSV
	sourceFlagTemplates  stringSliceCSV
	sourceFlagLocalRoles stringSliceCSV
	sourceFlagAll        bool
	sourceFlagCatalog    bool
	sourceFlagDirectory  string
)

var sourceCmd = &cobra.Command{
	Use:     "source",
	Short:   "Manage sources",
	Aliases: []string{"sources"},
}

var sourceAddCmd = &cobra.Command{
	Use:   "add <url | tarball | existing-sourceID>",
	Short: "Register a source or manage what's installed from an existing one",
	Long: `Register an external source of blueprints, or manage installs from a source you've
already registered.

The positional argument is classified by shape:
  ludus source add https://github.com/foo/bar    # git URL — register + pick
  ludus source add ./source.tar.gz               # uploaded tarball/zip — register + pick
  ludus source add goad                          # existing sourceID — open the picker
  ludus source add -d ./local-source-dir         # local directory (must use -d)

Run without selection flags in a TTY to open the interactive picker. Pass
--blueprints / --templates / --source-roles for a scripted install. Pass --all
to install everything. Pass --catalog to walk the source and render its
catalog without registering anything (tables by default, raw JSON when
combined with --json).`,
	Args: cobra.MaximumNArgs(1),
	Run:  runSourceAdd,
}

func runSourceAdd(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
	flags := readSourceAddFlags()

	// Resolve the target. -d wins outright. Otherwise fall through to the
	// positional argument.
	var arg string
	switch {
	case flags.Directory != "":
		arg = flags.Directory
	case len(args) == 1:
		arg = args[0]
	default:
		logger.Logger.Fatal("source add requires a URL, tarball, or existing sourceID (or -d <dir>)")
	}

	// --catalog: walk the source (or fetch the existing-source catalog) and
	// dump the JSON. Registers nothing. Honors -d for explicit directory.
	if flags.Catalog {
		runSourceCatalogDump(client, arg, flags)
		return
	}

	mode := selectInstallMode(flags, stdinIsTerminal())

	// Detection ladder:
	//   1. -d <dir>     → register-then-install via tar+upload
	//   2. git URL      → register-then-install
	//   3. local archive → register-then-install
	//   4. existing sourceID → fetch catalog and open picker (no register)
	if flags.Directory == "" && detectSourceArg(arg) == sourceArgUnknown {
		catalog, ok := tryFetchCatalog(client, arg)
		if ok {
			runExistingSourceFlow(client, arg, catalog, flags, mode)
			return
		}
		logger.Logger.Fatalf("could not interpret %q: expected a git URL, tarball/zip path, or existing sourceID (pass -d for a local directory)", arg)
	}

	registerResp, ok, err := postSourceRegister(client, arg, flags)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !ok {
		// Transport-level failure: the rest layer already surfaced the error.
		return
	}

	if mode == modeInstallAll {
		doInstallAll(client, registerResp.SourceID, sourcepicker.Advanced{
			GlobalRoles: flags.GlobalRoles,
			Force:       flags.Force,
			IsAdmin:     clientIsAdmin(),
		})
		return
	}

	selection, advanced, committed, err := chooseSelection(mode, flags, registerResp.Catalog)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !committed {
		logger.Logger.Infof("Aborted. Run `ludus source add %s` to resume.", registerResp.SourceID)
		return
	}
	if isEmptySelection(selection) {
		// Committing with nothing selected is a no-op; the server would 400
		// such a request anyway. Skip the round-trip.
		logger.Logger.Infof("Nothing selected. Run `ludus source add %s` to resume.", registerResp.SourceID)
		return
	}

	doInstall(client, registerResp.SourceID, selection, advanced)
}

// isEmptySelection reports whether the picker committed without ticking
// anything. The server would 400 such a request; we short-circuit to keep
// the CLI message clean.
func isEmptySelection(sel dto.InstallSelectionDTO) bool {
	return len(sel.Blueprints)+len(sel.Templates)+len(sel.LocalRoles) == 0
}

// runSourceCatalogDump renders the source's catalog without registering
// anything. For an already-registered source it hits GET /sources/{id}/catalog;
// for an unregistered URL/path it registers temporarily, renders, and deletes
// the row. With --json the output is the raw catalog JSON (pipeable to jq);
// otherwise we render section-by-section tables.
func runSourceCatalogDump(client *resty.Client, arg string, flags sourceFlags) {
	// Already-registered source: just hit /catalog.
	if flags.Directory == "" && detectSourceArg(arg) == sourceArgUnknown {
		if cat, ok := tryFetchCatalog(client, arg); ok {
			emitCatalog(cat)
			return
		}
		logger.Logger.Fatalf("could not interpret %q: pass a git URL, archive path, or existing sourceID (use -d for a local directory)", arg)
	}

	// Unregistered: register, capture catalog, delete the row to leave no trace.
	registerResp, ok, err := postSourceRegister(client, arg, flags)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !ok {
		return // rest layer surfaced the error already
	}
	emitCatalog(registerResp.Catalog)
	// Best-effort cleanup — leaves a registered-but-uninstalled source on
	// failure, which the user can `rm` manually.
	rmPath := fmt.Sprintf("/sources/%s", registerResp.SourceID)
	_, _ = rest.GenericDeleteWithBody(client, buildURLWithRangeAndUserID(rmPath), []byte(`{}`))
}

func emitCatalog(cat dto.SourceCatalogDTO) {
	if jsonFormat {
		body, err := json.MarshalIndent(cat, "", "  ")
		if err != nil {
			logger.Logger.Fatal(err)
		}
		fmt.Printf("%s\n", body)
		return
	}
	renderCatalogTables(cat)
}

// renderCatalogTables prints the catalog as tablewriter sections. Each
// category gets its own heading + table; empty sections are noted in dim
// text rather than an empty table so the output stays scannable.
func renderCatalogTables(cat dto.SourceCatalogDTO) {
	if cat.SourceName != "" {
		fmt.Printf("Source: %s (%s)\n", cat.SourceName, cat.SourceID)
	} else {
		fmt.Printf("Source: %s\n", cat.SourceID)
	}

	renderBlueprintsTable(cat.Blueprints)
	renderItemsTable("Templates", cat.Templates, false)
	renderItemsTable("Source roles", cat.LocalRoles, false)
	renderItemsTable("Blueprint roles", cat.GalaxyRoles, true)
	renderItemsTable("Blueprint collections", cat.GalaxyCollections, true)
	renderItemsTable("Subscription roles", cat.SubscriptionRoles, true)

	if len(cat.UndeclaredDependencies) > 0 {
		fmt.Printf("\nUndeclared dependencies (%d)\n", len(cat.UndeclaredDependencies))
		payload := make([]undeclaredDependencyPayload, 0, len(cat.UndeclaredDependencies))
		for _, d := range cat.UndeclaredDependencies {
			payload = append(payload, undeclaredDependencyPayload{
				BlueprintID:      d.BlueprintID,
				Role:             d.Role,
				Kind:             d.Kind,
				ParentCollection: d.ParentCollection,
			})
		}
		roles, collections := groupUndeclaredPayloads(payload)
		if len(roles) > 0 {
			fmt.Println("  Missing roles:")
			for _, e := range roles {
				fmt.Printf("    %s  in: %s\n", e.label, strings.Join(e.blueprints, ", "))
			}
			fmt.Println("    → declare in requirements.yml or ship as a local role under roles/")
		}
		if len(collections) > 0 {
			fmt.Println("  Missing collections:")
			for _, e := range collections {
				fmt.Printf("    %s  in: %s\n", e.label, strings.Join(e.blueprints, ", "))
			}
			fmt.Println("    → declare the parent collection in requirements.yml")
		}
	}
}

func renderBlueprintsTable(items []dto.CatalogBlueprintDTO) {
	if len(items) == 0 {
		return // skip empty sections; user only sees what's actually shippable
	}
	fmt.Printf("\nBlueprints (%d)\n", len(items))
	t := tablewriter.NewWriter(os.Stdout)
	t.SetHeader([]string{"ID", "Name", "Version", "State"})
	for _, bp := range items {
		t.Append([]string{bp.ID, bp.Name, bp.Version, prettyState(bp.State, bp.InstalledVersion)})
	}
	t.Render()
}

// renderItemsTable handles templates / roles / collections. `withImpliedBy`
// adds a column for the blueprint(s) that pulled the item in (only useful
// for galaxy/subscription/collection sections). Empty sections are
// skipped entirely — user only sees what the source actually ships.
func renderItemsTable(heading string, items []dto.CatalogItemDTO, withImpliedBy bool) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s (%d)\n", heading, len(items))
	t := tablewriter.NewWriter(os.Stdout)
	header := []string{"Name", "Version", "State"}
	if withImpliedBy {
		header = append(header, "Required by")
	}
	t.SetHeader(header)
	for _, it := range items {
		row := []string{it.Name, it.Version, prettyState(it.State, it.InstalledVersion)}
		if withImpliedBy {
			row = append(row, strings.Join(it.ImpliedBy, ", "))
		}
		t.Append(row)
	}
	t.Render()
}

func prettyState(state, installedVersion string) string {
	switch state {
	case "installed":
		return "installed"
	case "upgrade_available":
		if installedVersion != "" {
			return "△ installed " + installedVersion + " → upgrade"
		}
		return "△ upgrade"
	case "not_installed", "":
		return "not installed"
	}
	return state
}

// tryFetchCatalog probes GET /sources/{id}/catalog; on 404 (or any other
// non-success) it returns ok=false so the caller can fall through to the
// "unknown argument" error. Other errors are not surfaced to the user from
// here — they would shadow the more-actionable "could not interpret" hint.
func tryFetchCatalog(client *resty.Client, sourceID string) (dto.SourceCatalogDTO, bool) {
	body, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/catalog"))
	if !ok {
		return dto.SourceCatalogDTO{}, false
	}
	var cat dto.SourceCatalogDTO
	if err := json.Unmarshal(body, &cat); err != nil {
		return dto.SourceCatalogDTO{}, false
	}
	if cat.SourceID == "" {
		// Defensive: a 200 with the wrong shape shouldn't be treated as a hit.
		return dto.SourceCatalogDTO{}, false
	}
	return cat, true
}

// runExistingSourceFlow drives selection + install against an already-
// registered source. Mirrors the post-register branch of runSourceAdd but
// skips the upload/git work since the source content is already on disk.
func runExistingSourceFlow(client *resty.Client, sourceID string, cat dto.SourceCatalogDTO, flags sourceFlags, mode installMode) {
	if mode == modeInstallAll {
		doInstallAll(client, sourceID, sourcepicker.Advanced{
			GlobalRoles: flags.GlobalRoles,
			Force:       flags.Force,
			IsAdmin:     clientIsAdmin(),
		})
		return
	}

	selection, advanced, committed, err := chooseSelection(mode, flags, cat)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !committed {
		logger.Logger.Info("Aborted.")
		return
	}
	if isEmptySelection(selection) {
		logger.Logger.Info("Nothing selected; nothing to install.")
		return
	}
	doInstall(client, sourceID, selection, advanced)
}

// readSourceAddFlags snapshots the package-level flag vars wired onto
// sourceAddCmd into a sourceFlags struct so the orchestration helpers stay
// pure.
func readSourceAddFlags() sourceFlags {
	return sourceFlags{
		ID:          sourceFlagID,
		Ref:         sourceFlagRef,
		Directory:   sourceFlagDirectory,
		Catalog:     sourceFlagCatalog,
		All:         sourceFlagAll,
		Blueprints:  []string(sourceFlagBlueprints),
		Templates:   []string(sourceFlagTemplates),
		LocalRoles:  []string(sourceFlagLocalRoles),
		GlobalRoles: sourceFlagGlobalRoles,
		Force:       sourceFlagForce,
	}
}

// postSourceRegister performs the multipart/upload/git registration request.
// Always register-only: the server fetches+walks and returns a
// RegisterSourceResponse with the catalog. Install is a separate call via
// doInstall.
func postSourceRegister(client *resty.Client, arg string, flags sourceFlags) (dto.RegisterSourceResponse, bool, error) {
	req := dto.CreateSourceRequest{
		ID:          flags.ID,
		GlobalRoles: flags.GlobalRoles,
		Force:       flags.Force,
	}
	var fileField, fileName string
	var fileBytes []byte

	// -d/--directory forces directory tar+upload regardless of what the path
	// looks like. Otherwise classify by URL/archive — directories must come
	// in via the flag to keep behavior consistent across source subcommands.
	switch {
	case flags.Directory != "":
		roleTar, err := tarDirectoryInMemory(arg)
		if err != nil {
			return dto.RegisterSourceResponse{}, false, fmt.Errorf("tar %s: %w", arg, err)
		}
		gz, gzErr := gzipBytes(roleTar.Bytes())
		if gzErr != nil {
			return dto.RegisterSourceResponse{}, false, fmt.Errorf("gzip tar: %w", gzErr)
		}
		fileField = "archive"
		fileBytes = gz
		fileName = filepath.Base(strings.TrimSuffix(arg, string(os.PathSeparator))) + ".tar.gz"
		req.Type = "upload"
	default:
		switch detectSourceArg(arg) {
		case sourceArgGit:
			req.Type = "git"
			req.URL = arg
			req.Ref = flags.Ref
		case sourceArgArchive:
			data, err := os.ReadFile(arg)
			if err != nil {
				return dto.RegisterSourceResponse{}, false, err
			}
			fileField = "archive"
			fileBytes = data
			fileName = filepath.Base(arg)
			req.Type = "upload"
		default:
			return dto.RegisterSourceResponse{}, false, fmt.Errorf("could not interpret %q: expected a git URL or tarball/zip path (use -d for a local directory)", arg)
		}
	}

	endpoint := buildURLWithRangeAndUserID("/sources")
	var responseJSON []byte
	var success bool
	if fileField != "" {
		responseJSON, success = rest.FileUpload(client, "POST", endpoint, fileField, fileName, fileBytes, createSourceRequestToForm(req))
	} else {
		responseJSON, success = rest.GenericJSONPost(client, endpoint, req)
	}

	if !success {
		// Transport-level failure; rest layer already surfaced the error.
		return dto.RegisterSourceResponse{}, false, nil
	}

	var resp dto.RegisterSourceResponse
	if err := json.Unmarshal(responseJSON, &resp); err != nil {
		return dto.RegisterSourceResponse{}, false, fmt.Errorf("decode register response: %w", err)
	}
	return resp, true, nil
}

func chooseSelection(mode installMode, flags sourceFlags, cat dto.SourceCatalogDTO) (dto.InstallSelectionDTO, sourcepicker.Advanced, bool, error) {
	switch mode {
	case modeScripted:
		return dto.InstallSelectionDTO{
				Blueprints: flags.Blueprints,
				Templates:  flags.Templates,
				LocalRoles: flags.LocalRoles,
			}, sourcepicker.Advanced{
				GlobalRoles: flags.GlobalRoles,
				Force:       flags.Force,
				IsAdmin:     clientIsAdmin(),
			}, true, nil
	case modeInteractive:
		return sourcepicker.Run(cat, dto.InstallSelectionDTO{}, sourcepicker.Advanced{
			GlobalRoles: flags.GlobalRoles,
			Force:       flags.Force,
			IsAdmin:     clientIsAdmin(),
		})
	default:
		return dto.InstallSelectionDTO{}, sourcepicker.Advanced{}, false, fmt.Errorf("unreachable: mode %v", mode)
	}
}

// clientIsAdmin is currently a stub — the server already gates --global-roles
// for non-admins, so this only affects the picker's display.
func clientIsAdmin() bool { return true }

func doInstall(client *resty.Client, sourceID string, sel dto.InstallSelectionDTO, adv sourcepicker.Advanced) {
	postInstall(client, sourceID, dto.InstallRequest{
		Selection:   sel,
		GlobalRoles: adv.GlobalRoles,
		Force:       adv.Force,
	})
}

// doInstallAll is the "install everything walked" shortcut used by --all
// and non-TTY invocations. The server expands installAll=true into a sync
// with no selection scope.
func doInstallAll(client *resty.Client, sourceID string, adv sourcepicker.Advanced) {
	postInstall(client, sourceID, dto.InstallRequest{
		InstallAll:  true,
		GlobalRoles: adv.GlobalRoles,
		Force:       adv.Force,
	})
}

func postInstall(client *resty.Client, sourceID string, body dto.InstallRequest) {
	resp, ok := rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/install"), body)
	if didFailOrWantJSON(ok, resp) {
		return
	}
	var sync syncResultPayload
	if err := json.Unmarshal(resp, &sync); err != nil || sync.SourceID == "" {
		logger.Logger.Info(string(resp))
		return
	}
	printSyncFailures(fmt.Sprintf("Source '%s'", sourceID), "installed", sync)
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
)

// detectSourceArg classifies a positional source argument. URL wins outright;
// regular files with an archive suffix are treated as archives. Directories
// do not auto-detect — they must be passed via `-d`/`--directory` so source
// add and source update behave consistently.
func detectSourceArg(arg string) sourceArgKind {
	if isSourceURL(arg) {
		return sourceArgGit
	}
	info, err := os.Stat(arg)
	if err != nil {
		return sourceArgUnknown
	}
	if info.IsDir() {
		return sourceArgUnknown
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
		table.SetHeader([]string{"Source ID", "Name", "Owner", "Type", "Authors", "Last Synced", "Status"})
		for _, s := range sources {
			table.Append([]string{
				s.SourceID,
				s.Name,
				s.OwnerUserID,
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
	})

	// Multi-target syncs without a header leave per-source errors floating
	// — the rest layer's "Error from server!" line says nothing about which
	// source failed. Prepend a counted divider per source so success and
	// failure both attach to a sourceID, and the user knows how far along
	// the batch is. The rest layer already spins during each HTTP call.
	total := len(targets)
	if total > 1 {
		fmt.Printf("Syncing %d git sources...\n", total)
	}
	for i, sid := range targets {
		if total > 1 {
			fmt.Printf("\n== [%d/%d] %s ==\n", i+1, total, sid)
		}
		path := fmt.Sprintf("/sources/%s/sync", sid)
		responseJSON, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID(path), string(body))
		if didFailOrWantJSON(success, responseJSON) {
			continue
		}
		var resp syncResultPayload
		if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.SourceID == "" {
			logger.Logger.Info(string(responseJSON))
			continue
		}
		printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), "synced", resp)
	}
}

var sourceUpdateCmd = &cobra.Command{
	Use:   "update <sourceID> [<tarball>]",
	Short: "Change a source's tracked ref or content",
	Long: `Update a source.

  ludus source update <id> --ref main           # git: change tracked ref
  ludus source update <id> ./new-source.tar.gz  # upload: replace content
  ludus source update <id> -d ./source-dir      # upload: tar a directory and replace`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSourceUpdate,
}

func runSourceUpdate(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
	sid := args[0]
	path := fmt.Sprintf("/sources/%s", sid)

	if len(args) == 2 && sourceFlagDirectory != "" {
		logger.Logger.Fatal("pass either a positional tarball path or -d <dir>, not both")
	}

	var fileField, fileName string
	var fileBytes []byte
	switch {
	case sourceFlagDirectory != "":
		roleTar, err := tarDirectoryInMemory(sourceFlagDirectory)
		if err != nil {
			logger.Logger.Fatal(err)
		}
		gz, gzErr := gzipBytes(roleTar.Bytes())
		if gzErr != nil {
			logger.Logger.Fatal(gzErr)
		}
		fileField = "archive"
		fileBytes = gz
		fileName = filepath.Base(strings.TrimSuffix(sourceFlagDirectory, string(os.PathSeparator))) + ".tar.gz"
	case len(args) == 2:
		switch detectSourceArg(args[1]) {
		case sourceArgArchive:
			data, err := os.ReadFile(args[1])
			if err != nil {
				logger.Logger.Fatal(err)
			}
			fileField = "archive"
			fileBytes = data
			fileName = filepath.Base(args[1])
		default:
			logger.Logger.Fatalf("could not interpret %q: expected a tarball/zip path (use -d for a local directory)", args[1])
		}
	}

	if fileField == "" && sourceFlagRef == "" {
		logger.Logger.Fatal("provide --ref (git) or a tarball path / -d <dir> (upload)")
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
		printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), "updated", resp)
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
	// Cobra alphabetizes flags in --help by default; keep registration
	// order so related flags stay grouped: identifiers, then directory
	// override, then the selection block, then the catalog read-only
	// view, and finally execution modifiers.
	sourceAddCmd.Flags().SortFlags = false
	sourceAddCmd.Flags().StringVar(&sourceFlagID, "id", "", "explicit sourceID; overrides auto-derived slug")
	sourceAddCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "git branch/tag/commit (git sources only)")
	sourceAddCmd.Flags().StringVarP(&sourceFlagDirectory, "directory", "d", "", "treat the value as a local directory (required for directory uploads)")

	// Selection group — what to install from the source.
	sourceAddCmd.Flags().BoolVar(&sourceFlagAll, "all", false, "install everything from the source")
	sourceAddCmd.Flags().Var(&sourceFlagBlueprints, "blueprints", "blueprint IDs to install (CSV or repeated)")
	sourceAddCmd.Flags().Var(&sourceFlagTemplates, "templates", "template names to install (CSV or repeated)")
	sourceAddCmd.Flags().Var(&sourceFlagLocalRoles, "source-roles", "source role names to install (CSV or repeated)")

	sourceAddCmd.Flags().BoolVar(&sourceFlagCatalog, "catalog", false, "walk the source and render its catalog (tables by default, JSON with --json); registers nothing")

	sourceAddCmd.Flags().BoolVar(&sourceFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide")
	sourceAddCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "overwrite already-installed templates and galaxy/local roles")

	// Sync flags (reuses sourceFlagRef, etc. from add).
	sourceSyncCmd.Flags().BoolVar(&sourceFlagGlobalRoles, "global-roles", false, "admin only: install roles instance-wide")
	sourceSyncCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "overwrite already-installed templates and galaxy/local roles")

	// Update flags.
	sourceUpdateCmd.Flags().SortFlags = false
	sourceUpdateCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "new git branch/tag/commit (git sources)")
	sourceUpdateCmd.Flags().StringVarP(&sourceFlagDirectory, "directory", "d", "", "tar a local directory and upload it as the new source content (upload sources)")
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
