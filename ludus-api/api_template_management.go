package ludusapi

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"ludusapi/commandmanager"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"
)

// LudusOS represents the operating system category for a template.
type LudusOS string

const (
	LudusOSLinux   LudusOS = "linux"
	LudusOSWindows LudusOS = "windows"
	LudusOSMacOS   LudusOS = "macos"
	LudusOSOther   LudusOS = "other"
)

type TemplateStatus struct {
	Name     string  `json:"name"`
	Built    bool    `json:"built"`
	Status   string  `json:"status"`
	Os       LudusOS `json:"os"`
	FilePath string  `json:"-"`
}

const templateRegex string = `(?m)[^"]*?-template`

var templateStringRegex = regexp.MustCompile(templateRegex)
var templateLogSafeCharRegex = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

var templateProgressStore sync.Map

// Get all available packer templates from the main packer dir and the user packer dir
func getAvailableTemplates(user *models.User) ([]string, error) {
	globalTemplates, err := findFiles(fmt.Sprintf("%s/packer/", ludusInstallPath), "pkr.hcl", "pkr.json")
	if err != nil {
		return nil, errors.New("unable to get global packer templates")
	}
	userTemplates, err := findFiles(fmt.Sprintf("%s/users/%s/packer/", ludusInstallPath, user.ProxmoxUsername()), "pkr.hcl", "pkr.json")
	if err != nil {
		if user.Name() == "ROOT" {
			return nil, errors.New("ROOT user should not be used for this action. Use a normal user instead.")
		}
		return nil, errors.New("unable to get user packer templates")
	}
	allTemplates := append(globalTemplates, userTemplates...)
	return allTemplates, nil
}

func extractTemplateNameFromHCL(hclFile string, templateRegex *regexp.Regexp) string {
	// This should use the hcl package for golang and parse the files, but this will work for now
	fileBytes, err := os.ReadFile(hclFile)
	if err != nil {
		return "error reading file: " + hclFile
	}
	fileString := string(fileBytes)
	templateName := templateRegex.FindString(fileString)
	if templateName != "" {
		return templateName
	} else {
		return "could not find template name in " + hclFile
	}
}

// proxmoxOSToLudusOS maps a Proxmox guest OS type code to a Ludus OS category.
// Proxmox OS types: wxp, w2k, w2k3, w2k8, wvista, win7, win8, win10, win11, l24, l26, solaris, other
func proxmoxOSToLudusOS(proxmoxOs string) LudusOS {
	proxmoxOs = strings.TrimSpace(strings.ToLower(proxmoxOs))
	switch {
	case strings.HasPrefix(proxmoxOs, "win") || strings.HasPrefix(proxmoxOs, "w2k") ||
		proxmoxOs == "wxp" || proxmoxOs == "wvista":
		return LudusOSWindows
	case strings.HasPrefix(proxmoxOs, "l2"): // l24, l26
		return LudusOSLinux
	case proxmoxOs == "solaris" || proxmoxOs == "other" || proxmoxOs == "":
		return LudusOSOther
	default:
		return LudusOSOther
	}
}

var osVariableRegex = regexp.MustCompile(`(?s)variable\s+"os"\s*\{[^}]*default\s*=\s*"([^"]+)"`)

// extractOsFromHCL reads a Packer HCL file and extracts the default value of the "os" variable,
// then maps it to a Ludus OS category (linux, windows, macos, other).
func extractOSFromHCL(hclFile string) LudusOS {
	fileBytes, err := os.ReadFile(hclFile)
	if err != nil {
		return ""
	}
	matches := osVariableRegex.FindSubmatch(fileBytes)
	if len(matches) < 2 {
		return ""
	}
	return proxmoxOSToLudusOS(string(matches[1]))
}

// osFromTemplateName guesses the OS category from a template name as a fallback
// when no HCL file is available.
func osFromTemplateName(name string) LudusOS {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "win") || strings.Contains(lower, "windows"):
		return LudusOSWindows
	case strings.Contains(lower, "macos") || strings.Contains(lower, "osx"):
		return LudusOSMacOS
	default:
		return LudusOSLinux
	}
}

func getTemplateBuildLogPath(user *models.User, templateName string, startTime time.Time) string {
	safeTemplateName := strings.Trim(templateLogSafeCharRegex.ReplaceAllString(templateName, "_"), "._-")
	if safeTemplateName == "" {
		safeTemplateName = "template"
	}
	logDir := fmt.Sprintf("%s/users/%s/packer/log-history", ludusInstallPath, user.ProxmoxUsername())
	os.MkdirAll(logDir, 0755)
	return fmt.Sprintf("%s/%s-%s.log", logDir, safeTemplateName, startTime.UTC().Format("2006-01-02T15-04-05.000000000Z"))
}

