package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// Arguments can contain API credentials. Never include the command line.
		return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// commandRun and commandInput keep successful steps quiet: the combined output
// is shown only when the command fails, as the tail of the returned error.
func commandRun(ctx context.Context, name string, args ...string) error {
	return runQuietly(ctx, nil, name, args...)
}

func commandInput(ctx context.Context, input []byte, name string, args ...string) error {
	return runQuietly(ctx, bytes.NewReader(input), name, args...)
}

func runQuietly(ctx context.Context, input io.Reader, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = input
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		// Arguments can contain API credentials. Never include the command line.
		return fmt.Errorf("%s: %w%s", name, err, outputTail(output.Bytes()))
	}
	return nil
}

// commandAttached runs a command whose output is the result, such as the
// status report printed inside the LXC, on the caller's terminal.
func commandAttached(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// outputTail formats the last lines of a failed command's output for an error.
func outputTail(output []byte) string {
	const keep = 25
	text := strings.TrimRight(string(output), "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > keep {
		lines = append([]string{fmt.Sprintf("... (%d earlier lines omitted)", len(lines)-keep)}, lines[len(lines)-keep:]...)
	}
	return "\n    " + strings.Join(lines, "\n    ")
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	return writeAtomic(path, bytes.NewReader(data), mode)
}

func copyFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	return writeAtomic(destination, in, mode)
}

func writeAtomic(path string, reader io.Reader, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".ludus-installer-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := io.Copy(file, reader); err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
