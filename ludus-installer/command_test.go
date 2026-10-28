package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

// The install log shows only step lines; command output appears solely when a
// command fails, and then only its last lines.
func TestCommandOutputIsShownOnlyOnFailure(t *testing.T) {
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = write, write
	ctx := context.Background()
	runErr := commandRun(ctx, "sh", "-c", "echo progress; echo warning >&2")
	inputErr := commandInput(ctx, []byte("piped\n"), "sh", "-c", "cat; echo warning >&2")
	failure := commandRun(ctx, "sh", "-c", "for i in $(seq 1 40); do echo line$i; done; echo cause >&2; exit 3")
	os.Stdout, os.Stderr = stdout, stderr
	write.Close()
	leaked, _ := io.ReadAll(read)

	if runErr != nil || inputErr != nil {
		t.Fatalf("successful commands failed: %v %v", runErr, inputErr)
	}
	if len(leaked) != 0 {
		t.Fatalf("command output reached the terminal: %q", leaked)
	}
	if failure == nil {
		t.Fatal("failed command was not reported")
	}
	message := failure.Error()
	if !strings.Contains(message, "exit status 3") || !strings.Contains(message, "cause") || !strings.Contains(message, "line40") {
		t.Fatalf("failure must include the end of its output: %s", message)
	}
	if strings.Contains(message, "line1\n") || !strings.Contains(message, "earlier lines omitted") {
		t.Fatalf("failure output must be limited to its last lines: %s", message)
	}
}