func setLatestPackerLogForUser(user *models.User, sourceLogPath string) error {
	logBytes, err := os.ReadFile(sourceLogPath)
	if err != nil {
		return err
	}
	latestPackerLogPath := fmt.Sprintf("%s/users/%s/packer.log", ludusInstallPath, user.ProxmoxUsername())
	return os.WriteFile(latestPackerLogPath, logBytes, 0644)
}

func buildVMFromTemplateWithPacker(user *models.User, packerFile string, templateName string, verbose bool, packerLogFile string) error {
	proxmoxToken, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
	if err != nil {
		logger.Error(fmt.Sprintf("Unable to decrypt proxmox token secret: %v\n", err))
		return fmt.Errorf("unable to decrypt proxmox token secret: %v", err)
	}
	// Run the longest, grossest packer command you have ever seen...
	// There should be a better way to do this, but apparently not: https://devops.stackexchange.com/questions/14181/is-it-possible-to-control-packer-from-golang

	workingDir := filepath.Dir(packerFile)
	usersPackerDir := fmt.Sprintf("%s/users/%s/packer", ludusInstallPath, user.ProxmoxUsername())
	usersAnsibleDir := fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername())
	os.MkdirAll(fmt.Sprintf("%s/users/%s/packer/tmp", ludusInstallPath, user.ProxmoxUsername()), 0755)

	tmplStr := `PACKER_PLUGIN_PATH={{.LudusInstallPath}}/resources/packer/plugins ` +
		`PROXMOX_USERNAME='{{ .ProxmoxTokenID }}' ` +
		`PROXMOX_TOKEN={{ .ProxmoxToken }} ` +
		`PACKER_CONFIG_DIR={{.UsersPackerDir}} ` +
		`PACKER_CACHE_DIR={{.UsersPackerDir}}/packer_cache ` +
		`PKR_VAR_proxmox_password="" ` +
		`PKR_VAR_proxmox_username="" ` +
		`CHECKPOINT_DISABLE=1 PACKER_LOG={{.PackerVerbose}} ` +
		`PACKER_LOG_PATH='{{.PackerLogFile}}' ` +
		`TMPDIR='{{.UsersPackerDir}}/tmp' ` +
		` packer build -on-error=cleanup ` +
		`-var 'proxmox_url={{.ProxmoxURL}}/api2/json' ` +
		`-var 'proxmox_host={{.ProxmoxHost}}' ` +
		`-var 'proxmox_skip_tls_verify={{.ProxmoxSkipTLSVerify}}' ` +
		`-var 'proxmox_pool=SHARED' ` +
		`-var 'proxmox_storage_pool={{.ProxmoxVMStoragePool}}' ` +
		`-var 'proxmox_storage_format={{.ProxmoxVMStorageFormat}}' ` +
		`-var 'iso_storage_pool={{.ProxmoxISOStoragePool}}' ` +
		`-var 'ansible_home={{.UsersAnsibleDir}}' ` +
		`-var 'ludus_nat_interface={{.LudusNATInterface}}' ` +
		`{{.PackerFile}}`

	packerVerbose := "1"

	data := struct {
		LudusInstallPath       string
		ProxmoxTokenID         string
		ProxmoxToken           string
		UsersPackerDir         string
		PackerVerbose          string
		PackerLogFile          string
		ProxmoxURL             string
		ProxmoxHost            string
		ProxmoxSkipTLSVerify   string
		ProxmoxVMStoragePool   string
		ProxmoxVMStorageFormat string
		ProxmoxISOStoragePool  string
		UsersAnsibleDir        string
		PackerFile             string
		LudusNATInterface      string
	}{
		ludusInstallPath,
		user.ProxmoxTokenId(),
		proxmoxToken,
		usersPackerDir,
		packerVerbose,
		packerLogFile,
		ServerConfiguration.ProxmoxURL,
		ServerConfiguration.ProxmoxNode,
		strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert),
		ServerConfiguration.ProxmoxVMStoragePool,
		ServerConfiguration.ProxmoxVMStorageFormat,
		ServerConfiguration.ProxmoxISOStoragePool,
		usersAnsibleDir,
		packerFile,
		ServerConfiguration.LudusNATInterface,
	}

	tmpl, err := template.New("command").Parse(tmplStr)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to parse template: %v\n", err))
		return fmt.Errorf("failed to parse template: %v", err)
	}

	// Create a buffer to hold the rendered output
	var renderedOutput bytes.Buffer

	err = tmpl.Execute(&renderedOutput, data)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to execute template: %v\n", err))
		return fmt.Errorf("failed to execute template: %v", err)
	}

	// Get the contents of the buffer as a string
	renderedOutputString := renderedOutput.String()

	// Run the command and log to a file
	commandManager := commandmanager.GetInstance()
	packerBuildCommandID := uuid.New().String()
	packerBuildCommandMetadata := map[string]string{
		"command_type":     "packer_build",
		"template_name":    templateName,
		"template_file":    packerFile,
		"proxmox_username": user.ProxmoxUsername(),
	}
	_, packerCommandError := commandManager.StartCommandInShellAndWait(packerBuildCommandID, renderedOutputString, packerLogFile, workingDir, packerBuildCommandMetadata)

	// Write 'Build complete' to the per-template packer log so the user knows this build has finished.
	if packerCommandError == nil {
		file, err := os.OpenFile(packerLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Error(fmt.Sprintf("Error opening file: %v\n", err))
			return fmt.Errorf("error opening file: %v", err)
		}
		defer file.Close()

		if _, err := file.Write([]byte("\n\n=>================\n=> Build complete!\n=>================\n\n")); err != nil {
			logger.Error(fmt.Sprintf("Error writing to file: %v\n", err))
			return fmt.Errorf("error writing to file: %v", err)
		}
	}
	if !verbose {
		logger.Debug("Template build logs captured in parallel mode for template " + templateName)
	}

	return packerCommandError
}

