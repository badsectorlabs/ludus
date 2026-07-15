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
	"net/http"
	neturl "net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-resty/resty/v2"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/term"
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
// POST /sources and POST /sources/{id}/sync. Results are grouped by the
// source-repo dir the content came from: templates/, ansible/ (vendored
// roles + collections), and the blueprints' galaxy dependency closure.
type syncResultPayload struct {
	SourceID            string                     `json:"sourceID"`
	TemplateResults     []artifactResultPayload    `json:"templateResults"`
	LocalAnsibleResults localAnsibleResultsPayload `json:"localAnsibleResults"`
	BlueprintResults    blueprintResultsPayload    `json:"blueprintResults"`
}

type localAnsibleResultsPayload struct {
	RoleResults       []artifactResultPayload `json:"roleResults"`
	CollectionResults []artifactResultPayload `json:"collectionResults"`
}

type blueprintResultsPayload struct {
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
	failures := collectArtifactFailureLines(p.TemplateResults, p.LocalAnsibleResults.RoleResults, p.LocalAnsibleResults.CollectionResults, p.BlueprintResults.AnsibleResults)
	printArtifactOutcome(label, verbPast+" successfully", verbPast+" with errors", failures)
	printUndeclaredDependencies(p.BlueprintResults.UndeclaredDependencies)
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
to install everything. To inspect a registered source's catalog without
installing, use 'ludus source list <sourceID> --catalog'.`,
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
		doInstallAll(client, registerResp.SourceID, registerResp.Catalog, sourcepicker.Advanced{
			Global:  flags.Global,
			Force:   flags.Force,
			IsAdmin: true, // picker display only — the server gates --global
			NoDeps:  flags.NoDeps,
		})
		return
	}

	selection, advanced, committed, proceed, err := chooseSelection(mode, flags, registerResp.Catalog)
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

	doInstall(client, registerResp.SourceID, selection, advanced)
}

// isEmptySelection reports whether nothing was picked — the install path's
// "nothing to do" gate. (Distinct from a present-but-empty selection sent to
// the server, which is the prune-all signal.)
func isEmptySelection(sel dto.InstallSelectionDTO) bool {
	return len(sel.Blueprints)+len(sel.Templates)+len(sel.LocalRoles)+len(sel.LocalCollections) == 0
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
	renderItemsTable("Templates", cat.Templates)
	renderAnsibleTable(cat)

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

// renderItemsTable renders one catalog item list as a table. Empty sections
// are skipped entirely — user only sees what the source actually ships.
// No Version column: templates carry no version, and the installed version
// (when one matters) is surfaced inline by prettyState in the State column.
func renderItemsTable(heading string, items []dto.CatalogItemDTO) {
	if len(items) == 0 {
		return
	}
	fmt.Printf("\n%s (%d)\n", heading, len(items))
	t := tablewriter.NewWriter(os.Stdout)
	t.SetHeader([]string{"Name", "State"})
	for _, it := range items {
		t.Append([]string{it.Name, prettyState(it.State, it.InstalledVersion)})
	}
	t.Render()
}

// renderAnsibleTable lists the Ansible content the source vendors — the roles
// and collections under its ansible/ dir.
func renderAnsibleTable(cat dto.SourceCatalogDTO) {
	type ansibleRow struct {
		kind string
		item dto.CatalogItemDTO
	}
	var rows []ansibleRow
	for _, it := range cat.LocalRoles {
		rows = append(rows, ansibleRow{"role", it})
	}
	for _, it := range cat.LocalCollections {
		rows = append(rows, ansibleRow{"collection", it})
	}
	if len(rows) == 0 {
		return
	}
	fmt.Printf("\nAnsible (%d)\n", len(rows))
	t := tablewriter.NewWriter(os.Stdout)
	t.SetHeader([]string{"Name", "Kind", "Version", "Install State"})
	for _, r := range rows {
		version := r.item.Version
		if version == "" {
			version = "-"
		}
		t.Append([]string{
			r.item.Name,
			r.kind,
			version,
			scopedInstallState(r.item),
		})
	}
	t.Render()
}

// scopedInstallState renders where an item is installed, one entry per copy:
// "(global, v1.0.0), (user, v1.2.0)". A copy with no recorded version (local
// roles are versionless) is just its bare scope name, e.g. "global, user".
// Items without per-scope data fall back to a plain installed/not installed.
func scopedInstallState(it dto.CatalogItemDTO) string {
	if len(it.Scopes) > 0 {
		parts := make([]string, 0, len(it.Scopes))
		for _, s := range it.Scopes {
			if s.Version != "" {
				parts = append(parts, fmt.Sprintf("(%s, v%s)", s.Scope, strings.TrimPrefix(s.Version, "v")))
			} else {
				parts = append(parts, s.Scope)
			}
		}
		return strings.Join(parts, ", ")
	}
	if it.State == "not_installed" || it.State == "" {
		return "not installed"
	}
	if it.InstalledVersion != "" {
		return fmt.Sprintf("installed (v%s)", strings.TrimPrefix(it.InstalledVersion, "v"))
	}
	return "installed"
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
		doInstallAll(client, sourceID, cat, sourcepicker.Advanced{
			Global:  flags.Global,
			Force:   flags.Force,
			IsAdmin: true, // picker display only — the server gates --global
			NoDeps:  flags.NoDeps,
		})
		return
	}

	selection, advanced, committed, proceed, err := chooseSelection(mode, flags, cat)
	if err != nil {
		logger.Logger.Fatal(err)
	}
	if !committed {
		logger.Logger.Info("Aborted.")
		return
	}
	if !proceed {
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
		ID:               sourceFlagID,
		Ref:              sourceFlagRef,
		Directory:        sourceFlagDirectory,
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

// chooseSelection resolves the selection to POST.
//
// Returns (selection, advanced, committed, proceed, err):
//   - committed is false only when the user aborted the picker.
//   - proceed is false when there is nothing to do (no items picked / scripted
//     selection empty); the caller prints a "nothing selected" notice and
//     skips the round-trip.
func chooseSelection(mode installMode, flags sourceFlags, cat dto.SourceCatalogDTO) (dto.InstallSelectionDTO, sourcepicker.Advanced, bool, bool, error) {
	adv := sourcepicker.Advanced{
		Global:  flags.Global,
		Force:   flags.Force,
		IsAdmin: true, // picker display only — the server gates --global
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
		// set (items to install). An empty intent means "nothing to do"; a
		// non-empty intent folds against the current install state to build
		// the wire selection.
		picked, advOut, committed, err := sourcepicker.Run(cat, adv)
		if err != nil || !committed {
			return dto.InstallSelectionDTO{}, advOut, committed, false, err
		}
		if isEmptySelection(picked) {
			return dto.InstallSelectionDTO{}, advOut, true, false, nil
		}
		return composeInteractiveSelection(cat, picked), advOut, true, true, nil
	default:
		return dto.InstallSelectionDTO{}, adv, false, false, fmt.Errorf("unreachable: mode %v", mode)
	}
}

// deriveCurrentSelection rebuilds the source's installed set from the
// catalog's per-item state; composeInteractiveSelection adds the picker's
// intent on top. Items in state "installed" or "upgrade_available" count as
// currently installed; "not_installed" do not.
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

// clientIsAdmin reports whether the caller's API key belongs to an admin,
// via /whoami. ok is false when the endpoint couldn't be reached or parsed;
// admin-ness is never assumed on failure — each renderer picks its own
// fallback (display-only either way: every admin action is gated server-side).
func clientIsAdmin(client *resty.Client) (isAdmin bool, ok bool) {
	resp, err := client.R().Get(rest.APIBasePath + "/whoami")
	if err != nil || resp.StatusCode() != http.StatusOK {
		return false, false
	}
	var body struct {
		User struct {
			IsAdmin bool `json:"isAdmin"`
		} `json:"user"`
	}
	if err := json.Unmarshal(resp.Body(), &body); err != nil {
		return false, false
	}
	return body.User.IsAdmin, true
}

func doInstall(client *resty.Client, sourceID string, sel dto.InstallSelectionDTO, adv sourcepicker.Advanced) {
	postInstall(client, sourceID, dto.InstallRequest{
		Selection: &sel,
		Global:    adv.Global,
		Force:     adv.Force,
		NoDeps:    adv.NoDeps,
	}, "installed")
}

// doInstallAll is the "install everything currently walked" shortcut used
// by --all and non-TTY invocations. An omitted selection tells the server
// to install everything the source ships.
func doInstallAll(client *resty.Client, sourceID string, _ dto.SourceCatalogDTO, adv sourcepicker.Advanced) {
	postInstall(client, sourceID, dto.InstallRequest{
		Global: adv.Global,
		Force:  adv.Force,
		NoDeps: adv.NoDeps,
	}, "installed")
}

func postInstall(client *resty.Client, sourceID string, body dto.InstallRequest, verbPast string) {
	resp, success := rest.GenericJSONPost(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/install"), body)
	checkSuccessAndProvideJSON(success, resp)
	var sync syncResultPayload
	if err := json.Unmarshal(resp, &sync); err != nil || sync.SourceID == "" {
		logger.Logger.Info(string(resp))
		return
	}
	printSyncFailures(fmt.Sprintf("Source '%s'", sourceID), verbPast, sync)
}

func isSourceURL(s string) bool {
	// git:// is git's read-only smart-protocol transport (git daemon),
	// used by internal/airgapped mirrors.
	if u, err := neturl.Parse(s); err == nil && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "git") {
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

// requireSourceID fatals with a targeted hint unless arg matches the shared
// sourceID contract (dto.SourceIDRegex — the same rule the server enforces).
func requireSourceID(arg string) string {
	if dto.SourceIDRegex.MatchString(arg) {
		return arg
	}
	if isSourceURL(arg) {
		logger.Logger.Fatalf("%q is a git URL — this command takes the sourceID (see `ludus source list`)", arg)
	}
	logger.Logger.Fatalf("%q is not a valid sourceID (letters, digits, _ and - only; see `ludus source list`)", arg)
	return "" // unreachable; Fatalf exits
}

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
	Use:   "list [<sourceID>]",
	Short: "List registered sources, or show details for one",
	Long: `List registered sources. With a sourceID, show that source's metadata;
add --catalog to instead see what it ships (blueprints, templates, and
ansible roles/collections) joined with the current install state.`,
	Aliases: []string{"ls", "status"},
	Args:    cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		detailID := ""
		if len(args) == 1 {
			detailID = requireSourceID(args[0])
		}
		if sourceFlagCatalog && detailID == "" {
			logger.Logger.Fatal("--catalog requires a sourceID: ludus source list <sourceID> --catalog")
		}
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		if detailID != "" {
			runSourceDetail(client, detailID)
			return
		}
		responseJSON, success := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources"))
		checkSuccessAndProvideJSON(success, responseJSON)
		var sources []dto.SourceResponse
		if err := json.Unmarshal(responseJSON, &sources); err != nil {
			logger.Logger.Fatal(err)
		}
		if len(sources) == 0 {
			logger.Logger.Info("No sources registered.")
			return
		}
		// Admins see everyone's sources, so owner matters; non-admins only
		// ever see their own and the column would be noise. When whoami
		// can't answer, default to showing it.
		isAdmin, whoamiOK := clientIsAdmin(client)
		showOwner := isAdmin || !whoamiOK
		table := tablewriter.NewWriter(os.Stdout)
		// "Last Updated" is type-neutral on purpose: the timestamp is the last
		// content refresh — a re-pull for git sources, a tarball push for uploads.
		header := []string{"Source ID", "Name", "Authors", "Type", "Last Updated", "Status"}
		if showOwner {
			header = append(header, "Owner")
		}
		table.SetHeader(header)
		for _, s := range sources {
			row := []string{
				s.SourceID,
				s.Name,
				strings.Join(s.Authors, ", "),
				s.Type,
				s.LastSyncedAt,
				s.LastSyncStatus,
			}
			if showOwner {
				row = append(row, s.OwnerUserID)
			}
			table.Append(row)
		}
		table.Render()
	},
}

// runSourceDetail shows a source's metadata, or — with --catalog — the
// catalog tables instead (what upstream ships, joined with which items are
// currently installed; the State column tells the user whether an upgrade is
// waiting). --json emits the JSON of whichever view was requested.
func runSourceDetail(client *resty.Client, sourceID string) {
	if sourceFlagCatalog {
		catJSON, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID+"/catalog"))
		checkSuccessAndProvideJSON(ok, catJSON)
		var cat dto.SourceCatalogDTO
		if err := json.Unmarshal(catJSON, &cat); err != nil {
			logger.Logger.Fatal(err)
		}
		renderCatalogTables(cat)
		return
	}

	srcJSON, ok := rest.GenericGet(client, buildURLWithRangeAndUserID("/sources/"+sourceID))
	checkSuccessAndProvideJSON(ok, srcJSON)
	var src dto.SourceResponse
	if err := json.Unmarshal(srcJSON, &src); err != nil {
		logger.Logger.Fatal(err)
	}

	t := tablewriter.NewWriter(os.Stdout)
	row := func(label, value string) {
		if value != "" {
			t.Append([]string{label, value})
		}
	}
	row("Name", src.Name)
	row("Authors", strings.Join(src.Authors, ", "))
	row("Description", src.Description)
	if src.Type == "git" {
		row("Git URL", src.URL)
		row("Ref", src.Ref)
		row("Last Sync", src.LastSyncedAt)
	} else {
		row("Uploaded", src.LastSyncedAt)
	}
	row("Errors", src.LastSyncError)
	row("Homepage", src.Homepage)
	row("License", src.License)
	// Owner is only worth a row for admins (non-admins own what they see);
	// when whoami can't answer, default to showing it.
	if isAdmin, ok := clientIsAdmin(client); isAdmin || !ok {
		row("Owner", src.OwnerUserID)
	}
	t.Render()

	fmt.Printf("\nRun `ludus source list %s --catalog` to see what this source ships (blueprints, templates, ansible).\n", sourceID)
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
	// Validate before InitClient so a malformed argument is rejected without
	// demanding credentials first.
	var targets []string
	if len(args) == 1 {
		targets = []string{requireSourceID(args[0])}
	}

	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

	if len(targets) == 0 {
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
		checkSuccessAndProvideJSON(success, responseJSON)
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
	Short: "Replace an upload source's content",
	Long: `Push new content to an existing upload-type source. The archive is
extracted in place of the old content and everything this source has
installed is re-applied against it. (For git sources, use 'source set-url'
to repoint the remote or change the tracked ref.)

  ludus source update <id> ./new-source.tar.gz  # replace content with a tarball
  ludus source update <id> -d ./source-dir      # tar a directory and replace`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSourceUpdate,
}

