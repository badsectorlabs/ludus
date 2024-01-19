package main

import (
	"bytes"
	"embed"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

// return true if the file exists and is not a directory, else false
func fileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		// Permission error potentially
		log.Printf("%v", err)
		return false
	}
	return !info.IsDir()
}

// return true if the path exists (file or directory)
func exists(filePath string) bool {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return false
	}

	return true
}

// recursively extract an embed.FS directory to the ludus install path, skipping the file "config.yml.example"
// all files will be have 0644 permissions and all directories will have 0755 permissions
func extractDirectory(embeddedFS embed.FS, embeddedBaseDir string) {
	embeddedDirEntries, err := embeddedFS.ReadDir(embeddedBaseDir)
	if err != nil {
		log.Fatal(err.Error())
	}

	for _, embeddedDirEntry := range embeddedDirEntries {
		// log.Printf("Processing: %s Dir: %t\n", ansibleDirEntry.Name(), ansibleDirEntry.IsDir())
		if embeddedDirEntry.IsDir() { // Dir
			os.MkdirAll(fmt.Sprintf("%s/%s/%s", ludusInstallPath, embeddedBaseDir, embeddedDirEntry.Name()), 0755)
			// It's recursion time! Extract this directory, and any directories inside of it
			extractDirectory(embeddedFS, fmt.Sprintf("%s/%s", embeddedBaseDir, embeddedDirEntry.Name()))
		} else { // File
			// Skip the config example file
			if embeddedDirEntry.Name() == "config.yml.example" {
				continue
			}
			fileContent, err := embeddedFS.ReadFile(fmt.Sprintf("%s/%s", embeddedBaseDir, embeddedDirEntry.Name()))
			if err != nil {
				log.Fatal(err.Error())
			}

			filename := fmt.Sprintf("%s/%s/%s", ludusInstallPath, embeddedBaseDir, embeddedDirEntry.Name())
			// Make sure the dir we are writing the file into exists
			fileDir := fmt.Sprintf("%s/%s", ludusInstallPath, embeddedBaseDir)
			if _, err := os.Stat(fileDir); os.IsNotExist(err) {
				os.MkdirAll(fileDir, 0755)
			}
			if err := os.WriteFile(filename, fileContent, 0644); err != nil {
				log.Printf("error os.WriteFile error: %v", err)
				log.Fatal(err.Error())
			}
		}
	}
}

func userExists(username string) bool {
	_, err := user.Lookup(username)
	if err != nil {
		if _, ok := err.(user.UnknownUserError); ok {
			return false
		}
	}
	return true
}

// copy a file from src to dst
func copy(src, dst string) {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		log.Fatal(err.Error())
	}

	if !sourceFileStat.Mode().IsRegular() {
		log.Fatal(fmt.Errorf("%s is not a regular file", src))
	}

	source, err := os.Open(src)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		log.Fatal(err.Error())
	}
	defer destination.Close()
	_, err = io.Copy(destination, source)
	if err != nil {
		log.Fatal(err.Error())
	}
}

// check for vmx or svm features in /proc/cpu with egrep. Will print a message and exit if they are not found.
func checkForVirtualizationSupport() {
	cpuInfo := Run("egrep '(vmx|svm)' --color=never /proc/cpuinfo", false, false)
	if !strings.Contains(cpuInfo, "vmx") && !strings.Contains(cpuInfo, "svm") {
		log.Fatal(`This machine is not capable of virtualization. 
Ludus is requires a host with vmx or svm enabled on the CPU. 
This is usually a bare metal machine or a nested VM with virtualization support enabled.
For Proxmox, see: https://pve.proxmox.com/wiki/Nested_Virtualization`)
	}
}

// check the configuration file to ensure it exists and that it does not have default values
func checkConfig() {
	configPath := fmt.Sprintf("%s/config.yml", ludusPath)

	// First run, no config
	if ludusPath != fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) {
		// If we are running without prompts, generate a config automatically
		if autoGenerateConfig {
			log.Printf("No config.yml found. Generating a config at %s/config.yml. Please check that it contains the correct values.", ludusPath)
			automatedConfigGenerator()
		} else {
			log.Printf("No config.yml found. Generating an example config at %s/config.yml. Please edit it and re-run the install.", ludusPath)

			fileContent, err := embeddedAnsbileDir.ReadFile("ansible/config.yml.example")
			if err != nil {
				log.Fatal(err.Error())
			}

			if err := os.WriteFile(configPath, fileContent, 0644); err != nil {
				log.Printf("error os.WriteFile error: %v", err)
				log.Fatal(err.Error())
			}
			os.Exit(1)
		}

	} else if ludusPath != fmt.Sprintf("%s/ludus-server", ludusInstallPath) { // First run, example config provided
		configContents, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatal(err.Error())
		}
		if strings.Contains(string(configContents), "192.168.0.0") {
			log.Fatalf("The config file (%s) contains example values. Edit it to reflect this machine.\n", configPath)
		}

	} else if ludusPath == fmt.Sprintf("%s/ludus-server", ludusInstallPath) && !fileExists(configPath) { // Installed, but config missing
		log.Printf("Config file (%s) missing!\n", configPath)
		os.Exit(1)
	}

	// Open config file
	file, err := os.Open(configPath)
	if err != nil {
		log.Fatalf("Error opening config: %v", err)
	}
	defer file.Close()

	// Init new YAML decode
	d := yaml.NewDecoder(file)

	// Start YAML decoding from file
	if err := d.Decode(&config); err != nil {
		log.Fatalf("Error decoding config: %v", err)
	}
}

