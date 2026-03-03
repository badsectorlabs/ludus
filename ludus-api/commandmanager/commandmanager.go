package commandmanager

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var (
	// instance holds the single instance of the CommandManager.
	instance *CommandManager
	// once is used to ensure the CommandManager is initialized only once.
	once sync.Once
)

// Status represents the state of a running command.
type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusError     Status = "error"
	StatusKilled    Status = "killed"
	StatusKillError Status = "kill_error"
)

// CommandInfo holds all the relevant information for a tracked command.
type CommandInfo struct {
	ID        string
	Command   string
	Args      []string
	PID       int
	Status    Status
	StartTime time.Time
	EndTime   time.Time
	Error     error
	// Metadata holds arbitrary key-value data about the command.
	Metadata map[string]string
	// Stdout contains the captured stdout output.
	Stdout string
	// Stderr contains the captured stderr output.
	Stderr string

	cmd *exec.Cmd
}

// CommandManager tracks all running commands in a thread-safe way.
type CommandManager struct {
	mu       sync.Mutex
	commands map[string]*CommandInfo
}

// GetInstance returns the singleton instance of the CommandManager.
// It creates the instance on the first call and returns it on every subsequent call.
func GetInstance() *CommandManager {
	once.Do(func() {
		// This function is guaranteed to be executed only once.
		instance = &CommandManager{
			commands: make(map[string]*CommandInfo),
		}
	})
	return instance
}

// StartCommandAsync starts a new long-running command and returns immediately.
// The command's status is updated in the background.
// If logfile is not empty, stdout and stderr are written to both the logfile and captured in the CommandInfo struct.
// Otherwise, stdout and stderr are only captured and stored in the CommandInfo struct.
// If workingDir is not empty, the command is executed with that directory as the working directory.
func (cm *CommandManager) StartCommandAsync(id, command, logfile, workingDir string, args ...string) (*CommandInfo, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cmd := exec.Command(command, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	info := &CommandInfo{
		ID:        id,
		Command:   command,
		Args:      args,
		Status:    StatusRunning,
		StartTime: time.Now(),
		cmd:       cmd,
		Metadata:  make(map[string]string),
	}

	// Always create buffers to capture output
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	var logFile *os.File

	if logfile != "" {
		// Open or create the log file for appending
		var err error
		logFile, err = os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open logfile: %w", err)
		}
		// Use MultiWriter to write to both the logfile and the buffer
		cmd.Stdout = io.MultiWriter(logFile, stdoutBuf)
		cmd.Stderr = io.MultiWriter(logFile, stderrBuf)
	} else {
		// Only capture stdout and stderr
		cmd.Stdout = stdoutBuf
		cmd.Stderr = stderrBuf
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}

	info.PID = cmd.Process.Pid
	cm.commands[id] = info

	// Launch a goroutine to wait for the command to finish.
	go func() {
		err := cmd.Wait()

		// Close log file if it was opened
		if logFile != nil {
			logFile.Close()
		}

		// Lock again to safely update the shared resource.
		cm.mu.Lock()
		defer cm.mu.Unlock()

		info.EndTime = time.Now()
		if err != nil {
			info.Status = StatusError
			info.Error = err
		} else {
			info.Status = StatusCompleted
		}

		// Store captured output
		info.Stdout = stdoutBuf.String()
		info.Stderr = stderrBuf.String()
	}()

	return info, nil
}

// StartCommandInShellAsync starts a new long-running command in a bash shell and returns immediately.
// The command string is executed via `/bin/bash -c`. The command's status is updated in the background.
// If logfile is not empty, stdout and stderr are written to both the logfile and captured in the CommandInfo struct.
// Otherwise, stdout and stderr are only captured and stored in the CommandInfo struct.
// If workingDir is not empty, the command is executed with that directory as the working directory.
func (cm *CommandManager) StartCommandInShellAsync(id, command, logfile, workingDir string) (*CommandInfo, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cmd := exec.Command("/bin/bash", "-c", command)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	info := &CommandInfo{
		ID:        id,
		Command:   "/bin/bash",
		Args:      []string{"-c", command},
		Status:    StatusRunning,
		StartTime: time.Now(),
		cmd:       cmd,
		Metadata:  make(map[string]string),
	}

	// Always create buffers to capture output
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	var logFile *os.File

	if logfile != "" {
		// Open or create the log file for appending
		var err error
		logFile, err = os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open logfile: %w", err)
		}
		// Use MultiWriter to write to both the logfile and the buffer
		cmd.Stdout = io.MultiWriter(logFile, stdoutBuf)
		cmd.Stderr = io.MultiWriter(logFile, stderrBuf)
	} else {
		// Only capture stdout and stderr
		cmd.Stdout = stdoutBuf
		cmd.Stderr = stderrBuf
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}

	info.PID = cmd.Process.Pid
	cm.commands[id] = info

	// Launch a goroutine to wait for the command to finish.
	go func() {
		err := cmd.Wait()

		// Close log file if it was opened
		if logFile != nil {
			logFile.Close()
		}

		// Lock again to safely update the shared resource.
		cm.mu.Lock()
		defer cm.mu.Unlock()

		info.EndTime = time.Now()
		if err != nil {
			info.Status = StatusError
			info.Error = err
		} else {
			info.Status = StatusCompleted
		}

		// Store captured output
		info.Stdout = stdoutBuf.String()
		info.Stderr = stderrBuf.String()
	}()

	return info, nil
}