func runSourceUpdate(cmd *cobra.Command, args []string) {
	sid := requireSourceID(args[0])
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
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
		if detectSourceArg(args[1]) != sourceArgArchive {
			logger.Logger.Fatalf("could not interpret %q: expected a tarball/zip path (use -d for a local directory)", args[1])
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			logger.Logger.Fatal(err)
		}
		fileField = "archive"
		fileBytes = data
		fileName = filepath.Base(args[1])
	default:
		logger.Logger.Fatal("provide a tarball path or -d <dir>")
	}

	updateReq := dto.UpdateSourceRequest{
		Global: sourceFlagGlobal,
		Force:  sourceFlagForce,
	}
	responseJSON, success := rest.FileUpload(client, "PATCH",
		buildURLWithRangeAndUserID(path), fileField, fileName, fileBytes, updateSourceRequestToForm(updateReq))
	checkSuccessAndProvideJSON(success, responseJSON)
	var resp syncResultPayload
	if err := json.Unmarshal(responseJSON, &resp); err != nil || resp.SourceID == "" {
		logger.Logger.Info(string(responseJSON))
		return
	}
	printSyncFailures(fmt.Sprintf("Source '%s'", resp.SourceID), "updated", resp)
}

var sourceSetURLCmd = &cobra.Command{
	Use:   "set-url <sourceID> [<git-url>]",
	Short: "Repoint a git source's remote URL and/or tracked ref",
	Long: `Change where a git source pulls from. The old checkout is dropped and the
next sync re-clones from the new remote.

  ludus source set-url <id> <git-url>             # repoint the remote
  ludus source set-url <id> <git-url> --ref main  # repoint and switch the tracked ref
  ludus source set-url <id> --ref v2              # just switch the tracked ref`,
	Args: cobra.RangeArgs(1, 2),
	Run:  runSourceSetURL,
}

