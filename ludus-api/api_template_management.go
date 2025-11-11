package ludusapi

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"ludusapi/dto"
	"ludusapi/models"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

type TemplateStatus struct {
	Name     string `json:"name"`
	Built    bool   `json:"built"`
	FilePath string `json:"-"`
}

const templateRegex string = `(?m)[^"]*?-template`

var templateProgressStore sync.Map

// Get all available packer templates from the main packer dir and the user packer dir
func getAvailableTemplates(user *models.User) ([]string, error) {
	globalTemplates, err := findFiles(fmt.Sprintf("%s/packer/", ludusInstallPath), "pkr.hcl", "pkr.json")
	if err != nil {
		return nil, errors.New("unable to get global packer templates")
	}
	userTemplates, err := findFiles(fmt.Sprintf("%s/users/%s/packer/", ludusInstallPath, user.ProxmoxUsername()), ".hcl", ".json")
	if err != nil {
		return nil, errors.New("unable to get user packer templates")
	}
	// Filter out pkrvars.hcl files from userTemplates, re: https://gitlab.com/badsectorlabs/ludus/-/issues/103
	userTemplates = slices.DeleteFunc(userTemplates, func(template string) bool {
		return strings.HasSuffix(template, "pkrvars.hcl")
	})
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

func Run(command string, workingDir string, outputLog string) error {

	f, err := os.OpenFile(outputLog, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0660)
	if err != nil {
		log.Fatalf("error opening file: %v", err)
	}
	defer f.Close()
	log.SetOutput(f)

	shellBin := "/bin/bash"
	if _, err := os.Stat(shellBin); err != nil {
		if _, err = os.Stat("/bin/sh"); err != nil {
			log.Println("Could not find /bin/bash or /bin/sh")
		} else {
			shellBin = "/bin/sh"
		}
	}
	log.Println(command)
	cmd := exec.Command(shellBin)
	cmd.Dir = workingDir
	cmd.Stdin = strings.NewReader(command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	var outputString string
	if out.String() == "" {
		outputString = "Command processed (no output)."
	} else {
		outputString = out.String()
	}
	log.Println(outputString)
	if err != nil {
		log.Println(err.Error())
		return err
	}
	return nil
}

func buildVMFromTemplateWithPacker(user *models.User, packerFile string, verbose bool) {
	proxmoxPassword, err := getProxmoxPasswordForUser(user)
	if err != nil {
		logger.Error(fmt.Sprintf("Unable to get proxmox password: %v\n", err))
	}
	proxmoxToken, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
	if err != nil {
		logger.Error(fmt.Sprintf("Unable to decrypt proxmox token secret: %v\n", err))
	}
	// Run the longest, grossest packer command you have ever seen...
	// There should be a better way to do this, but apparently not: https://devops.stackexchange.com/questions/14181/is-it-possible-to-control-packer-from-golang

	workingDir := filepath.Dir(packerFile)
	packerLogFile := fmt.Sprintf("%s/users/%s/packer.log", ludusInstallPath, user.ProxmoxUsername())
	packerLogFileDebug := fmt.Sprintf("%s/users/%s/packer-debug.log", ludusInstallPath, user.ProxmoxUsername())
	usersPackerDir := fmt.Sprintf("%s/users/%s/packer", ludusInstallPath, user.ProxmoxUsername())
	usersAnsibleDir := fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername())
	os.MkdirAll(fmt.Sprintf("%s/users/%s/packer/tmp", ludusInstallPath, user.ProxmoxUsername()), 0755)

	tmplStr := `PACKER_PLUGIN_PATH={{.LudusInstallPath}}/resources/packer/plugins PKR_VAR_proxmox_password={{.ProxmoxPassword}} PKR_VAR_proxmox_token={{.ProxmoxToken}} PROXMOX_TOKEN={{ .ProxmoxToken }}` +
		`PACKER_CONFIG_DIR={{.UsersPackerDir}} PACKER_CACHE_DIR={{.UsersPackerDir}}/packer_cache ` +
		`CHECKPOINT_DISABLE=1 PACKER_LOG={{.PackerVerbose}} PACKER_LOG_PATH='{{.PackerLogFile}}' TMPDIR='{{.UsersPackerDir}}/tmp' packer build -on-error=cleanup ` +
		`-var 'proxmox_url={{.ProxmoxURL}}/api2/json' -var 'proxmox_host={{.ProxmoxHost}}' ` +
		`-var 'proxmox_username={{.ProxmoxUsername}}@pam' -var 'proxmox_skip_tls_verify={{.ProxmoxSkipTLSVerify}}' ` +
		`-var 'proxmox_pool=SHARED' -var 'proxmox_storage_pool={{.ProxmoxVMStoragePool}}' ` +
		`-var 'proxmox_storage_format={{.ProxmoxVMStorageFormat}}' -var 'iso_storage_pool={{.ProxmoxISOStoragePool}}' ` +
		`-var 'ansible_home={{.UsersAnsibleDir}}' -var 'ludus_nat_interface={{.LudusNATInterface}}' {{.PackerFile}}`

	var packerVerbose string
	if verbose {
		packerVerbose = "1"
		// Remove the verbose log file, if this packer build fails in verbose mode we append the debug log to the regular log file
		// so we need to make sure we don't spam the user with a ton of debug logs from old builds
		os.Remove(packerLogFileDebug)
	} else {
		packerVerbose = "0"
		// Since the log file is only used in verbose mode, we need to write to the log file path with a message to alert the user that
		// no logs will be written in parallel mode
		file, err := os.OpenFile(packerLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Printf("Error opening file: %v\n", err)
			return
		}
		defer file.Close()

		if _, err := file.Write([]byte("\n\n=>================\n=> No logs will be written in parallel mode\n=>================\n\n")); err != nil {
			fmt.Printf("Error writing to file: %v\n", err)
			return
		}
	}

	data := struct {
		LudusInstallPath       string
		ProxmoxPassword        string
		ProxmoxToken           string
		UsersPackerDir         string
		PackerVerbose          string
		PackerLogFile          string
		ProxmoxURL             string
		ProxmoxHost            string
		ProxmoxUsername        string
		ProxmoxSkipTLSVerify   string
		ProxmoxVMStoragePool   string
		ProxmoxVMStorageFormat string
		ProxmoxISOStoragePool  string
		UsersAnsibleDir        string
		PackerFile             string
		LudusNATInterface      string
	}{
		ludusInstallPath,
		proxmoxPassword,
		proxmoxToken,
		usersPackerDir,
		packerVerbose,
		packerLogFile,
		ServerConfiguration.ProxmoxURL,
		ServerConfiguration.ProxmoxNode,
		user.ProxmoxUsername(),
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
		return
	}

	// Create a buffer to hold the rendered output
	var renderedOutput bytes.Buffer

	err = tmpl.Execute(&renderedOutput, data)
	if err != nil {
		logger.Error(fmt.Sprintf("Failed to execute template: %v\n", err))
		return
	}

	// Get the contents of the buffer as a string
	renderedOutputString := renderedOutput.String()

	// Run the command and log to a file
	packerCommandError := Run(renderedOutputString, workingDir, packerLogFileDebug)

	// Write 'Build complete' to the packerLogFile to indicate the end of the build so the user knows it's done
	if verbose && packerCommandError == nil {
		file, err := os.OpenFile(packerLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Error(fmt.Sprintf("Error opening file: %v\n", err))
			return
		}
		defer file.Close()

		if _, err := file.Write([]byte("\n\n=>================\n=> Build complete!\n=>================\n\n")); err != nil {
			logger.Error(fmt.Sprintf("Error writing to file: %v\n", err))
			return
		}
	} else if verbose && packerCommandError != nil {
		// Copy the debug log to the regular log if the command failed
		if err := copyFileContents(packerLogFileDebug, packerLogFile); err != nil {
			logger.Error(fmt.Sprintf("Failed to copy file contents: %v\n", err))
		}
	}

}

func buildVMsFromTemplates(templateStatusArray []TemplateStatus, user *models.User, templateNames []string, parallel int, verbose bool) error {
	// Create a WaitGroup to wait for all goroutines to finish.
	var wg sync.WaitGroup

	// Create a semaphore (buffered channel of empty structs) to limit the number of concurrent goroutines.
	semaphoreChannel := make(chan struct{}, parallel)

	// Iterate over the array of template statuses.
	for _, templateStatus := range templateStatusArray {
		// Check the canary file before launching a new goroutine.
		if modifiedTimeLessThan(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername()), 10) {
			log.Println("Canary check failed")
			break
		}

		// Determine whether a VM should be built from the template.
		// Check that:
		// 1. The template is not already built (proxmox returns this)
		// 2. The user asked to build this specific template (by including it in the templateNames array or by specifying "all")
		// 3. This template is not already in progress - the user could call this method twice with a parallel value > number of templates,
		//    and longer running templates would then be built a second time as the have not finished building to return .Built by proxmox
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

			if buildAll {
				// If "all" is specified, build all templates that aren't already built
				// shouldBuildTemplate is already set to !templateStatus.Built above
			} else {
				// Otherwise, only build templates that are explicitly in the list
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

		if shouldBuildTemplate {
			_, ok := templateProgressStore.Load(templateStatus.Name)
			if ok {
				// This template is already building or in the queue to be built by a user
				// skip to the next template instead of queuing this one again
				continue
			}

			// If a VM should be built, increment the WaitGroup counter
			wg.Add(1)

			// Add this template name to the sync map and set its value to true to indicate that it is building or in the queue to build
			// Have to get a little tricky here, since if two users are building templates and one aborts
			// we don't want to remove queued templates for the other user
			// To accomplish this, we store the username of the building user as the value to the key of the template.
			templateProgressStore.Store(templateStatus.Name, user.ProxmoxUsername())

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
				if modifiedTimeLessThan(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername()), 10) {
					logger.Error("Canary check failed - not building template " + templateStatus.Name)
					return
				}

				// Build the VM from the template.
				buildVMFromTemplateWithPacker(user, templateStatus.FilePath, verbose)

			}(templateStatus, user.ProxmoxUsername())

			// Sleep for 3 seconds so the server isn't flooded with builds all at exactly the same time if the user gives a high number for parallel
			time.Sleep(3 * time.Second)

		}
	}

	// Wait for all goroutines to finish.
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
		if slices.Contains(templates, thisTemplateName) {
			thisTemplateStatus.Built = true
		} else {
			thisTemplateStatus.Built = false
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

func templateActions(e *core.RequestEvent, buildTemplates bool, templateNames []string, parallel int, verbose bool) error {

	if parallel == 0 {
		parallel = 1
	}

	templateStatusArray, err := getTemplatesStatus(e)
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, "Unable to get template status array: "+err.Error())
	}

	if !buildTemplates {
		return e.JSON(http.StatusOK, templateStatusArray)
	}

	user := e.Get("user").(*models.User)

	go buildVMsFromTemplates(templateStatusArray, user, templateNames, parallel, verbose)

	return JSONResult(e, http.StatusOK, fmt.Sprintf("Template building started - this will take a while. Building %d template(s) at a time.", parallel))

}

// GetTemplates - returns a list of VM templates available for use in Ludus
func GetTemplates(e *core.RequestEvent) error {
	return templateActions(e, false, []string{}, 1, false)
}

func GetTemplateStatus(e *core.RequestEvent) error {
	templatesInProgress := findRunningPackerProcesses()
	return e.JSON(http.StatusOK, templatesInProgress)
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
		return JSONError(e, http.StatusInternalServerError, "More than one packer file (*.pkr.hcl or *.pkr.json) found in the tar!")
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
				return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("The uploaded template name is already present on the server. Template names must be unique. AND Error removing '%s': %v", templateDirPath, removeErr))
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
		// If the value matches this user, delete the key
		if value == user.ProxmoxUsername() {
			templateProgressStore.Delete(key)
		}
		return true // continue iteration
	})

	// Then find and kill any running Packer processes
	packerPids := findPackerPidsForUser(user.ProxmoxUsername())
	if len(packerPids) == 0 {
		return JSONError(e, http.StatusInternalServerError, "No packer processes found for user "+user.ProxmoxUsername())
	}
	for _, pid := range packerPids {
		killProcessAndChildren(pid)
	}

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
