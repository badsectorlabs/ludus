package ludusapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
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

	"github.com/gin-gonic/gin"
)

type TemplateStatus struct {
	Name     string `json:"name"`
	Built    bool   `json:"built"`
	FilePath string `json:"-"`
}

const templateRegex string = `(?m)[^"]*?-template`

var templateProgressStore sync.Map

// Get all available packer templates from the main packer dir and the user packer dir
func getAvailableTemplates(user UserObject) ([]string, error) {
	globalTemplates, err := findFiles(fmt.Sprintf("%s/packer/", ludusInstallPath), ".hcl", ".json")
	if err != nil {
		return nil, errors.New("unable to get global packer templates")
	}
	userTemplates, err := findFiles(fmt.Sprintf("%s/users/%s/packer/", ludusInstallPath, user.ProxmoxUsername), ".hcl", ".json")
	if err != nil {
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

func Run(command string, workingDir string, outputLog string) {

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
	}
}

func buildVMFromTemplateWithPacker(user UserObject, proxmoxPassword string, packerFile string, verbose bool) {

	// Run the longest, grossest packer command you have ever seen...
	// There should be a better way to do this, but apparently not: https://devops.stackexchange.com/questions/14181/is-it-possible-to-control-packer-from-golang

	workingDir := filepath.Dir(packerFile)
	packerLogFile := fmt.Sprintf("%s/users/%s/packer.log", ludusInstallPath, user.ProxmoxUsername)
	packerLogFileDebug := fmt.Sprintf("%s/users/%s/packer-debug.log", ludusInstallPath, user.ProxmoxUsername)
	usersPackerDir := fmt.Sprintf("%s/users/%s/packer", ludusInstallPath, user.ProxmoxUsername)
	usersAnsibleDir := fmt.Sprintf("%s/users/%s/.ansible", ludusInstallPath, user.ProxmoxUsername)
	os.MkdirAll(fmt.Sprintf("%s/users/%s/packer/tmp", ludusInstallPath, user.ProxmoxUsername), 0755)

	tmplStr := `PACKER_PLUGIN_PATH={{.LudusInstallPath}}/resources/packer/plugins PKR_VAR_proxmox_password={{.ProxmoxPassword}} PACKER_CONFIG_DIR={{.UsersPackerDir}} PACKER_CACHE_DIR={{.UsersPackerDir}}/packer_cache ` +
		`CHECKPOINT_DISABLE=1 PACKER_LOG={{.PackerVerbose}} PACKER_LOG_PATH='{{.PackerLogFile}}' TMPDIR='{{.UsersPackerDir}}/tmp' packer build -on-error=cleanup ` +
		`-var 'proxmox_url={{.ProxmoxURL}}/api2/json' -var 'proxmox_host={{.ProxmoxHost}}' ` +
		`-var 'proxmox_username={{.ProxmoxUsername}}@pam' -var 'proxmox_skip_tls_verify={{.ProxmoxSkipTLSVerify}}' ` +
		`-var 'proxmox_pool=SHARED' -var 'proxmox_storage_pool={{.ProxmoxVMStoragePool}}' ` +
		`-var 'proxmox_storage_format={{.ProxmoxVMStorageFormat}}' -var 'iso_storage_pool={{.ProxmoxISOStoragePool}}' ` +
		`-var 'ansible_home={{.UsersAnsibleDir}}' {{.PackerFile}}`

	var packerVerbose string
	if verbose {
		packerVerbose = "1"
	} else {
		packerVerbose = "0"
	}

	data := struct {
		LudusInstallPath       string
		ProxmoxPassword        string
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
	}{
		ludusInstallPath,
		proxmoxPassword,
		usersPackerDir,
		packerVerbose,
		packerLogFile,
		ServerConfiguration.ProxmoxURL,
		ServerConfiguration.ProxmoxNode,
		user.ProxmoxUsername,
		strconv.FormatBool(ServerConfiguration.ProxmoxInvalidCert),
		ServerConfiguration.ProxmoxVMStoragePool,
		ServerConfiguration.ProxmoxVMStorageFormat,
		ServerConfiguration.ProxmoxISOStoragePool,
		usersAnsibleDir,
		packerFile,
	}

	tmpl, err := template.New("command").Parse(tmplStr)
	if err != nil {
		fmt.Println("Failed to parse template:", err)
		return
	}

	// Create a buffer to hold the rendered output
	var renderedOutput bytes.Buffer

	err = tmpl.Execute(&renderedOutput, data)
	if err != nil {
		fmt.Println("Failed to execute template:", err)
		return
	}

	// Get the contents of the buffer as a string
	renderedOutputString := renderedOutput.String()

	// Run the command and log to a file
	Run(renderedOutputString, workingDir, packerLogFileDebug)

}

func buildVMsFromTemplates(templateStatusArray []TemplateStatus, user UserObject, proxmoxPassword string, templateName string, parallel int, verbose bool) error {
	// Create a WaitGroup to wait for all goroutines to finish.
	var wg sync.WaitGroup

	// Create a semaphore (buffered channel of empty structs) to limit the number of concurrent goroutines.
	semaphoreChannel := make(chan struct{}, parallel)

	// Iterate over the array of template statuses.
	for _, templateStatus := range templateStatusArray {
		// Check the canary file before launching a new goroutine.
		if modifiedTimeLessThan(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername), 10) {
			log.Println("Canary check failed")
			break
		}

		// Determine whether a VM should be built from the template.
		// Check that:
		// 1. The template is not already built (proxmox returns this)
		// 2. The user asked to build this specific template (by passing 'all' or by name)
		// 3. This template is not already in progress - the user could call this method twice with a parallel value > number of templates,
		//    and longer running templates would then be built a second time as the have not finished building to return .Built by proxmox
		if !templateStatus.Built && (templateName == "all" || templateStatus.Name == templateName) {
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
			templateProgressStore.Store(templateStatus.Name, user.ProxmoxUsername)

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
				if modifiedTimeLessThan(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername), 10) {
					log.Println("Canary check failed")
					return
				}

				// Build the VM from the template.
				buildVMFromTemplateWithPacker(user, proxmoxPassword, templateStatus.FilePath, verbose)

			}(templateStatus, user.ProxmoxUsername)

			// Sleep for 3 seconds so the server isn't flooded with builds all at exactly the same time if the user gives a high number for parallel
			time.Sleep(3 * time.Second)

		}
	}

	// Wait for all goroutines to finish.
	wg.Wait()

	return nil
}