// check if the current uid is zero (root) and throw a fatal error if not
func checkRoot() {
	if os.Geteuid() != 0 {
		log.Fatal("Ludus must be run as root.")
	}
}

// check /etc/os-release for Debian 11, throw a fatal error /etc/os-release does not exist or does not contain the Debian 12 string
func checkDebian12() {
	if fileExists("/etc/os-release") {
		osReleaseContents, err := os.ReadFile("/etc/os-release")
		if err != nil {
			log.Fatal(err.Error())
		}
		if !strings.Contains(string(osReleaseContents), "Debian GNU/Linux 12 (bookworm)") {
			log.Fatal("/etc/os-release did not indicate this is Debian 12. Ludus only supports Debian 12.")
		}
	} else {
		log.Fatal("Could not read /etc/os-release to check for Debian 12. Ludus only supports Debian 12.")
	}
}

// run a command in /bin/bash or /bin/sh (if /bin/bash doesn't exist) and optionally print the output
// if the command fails, and fatalFailure is set to true, it will exit the whole program
func Run(command string, printOutput bool, fatalFailure bool) string {
	shellBin := "/bin/bash"
	if _, err := os.Stat(shellBin); err != nil {
		if _, err = os.Stat("/bin/sh"); err != nil {
			log.Println("Could not find /bin/bash or /bin/sh")
			return "Could not find /bin/bash or /bin/sh"
		} else {
			shellBin = "/bin/sh"
		}
	}

	cmd := exec.Command(shellBin)
	cmd.Stdin = strings.NewReader(command)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	var outputString string
	if out.String() == "" {
		outputString = "Command processed (no output)."
	} else {
		outputString = out.String()
	}
	if printOutput {
		log.Println(outputString)
	}
	if err != nil && fatalFailure {
		log.Printf("Fail error running %s: %s", command, err.Error())
		log.Fatal(err.Error())
	}
	return outputString
}

// get interface information for the machine, and create a config automatically
// useful for CI/CD tests
func automatedConfigGenerator() {
	f, err := os.Create("config.yml")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	// Name this node with the Pipeline ID if we are in CI
	// Make sure this is not all numbers or it will be interpreted
	// as an IP address
	var nodeName string
	if len(os.Args) > 2 {
		nodeName = os.Args[2]
	} else {
		nodeName = "ludus"
	}

	interfaces, _ := net.Interfaces()
	_, localhost, _ := net.ParseCIDR("127.0.0.0/8")
	for _, inter := range interfaces {
		addrs, _ := inter.Addrs()
		for _, ipaddr := range addrs {
			ipv4, ipnet, _ := net.ParseCIDR(ipaddr.String())
			isIPv4 := ipv4.To4()
			if isIPv4 != nil && !localhost.Contains(ipv4) {
				_, err = f.WriteString("---\n")
				if err != nil {
					log.Fatal(err)
				}
				f.WriteString(fmt.Sprintf("proxmox_node: %s\n", nodeName))
				f.WriteString(fmt.Sprintf("proxmox_interface: %s\n", inter.Name))
				f.WriteString(fmt.Sprintf("proxmox_local_ip: %s\n", ipv4.String()))
				f.WriteString(fmt.Sprintf("proxmox_public_ip: %s\n", ipv4.String()))
				// TODO clean this up/do it in Go. Since we know we will be on a Debian 12 box, it's ok for now
				gateway := strings.Trim(Run("ip route show | grep default | grep -Po '(?<=via )[^ ]*'", false, true), "\n")
				f.WriteString(fmt.Sprintf("proxmox_gateway: %s\n", gateway))
				f.WriteString(fmt.Sprintf("proxmox_netmask: %d.%d.%d.%d\n", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3]))
				f.WriteString("proxmox_vm_storage_pool: local\n")
				f.WriteString("proxmox_vm_storage_format: qcow2\n")
				f.WriteString("proxmox_iso_storage_pool: local\n")
			}
		}
	}
}