// StartCommandAndWait starts a command and blocks until it completes.
// It returns the final state of the command.
// If logfile is not empty, stdout and stderr are written to both the logfile and captured in the CommandInfo struct.
// Otherwise, stdout and stderr are only captured and stored in the CommandInfo struct.
// If workingDir is not empty, the command is executed with that directory as the working directory.
// If metadata is not nil, its entries are copied into the CommandInfo's Metadata map before the command starts.
func (cm *CommandManager) StartCommandAndWait(id, command, logfile, workingDir string, metadata map[string]string, args ...string) (*CommandInfo, error) {
	cmd := exec.Command(command, args...)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	info := &CommandInfo{
		ID:        id,
		Command:   command,
		Args:      args,
		Status:    StatusRunning,
		StartTime: time.Now(),
		cmd:       cmd,
		Metadata:  make(map[string]string),
	}

	// Copy metadata if provided (ranging over nil map is safe in Go)
	for k, v := range metadata {
		info.Metadata[k] = v
	}

	// Always create buffers to capture output
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	var logFile *os.File

	if logfile != "" {
		// Open or create the log file for appending
		var err error
		logFile, err = os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open logfile: %w", err)
		}
		defer logFile.Close()
		// Use MultiWriter to write to both the logfile and the buffer
		cmd.Stdout = io.MultiWriter(logFile, stdoutBuf)
		cmd.Stderr = io.MultiWriter(logFile, stderrBuf)
	} else {
		// Only capture stdout and stderr
		cmd.Stdout = stdoutBuf
		cmd.Stderr = stderrBuf
	}

	// Lock to add the command to the manager before running it.
	cm.mu.Lock()
	if cmd.Process != nil {
		info.PID = cmd.Process.Pid
	}
	cm.commands[id] = info
	cm.mu.Unlock()

	// cmd.Run() starts the command and waits for it to complete.
	err := cmd.Run()

	// Lock again to update the final status.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	info.EndTime = time.Now()
	if err != nil {
		info.Status = StatusError
		info.Error = err
	} else {
		info.Status = StatusCompleted
	}

	// Store captured output
	info.Stdout = stdoutBuf.String()
	info.Stderr = stderrBuf.String()

	// It's possible the PID was only available after start, so we check again.
	// This is more relevant for cmd.Start(), but good practice.
	if info.PID == 0 && cmd.Process != nil {
		info.PID = cmd.Process.Pid
	}

	return info, err
}

// StartCommandInShellAndWait starts a command in a bash shell and blocks until it completes.
// The command string is executed via `/bin/bash -c`. It returns the final state of the command.
// If logfile is not empty, stdout and stderr are written to both the logfile and captured in the CommandInfo struct.
// Otherwise, stdout and stderr are only captured and stored in the CommandInfo struct.
// If workingDir is not empty, the command is executed with that directory as the working directory.
// If metadata is not nil, its entries are copied into the CommandInfo's Metadata map before the command starts.
func (cm *CommandManager) StartCommandInShellAndWait(id, command, logfile, workingDir string, metadata map[string]string) (*CommandInfo, error) {
	cmd := exec.Command("/bin/bash", "-c", command)
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	info := &CommandInfo{
		ID:        id,
		Command:   "/bin/bash",
		Args:      []string{"-c", command},
		Status:    StatusRunning,
		StartTime: time.Now(),
		cmd:       cmd,
		Metadata:  make(map[string]string),
	}

	// Copy metadata if provided (ranging over nil map is safe in Go)
	for k, v := range metadata {
		info.Metadata[k] = v
	}

	// Always create buffers to capture output
	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	var logFile *os.File

	if logfile != "" {
		// Open or create the log file for appending
		var err error
		logFile, err = os.OpenFile(logfile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return nil, fmt.Errorf("failed to open logfile: %w", err)
		}
		defer logFile.Close()
		// Use MultiWriter to write to both the logfile and the buffer
		cmd.Stdout = io.MultiWriter(logFile, stdoutBuf)
		cmd.Stderr = io.MultiWriter(logFile, stderrBuf)
	} else {
		// Only capture stdout and stderr
		cmd.Stdout = stdoutBuf
		cmd.Stderr = stderrBuf
	}

	// Start the command (but don't wait for it yet)
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, err
	}

	// Lock to add the command to the manager and set the PID after starting.
	cm.mu.Lock()
	info.PID = cmd.Process.Pid
	cm.commands[id] = info
	cm.mu.Unlock()

	// Wait for the command to complete.
	err := cmd.Wait()

	// Lock again to update the final status.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	info.EndTime = time.Now()
	if err != nil {
		info.Status = StatusError
		info.Error = err
	} else {
		info.Status = StatusCompleted
	}

	// Store captured output
	info.Stdout = stdoutBuf.String()
	info.Stderr = stderrBuf.String()

	return info, err
}

