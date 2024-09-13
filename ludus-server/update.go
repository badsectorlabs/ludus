package main

import (
	"embed"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func updateLudus() {
	// Check for running ansible or packer processes
	// This assumes that ludus is the only thing that would run
	// packer or ansible on the system - an ok assumption for
	// a dedicated ludus machine
	ansiblePackerPIDs := Run("pgrep 'ansible|packer'", false, false)
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
	fmt.Printf("Ludus updated to %s\n", LudusVersion)
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