func buildVMsFromTemplates(app core.App, templateStatusArray []TemplateStatus, user *models.User, templateNames []string, parallel int, verbose bool) error {
	// Create a WaitGroup to wait for all goroutines to finish.
	var wg sync.WaitGroup
	var latestLogMu sync.Mutex

	// Create a semaphore (buffered channel of empty structs) to limit the number of concurrent goroutines.
	semaphoreChannel := make(chan struct{}, parallel)

	username := user.ProxmoxUsername()
	canaryPath := fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, username)

	// Helper to determine if a template should be built.
	// Determine whether a VM should be built from the template.
	// Check that:
	// 1. The template is not already built (proxmox returns this)
	// 2. The user asked to build this specific template (by including it in the templateNames array or by specifying "all")
	// 3. This template is not already in progress - the user could call this method twice with a parallel value > number of templates,
	//    and longer running templates would then be built a second time as the have not finished building to return .Built by proxmox
	//    This step is handled by the templateProgressStore sync map in the first pass of the templateStatusArray loop.
	shouldBuild := func(templateStatus TemplateStatus) bool {
		shouldBuildTemplate := !templateStatus.Built
		if len(templateNames) > 0 {
			// Check if "all" is specified, which means build all templates
			buildAll := false
			for _, name := range templateNames {
				if name == "all" {
					buildAll = true
					break
				}
			}
			if !buildAll {
				found := false
				for _, name := range templateNames {
					if templateStatus.Name == name {
						found = true
						break
					}
				}
				shouldBuildTemplate = shouldBuildTemplate && found
			}
		}
		return shouldBuildTemplate
	}

	// First pass: pre-populate the progress store for all templates that will be built.
	// This ensures Abort can clear the store immediately, even if called before goroutines
	// have started or between the 3s delays. Without this, the store is filled one entry
	// per 3 seconds inside the loop, so Abort often sees an empty or partial store.
	var templatesToBuild []TemplateStatus
	for _, templateStatus := range templateStatusArray {
		if modifiedTimeLessThan(canaryPath, 10) {
			logger.Debug("Canary check failed for template: " + templateStatus.Name)
			break
		}
		if !shouldBuild(templateStatus) {
			continue
		}
		if _, ok := templateProgressStore.Load(templateStatus.Name); ok {
			continue // already building or queued (e.g. duplicate Build request)
		}
		// Add this template name to the sync map and set its value to true to indicate that it is building or in the queue to build
		// Have to get a little tricky here, since if two users are building templates and one aborts
		// we don't want to remove queued templates for the other user
		// To accomplish this, we store the username of the building user as the value to the key of the template.
		templateProgressStore.Store(templateStatus.Name, username)
		templatesToBuild = append(templatesToBuild, templateStatus)
	}

	// Second pass: launch a goroutine per template (same semantics as before, with 3s stagger).
	for _, templateStatus := range templatesToBuild {
		// If a VM should be built, increment the WaitGroup counter
		wg.Add(1)
		// Launch a go routine to build the VM.
		go func(templateStatus TemplateStatus, username string) {
			// Send an empty struct into the channel, if it is full this will block and we will wait our turn
			semaphoreChannel <- struct{}{}

			// Ensure that the WaitGroup counter is decremented and remove one struct from the channel by reading it when the goroutine finishes.
			// Since all structs are the same (empty), reading one is the same as removing the one we put in - it frees up a slot for another go routine
			defer wg.Done()
			defer func() { <-semaphoreChannel }()
			// When we finish, delete our entry from the sync map
			defer func() {
				templateProgressStore.Range(func(key, value interface{}) bool {
					// If the key matches this template and the value matches this user, delete the key
					if key == templateStatus.Name && value == username {
						templateProgressStore.Delete(key)
					}
					return true // continue iteration
				})
			}()
			// Check the canary file before starting the VM build.
			if modifiedTimeLessThan(canaryPath, 10) {
				logger.Debug("Canary check failed - not building template " + templateStatus.Name)
				return
			}
			buildStartTime := time.Now()
			templateLogPath := getTemplateBuildLogPath(user, templateStatus.Name, buildStartTime)
			runningLogID := createRunningLogHistory(app, user.Id, "", templateStatus.Name, templateLogPath, buildStartTime)

			status := "success"
			if err := buildVMFromTemplateWithPacker(user, templateStatus.FilePath, templateStatus.Name, verbose, templateLogPath); err != nil {
				status = "failure"
			}

			latestLogMu.Lock()
			err := setLatestPackerLogForUser(user, templateLogPath)
			latestLogMu.Unlock()
			if err != nil {
				logger.Error(fmt.Sprintf("Failed to update latest packer log for template %s: %v", templateStatus.Name, err))
			}

			if runningLogID != "" {
				finalizeRunningLogHistoryByID(app, runningLogID, status, templateLogPath, time.Now())
			} else {
				saveLogHistory(app, user.Id, "", templateStatus.Name, status, templateLogPath, buildStartTime)
			}

		}(templateStatus, username)

		// Sleep for 3 seconds so the server isn't flooded with builds all at exactly the same time if the user gives a high number for parallel
		time.Sleep(3 * time.Second)
	}

	wg.Wait()

	return nil
}

