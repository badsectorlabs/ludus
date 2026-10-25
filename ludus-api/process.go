package ludusapi

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// rangeProcessPIDs returns every live process carrying LUDUS_RANGE_ID=rangeID.
// ansible.go injects the variable into each range-management ansible run and
// descendants inherit it, so the result covers ansible-playbook, its workers,
// and their module processes, including those re-parented after their worker
// died. Zombies expose an empty environ and are skipped.
func rangeProcessPIDs(rangeID string) ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}

	// An exact entry match avoids LUDUS_RANGE_ID=AB matching LUDUS_RANGE_ID=ABC.
	target := []byte("LUDUS_RANGE_ID=" + rangeID)
	self := os.Getpid()
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		// Unreadable entries belong to exited processes or other users.
		environ, err := os.ReadFile("/proc/" + entry.Name() + "/environ")
		if err != nil {
			continue
		}
		for _, envVar := range bytes.Split(environ, []byte{0}) {
			if bytes.Equal(envVar, target) {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids, nil
}

// terminateRangeProcesses sends SIGTERM to every process of the range, then
// SIGKILL to those still running after grace. Each pass rescans, so workers
// forked during the abort are signaled too. It returns nil once none remain,
// or an error if any survive SIGKILL for killWait.
func terminateRangeProcesses(rangeID string, grace, killWait time.Duration) error {
	sig := syscall.SIGTERM
	killAt := time.Now().Add(grace)
	giveUpAt := killAt.Add(killWait)
	for {
		pids, err := rangeProcessPIDs(rangeID)
		if err != nil {
			return err
		}
		if len(pids) == 0 {
			return nil
		}
		now := time.Now()
		if now.After(giveUpAt) {
			return fmt.Errorf("ansible processes for range %s still running after SIGKILL: %v", rangeID, pids)
		}
		if now.After(killAt) {
			sig = syscall.SIGKILL
		}
		for _, pid := range pids {
			// ESRCH only means the process exited after the scan.
			_ = syscall.Kill(pid, sig)
		}
		time.Sleep(200 * time.Millisecond)
	}
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
