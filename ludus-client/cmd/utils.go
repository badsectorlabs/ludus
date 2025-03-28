package cmd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ludus/logger"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const regexStringForMissingRole = "the role '([\\w._-]+)'"

var regexForMissingRole = regexp.MustCompile(regexStringForMissingRole)

func formatTimeObject(timeObject time.Time, format string) string {
	localTimeZone, err := time.LoadLocation("Local")
	if err != nil {
		logger.Logger.Warnf("Error loading time zone: %s\n", err)
		logger.Logger.Warn("No time zone automatically detected - using America/New_York")
		localTimeZone, err = time.LoadLocation("America/New_York")
		if err != nil {
			fmt.Printf("Error loading time zone (hardcoded): %s\n", err)
			return "ERROR"
		}
	}
	localTimeObject := timeObject.In(localTimeZone)
	return localTimeObject.Format(format)
}

func handleGenericResult(responseJSON []byte) {
	type Data struct {
		Result string `json:"result"`
	}

	// Unmarshal JSON data
	var data Data
	err := json.Unmarshal([]byte(responseJSON), &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}

	logger.Logger.Info(data.Result)
}

func stringAndCursorFromResult(responseJSON []byte) (string, int) {
	type Data struct {
		Result string `json:"result"`
		Cursor int    `json:"cursor"`
	}
	var data Data
	err := json.Unmarshal([]byte(responseJSON), &data)
	if err != nil {
		logger.Logger.Fatal(err.Error())
	}
	return data.Result, data.Cursor
}