func getTemplatesStatus(e *core.RequestEvent) ([]TemplateStatus, error) {
	user := e.Get("user").(*models.User)

	proxmoxClient, err := GetGoProxmoxClientForUserUsingToken(e)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get all resources of type "vm" (which includes 'qemu' and 'lxc' types)
	allVMs, err := getAllVMs(e, ctx, proxmoxClient)
	if err != nil {
		return nil, fmt.Errorf("unable to list VMs from cluster: %w", err)
	}

	var templates []string

	for _, vm := range allVMs {
		if vm.Type == "qemu" && vm.Template == 1 {
			templates = append(templates, vm.Name)
		}
	}

	allTemplates, err := getAvailableTemplates(user)
	if err != nil {
		return nil, err
	}

	// Check all the .hcl files
	templateStringRegex, _ := regexp.Compile(templateRegex)
	var templateStatusArray []TemplateStatus
	var templateHCLNames []string
	for _, templateFile := range allTemplates {
		// Create a template status, fill out the details, and save it to the templateStatusArray
		var thisTemplateStatus TemplateStatus
		thisTemplateName := extractTemplateNameFromHCL(templateFile, templateStringRegex)
		// Save this name for later comparison with template VM names
		templateHCLNames = append(templateHCLNames, thisTemplateName)
		// Fill out the template status details
		thisTemplateStatus.Name = thisTemplateName
		thisTemplateStatus.FilePath = templateFile
		thisTemplateStatus.Os = extractOSFromHCL(templateFile)
		if thisTemplateStatus.Os == "" {
			thisTemplateStatus.Os = osFromTemplateName(thisTemplateName)
		}
		if slices.Contains(templates, thisTemplateName) {
			thisTemplateStatus.Built = true
			thisTemplateStatus.Status = "built"
		} else {
			thisTemplateStatus.Built = false
			thisTemplateStatus.Status = "not_built"
			// Check if the template is being built by a user using the command manager
			commandManager := commandmanager.GetInstance()
			commands := commandManager.GetAllCommands()
			for _, command := range commands {
				if command.Metadata["command_type"] == "packer_build" && command.Metadata["template_name"] == thisTemplateName && command.Status == commandmanager.StatusRunning {
					thisTemplateStatus.Status = "building"
					break
				}
			}
		}
		templateStatusArray = append(templateStatusArray, thisTemplateStatus)
	}

	// Check all the template VMs (possibly some templates built by hand)
	for _, templateVM := range templates {
		if !slices.Contains(templateHCLNames, templateVM) {
			var thisTemplateStatus TemplateStatus
			thisTemplateStatus.Name = templateVM
			thisTemplateStatus.FilePath = "None"
			thisTemplateStatus.Built = true
			thisTemplateStatus.Status = "built"
			thisTemplateStatus.Os = osFromTemplateName(templateVM)
			templateStatusArray = append(templateStatusArray, thisTemplateStatus)
		}
	}
	return templateStatusArray, nil
}

func getTemplateNameArray(e *core.RequestEvent, onlyBuilt bool) ([]string, error) {
	// Get a list of all the templates on the system
	templateStatusArray, err := getTemplatesStatus(e)
	if err != nil {
		return nil, err
	}
	var templateSlice []string
	for _, templateStatus := range templateStatusArray {
		if onlyBuilt && templateStatus.Built {
			templateSlice = append(templateSlice, templateStatus.Name)
		} else if !onlyBuilt {
			templateSlice = append(templateSlice, templateStatus.Name)
		}
	}
	return templateSlice, nil
}