func getTemplatesStatus(c *gin.Context) ([]TemplateStatus, error) {
	var user UserObject

	user, err := getUserObject(c)
	if err != nil {
		return nil, err // JSON set in getUserObject
	}

	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return nil, errors.New("error getting proxmox password for user") // JSON set in getProxmoxPasswordForUser
	}

	proxmoxClient, err := getProxmoxClientForUser(c)
	if err != nil {
		return nil, err // JSON set in getProxmoxClientForUser
	}

	rawVMs, err := proxmoxClient.GetVmList()
	if err != nil {
		return nil, err
	}

	var templates []string

	// Loop over the VMs and add them to the templates array
	vms := rawVMs["data"].([]interface{})
	for vmCounter := range vms {
		vm := vms[vmCounter].(map[string]interface{})
		// Only include VM templates
		if int(vm["template"].(float64)) == 1 {
			templates = append(templates, vm["name"].(string))
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

func getTemplateNameArray(c *gin.Context, onlyBuilt bool) ([]string, error) {
	// Get a list of all the built templates on the system
	templateStatusArray, err := getTemplatesStatus(c)
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

func templateActions(c *gin.Context, buildTemplates bool, templateName string, parallel int, verbose bool) {

	if parallel == 0 {
		parallel = 1
	}

	templateStatusArray, err := getTemplatesStatus(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !buildTemplates {
		c.JSON(http.StatusOK, templateStatusArray)
		return
	}

	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	proxmoxPassword := getProxmoxPasswordForUser(user, c)
	if proxmoxPassword == "" {
		return // JSON set in getProxmoxPasswordForUser
	}

	go buildVMsFromTemplates(templateStatusArray, user, proxmoxPassword, templateName, parallel, verbose)

	c.JSON(http.StatusOK, gin.H{
		"result": fmt.Sprintf("Template building started - this will take a while. Building %d template(s) at a time.", parallel),
	})

}

// GetTemplates - returns a list of VM templates available for use in Ludus
func GetTemplates(c *gin.Context) {
	templateActions(c, false, "", 1, false)
}

// Build all templates
func BuildTemplates(c *gin.Context) {
	type TemplateBody struct {
		Template string `json:"template"`
		Parallel int    `json:"parallel"`
	}
	var templateBody TemplateBody
	c.Bind(&templateBody)

	// Set the default value to all if nothing is presented
	if templateBody.Template == "" {
		templateBody.Template = "all"
	}

	// Set the default value to 1 if nothing is presented
	if templateBody.Parallel == 0 {
		templateBody.Parallel = 1
	}

	verbose := true
	if templateBody.Parallel > 1 {
		verbose = false
	}

	if templateBody.Template != "all" {
		templateArray, _ := getTemplateNameArray(c, false)
		if !slices.Contains(templateArray, templateBody.Template) {
			c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Template '%s' not found", templateBody.Template)})
			return
		}
	}

	templateActions(c, true, templateBody.Template, templateBody.Parallel, verbose)
}

// GetPackerLogs - retrieves the latest packer logs
func GetPackerLogs(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	packerLogPath := fmt.Sprintf("%s/users/%s/packer.log", ludusInstallPath, user.ProxmoxUsername)
	GetLogsFromFile(c, packerLogPath)
}

func GetTemplateStatus(c *gin.Context) {
	templatesInProgress := findRunningPackerProcesses()
	c.JSON(http.StatusOK, templatesInProgress)
}

func PutTemplateTar(c *gin.Context) {
	// Parse the multipart form
	if err := c.Request.ParseMultipartForm(1073741824); err != nil { // allow 1GB
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Retrieve the 'force' field and convert it to boolean
	forceStr := c.Request.FormValue("force")
	force, err := strconv.ParseBool(forceStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid boolean value"})
		return
	}

	// Retrieve the file
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File retrieval failed"})
		return
	}

	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}

	// Save the file to the server
	os.MkdirAll(fmt.Sprintf("%s/users/%s/packer/tmp", ludusInstallPath, user.ProxmoxUsername), 0755)
	templateTarPath := fmt.Sprintf("%s/users/%s/packer/tmp/%s", ludusInstallPath, user.ProxmoxUsername, file.Filename)
	templateDirPath := fmt.Sprintf("%s/users/%s/packer/%s", ludusInstallPath, user.ProxmoxUsername, file.Filename)
	if _, err := os.Stat(templateDirPath); errors.Is(err, os.ErrNotExist) {
		// templateDirPath does not exist
	} else {
		if !force {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Template already exists, use --force to overwrite it"})
			return
		}
	}
	err = c.SaveUploadedFile(file, templateTarPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Saving file failed"})
		return
	}
	// Destroy the existing folder with the same name if it exists when the user specifies "force"
	if force {
		os.RemoveAll(templateDirPath)
	}
	err = Untar(templateTarPath, fmt.Sprintf("%s/users/%s/packer", ludusInstallPath, user.ProxmoxUsername))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error untaring file:" + err.Error()})
		return
	}
	os.Remove(templateTarPath)
	c.JSON(http.StatusOK, gin.H{"result": "Successfully added template"})
}

// Find the packer process(es) for this user and kill them
func AbortPacker(c *gin.Context) {
	user, err := getUserObject(c)
	if err != nil {
		return // JSON set in getUserObject
	}
	// First touch the canary file to prevent more templates being built (in the case of "all" and not parallel)
	touch(fmt.Sprintf("%s/users/%s/.stop-template-build", ludusInstallPath, user.ProxmoxUsername))

	// Empty the sync map (queue) of any templates this user was building
	templateProgressStore.Range(func(key, value interface{}) bool {
		// If the value matches this user, delete the key
		if value == user.ProxmoxUsername {
			templateProgressStore.Delete(key)
		}
		return true // continue iteration
	})

	// Then find and kill any running Packer processes
	packerPids := findPackerPidsForUser(user.ProxmoxUsername)
	if len(packerPids) == 0 {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "No packer processes found for user " + user.ProxmoxUsername})
		return
	}
	for _, pid := range packerPids {
		killProcessAndChildren(pid)
	}

	c.JSON(http.StatusOK, gin.H{"result": "Packer process(es) aborted for user " + user.ProxmoxUsername})

}

