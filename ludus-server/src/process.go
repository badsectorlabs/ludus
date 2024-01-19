package ludusapi

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

func findAnsiblePidForUser(username string) (string, error) {
	// Get the list of all ansible processes
	out, err := exec.Command("bash", "-c", "ps aux | egrep 'ansibl[e]'").Output()
	if err != nil {
		fmt.Println("Error executing command: ", err)
		return "", err
	}

	processes := strings.Split(string(out), "\n")

	for _, process := range processes {
		if strings.Contains(process, "ansible") {
			fields := strings.Fields(process)
			if len(fields) > 1 {
				pid := fields[1]

				// Get the environment variables for the process
				envOut, err := exec.Command("bash", "-c", fmt.Sprintf("cat /proc/%s/environ", pid)).Output()
				if err != nil {
					fmt.Printf("Error executing command: cat /proc/%s/environ: %v\n", pid, err)
					continue
				}

				envVars := strings.Split(string(envOut), "\\0")
				for _, envVar := range envVars {
					if strings.Contains(envVar, fmt.Sprintf("%s@pam", username)) {
						fmt.Printf("Process %s has '%s@pam' in its environment variables\n", pid, username)
						return pid, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("no ansible process found for user %s", username)
}

// Send a SIGINT (control+c) to all children, then the given pid - ignore errors and output
func killProcessAndChildren(pid string) {
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGINT -P %s", pid)).Output()
	// Packer children (ansible) don't respect SIGINT, so kill them more harshly
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGTERM -P %s", pid)).Output()
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGINT %s", pid)).Output()

}

type PackerProcessItem struct {
	Name string
	User string
}

// findRunningPackerProcesses runs the 'ps' command and returns unique names of templates extracted from
// .hcl files in packer processes
func findRunningPackerProcesses() []PackerProcessItem {
	// Get the list of all packer processes
	processList, err := exec.Command("bash", "-c", "ps aux | egrep 'packe[r]'").Output()
	if err != nil {
		return []PackerProcessItem{} // exit status 1 == no packer process found
	}

	var templatesBuilding []PackerProcessItem
	scanner := bufio.NewScanner(bytes.NewReader(processList))

	templateStringRegex, _ := regexp.Compile(templateRegex)

	for scanner.Scan() {
		line := scanner.Text()

		// Check if the line contains a packer process
		if strings.Contains(line, "packer build") {
			words := strings.Fields(line)

			// Find .hcl files and the username from the command line
			var templateName, templateUser string
			for _, word := range words {
				if strings.HasSuffix(word, ".hcl") {
					templateName = extractTemplateNameFromHCL(word, templateStringRegex)
				}
				if strings.HasSuffix(word, "@pam") {
					// We know the format of the string here, still a little dangerous...
					if strings.Contains(word, "@") && strings.Contains(word, "=") {
						templateUser = strings.Split(strings.Split(word, "@")[0], "=")[1]
					} else {
						templateUser = "UNKNOWN"
					}
				}
			}
			thisPackerProcessItem := PackerProcessItem{Name: templateName, User: templateUser}
			if !containsProcess(templatesBuilding, thisPackerProcessItem) {
				templatesBuilding = append(templatesBuilding, thisPackerProcessItem)
			}

		}
	}

	return templatesBuilding
}

func findPackerPidsForUser(username string) []string {
	// Get the list of all packer processes
	out, err := exec.Command("bash", "-c", "ps aux | egrep 'packe[r]' | egrep "+fmt.Sprintf("%s@pa[m]", username)).Output()
	if err != nil {
		return []string{} // exit status 1 == no packer process found
	}

	processes := strings.Split(string(out), "\n")
	var packerPidsForUser []string

	for _, process := range processes {
		if strings.Contains(process, "packer") {
			fields := strings.Fields(process)
			if len(fields) > 1 {
				pid := fields[1]
				packerPidsForUser = append(packerPidsForUser, pid)
			}
		}
	}
	return packerPidsForUser
}

// containsProcess checks if a PackerProcessItem is in the slice
func containsProcess(processList []PackerProcessItem, target PackerProcessItem) bool {
	for _, item := range processList {
		// Check if both Name and User fields match
		if item.Name == target.Name && item.User == target.User {
			return true
		}
	}
	return false
}