func syncTemplatesCollection(app core.App, templateStatusArray []TemplateStatus) error {
	templatesCollection, err := app.FindCollectionByNameOrId("templates")
	if err != nil {
		return fmt.Errorf("unable to find templates collection: %w", err)
	}

	for _, templateStatus := range templateStatusArray {
		templateName := strings.TrimSpace(templateStatus.Name)
		if templateName == "" {
			continue
		}

		existingRecord, err := app.FindFirstRecordByData("templates", "name", templateName)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("unable to query template '%s' in templates collection: %w", templateName, err)
		}
		if existingRecord != nil {
			continue
		}

		templateRecord := core.NewRecord(templatesCollection)
		template := &models.Templates{}
		template.SetProxyRecord(templateRecord)
		template.SetName(templateName)

		templateOS := string(templateStatus.Os)
		if templateOS == "" {
			templateOS = string(osFromTemplateName(templateName))
		}
		templateRecord.Set("os", templateOS)
		template.SetShared(true)

		if err := app.Save(template); err != nil {
			return fmt.Errorf("unable to create template '%s' in templates collection: %w", templateName, err)
		}
	}

	return nil
}

func discoverTemplateStatusesForStartup(app core.App) ([]TemplateStatus, error) {
	templateRegexCompiled, err := regexp.Compile(templateRegex)
	if err != nil {
		return nil, fmt.Errorf("unable to compile template regex: %w", err)
	}

	templateStatusByName := make(map[string]TemplateStatus)
	addTemplateFromFile := func(templateFile string) {
		templateName := strings.TrimSpace(extractTemplateNameFromHCL(templateFile, templateRegexCompiled))
		if templateName == "" || strings.HasPrefix(templateName, "error reading file:") || strings.HasPrefix(templateName, "could not find template name in ") {
			return
		}
		if _, exists := templateStatusByName[templateName]; exists {
			return
		}
		templateOS := extractOSFromHCL(templateFile)
		if templateOS == "" {
			templateOS = osFromTemplateName(templateName)
		}
		templateStatusByName[templateName] = TemplateStatus{
			Name: templateName,
			Os:   templateOS,
		}
	}

	globalTemplateFiles, err := findFiles(fmt.Sprintf("%s/packer/", ludusInstallPath), "pkr.hcl", "pkr.json")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("unable to list global templates: %w", err)
	}
	for _, templateFile := range globalTemplateFiles {
		addTemplateFromFile(templateFile)
	}

	userRecords, err := app.FindAllRecords("users")
	if err != nil {
		return nil, fmt.Errorf("unable to list users for template startup sync: %w", err)
	}
	for _, userRecord := range userRecords {
		user := &models.User{}
		user.SetProxyRecord(userRecord)
		if user.UserId() == "ROOT" {
			continue
		}
		proxmoxUsername := strings.TrimSpace(user.ProxmoxUsername())
		if proxmoxUsername == "" {
			continue
		}
		userTemplateRoot := fmt.Sprintf("%s/users/%s/packer/", ludusInstallPath, proxmoxUsername)
		userTemplateFiles, err := findFiles(userTemplateRoot, ".hcl", ".json")
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("unable to list templates for user %s: %w", user.UserId(), err)
		}
		userTemplateFiles = slices.DeleteFunc(userTemplateFiles, func(template string) bool {
			return strings.HasSuffix(template, "pkrvars.hcl")
		})
		for _, templateFile := range userTemplateFiles {
			addTemplateFromFile(templateFile)
		}
	}

	templateStatuses := make([]TemplateStatus, 0, len(templateStatusByName))
	for _, templateStatus := range templateStatusByName {
		templateStatuses = append(templateStatuses, templateStatus)
	}

	return templateStatuses, nil
}

func startupSyncTemplatesCollection(app core.App) error {
	templateStatuses, err := discoverTemplateStatusesForStartup(app)
	if err != nil {
		return err
	}
	return syncTemplatesCollection(app, templateStatuses)
}

func templateActions(e *core.RequestEvent, buildTemplates bool, templateNames []string, parallel int, verbose bool) error {

	if parallel == 0 {
		parallel = 1
	}

	templateStatusArray, err := getTemplatesStatus(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get template status array: "+err.Error())
	}

	if !buildTemplates {
		if err := syncTemplatesCollection(e.App, templateStatusArray); err != nil {
			return JSONError(e, http.StatusInternalServerError, "Unable to sync templates collection: "+err.Error())
		}
		return e.JSON(http.StatusOK, templateStatusArray)
	}

	user := e.Get("user").(*models.User)

	go buildVMsFromTemplates(e.App, templateStatusArray, user, templateNames, parallel, verbose)

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Template building started - this will take a while. Building %d template(s) at a time.", parallel))

}

// GetTemplates - returns a list of VM templates available for use in Ludus
func GetTemplates(e *core.RequestEvent) error {
	return templateActions(e, false, []string{}, 1, false)
}