func copyThisBinaryToInstallPath() {
	// Get the path of the currently running binary
	exePath, err := os.Executable()
	if err != nil {
		log.Fatal(err)
	}

	// Open the source file
	srcFile, err := os.Open(exePath)
	if err != nil {
		log.Fatal(err)
	}
	defer srcFile.Close()

	// Create the destination file
	destPath := filepath.Join(ludusInstallPath, "ludus-server")
	destFile, err := os.Create(destPath)
	if err != nil {
		log.Fatal(err)
	}
	defer destFile.Close()

	// Copy the contents from the source to the destination
	_, err = io.Copy(destFile, srcFile)
	if err != nil {
		log.Fatal(err)
	}
}

func updateLudus() {
	// Check for running ansible or packer processes
	// This assumes that ludus is the only thing that would run
	// packer or ansible on the system - an ok assumption for
	// a dedicated ludus machine
	ansiblePackerPIDs := Run("pgrep -f 'ansible|packer'", false, false)
	if ansiblePackerPIDs != "Command processed (no output)." {
		log.Fatal(`Ansible or Packer processes are running on this host.
Refusing to update to prevent interruption of template builds
or range deployments.`)
	}

	// Check if we are /opt/ludus/ludus-server, if so, refuse to update
	// If we're feeling froggy, we could pull ourselves out of /proc/pid/exe and allow in-place upgrades
	if ludusPath == ludusInstallPath {
		log.Fatal(`You are attempting to run update from the install location.
Refusing to continue as the install process removes this binary.
Move the updated binary to a different location and run it with --update to complete the update.`)
	}

	// Stop ludus and ludus-admin
	Run("systemctl stop ludus", false, true)
	Run("systemctl stop ludus-admin", false, true)

	// Backup, and extract files from this binary
	checkDirAndReplaceFiles()

	// Replace ludus service binary and chown/chmod correctly
	ludusServerBinaryPath := fmt.Sprintf("%s/ludus-server", ludusInstallPath)
	if fileExists(ludusServerBinaryPath) {
		err := os.Remove(ludusServerBinaryPath)
		if err != nil {
			log.Fatalf("Error removing ludus-server binary: %s\n", err.Error())
		}
	}
	copyThisBinaryToInstallPath()
	chownFileToUsername(ludusServerBinaryPath, "root")
	os.Chmod(ludusServerBinaryPath, 0711)

	// Start ludus and ludus-admin
	Run("systemctl start ludus", false, true)
	Run("systemctl start ludus-admin", false, true)
	fmt.Printf("Ludus updated to v%s\n", LudusVersion)
}

func checkArgs() {
	if len(os.Args) > 1 && os.Args[1] == "--no-prompt" {
		interactiveInstall = false
	} else {
		interactiveInstall = true
	}
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Print(`
Ludus is a project to enable teams to quickly and
safely deploy test environments (ranges) to test tools and
techniques against representative virtual machines.

When run without arguments, Ludus will check for a Ludus
install at /opt/ludus and prompt the user to install Ludus
if an existing install is not found.

When run with --no-prompt an optional node name can be provided as an
argument to set the proxmox node name in the configuration file.

Usage:
    ludus-server
    ludus-server --no-prompt (node name)
    ludus-server --update

Flags:
        --update       update the ludus install with this binary and 
                       embedded files and restart the ludus services
        --no-prompt    run the installer without prompting for confirmation
    -h, --help         help for ludus-server
    -v, --version      print the version of this ludus server
`)
		os.Exit(0)
	} else if len(os.Args) > 1 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(LudusVersion)
		os.Exit(0)
	} else if len(os.Args) > 1 && os.Args[1] == "--update" {
		// If the user wants to update, just do that and exit
		checkRoot()
		updateLudus()
		os.Exit(0)
	}
}

func backupDir(srcDir string, timestamp string) {

	// Make the previous-versions dir if it doesn't exist
	previousVersionDir := fmt.Sprintf("%s/previous-versions", ludusInstallPath)
	if !exists(previousVersionDir) {
		os.MkdirAll(previousVersionDir, 0755)
	}

	// Make the timestamp dir if it doesn't exist
	timestampDir := fmt.Sprintf("%s/%s", previousVersionDir, timestamp)
	if !exists(timestampDir) {
		os.MkdirAll(timestampDir, 0755)
	}

	srcDirPath := fmt.Sprintf("%s/%s", ludusInstallPath, srcDir)

	if exists(srcDirPath) {
		// Why is it 130 lines of go to copy a directory recursively? This will only ever run on Debian, so shell it out
		Run(fmt.Sprintf("cp -r %s %s/%s", srcDirPath, timestampDir, srcDir), false, true)
		log.Printf("Backed up %s to %s/%s\n", srcDirPath, timestampDir, srcDir)
		// Remove the dir
		err := os.RemoveAll(srcDirPath)
		if err != nil {
			log.Fatal(err)
		}
	}
}