func removeEmptyStrings(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

// Tar only the final directory in the given directory path
func tarDirectoryInMemory(dirPath string) (bytes.Buffer, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	fileInfo, err := os.Stat(dirPath)
	if err != nil {
		return buf, err
	}

	// Handle the case where dirPath is actually a full path to a file
	// Trim the file name and final separator from the path
	if !fileInfo.IsDir() {
		dirPath = filepath.Dir(dirPath)
	}

	// Extract the base directory name
	baseDir := filepath.Base(dirPath)

	filepath.Walk(dirPath, func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip if not in the base directory
		if !strings.Contains(file, baseDir+string(os.PathSeparator)) {
			return nil
		}

		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		// Modify the header name to only include the base directory and its contents
		header.Name = filepath.ToSlash(strings.TrimPrefix(file, filepath.Dir(dirPath)+string(os.PathSeparator)))
		err = tw.WriteHeader(header)
		if err != nil {
			return err
		}

		if !fi.Mode().IsRegular() { // Skip non-regular files
			return nil
		}

		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
	tw.Close()
	return buf, nil
}

func didFailOrWantJSON(success bool, responseJSON []byte) bool {
	if !success {
		return true
	}
	if jsonFormat {
		fmt.Printf("%s\n", responseJSON)
		return true
	}
	return false
}

func findFiles(rootDir, pattern1, pattern2 string) ([]string, error) {
	var files []string

	fileInfo, err := os.Stat(templateDirectory)
	if err != nil {
		return nil, err
	}
	if !fileInfo.IsDir() {
		return nil, errors.New("the provided path is not a directory")
	}

	err = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && (strings.HasSuffix(path, pattern1) || strings.HasSuffix(path, pattern2)) {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

func checkErrorForAnsibleTemporaryDirectory(errorString string) {
	searchString := "Failed to create temporary directory. In some cases, you may have been able to authenticate and did not have permissions on the target directory."

	if strings.Contains(errorString, searchString) {
		regexPattern := regexp.MustCompile(`fatal: ([^:]+): UNREACHABLE!`)
		match := regexPattern.FindStringSubmatch(errorString)
		if len(match) > 1 {
			// The first element (match[0]) is the entire match,
			// and the second element (match[1]) is the first parenthesized submatch,
			// which in this case, is the VM name.
			logger.Logger.Errorf("%s may be unreachable or powered off! Power it on or reboot it and try the command again.\n", match[1])
		} else {
			logger.Logger.Error("The VM may be unreachable or powered off. Power it on or reboot it and try the command again.")
		}
	}
}

func formatAndPrintError(errorLine string, errorCount int) {
	formattedLine := strings.ReplaceAll(errorLine, "\\r\\n", "\n")
	formattedLine = strings.ReplaceAll(formattedLine, "\\n", "\n")
	fmt.Printf("\n******************************************** ERROR %d ********************************************\n", errorCount)
	fmt.Println(formattedLine)
	if strings.Contains(formattedLine, "hashes do not match") && strings.Contains(formattedLine, "Consider passing the actual checksums through with") {
		fmt.Printf("\nSome chocolatey packages pull from external sources and do not update their checksums frequently.")
		fmt.Printf("\nConsider setting `chocolatey_ignore_checksums: true` in your range configuration for this VM to ignore checksums and bypass this error.\n")
	}
	fmt.Println("*************************************************************************************************")
	checkErrorForAnsibleTemporaryDirectory(errorLine)
}

func printFatalErrorsFromString(input string) {
	scanner := bufio.NewScanner(strings.NewReader(input))
	fatalRegex := regexp.MustCompile(`^fatal:.*$|^failed:.*$|^ERROR! .*$`)
	ignoreRegex := regexp.MustCompile(`\.\.\.ignoring$`)
	errorCount := 0

	var threeLinesAgo string
	var twoLinesAgo string
	var previousLine string
	for scanner.Scan() {
		currentLine := scanner.Text()
		// Check if the current line is an ignoring line and the previous line was a fatal line
		if ignoreRegex.MatchString(currentLine) && fatalRegex.MatchString(previousLine) {
			// Skip this fatal line because it's followed by ...ignoring
			previousLine = "" // Reset previousLine to avoid false positives
			continue
		}

		if fatalRegex.MatchString(previousLine) {
			// This means the previous line was a fatal line not followed by ...ignoring
			// Check if this is 'TASK [Promote this server to Additional DC 2]' which is known to fail without ...ignoring
			if strings.Contains(previousLine, "Unhandled exception while executing module: Verification of prerequisites for Domain Controller promotion failed. Role change is in progress or this computer needs to be restarted") && strings.Contains(threeLinesAgo, "TASK [Promote this server to Additional DC 2]") {
				continue
			}
			errorCount += 1
			formatAndPrintError(previousLine, errorCount)
		}

		// Update previous lines for the next iteration
		threeLinesAgo = twoLinesAgo
		twoLinesAgo = previousLine
		previousLine = currentLine
	}

	// Check the last line in case the file ends with a fatal line
	if fatalRegex.MatchString(previousLine) {
		errorCount += 1
		formatAndPrintError(previousLine, errorCount)
	}
}

// Parse the logs and skip printing lines that contain
// 'Error getting WinRM host: 500 QEMU guest agent is not running' or
// 'Error getting SSH address: 500 QEMU guest agent is not running'
func filterAndPrintTemplateLogs(logs string, verbose bool) {
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Error getting WinRM host: 500 QEMU guest agent is not running") ||
			strings.Contains(line, "Error getting SSH address: 500 QEMU guest agent is not running") {
			// Print a message prepended with the current time in the format 2024/05/09 19:36:46
			fmt.Printf("%s %s\n", formatTimeObject(time.Now(), "2006/01/02 15:04:05"), "Waiting for the VM to boot and complete initial setup...")
			continue
		}
		// Check for a missing role error
		if strings.Contains(line, "ERROR! the role '") && strings.Contains(line, "was not found in") {

			if verbose {
				fmt.Println(line)
			}

			// Extract the missing role name with regex
			matches := regexForMissingRole.FindStringSubmatch(line)
			if len(matches) > 1 {
				fmt.Printf("\n******************************** ERROR - Missing Role *******************************************\n")
				fmt.Printf("The role '%s' was not found in the inventory\n", matches[1])
				fmt.Printf("Run the command: ludus ansible role add %s\n", matches[1])
				fmt.Println("to add the missing role (assuming it's hosted on Ansible Galaxy)")
				fmt.Printf("*************************************************************************************************\n\n")
				continue
			} else {
				fmt.Printf("\n******************************** ERROR - Missing Role *******************************************\n")
				fmt.Printf("A role was not found in the inventory\n")
				fmt.Printf("Raw line: %s\n", line)
				fmt.Printf("*************************************************************************************************\n\n")
				continue
			}
		}
		// This will ignore all lines without the '=>', which is most of the verbose stuff, as well as the python3-apt error
		if !verbose &&
			(!strings.Contains(line, "=>") ||
				strings.Contains(line, "proxmox-iso.kali: fatal: [default]: FAILED! => {\"changed\": false, \"msg\": \"python3-apt must be installed and visible from /usr/bin/python3.\"}")) {

			continue
		}
		fmt.Println(line)
	}
}

func printTaskOutputFromString(logs string, taskName string) {
	// Split logs into lines
	lines := strings.Split(logs, "\n")

	// Variables to track state
	var currentOutput []string
	var allOutputs [][]string
	collecting := false

	// Search for all instances of the task and collect their output
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check if we found our task (case insensitive)
		if strings.Contains(strings.ToLower(line), "task ["+strings.ToLower(taskName)+"]") {
			// If we were already collecting, save the previous output
			if collecting && len(currentOutput) > 0 {
				allOutputs = append(allOutputs, currentOutput)
			}

			// Start new collection
			collecting = true
			currentOutput = []string{line}
			continue
		}

		// If we're collecting output
		if collecting {
			// Stop current collection when we hit the next task or play
			if strings.HasPrefix(line, "TASK [") || strings.HasPrefix(line, "PLAY [") {
				if len(currentOutput) > 0 {
					allOutputs = append(allOutputs, currentOutput)
				}
				collecting = false
				currentOutput = nil
				continue
			}

			// Add non-empty lines to current output
			if strings.TrimSpace(line) != "" {
				currentOutput = append(currentOutput, line)
			}
		}
	}

	// Print all collected outputs with separation between multiple instances
	for i, output := range allOutputs {
		fmt.Println(strings.Join(output, "\n"))
		// Add separator between multiple instances
		if i < len(allOutputs)-1 {
			fmt.Println("\n---\n")
		}
	}
}
