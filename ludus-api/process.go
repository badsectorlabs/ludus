package ludusapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

func findAnsiblePidForUser(username string) (string, error) {
	// Get the list of all ansible processes
	out, err := exec.Command("bash", "-c", "ps aux | egrep 'ansibl[e]'").Output()
	if err != nil {
		if err.Error() == "exit status 1" {
			// egrep failed, no ansible running
			return "", errors.New("no ansible processes are running")
		}
		logger.Error(fmt.Sprintf("Error executing command: %v\n", err))
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
					logger.Debug(fmt.Sprintf("Error executing command: cat /proc/%s/environ: %v\n", pid, err))
					continue
				}

				envVars := strings.Split(string(envOut), "\\0")
				for _, envVar := range envVars {
					if strings.Contains(envVar, fmt.Sprintf("%s@pam", username)) {
						logger.Debug(fmt.Sprintf("Process %s has '%s@pam' in its environment variables\n", pid, username))
						return pid, nil
					}
				}
			}
		}
	}
	return "", fmt.Errorf("no ansible process found for user %s", username)
}

func RunWithOutput(command string) (string, error) {
	shellBin := "/bin/bash"
	if _, err := os.Stat(shellBin); err != nil {
		if _, err = os.Stat("/bin/sh"); err != nil {
			return "", errors.New("could not find /bin/bash or /bin/sh")
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
	if err != nil {
		return "", errors.New("Error running command: " + err.Error())
	}
	return out.String(), nil
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
