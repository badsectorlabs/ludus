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

// findAnsiblePidForRange returns the PID of the ansible process deploying the
// given range, identified by the LUDUS_RANGE_ID environment variable that
// ansible.go injects into every range-management ansible run.
func findAnsiblePidForRange(rangeID string) (string, error) {
	out, err := exec.Command("bash", "-c", "ps aux | egrep 'ansibl[e]'").Output()
	if err != nil {
		if err.Error() == "exit status 1" {
			return "", errors.New("no ansible processes are running")
		}
		logger.Error(fmt.Sprintf("Error executing command: %v\n", err))
		return "", err
	}

	target := fmt.Sprintf("LUDUS_RANGE_ID=%s", rangeID)
	processes := strings.Split(string(out), "\n")

	for _, process := range processes {
		if !strings.Contains(process, "ansible") {
			continue
		}
		fields := strings.Fields(process)
		if len(fields) < 2 {
			continue
		}
		pid := fields[1]

		envBytes, err := os.ReadFile(fmt.Sprintf("/proc/%s/environ", pid))
		if err != nil {
			logger.Debug(fmt.Sprintf("Error reading /proc/%s/environ: %v\n", pid, err))
			continue
		}

		// /proc/PID/environ separates entries with NUL bytes; an exact match
		// on the entry avoids LUDUS_RANGE_ID=AB matching LUDUS_RANGE_ID=ABC.
		for _, envVar := range strings.Split(string(envBytes), "\x00") {
			if envVar == target {
				logger.Debug(fmt.Sprintf("Process %s is deploying range %s\n", pid, rangeID))
				return pid, nil
			}
		}
	}
	return "", fmt.Errorf("no ansible process found for range %s", rangeID)
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