func GetTemplateStatus(e *core.RequestEvent) error {
	commandManager := commandmanager.GetInstance()

	// Get all the commands in the command manager
	commands := commandManager.GetAllCommands()

	var templateStatusArray []dto.GetTemplatesStatusResponseItem

	for _, packerCommand := range commands {
		if packerCommand.Metadata["command_type"] == "packer_build" && packerCommand.Status == commandmanager.StatusRunning {
			templateStatusArray = append(templateStatusArray, dto.GetTemplatesStatusResponseItem{
				Template: packerCommand.Metadata["template_name"],
				User:     packerCommand.Metadata["proxmox_username"],
			})
		}
	}

	return e.JSON(http.StatusOK, templateStatusArray)
}

// Build all templates
func BuildTemplates(e *core.RequestEvent) error {

	var templateBody dto.BuildTemplatesRequest
	e.BindBody(&templateBody)

	// Set the default value to 1 if nothing is presented
	if templateBody.Parallel == 0 {
		templateBody.Parallel = 1
	}

	verbose := true
	if templateBody.Parallel > 1 {
		verbose = false
	}

	// Validate that templates array is not empty
	if len(templateBody.Templates) == 0 {
		return JSONError(e, http.StatusBadRequest, "templates array cannot be empty")
	}

	// Validate all templates in the array exist (skip validation for "all")
	templateArray, err := getTemplateNameArray(e, false)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get template name array: "+err.Error())
	}

	for _, templateName := range templateBody.Templates {
		if templateName != "all" && !slices.Contains(templateArray, templateName) {
			return JSONError(e, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateName))
		}
	}

	return templateActions(e, true, templateBody.Templates, templateBody.Parallel, verbose)
}

// GetPackerLogs - retrieves the latest packer logs
func GetPackerLogs(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	logID := e.Request.URL.Query().Get("logID")
	if logID != "" {
		if runningLogPath, ok := getRunningTemplateLogPathByLogID(e.App, user.Id, logID); ok {
			return GetLogsFromFile(e, runningLogPath)
		}
		return JSONError(e, http.StatusBadRequest, "The provided log history ID is not currently running. Follow mode is only supported for active template builds.")
	}
	if runningLogPath, ok := getLatestRunningTemplateLogPath(e.App, user.Id); ok {
		return GetLogsFromFile(e, runningLogPath)
	}
	packerLogPath := fmt.Sprintf("%s/users/%s/packer.log", ludusInstallPath, user.ProxmoxUsername())
	return GetLogsFromFile(e, packerLogPath)
}