// DeleteTemplate - removes a template folder from the server
func DeleteTemplate(c *gin.Context) {

	templateName := c.Param("templateName")
	if len(templateName) == 0 {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Template name not provided"})
		return
	}
	templateStatusArray, err := getTemplatesStatus(c)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get the index of the template we want in the array
	index := slices.IndexFunc(templateStatusArray, func(t TemplateStatus) bool { return t.Name == templateName })
	if index == -1 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("Template '%s' not found", templateName)})
		return
	}

	// If the template is built, remove it from proxmox
	if templateStatusArray[index].Built {
		proxmoxClient, err := getProxmoxClientForUser(c)
		if err != nil {
			return // JSON set in getProxmoxClientForUser
		}
		thisVmRef, err := proxmoxClient.GetVmRefByName(templateName)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		_, err = proxmoxClient.DeleteVm(thisVmRef)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
	}

	// Check that this is a user template
	userObject, err := getUserObject(c)
	if err != nil {
		return
	}
	templateDir := filepath.Dir(templateStatusArray[index].FilePath)
	if !strings.Contains(templateDir, fmt.Sprintf("%s/users/%s/", ludusInstallPath, userObject.ProxmoxUsername)) {
		c.JSON(http.StatusOK, gin.H{"error": fmt.Sprintf("Built template removed but template '%s' is a ludus built-in template and cannot be deleted", templateName)})
		return
	}

	// Delete the folder that contains the template file
	err = os.RemoveAll(templateDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Error removing '%s': %v", templateName, err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Template '%s' removed", templateName)})
}
