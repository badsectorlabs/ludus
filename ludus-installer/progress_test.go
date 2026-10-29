package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func runProgressFixture(t *testing.T, steps []string, done progressDoneMsg) string {
	t.Helper()
	var out bytes.Buffer
	program := tea.NewProgram(newProgressModel(true), tea.WithInput(nil), tea.WithOutput(&out), tea.WithoutSignalHandler(), tea.WithWindowSize(100, 30))
	go func() {
		for _, step := range steps {
			program.Send(progressStepMsg(step))
		}
		program.Send(done)
	}()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	return ansi.Strip(out.String())
}

func TestProgressScrollsFinishedStepsAndBoxesTheSummary(t *testing.T) {
	steps := []string{"Creating LXC 100", "Waiting for first-boot bootstrap (up to 5m)"}
	commands := []string{"export LUDUS_URL=https://203.0.113.2:8080", "export LUDUS_API_KEY=JD.secret", "ludus range list all"}
	result := installResult{Title: "Ludus is running in LXC 100", Rows: [][2]string{{"API", "https://203.0.113.2:8080"}}, Notes: []string{"Initial admin JD created."}, Commands: commands}
	output := runProgressFixture(t, steps, progressDoneMsg{result: result})
	previous := -1
	for _, step := range append([]string{"Preparing the installation"}, steps...) {
		index := strings.Index(output, "✓ "+step)
		if index <= previous {
			t.Fatalf("finished step %q missing or out of order:\n%s", step, output)
		}
		previous = index
	}
	rule := strings.Repeat("─", 40)
	first, last := strings.Index(output, rule), strings.LastIndex(output, rule)
	if first < 0 || first == last {
		t.Fatalf("summary must be framed by rules above and below:\n%s", output)
	}
	box := output[first:last]
	for _, want := range []string{"Ludus is running in LXC 100", "API", "https://203.0.113.2:8080", "Initial admin JD created."} {
		if !strings.Contains(box, want) {
			t.Fatalf("summary lacks %q:\n%s", want, box)
		}
	}
	// Each command must be a whole line inside the frame, with no side border
	// or padding, so selecting the lines copies exactly a runnable snippet.
	lines := strings.Split(strings.ReplaceAll(box, "\r", ""), "\n")
	for _, command := range commands {
		found := false
		for _, line := range lines {
			found = found || line == command
		}
		if !found {
			t.Fatalf("command %q is not an exact line inside the summary:\n%s", command, box)
		}
	}
	if strings.ContainsAny(box, "│╭╮╰╯") {
		t.Fatalf("side borders would be copied with the commands:\n%s", box)
	}

	failed := runProgressFixture(t, steps, progressDoneMsg{result: result, err: errors.New("bootstrap failed")})
	if !strings.Contains(failed, "✗ Waiting for first-boot bootstrap") || strings.Contains(failed, "✓ Waiting for first-boot bootstrap") {
		t.Fatalf("the failing step must be marked as failed:\n%s", failed)
	}
	if strings.Contains(failed, "Ludus is running") {
		t.Fatalf("a failed install must not print the success summary:\n%s", failed)
	}
}

func TestProgressWarningDoesNotFinishTheRunningStep(t *testing.T) {
	var out bytes.Buffer
	program := tea.NewProgram(newProgressModel(true), tea.WithInput(nil), tea.WithOutput(&out), tea.WithoutSignalHandler(), tea.WithWindowSize(100, 30))
	go func() {
		program.Send(progressStepMsg("Verifying media"))
		program.Send(progressWarningMsg("Signed release artifact verification is disabled"))
		program.Send(progressDoneMsg{err: errors.New("stop")})
	}()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	output := ansi.Strip(out.String())
	if !strings.Contains(output, "! Signed release artifact verification is disabled") || strings.Contains(output, "✓ Signed release") {
		t.Fatalf("a warning must be shown as a warning, not a completed step:\n%s", output)
	}
	if strings.Count(output, "✓ Verifying media") != 0 || !strings.Contains(output, "✗ Verifying media") {
		t.Fatalf("a warning must not complete the step that is running:\n%s", output)
	}
}
