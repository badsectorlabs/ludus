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
	"sort"
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
	LocalCollectionResults []artifactResultPayload       `json:"localCollectionResults"`
	AnsibleResults         []ansibleResultPayload        `json:"ansibleResults"`
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
	if req.Global {
		form["global"] = "true"
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
	if req.Global {
		form["global"] = "true"
	}
	if req.Force {
		form["force"] = "true"
	}
	return form
}

type ansibleResultPayload struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Type  string `json:"type,omitempty"`
}

func collectArtifactFailureLines(templates, localRoles, localCollections []artifactResultPayload, ansible []ansibleResultPayload) []string {
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
	for _, r := range localCollections {
		if !r.OK {
			failures = append(failures, fmt.Sprintf("local_collection %s: %s", r.Name, r.Message))
		}
	}
	for _, r := range ansible {
		if !r.OK {
			kind := r.Type
			if kind == "" {
				kind = "role"
			}
			failures = append(failures, fmt.Sprintf("%s %s: %s", kind, r.Name, r.Error))
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
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalRoleResults, p.LocalCollectionResults, p.AnsibleResults)
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
	sourceFlagID       string
	sourceFlagRef      string
	sourceFlagGlobal   bool
	sourceFlagForce    bool
	sourceFlagNoDeps   bool
	sourceFlagNoPrompt bool

	// Selection flags for `source add`. Picker is used when none are set
	// and stdin is a TTY; otherwise these drive scripted installs.
	sourceFlagBlueprints       stringSliceCSV
	sourceFlagTemplates        stringSliceCSV
	sourceFlagLocalRoles       stringSliceCSV
	sourceFlagLocalCollections stringSliceCSV
	sourceFlagAll              bool
	sourceFlagCatalog          bool
	sourceFlagDirectory        string
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
--blueprints / --templates / --source-roles / --source-collections for a scripted install. Pass --all
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
			runExistingSourceFlow(client, arg, catalog, flags, mode, sourcepicker.ModeInstall)
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
		doInstallAll(client, registerResp.SourceID, registerResp.Catalog, sourcepicker.Advanced{
			Global:  flags.Global,
			Force:   flags.Force,
			IsAdmin: clientIsAdmin(),
			NoDeps:  flags.NoDeps,
		})
		return
	}

	selection, advanced, committed, proceed, err := chooseSelection(mode, sourcepicker.ModeInstall, flags, registerResp.Catalog)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !committed {
		logger.Logger.Infof("Aborted. Run `ludus source add %s` to resume.", registerResp.SourceID)
		return
	}
	if !proceed {
		// Nothing picked to install — skip the round-trip.
		logger.Logger.Infof("Nothing selected. Run `ludus source add %s` to resume.", registerResp.SourceID)
		return
	}

	doInstall(client, registerResp.SourceID, selection, advanced, "installed")
}

