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

func formatTimeObject(timeObject time.Time) string {
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
	return localTimeObject.Format("2006-01-02 15:04")
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
		if !strings.Contains(file, baseDir+"/") {
			return nil
		}

		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		// Modify the header name to only include the base directory and its contents
		header.Name = filepath.ToSlash(strings.TrimPrefix(file, filepath.Dir(dirPath)+"/"))
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

func formatAndPrintError(errorLine string, errorCount int) {
	formattedLine := strings.ReplaceAll(errorLine, "\\r\\n", "\n")
	formattedLine = strings.ReplaceAll(formattedLine, "\\n", "\n")
	fmt.Printf("\n******************************************** ERROR %d ********************************************\n", errorCount)
	fmt.Println(formattedLine)
	fmt.Println("*************************************************************************************************")

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
