package cmd

import (
	"os"
	"testing"
)

func TestStdinIsTerminalRejectsCharacterDeviceWithoutTerminal(t *testing.T) {
	stdin := os.Stdin
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	os.Stdin = null
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = null.Close()
	})

	if stdinIsTerminal() {
		t.Fatalf("%s is a character device, not an interactive terminal", os.DevNull)
	}
}

func TestSelectInstallModeAllOverridesTerminal(t *testing.T) {
	if got := selectInstallMode(sourceFlags{All: true}, true); got != modeInstallAll {
		t.Fatalf("selectInstallMode(--all, TTY) = %v, want modeInstallAll", got)
	}
}