func runSourceSetURL(cmd *cobra.Command, args []string) {
	sid := requireSourceID(args[0])
	client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

	newURL := ""
	if len(args) == 2 {
		if !isSourceURL(args[1]) {
			logger.Logger.Fatalf("could not interpret %q as a git URL", args[1])
		}
		newURL = args[1]
	}
	if newURL == "" && sourceFlagRef == "" {
		logger.Logger.Fatal("provide a git URL and/or --ref")
	}

	body, _ := json.Marshal(dto.UpdateSourceRequest{Ref: sourceFlagRef, URL: newURL})
	responseJSON, success := rest.GenericJSONPatch(client, buildURLWithRangeAndUserID(fmt.Sprintf("/sources/%s", sid)), string(body))
	checkSuccessAndProvideJSON(success, responseJSON)
	var changed []string
	if newURL != "" {
		changed = append(changed, "url")
	}
	if sourceFlagRef != "" {
		changed = append(changed, "ref")
	}
	logger.Logger.Infof("Source '%s' %s updated. Run `ludus source sync %s` to apply.", sid, strings.Join(changed, " and "), sid)
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
// selection to POST: everything currently installed plus the picked items.
// The interactive picker doesn't pre-check installed items — checking a box
// expresses intent for the current command — so folding the current state
// back in keeps already-installed items registered and refreshed alongside
// the new picks.
func composeInteractiveSelection(cat dto.SourceCatalogDTO, picked dto.InstallSelectionDTO) dto.InstallSelectionDTO {
	current := deriveCurrentSelection(cat)
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
or other blueprints. To uninstall those, remove individual items with the
templates/ansible commands ('ludus templates rm', 'ludus ansible role rm',
'ludus ansible collection rm').`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sid := requireSourceID(args[0])
		client := rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)
		// Skip the prompt automatically when stdin is piped/non-TTY (CI, scripts).
		// Callers can also force-skip with --no-prompt.
		if !sourceFlagNoPrompt && stdinIsTerminal() {
			fmt.Printf("Remove source '%s' and its blueprints? Installed templates, roles, and collections stay on disk. [y/N]: ", sid)
			var resp string
			_, _ = fmt.Scanln(&resp)
			if !strings.EqualFold(strings.TrimSpace(resp), "y") {
				logger.Logger.Info("Aborted.")
				return
			}
		}
		path := fmt.Sprintf("/sources/%s", sid)
		responseJSON, success := rest.GenericDelete(client, buildURLWithRangeAndUserID(path))
		checkSuccessAndProvideJSON(success, responseJSON)
		logger.Logger.Infof("Source %q removed. Its blueprints are gone; installed templates, roles, and collections remain on disk.", sid)
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
	sourceAddCmd.Flags().Var(&sourceFlagBlueprints, "blueprints", "blueprint IDs to install (Comma separated values)")
	sourceAddCmd.Flags().Var(&sourceFlagTemplates, "templates", "template names to install (Comma separated values)")
	sourceAddCmd.Flags().Var(&sourceFlagLocalRoles, "source-roles", "source role names to install (Comma separated values)")
	sourceAddCmd.Flags().Var(&sourceFlagLocalCollections, "source-collections", "source collection FQCNs to install (Comma separated values)")

	sourceAddCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users")
	sourceAddCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install: re-extract templates and source roles, and rerun ansible-galaxy with -f for galaxy roles and collections")
	sourceAddCmd.Flags().BoolVar(&sourceFlagNoDeps, "no-deps", false, "skip installing blueprint galaxy role/collection dependencies; use only what's already on disk")

	// List flags.
	sourceListCmd.Flags().BoolVar(&sourceFlagCatalog, "catalog", false, "show the source's catalog (blueprints, templates, ansible) instead of its metadata; requires a sourceID")

	// Sync flags (reuses sourceFlagRef, etc. from add).
	sourceSyncCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users")
	sourceSyncCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install: re-extract templates and source roles, and rerun ansible-galaxy with -f for galaxy roles and collections")

	// Update flags.
	sourceUpdateCmd.Flags().SortFlags = false
	sourceUpdateCmd.Flags().StringVarP(&sourceFlagDirectory, "directory", "d", "", "tar a local directory and upload it as the new source content")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagGlobal, "global", false, "admin only: install the source's roles and collections for all users")
	sourceUpdateCmd.Flags().BoolVar(&sourceFlagForce, "force", false, "force install when the new archive triggers the inline reinstall")

	// Set-url flags.
	sourceSetURLCmd.Flags().StringVar(&sourceFlagRef, "ref", "", "git branch/tag/commit to track")

	// Rm flags.
	sourceRmCmd.Flags().BoolVar(&sourceFlagNoPrompt, "no-prompt", false, "skip confirmation prompt")

	sourceCmd.AddCommand(sourceAddCmd)
	sourceCmd.AddCommand(
		sourceListCmd,
		sourceSyncCmd,
		sourceUpdateCmd,
		sourceSetURLCmd,
		sourceRmCmd,
	)
	rootCmd.AddCommand(sourceCmd)
}

// stdinIsTerminal reports whether stdin is connected to an interactive TTY.
// Returns false when stdin is a pipe/redirect (e.g. CI), so prompts can be skipped.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
