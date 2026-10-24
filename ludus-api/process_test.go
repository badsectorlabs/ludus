//go:build linux

package ludusapi

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// An aborted range must not leave processes behind, even ones that ignore
// SIGTERM and were orphaned when their parent exited, and must not touch
// processes of a range whose ID merely starts with the aborted one.
func TestTerminateRangeProcessesKillsOrphansIgnoringSIGTERM(t *testing.T) {
	rangeID := fmt.Sprintf("ABORT%d", os.Getpid())

	// The shell exits immediately, orphaning a sleep that ignores SIGTERM.
	spawn := exec.Command("sh", "-c", `trap "" TERM; sleep 60 >/dev/null 2>&1 & echo $!`)
	spawn.Env = append(os.Environ(), "LUDUS_RANGE_ID="+rangeID)
	out, err := spawn.Output()
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}

	other := exec.Command("sleep", "60")
	other.Env = append(os.Environ(), "LUDUS_RANGE_ID="+rangeID+"X")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})

	pids, err := rangeProcessPIDs(rangeID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pids, []int{orphan}) {
		t.Fatalf("rangeProcessPIDs(%q) = %v, want [%d]", rangeID, pids, orphan)
	}

	if err := terminateRangeProcesses(rangeID, 200*time.Millisecond, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	otherPIDs, err := rangeProcessPIDs(rangeID + "X")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(otherPIDs, []int{other.Process.Pid}) {
		t.Fatalf("process of range %sX = %v after abort, want [%d] still running", rangeID, otherPIDs, other.Process.Pid)
	}
}