// Chown a file to a user and their group
func chownFileToUsername(filePath string, username string) {
	runnerUser, err := user.Lookup(username)
	if err != nil {
		fmt.Printf("Failed to lookup user %s for chown of %s\n", err, filePath)
		return
	}

	uid, err := strconv.Atoi(runnerUser.Uid)
	if err != nil {
		fmt.Printf("Failed to convert UID to integer: %s\n", err)
		return
	}

	gid, err := strconv.Atoi(runnerUser.Gid)
	if err != nil {
		fmt.Printf("Failed to convert GID to integer: %s\n", err)
		return
	}

	// Change ownership of the file
	err = os.Chown(filePath, uid, gid)
	if err != nil {
		fmt.Printf("Failed to change ownership of the file: %s\n", err)
		return
	}
}

// Check to make sure the ludus install directory exists
// then back it up and replace ansible, packer, and ci dirs
// with the embedded files
func checkDirAndReplaceFiles() {
	if !exists(ludusInstallPath) {
		log.Fatalf("%s does not exist, cannot extract files.", ludusInstallPath)
		return
	}

	// Backup ansible if it exists
	timestamp := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	backupDir("ansible", timestamp)

	// Backup packer if it exists
	backupDir("packer", timestamp)

	// Backup ci if it exists
	backupDir("ci", timestamp)

	// Copy the ludus-server binary to the timestamp dir
	if fileExists(fmt.Sprintf("%s/ludus-server", ludusInstallPath)) {
		Run(fmt.Sprintf("cp %s/ludus-server %s/previous-versions/%s/ludus-server", ludusInstallPath, ludusInstallPath, timestamp), false, true)
	}

	log.Printf("Extracting ludus to %s...\n", ludusInstallPath)
	extractDirectory(embeddedAnsbileDir, "ansible")

	// proxmox.py has to be executable or it will not work!
	os.Chmod(ludusInstallPath+"/ansible/range-management/proxmox.py", 0770)

	if userExists("ludus") {
		Run(fmt.Sprintf("chown -R ludus:ludus %s/ansible", ludusInstallPath), false, true)
	}

	extractDirectory(embeddedPackerDir, "packer")
	if userExists("ludus") {
		Run(fmt.Sprintf("chown -R ludus:ludus %s/packer", ludusInstallPath), false, true)
	}

	// Extract the CI directory. All files will be owned by root
	// The ci setup play will handle permission changes after creating the user
	// and if the gitlab-runner users exists, we will chown the ci directory below
	extractDirectory(embeddedCIDir, "ci")
	// Make all the CI scripts executable
	Run(fmt.Sprintf("chmod +x %s/ci/*.sh", ludusInstallPath), false, true)

	if userExists("gitlab-runner") {
		// If this server has been set up for CI work previously, restore the API key for the CI user
		if fileExists(fmt.Sprintf("%s/previous-versions/%s/ci/.apikey", ludusInstallPath, timestamp)) {
			copy(fmt.Sprintf("%s/previous-versions/%s/ci/.apikey", ludusInstallPath, timestamp), fmt.Sprintf("%s/ci/.apikey", ludusInstallPath))
		}
		// Copy the .gitlab-runner-password file back into ci if it existed
		gitlabRunnerPasswordPath := fmt.Sprintf("%s/previous-versions/%s/ci/.gitlab-runner-password", ludusInstallPath, timestamp)
		if fileExists(gitlabRunnerPasswordPath) {
			bytesRead, err := os.ReadFile(gitlabRunnerPasswordPath)

			if err != nil {
				log.Fatal(err)
			}
			destinationPath := fmt.Sprintf("%s/ci/.gitlab-runner-password", ludusInstallPath)

			err = os.WriteFile(destinationPath, bytesRead, 0600)

			if err != nil {
				log.Fatal(err)
			}

			chownFileToUsername(destinationPath, "gitlab-runner")

		}
		// Chown the entire ci path to gitlab-runner
		Run(fmt.Sprintf("chown -R gitlab-runner:gitlab-runner %s/ci", ludusInstallPath), false, true)
	}

	log.Println("Ludus files extracted successfully")
}

// downloadFile downloads a file from the given URL and saves it to the specified local path.
func downloadFile(url, targetDir, fileName string) error {
	// Create the target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, os.ModePerm); err != nil {
		return err
	}

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	path := filepath.Join(targetDir, fileName)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the data to the file
	_, err = io.Copy(out, resp.Body)
	return err
}