func PutTemplateTar(e *core.RequestEvent) error {
	// Parse the multipart form
	if err := e.Request.ParseMultipartForm(1073741824); err != nil { // allow 1GB
		return JSONError(e, http.StatusBadRequest, err.Error())
	}

	// Retrieve the 'force' field and convert it to boolean
	forceStr := e.Request.FormValue("force")
	force, err := strconv.ParseBool(forceStr)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Invalid boolean value")
	}

	// Retrieve the file
	file, _, err := e.Request.FormFile("file")
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "File retrieval failed")
	}

	fileBytes, err := io.ReadAll(file)
	if err != nil {
		return JSONError(e, http.StatusBadRequest, "Unable to read uploaded file")
	}

	// Inspect tar contents to ensure a single root directory and capture its name
	rootNames := make(map[string]struct{})
	hasNestedEntries := false
	sawExplicitRootDir := false
	tarReader := tar.NewReader(bytes.NewReader(fileBytes))
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("Invalid tar archive: %v", err))
		}
		name := header.Name
		name = strings.TrimPrefix(name, "./")
		name = strings.TrimLeft(name, "/")
		if name == "" {
			continue
		}
		parts := strings.SplitN(name, "/", 2)
		root := parts[0]
		if root == "" {
			continue
		}
		rootNames[root] = struct{}{} // Minor memory saving hack. struct{} is a zero-byte value, a bool would map memory we don't care about
		if len(parts) > 1 {
			hasNestedEntries = true
		}
		if header.Typeflag == tar.TypeDir && (name == root || name == root+"/") {
			sawExplicitRootDir = true
		}
	}

	if len(rootNames) != 1 || (!sawExplicitRootDir && !hasNestedEntries) {
		return JSONError(e, http.StatusBadRequest, "Tar must contain a single root directory")
	}

	// We have to range over the map since rootNames isn't a slice and we can't use the index of 0 to get the value
	var rootDirName string
	for k := range rootNames {
		rootDirName = k
	}

	user := e.Get("user").(*models.User)

	// Get all the templates on the server before we unpack this one for the name check later
	currentTemplateNames, err := getTemplateNameArray(e, false)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting template names: %v", err))
	}
	// Will need this later to check if the potential --force overwrite is allowable
	templateStatusArray, err := getTemplatesStatus(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting template status array: %v", err))
	}

	// Save the file to the server
	os.MkdirAll(fmt.Sprintf("%s/users/%s/packer/tmp", ludusInstallPath, user.ProxmoxUsername()), 0755)
	templateTarPath := fmt.Sprintf("%s/users/%s/packer/tmp/%s", ludusInstallPath, user.ProxmoxUsername(), rootDirName)
	templateDirPath := fmt.Sprintf("%s/users/%s/packer/%s", ludusInstallPath, user.ProxmoxUsername(), rootDirName)
	if _, err := os.Stat(templateDirPath); errors.Is(err, os.ErrNotExist) {
		// templateDirPath does not exist
	} else {
		if !force {
			return JSONError(e, http.StatusBadRequest, "Template already exists, use --force to overwrite it")
		}
	}
	// Save uploaded tar bytes to temporary path
	err = os.WriteFile(templateTarPath, fileBytes, 0644)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Saving file failed")
	}
	// Destroy the existing folder with the same name if it exists when the user specifies "force"
	if force {
		os.RemoveAll(templateDirPath)
	}
	err = Untar(templateTarPath, fmt.Sprintf("%s/users/%s/packer", ludusInstallPath, user.ProxmoxUsername()))
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Error untaring file:"+err.Error())
	}
	os.Remove(templateTarPath)

	// Check the uploaded folder for a packer file
	uploadedTemplatePackerFiles, err := findFiles(templateDirPath, "pkr.hcl", "pkr.json")
	if err != nil {
		removeErr := os.RemoveAll(templateDirPath)
		if removeErr != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding *.pkr.hcl or *.pkr.json files in tar AND Error removing '%s': %v", templateDirPath, removeErr))
		}
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error finding *.pkr.hcl or *.pkr.json files: %v", err))
	}
	if len(uploadedTemplatePackerFiles) == 0 {
		removeErr := os.RemoveAll(templateDirPath)
		if removeErr != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("No packer file (*.pkr.hcl or *.pkr.json) found in the tar AND Error removing '%s': %v", templateDirPath, removeErr))
		}
		return JSONError(e, http.StatusInternalServerError, "No packer file (*.pkr.hcl or *.pkr.json) found in the tar!")
	} else if len(uploadedTemplatePackerFiles) > 1 {
		removeErr := os.RemoveAll(templateDirPath)
		if removeErr != nil {
			return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("More than one packer file (*.pkr.hcl or *.pkr.json) found in the tar AND Error removing '%s': %v", templateDirPath, removeErr))
		}
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("More than one packer file (*.pkr.hcl or *.pkr.json) found in the tar: %v", uploadedTemplatePackerFiles))
	} else {
		// Check the name of this template to see if it is already on the server - templates must have unique names
		templateStringRegex, _ := regexp.Compile(templateRegex)
		// The if else chain above has validated we only have one entry in the uploadedTemplatePackerFiles slice
		thisTemplateName := extractTemplateNameFromHCL(uploadedTemplatePackerFiles[0], templateStringRegex)
		if slices.Contains(currentTemplateNames, thisTemplateName) {
			// The uploaded template exists on the server, but this could be a `--force` override of a template in the user's packer dir
			if force {
				// The user is forcing the upload, so make sure the template they are forcing is one of their own that was previously deleted and overwritten in this function
				existingTemplateFilePath := ""
				for _, template := range templateStatusArray {
					if template.Name == thisTemplateName {
						existingTemplateFilePath = template.FilePath
					}
				}
				// Path will either be None (other user's template), the install path/packer (built-in templates), or the user's packer dir
				if !strings.Contains(existingTemplateFilePath, fmt.Sprintf("%s/users/%s/", ludusInstallPath, user.ProxmoxUsername())) {
					var errorString string
					if existingTemplateFilePath != "None" {
						errorString = fmt.Sprintf("'%s' is a template that does not belong to you (path: %s)", thisTemplateName, existingTemplateFilePath)
					} else {
						errorString = fmt.Sprintf("'%s' is a template that does not belong to you", thisTemplateName)
					}
					// We need to remove the untar'd template dir now since the template name is either another user's or built-in
					removeErr := os.RemoveAll(templateDirPath)
					if removeErr != nil {
						errorString = fmt.Sprintf("'%s' is a template that does not belong to you AND Error removing '%s': %v", thisTemplateName, templateDirPath, removeErr)
					}
					return JSONError(e, http.StatusBadRequest, errorString)
				} else {
					return JSONResult(e, http.StatusOK, "Successfully added template")
				}
			} else {
				// The template name exists on the server and it isn't a template this user previously had (would have hit the 'already exists' error above) so remove it from the file system
				removeErr := os.RemoveAll(templateDirPath)
				if removeErr != nil {
					return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("The uploaded template name is already present on the server. Template names must be unique. AND Error removing '%s': %v", templateDirPath, removeErr))
				}
				return JSONError(e, http.StatusInternalServerError, "The uploaded template name is already present on the server. Template names must be unique.")
			}
		}
	}

	return JSONResult(e, http.StatusOK, "Successfully added template")
}

