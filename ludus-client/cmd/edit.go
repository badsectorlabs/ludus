package cmd

import (
	"encoding/json"
	"fmt"
	"ludus/logger"
	"ludus/rest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
)

var (
	editor     string
	tempPath   string
	readInPath string
	textArea   *tview.TextArea
	app        *tview.Application
)

// getDefaultTempPath returns the default temporary file path based on OS
func getDefaultTempPath() string {
	if runtime.GOOS == "windows" {
		username := os.Getenv("USERNAME")
		return filepath.Join("C:", "Users", username, "AppData", "Local", "Temp", "ludus-config.yml")
	}
	return "/tmp/ludus-config.yml"
}

// editWithExternalEditor opens the config in the specified external editor
func editWithExternalEditor(content []byte, editorCmd string, tempFilePath string) ([]byte, error) {
	// Write content to temp file
	if err := os.WriteFile(tempFilePath, content, 0644); err != nil {
		return nil, fmt.Errorf("failed to write temp file: %v", err)
	}

	// Prepare editor command
	cmd := exec.Command(editorCmd, tempFilePath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run editor
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("editor process failed: %v", err)
	}

	// Read modified content
	newContent, err := os.ReadFile(tempFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp file: %v", err)
	}

	return newContent, nil
}

// createBuiltinEditor creates the TUI editor interface
func createBuiltinEditor(content string) *tview.Application {
	app = tview.NewApplication()
	textArea = tview.NewTextArea().
		SetText(content, false)

	// Add keyboard shortcuts
	textArea.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlS {
			// Save and exit
			app.Stop()
		} else if event.Key() == tcell.KeyCtrlQ {
			// Quit without saving
			app.Stop()
			os.Exit(0)
		}
		return event
	})

	// Create a frame for the editor
	frame := tview.NewFrame(textArea).
		SetBorders(0, 0, 0, 0, 0, 0).
		AddText("Ctrl-S: Save and Exit | Ctrl-Q: Quit without saving", false, tview.AlignLeft, tcell.ColorWhite)

	return app.SetRoot(frame, true)
}

var rangeConfigEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Edit the range configuration in an editor",
	Long:  `Edit the range configuration either in a built-in editor or an external editor specified by --editor`,
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		var client = rest.InitClient(url, apiKey, proxy, verify, verbose, LudusVersion)

		var oldContent string
		var err error
		var responseJSON []byte
		var success bool

		// Get current config from local file
		if readInPath != "" {
			// Read in the file
			oldContentBytes, err := os.ReadFile(readInPath)
			if err != nil {
				logger.Logger.Fatal(err.Error())
			}
			oldContent = string(oldContentBytes)
		} else { // Get the config from the server
			if userID != "" {
				responseJSON, success = rest.GenericGet(client, fmt.Sprintf("/range/config?userID=%s", userID))
			} else {
				responseJSON, success = rest.GenericGet(client, "/range/config")
			}
			if !success {
				return
			}

			type Result struct {
				RangeConfig string `json:"result"`
			}

			var result Result
			err := json.Unmarshal(responseJSON, &result)
			if err != nil {
				logger.Logger.Fatal(err.Error())
			}
			oldContent = result.RangeConfig
		}

		var newContent []byte

		if editor != "" {
			// Use external editor
			newContent, err = editWithExternalEditor([]byte(oldContent), editor, tempPath)
			if err != nil {
				logger.Logger.Fatal(err.Error())
			}
		} else {
			// Use built-in editor
			app := createBuiltinEditor(oldContent)
			if err := app.Run(); err != nil {
				logger.Logger.Fatal(err.Error())
			}
			newContent = []byte(textArea.GetText())
			// Write the new content to the temp file
			if err := os.WriteFile(tempPath, newContent, 0644); err != nil {
				logger.Logger.Fatal(err.Error())
			}
		}

		// Send updated config back to server
		if userID != "" {
			responseJSON, success = rest.PostFileAndForce(client, fmt.Sprintf("/range/config?userID=%s", userID), newContent, "file", force)
		} else {
			responseJSON, success = rest.PostFileAndForce(client, "/range/config", newContent, "file", force)
		}

		if didFailOrWantJSON(success, responseJSON) {
			if !success && !jsonFormat {
				logger.Logger.Fatal("Load your edits with:\nludus range config edit --file " + tempPath)
			}
			return
		}
		handleGenericResult(responseJSON)
	},
}

func setupRangeConfigEdit(command *cobra.Command) {
	command.Flags().StringVarP(&editor, "editor", "e", "", "external editor to use (e.g., vim, nano, code)")
	command.Flags().StringVarP(&tempPath, "temp-file-path", "t", getDefaultTempPath(), "temporary file path for external editor")
	command.Flags().StringVarP(&readInPath, "file", "f", "", "path to a file to read in for editing (default: get config from server)")
	command.Flags().BoolVar(&force, "force", false, "force the configuration to be updated, even with testing enabled")
}

func init() {
	setupRangeConfigEdit(rangeConfigEditCmd)
	rangeConfigCmd.AddCommand(rangeConfigEditCmd)
}