// SetMetadata adds or updates a key-value pair for a given command ID.
// It returns true if the command was found and the metadata was set.
func (cm *CommandManager) SetMetadata(id string, key string, value string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	info, ok := cm.commands[id]
	if !ok {
		return false
	}
	info.Metadata[key] = value
	return true
}

// GetCommandInfoByPID finds and returns the CommandInfo for a given process ID.
func (cm *CommandManager) GetCommandInfoByPID(pid int) (*CommandInfo, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for _, info := range cm.commands {
		if info.PID == pid {
			return info, true
		}
	}
	return nil, false
}

// GetValuesForPID returns a map of requested metadata values for a given process ID.
// It only returns the keys that were found.
func (cm *CommandManager) GetValuesForPID(pid int, keys ...string) (map[string]string, bool) {
	// We are locking here to ensure that we get a consistent view of the command's data.
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var info *CommandInfo
	var found bool

	for _, cmdInfo := range cm.commands {
		if cmdInfo.PID == pid {
			info = cmdInfo
			found = true
			break
		}
	}

	if !found {
		return nil, false
	}

	// Create a new map to hold the results so we are not returning a direct
	// reference to the internal map's data structure while it could be modified.
	values := make(map[string]string)
	for _, key := range keys {
		if val, ok := info.Metadata[key]; ok {
			values[key] = val
		}
	}

	return values, true
}

// GetCommandStatus returns the status of a command by its ID.
func (cm *CommandManager) GetCommandStatus(id string) (*CommandInfo, bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	info, ok := cm.commands[id]
	if !ok {
		return nil, false
	}
	if info.Status == StatusRunning {
		process, err := os.FindProcess(info.PID)
		if err != nil || process.Signal(syscall.Signal(0)) != nil {
			info.Status = StatusError
			info.Error = fmt.Errorf("process with PID %d not found", info.PID)
			info.EndTime = time.Now()
		}
	}
	return info, true
}

// GetAllCommands returns a copy of all tracked commands.
// The returned map is a snapshot and modifications to it will not affect the internal state.
func (cm *CommandManager) GetAllCommands() map[string]*CommandInfo {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Create a new map and copy all entries to avoid returning a direct reference
	// to the internal map that could be modified by the caller.
	result := make(map[string]*CommandInfo, len(cm.commands))
	for k, v := range cm.commands {
		result[k] = v
	}
	return result
}

// KillCommand forcibly kills a command by its ID.
// It returns an error if the command is not found or if killing the process fails.
// If the command has already completed or errored, this function returns nil (no error).
func (cm *CommandManager) KillCommand(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	info, ok := cm.commands[id]
	if !ok {
		return fmt.Errorf("command with ID %s not found", id)
	}

	// If the command is already completed or errored, nothing to kill
	if info.Status != StatusRunning {
		return nil
	}

	var killErr error

	// Try to kill using the exec.Cmd's Process if available
	if info.cmd != nil && info.cmd.Process != nil {
		killErr = info.cmd.Process.Kill()
	} else if info.PID > 0 {
		// Fall back to syscall.Kill if cmd.Process is not available
		killErr = syscall.Kill(info.PID, syscall.SIGKILL)
	} else {
		killErr = fmt.Errorf("no process available to kill for command %s", id)
	}

	if info.PID > 0 && info.Metadata["command_type"] == "packer_build" {
		// Packer is a special case - it has children (ansible) that need to be killed too
		KillProcessAndChildren(info.PID)
	}

	// Update the command status regardless of kill result
	// (the process might have already terminated)
	info.EndTime = time.Now()
	if killErr != nil {
		info.Status = StatusKillError
		info.Error = fmt.Errorf("failed to kill process: %w", killErr)
		return killErr
	}

	// Mark as error since it was killed
	info.Status = StatusKilled
	info.Error = fmt.Errorf("command was forcibly killed")

	return nil
}

// RemoveCommand removes a command from the command manager by its ID.
// It returns an error if the command is not found.
func (cm *CommandManager) RemoveCommand(id string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	_, ok := cm.commands[id]
	if !ok {
		return fmt.Errorf("command with ID %s not found", id)
	}

	delete(cm.commands, id)
	return nil
}

// Send a SIGINT (control+c) to all children, then the given pid - ignore errors and output
func KillProcessAndChildren(pid int) {
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGINT -P %d", pid)).Output()
	// Packer children (ansible) don't respect SIGINT, so kill them more harshly
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGTERM -P %d", pid)).Output()
	_, _ = exec.Command("bash", "-c", fmt.Sprintf("pkill --signal SIGINT %d", pid)).Output()

}