// Find the packer process(es) for this user and kill them
func AbortPacker(e *core.RequestEvent) error {
	user := e.Get("user").(*models.User)
	// First touch the canary file to prevent more templates being built (in the case of "all" and not parallel)
	touch(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername()))

	// Empty the sync map (queue) of any templates this user was building
	templateProgressStore.Range(func(key, value interface{}) bool {
		logger.Debug(fmt.Sprintf("Template progress store key: %v, value: %v", key, value))
		// If the value matches this user, delete the key
		if value == user.ProxmoxUsername() {
			templateProgressStore.Delete(key)
		}
		return true // continue iteration
	})

	// Get all the commands in the command manager
	commandManager := commandmanager.GetInstance()
	commands := commandManager.GetAllCommands()
	foundCommand := false
	for _, packerCommand := range commands {
		if packerCommand.Metadata["command_type"] == "packer_build" && packerCommand.Metadata["proxmox_username"] == user.ProxmoxUsername() && packerCommand.Status == commandmanager.StatusRunning {
			logger.Debug(fmt.Sprintf("Killing packer command for template %s with PID %d", packerCommand.Metadata["template_name"], packerCommand.PID))
			templateName := packerCommand.Metadata["template_name"]
			err := commandManager.KillCommand(packerCommand.ID)
			if err != nil {
				return JSONError(e, http.StatusInternalServerError, "Error killing packer command for template "+templateName+": "+err.Error())
			}
			if runningLogPath, ok := getRunningTemplateLogPathByUserAndName(user.Id, templateName); ok {
				finalizeRunningTemplateLogHistory(e.App, user.Id, templateName, "aborted", runningLogPath, time.Now())
			}
			commandManager.RemoveCommand(packerCommand.ID)
			foundCommand = true
		}
	}

	if !foundCommand {
		return JSONError(e, http.StatusInternalServerError, "No packer processes found for user "+user.ProxmoxUsername())
	}

	// Then find and kill any running Packer processes
	// packerPids := findPackerPidsForUser(user.ProxmoxUsername())
	// if len(packerPids) == 0 {
	// 	return JSONError(e, http.StatusInternalServerError, "No packer processes found for user "+user.ProxmoxUsername())
	// }
	// for _, pid := range packerPids {
	// 	killProcessAndChildren(pid)
	// }

	return JSONResult(e, http.StatusOK, "Packer process(es) aborted for user "+user.ProxmoxUsername())

}

// DeleteTemplate - removes a template folder from the server
func DeleteTemplate(e *core.RequestEvent) error {

	templateName := e.Request.PathValue("templateName")
	if len(templateName) == 0 {
		return JSONError(e, http.StatusBadRequest, "Template name not provided")
	}
	templateStatusArray, err := getTemplatesStatus(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, err.Error())
	}

	// Get the index of the template we want in the array
	index := slices.IndexFunc(templateStatusArray, func(t TemplateStatus) bool { return t.Name == templateName })
	if index == -1 {
		return JSONError(e, http.StatusNotFound, fmt.Sprintf("Template '%s' not found", templateName))
	}

	// Check that this is a user template
	user := e.Get("user").(*models.User)
	templateDir := filepath.Dir(templateStatusArray[index].FilePath)
	if !strings.Contains(templateDir, fmt.Sprintf("%s/users/%s/", ludusInstallPath, user.ProxmoxUsername())) && !strings.Contains(templateDir, fmt.Sprintf("%s/packer/", ludusInstallPath)) {
		if !e.Auth.GetBool("isAdmin") {
			return JSONError(e, http.StatusBadRequest, fmt.Sprintf("'%s' is a user template that belongs to another user and you are not an Admin (path: %s)", templateName, templateDir))
		}
	}

	// If the template is built, remove it from proxmox
	if templateStatusArray[index].Built {
		proxmoxClient, err := GetProxmoxClientForUserUsingToken(e)
		if err != nil {
			return JSONError(e, http.StatusInternalServerError, err.Error())
		}
		thisVmRef, err := proxmoxClient.GetVmRefByName(templateName)
		if err != nil {
			return JSONError(e, http.StatusConflict, err.Error())
		}
		_, err = proxmoxClient.DeleteVm(thisVmRef)
		if err != nil {
			return JSONError(e, http.StatusConflict, err.Error())
		}
	}

	if strings.Contains(templateDir, fmt.Sprintf("%s/packer/", ludusInstallPath)) {
		return JSONResult(e, http.StatusOK, fmt.Sprintf("Built template removed but template '%s' is a ludus server included template and cannot be deleted", templateName))
	}

	// Delete the folder that contains the template file
	err = os.RemoveAll(templateDir)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error removing '%s' (path: %s): %v", templateName, templateDir, err))
	}

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Template '%s' removed", templateName))
}