// isEmptySelection reports whether nothing was picked — the install path's
// "nothing to do" gate. (Distinct from a present-but-empty selection sent to
// the server, which is the prune-all signal.)
func isEmptySelection(sel dto.InstallSelectionDTO) bool {
	return len(sel.Blueprints)+len(sel.Templates)+len(sel.LocalRoles)+len(sel.LocalCollections) == 0
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

	renderBlueprintsTable(cat.Blueprints.Items)
	renderItemsTable("Templates", cat.Templates, false)
	renderItemsTable("Source roles", cat.LocalRoles, false)
	renderItemsTable("Blueprint role requirements", cat.Blueprints.RequiredRoles, true)
	renderItemsTable("Blueprint collection requirements", cat.Blueprints.RequiredCollections, true)
	renderItemsTable("Subscription roles", cat.Blueprints.SubscriptionRoles, true)

	if len(cat.Blueprints.UndeclaredDependencies) > 0 {
		fmt.Printf("\nUndeclared dependencies (%d)\n", len(cat.Blueprints.UndeclaredDependencies))
		payload := make([]undeclaredDependencyPayload, 0, len(cat.Blueprints.UndeclaredDependencies))
		for _, d := range cat.Blueprints.UndeclaredDependencies {
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

// renderItemsTable handles templates / roles / collections. `withRequiredBy`
// adds a column for the blueprint(s) that pulled the item in (only useful
// for galaxy/subscription/collection sections). Empty sections are
// skipped entirely — user only sees what the source actually ships.
func renderItemsTable(heading string, items []dto.CatalogItemDTO, withRequiredBy bool) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s (%d)\n", heading, len(items))
	t := tablewriter.NewWriter(os.Stdout)
	header := []string{"Name", "Version", "State"}
	if withRequiredBy {
		header = append(header, "Required by")
	}
	t.SetHeader(header)
	for _, it := range items {
		row := []string{it.Name, it.Version, prettyState(it.State, it.InstalledVersion)}
		if withRequiredBy {
			row = append(row, strings.Join(it.RequiredBy, ", "))
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
func runExistingSourceFlow(client *resty.Client, sourceID string, cat dto.SourceCatalogDTO, flags sourceFlags, mode installMode, intent sourcepicker.Mode) {
	if mode == modeInstallAll {
		doInstallAll(client, sourceID, cat, sourcepicker.Advanced{
			Global:  flags.Global,
			Force:   flags.Force,
			IsAdmin: clientIsAdmin(),
			NoDeps:  flags.NoDeps,
		})
		return
	}

	selection, advanced, committed, proceed, err := chooseSelection(mode, intent, flags, cat)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !committed {
		logger.Logger.Info("Aborted.")
		return
	}
	if !proceed {
		if intent == sourcepicker.ModeRemove {
			logger.Logger.Info("Nothing selected; nothing to remove.")
		} else {
			logger.Logger.Info("Nothing selected; nothing to install.")
		}
		return
	}
	doInstall(client, sourceID, selection, advanced, installVerbPast(intent))
}

// readSourceAddFlags snapshots the package-level flag vars wired onto
// sourceAddCmd into a sourceFlags struct so the orchestration helpers stay
// pure.
func readSourceAddFlags() sourceFlags {
	return sourceFlags{
		ID:               sourceFlagID,
		Ref:              sourceFlagRef,
		Directory:        sourceFlagDirectory,
		Catalog:          sourceFlagCatalog,
		All:              sourceFlagAll,
		Blueprints:       []string(sourceFlagBlueprints),
		Templates:        []string(sourceFlagTemplates),
		LocalRoles:       []string(sourceFlagLocalRoles),
		LocalCollections: []string(sourceFlagLocalCollections),
		Global:           sourceFlagGlobal,
		Force:            sourceFlagForce,
		NoDeps:           sourceFlagNoDeps,
	}
}

// postSourceRegister performs the multipart/upload/git registration request.
// Always register-only: the server fetches+walks and returns a
// RegisterSourceResponse with the catalog. Install is a separate call via
// doInstall.
func postSourceRegister(client *resty.Client, arg string, flags sourceFlags) (dto.RegisterSourceResponse, bool, error) {
	req := dto.CreateSourceRequest{
		ID:     flags.ID,
		Global: flags.Global,
		Force:  flags.Force,
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

// chooseSelection resolves the selection to POST. intent (install vs remove)
// only matters for the interactive picker: it drives the picker's mode and
// how the picked intent set folds against the current install state.
//
// Returns (selection, advanced, committed, proceed, err):
//   - committed is false only when the user aborted the picker.
//   - proceed is false when there is nothing to do (no items picked / scripted
//     selection empty); the caller prints a "nothing selected" notice and
//     skips the round-trip. A remove that drops every installed item still
//     proceeds — its empty selection is the server's prune-all signal.
func chooseSelection(mode installMode, intent sourcepicker.Mode, flags sourceFlags, cat dto.SourceCatalogDTO) (dto.InstallSelectionDTO, sourcepicker.Advanced, bool, bool, error) {
	adv := sourcepicker.Advanced{
		Global:  flags.Global,
		Force:   flags.Force,
		IsAdmin: clientIsAdmin(),
		NoDeps:  flags.NoDeps,
	}
	switch mode {
	case modeScripted:
		sel := dto.InstallSelectionDTO{
			Blueprints:       flags.Blueprints,
			Templates:        flags.Templates,
			LocalRoles:       flags.LocalRoles,
			LocalCollections: flags.LocalCollections,
		}
		return sel, adv, true, !isEmptySelection(sel), nil
	case modeInteractive:
		// The picker opens with nothing checked and returns the user's intent
		// set (items to install, or to drop). An empty intent means "nothing
		// to do"; a non-empty intent folds against the current install state
		// to build the wire selection.
		picked, advOut, committed, err := sourcepicker.Run(cat, intent, adv)
		if err != nil || !committed {
			return dto.InstallSelectionDTO{}, advOut, committed, false, err
		}
		if isEmptySelection(picked) {
			return dto.InstallSelectionDTO{}, advOut, true, false, nil
		}
		return composeInteractiveSelection(intent, cat, picked), advOut, true, true, nil
	default:
		return dto.InstallSelectionDTO{}, adv, false, false, fmt.Errorf("unreachable: mode %v", mode)
	}
}

// deriveCurrentSelection rebuilds the source's installed-selection from
// the catalog's per-item state. composeInteractiveSelection folds the
// picker's intent set against it (install adds to it, remove subtracts from
// it). Items in state "installed" or "upgrade_available" count as currently
// installed; "not_installed" do not. Note: this can drift from the server's persisted installSelection
// when upstream has removed an item the user previously selected — the
// orphan won't appear in the catalog walk so we can't surface it here.
// Acceptable: orphans aren't actionable from this UI anyway (the files
// they reference no longer exist upstream).
func deriveCurrentSelection(cat dto.SourceCatalogDTO) dto.InstallSelectionDTO {
	out := dto.InstallSelectionDTO{}
	for _, bp := range cat.Blueprints.Items {
		if bp.State == "installed" || bp.State == "upgrade_available" {
			out.Blueprints = append(out.Blueprints, bp.ID)
		}
	}
	for _, t := range cat.Templates {
		if t.State == "installed" || t.State == "upgrade_available" {
			out.Templates = append(out.Templates, t.Name)
		}
	}
	for _, r := range cat.LocalRoles {
		if r.State == "installed" || r.State == "upgrade_available" {
			out.LocalRoles = append(out.LocalRoles, r.Name)
		}
	}
	for _, c := range cat.LocalCollections {
		if c.State == "installed" || c.State == "upgrade_available" {
			out.LocalCollections = append(out.LocalCollections, c.Name)
		}
	}
	return out
}

// clientIsAdmin is currently a stub — the server already gates --global
// for non-admins, so this only affects the picker's display.
func clientIsAdmin() bool { return true }

// installVerbPast returns the past-tense verb for the result summary, so a
// removal reports "removed successfully" rather than "installed". The /install
// endpoint is intent-agnostic (it just reconciles the selection); the verb is
// the client's view of what the user asked for.
func installVerbPast(intent sourcepicker.Mode) string {
	if intent == sourcepicker.ModeRemove {
		return "removed"
	}
	return "installed"
}

func doInstall(client *resty.Client, sourceID string, sel dto.InstallSelectionDTO, adv sourcepicker.Advanced, verbPast string) {
	postInstall(client, sourceID, dto.InstallRequest{
		Selection: &sel,
		Global:    adv.Global,
		Force:     adv.Force,
		NoDeps:    adv.NoDeps,
	}, verbPast)
}

// doInstallAll is the "install everything currently walked" shortcut used
// by --all and non-TTY invocations. The server treats an omitted selection
// as "snapshot the current walk into installSelection," so the persisted
// state is a concrete list of names and future syncs don't drift with
// upstream changes.
func doInstallAll(client *resty.Client, sourceID string, _ dto.SourceCatalogDTO, adv sourcepicker.Advanced) {
	// install-all is only ever an install path (remove builds an explicit
	// selection), so the verb is fixed.
	postInstall(client, sourceID, dto.InstallRequest{
		Global: adv.Global,
		Force:  adv.Force,
		NoDeps: adv.NoDeps,
	}, "installed")
}

func postInstall(client *resty.Client, sourceID string, body dto.InstallRequest, verbPast string) {
	resp, ok := rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/install"), body)
	if didFailOrWantJSON(ok, resp) {
		return
	}
	var sync syncResultPayload
	if err := json.Unmarshal(resp, &sync); err != nil || sync.SourceID == "" {
		logger.Logger.Info(string(resp))
		return
	}
	printSyncFailures(fmt.Sprintf("Source '%s'", sourceID), verbPast, sync)
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

// runSourceDetail prints metadata followed by the source's catalog (what
// upstream ships, joined with which items are currently installed). Sync
// is read-only, so "installed" and "available" can drift between syncs —
// the State column on each row tells the user which is which and whether
// an upgrade is waiting.
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

	catalog, ok := tryFetchCatalog(client, sourceID)
	if !ok {
		logger.Logger.Warnf("Could not fetch catalog for %q; showing source metadata only.", sourceID)
		return
	}
	renderCatalogTables(catalog)
}

func printField(label, value string) {
	if value == "" {
		return
	}
	fmt.Printf("  %-13s %s\n", label+":", value)
}

var sourceSyncCmd = &cobra.Command{
	Use:   "sync [<sourceID>]",
	Short: "Re-pull a git source and refresh its catalog",
	Long: `Refresh the catalog view of a git source by re-pulling its working tree.
Sync is read-only — it does NOT install, update, or remove any blueprints,
templates, or roles. To apply upstream changes, re-run 'ludus source add
<sourceID>' to commit a fresh selection. Upload sources don't sync — push
a new tarball with 'ludus source update' instead.`,
	Args: cobra.MaximumNArgs(1),
	Run:  runSourceSync,
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
		Global: sourceFlagGlobal,
		Force:  sourceFlagForce,
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
			Global: sourceFlagGlobal,
			Force:  sourceFlagForce,
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

var sourceRemoveCmd = &cobra.Command{
	Use:     "remove <sourceID>",
	Short:   "Remove items from an installed source (or open the picker to manage)",
	Aliases: []string{"uninstall"},
	Long: `Drop items from a source's installed selection. The source itself stays
registered; only the named items get removed. Files unique to this source
are deleted from disk; items still claimed by another source keep their
files.

  ludus source remove <id>                              # open the picker showing installed items; check what you want to drop
  ludus source remove <id> --blueprints A,B             # scripted: drop blueprints A and B from the current selection
  ludus source remove <id> --templates T1               # scripted: drop template T1
  ludus source remove <id> --source-roles R1            # scripted: drop local role R1
  ludus source remove <id> --source-collections ns.col  # scripted: drop local collection ns.col
  ludus source remove <id> --all                        # scripted: drop every item (the source stays registered)

To DELETE the source entirely (registration + every installed item),
use 'ludus source rm <id>' instead.`,
	Args: cobra.ExactArgs(1),
	Run:  runSourceRemove,
}

func runSourceRemove(cmd *cobra.Command, args []string) {
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
	sid := args[0]

	dropBlueprints := []string(sourceFlagBlueprints)
	dropTemplates := []string(sourceFlagTemplates)
	dropLocalRoles := []string(sourceFlagLocalRoles)
	dropAll := sourceFlagAll

	// No flags: open the picker against the existing source in remove mode.
	// It shows only installed items, all unchecked; the user checks what to
	// drop. The picked items are subtracted from the persisted selection.
	if !dropAll && len(dropBlueprints)+len(dropTemplates)+len(dropLocalRoles) == 0 {
		catalog, ok := tryFetchCatalog(client, sid)
		if !ok {
			logger.Logger.Fatalf("Could not fetch catalog for source %q", sid)
		}
		runExistingSourceFlow(client, sid, catalog, sourceFlags{
			Global: sourceFlagGlobal,
			Force:  sourceFlagForce,
		}, modeInteractive, sourcepicker.ModeRemove)
		return
	}

	var newSelection dto.InstallSelectionDTO
	if dropAll {
		// Explicit empty selection — server-side this is the "uninstall
		// everything from this source" signal. The wrapping struct itself
		// has to be present, just with empty arrays.
		newSelection = dto.InstallSelectionDTO{
			Blueprints: []string{},
			Templates:  []string{},
			LocalRoles: []string{},
		}
	} else {
		// Derive the current installed selection from the catalog (state
		// = installed or upgrade_available), then subtract the names the
		// user wants to drop. The result is the post-uninstall selection.
		catalog, ok := tryFetchCatalog(client, sid)
		if !ok {
			logger.Logger.Fatalf("Could not fetch catalog for source %q", sid)
		}
		newSelection = dto.InstallSelectionDTO{
			Blueprints: subtractStrings(installedBlueprintIDs(catalog), dropBlueprints),
			Templates:  subtractStrings(installedItemNames(catalog.Templates), dropTemplates),
			LocalRoles: subtractStrings(installedItemNames(catalog.LocalRoles), dropLocalRoles),
		}
	}

	postInstall(client, sid, dto.InstallRequest{
		Selection: &newSelection,
		Global:    sourceFlagGlobal,
		Force:     sourceFlagForce,
	}, "removed")
}

// installedBlueprintIDs returns the sourceBlueprintID of every catalog
// blueprint currently installed (or in upgrade-available state).
func installedBlueprintIDs(cat dto.SourceCatalogDTO) []string {
	out := make([]string, 0, len(cat.Blueprints.Items))
	for _, bp := range cat.Blueprints.Items {
		if bp.State == "installed" || bp.State == "upgrade_available" {
			out = append(out, bp.ID)
		}
	}
	return out
}

// installedItemNames returns the name of every catalog item currently
// installed (or in upgrade-available state).
func installedItemNames(items []dto.CatalogItemDTO) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.State == "installed" || it.State == "upgrade_available" {
			out = append(out, it.Name)
		}
	}
	return out
}

// subtractStrings returns base with every element of remove dropped.
// Order in base is preserved; duplicates in remove are tolerated.
func subtractStrings(base, remove []string) []string {
	if len(remove) == 0 {
		return base
	}
	skip := make(map[string]struct{}, len(remove))
	for _, r := range remove {
		skip[r] = struct{}{}
	}
	out := make([]string, 0, len(base))
	for _, b := range base {
		if _, drop := skip[b]; drop {
			continue
		}
		out = append(out, b)
	}
	return out
}

// unionStrings returns the sorted, de-duplicated union of a and b. Used to
// fold freshly-picked install items into the set already installed so an
// install never drops an item the user simply left unchecked.
func unionStrings(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]string, 0, len(a)+len(b))
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// composeInteractiveSelection turns the picker's intent set into the final
// desired selection to POST, given the catalog's current install state.
// The interactive picker no longer pre-checks installed items — checking a
// box expresses intent for the current command, not the desired end-state —
// so we reconstruct the end-state here:
//
//	Install: keep everything currently installed, ADD the picked items.
//	Remove:  keep everything currently installed, DROP the picked items.
//
// Remove that drops every installed item yields an empty-but-present
// selection (the server's prune-all signal); the caller's intent gate, not
// this result's emptiness, guards the no-op case.
func composeInteractiveSelection(mode sourcepicker.Mode, cat dto.SourceCatalogDTO, picked dto.InstallSelectionDTO) dto.InstallSelectionDTO {
	current := deriveCurrentSelection(cat)
	if mode == sourcepicker.ModeRemove {
		return dto.InstallSelectionDTO{
			Blueprints:       subtractStrings(current.Blueprints, picked.Blueprints),
			Templates:        subtractStrings(current.Templates, picked.Templates),
			LocalRoles:       subtractStrings(current.LocalRoles, picked.LocalRoles),
			LocalCollections: subtractStrings(current.LocalCollections, picked.LocalCollections),
		}
	}
	return dto.InstallSelectionDTO{
		Blueprints:       unionStrings(current.Blueprints, picked.Blueprints),
		Templates:        unionStrings(current.Templates, picked.Templates),
		LocalRoles:       unionStrings(current.LocalRoles, picked.LocalRoles),
		LocalCollections: unionStrings(current.LocalCollections, picked.LocalCollections),
	}
}

var sourceRmCmd = &cobra.Command{
	Use:     "rm <sourceID>",
	Short:   "Delete a source (its registration + blueprints; installed templates/roles stay)",
	Aliases: []string{"delete", "del"},
	Long: `Delete the source from Ludus. Drops the source record and the blueprints
it provided.

Installed templates, roles, and collections are left on disk — templates live
in your per-user packer dir, and roles/collections may be shared with ranges
or other blueprints. To uninstall those, use 'ludus source remove' (de-select),
which now cleans up source roles AND collections, or remove individual items
with the ansible/templates commands (e.g. 'ludus ansible collection rm').`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		// Skip the prompt automatically when stdin is piped/non-TTY (CI, scripts).
		// Callers can also force-skip with --no-prompt.
		if !sourceFlagNoPrompt && stdinIsTerminal() {
			fmt.Printf("Remove source '%s' and its blueprints? Installed templates, roles, and collections stay on disk. [y/N]: ", args[0])
			var resp string
			_, _ = fmt.Scanln(&resp)
			if !strings.EqualFold(strings.TrimSpace(resp), "y") {
				logger.Logger.Info("Aborted.")
				return
			}
		}
		path := fmt.Sprintf("/sources/%s", args[0])
		responseJSON, success := rest.GenericDelete(client, buildURLWithRangeAndUserID(path))
		if didFailOrWantJSON(success, responseJSON) {
			return
		}
		logger.Logger.Infof("Source %q removed. Its blueprints are gone; installed templates, roles, and collections remain on disk.", args[0])
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
	sourceAddCmd.Flags().Var(&sourceFlagLocalCollections, "source-collections", "source collection FQCNs to install (CSV or repeated)")

	sourceAddCmd.Flags().BoolVar(&sourceFlagCatalog, "catalog", false, "walk the source and render its catalog (tables by default, JSON with --json); registers nothing")

	sourceAddCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users")
	sourceAddCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install: re-extract templates and source roles, and rerun ansible-galaxy with -f for galaxy roles and collections")
	sourceAddCmd.Flags().BoolVar(&sourceFlagNoDeps, "no-deps", false, "skip installing blueprint galaxy role/collection dependencies; use only what's already on disk")

	// Sync flags (reuses sourceFlagRef, etc. from add).
	sourceSyncCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users")
	sourceSyncCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install: re-extract templates and source roles, and rerun ansible-galaxy with -f for galaxy roles and collections")

	// Update flags.
	sourceUpdateCmd.Flags().SortFlags = false
	sourceUpdateCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "new git branch/tag/commit (git sources)")
	sourceUpdateCmd.Flags().StringVarP(&sourceFlagDirectory, "directory", "d", "", "tar a local directory and upload it as the new source content (upload sources)")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users (upload only)")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install when a new archive triggers an inline reinstall (upload only)")

	// Rm flags.
	sourceRmCmd.Flags().BoolVar(&sourceFlagNoPrompt, "no-prompt", false, "skip confirmation prompt")

	// Remove flags. Re-uses sourceFlag* declared above so the picker-style
	// selection vocabulary is the same on both add and remove. No flags →
	// opens the picker (managed via runSourceRemove).
	sourceRemoveCmd.Flags().SortFlags = false
	sourceRemoveCmd.Flags().Var(&sourceFlagBlueprints, "blueprints", "blueprint IDs to drop from the current selection (CSV or repeated)")
	sourceRemoveCmd.Flags().Var(&sourceFlagTemplates, "templates", "template names to drop (CSV or repeated)")
	sourceRemoveCmd.Flags().Var(&sourceFlagLocalRoles, "source-roles", "source role names to drop (CSV or repeated)")
	sourceRemoveCmd.Flags().Var(&sourceFlagLocalCollections, "source-collections", "source collection FQCNs to drop (CSV or repeated)")
	sourceRemoveCmd.Flags().BoolVar(&sourceFlagAll, "all", false, "drop every item from this source (the source itself stays registered)")
	sourceRemoveCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: also affect roles and collections installed instance-wide")
	sourceRemoveCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "no-op for remove; kept for symmetry with add")

	sourceCmd.AddCommand(sourceAddCmd)
	sourceCmd.AddCommand(
		sourceListCmd,
		sourceSyncCmd,
		sourceUpdateCmd,
		sourceRemoveCmd,
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
