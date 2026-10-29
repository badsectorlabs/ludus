package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// stepSink receives installer step descriptions and warningSink warnings. When
// nil, logStep and logWarning print plain "[+]"/"[!]" lines, as unattended runs
// and CI logs expect.
var stepSink, warningSink func(string)

func logStep(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if stepSink != nil {
		stepSink(text)
		return
	}
	fmt.Fprintf(os.Stderr, "[+] %s\n", text)
}

// logWarning reports a condition without starting or finishing a step.
func logWarning(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if warningSink != nil {
		warningSink(text)
		return
	}
	fmt.Fprintf(os.Stderr, "[!] %s\n", text)
}

type progressStepMsg string

type progressWarningMsg string

type progressDoneMsg struct {
	result installResult
	err    error
}

// progressModel shows the running step beside a spinner. Each finished step is
// printed above the view in grey, so completed work scrolls up.
type progressModel struct {
	styles   *Styles
	spinner  spinner.Model
	current  string
	started  time.Time
	width    int
	finished bool
}

func newProgressModel(dark bool) *progressModel {
	styles := NewStyles(dark)
	return &progressModel{
		styles:  styles,
		spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(styles.Gold))),
		current: "Preparing the installation",
		started: time.Now(),
		width:   80,
	}
}

func (m *progressModel) Init() tea.Cmd { return m.spinner.Tick }

func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case progressStepMsg:
		done := m.finishedLine(true)
		m.current, m.started = string(msg), time.Now()
		return m, tea.Println(done)
	case progressWarningMsg:
		return m, tea.Println(lipgloss.NewStyle().Foreground(m.styles.Gold).Render("! " + string(msg)))
	case progressDoneMsg:
		m.finished = true
		lines := []tea.Cmd{tea.Println(m.finishedLine(msg.err == nil))}
		if msg.err == nil {
			lines = append(lines, tea.Println("\n"+m.summaryBox(msg.result)+"\n"))
		}
		return m, tea.Sequence(append(lines, tea.Quit)...)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m *progressModel) elapsed() string {
	return time.Since(m.started).Round(time.Second).String()
}

// finishedLine renders the current step as completed or failed.
func (m *progressModel) finishedLine(ok bool) string {
	if !ok {
		return lipgloss.NewStyle().Foreground(m.styles.Red).Bold(true).Render("✗ "+m.current) + " " + m.styles.Help.Render(m.elapsed())
	}
	check := lipgloss.NewStyle().Foreground(m.styles.Green).Render("✓")
	return check + " " + m.styles.Help.Render(m.current+"  "+m.elapsed())
}

func (m *progressModel) View() tea.View {
	if m.finished {
		return tea.NewView("")
	}
	line := m.spinner.View() + " " + lipgloss.NewStyle().Bold(true).Render(m.current) + "  " + m.styles.Help.Render(m.elapsed())
	// A wrapped line would leave stale rows behind in the inline renderer.
	return tea.NewView(ansi.Truncate(line, max(1, m.width-1), "…"))
}

// summaryBox frames the summary with gold rules above and below but no side
// borders, so selecting the command lines copies only the commands. Those lines
// are neither padded nor wrapped; colour codes never reach the clipboard.
func (m *progressModel) summaryBox(result installResult) string {
	width := min(max(40, m.width-1), 90)
	labelWidth := 0
	for _, row := range result.Rows {
		labelWidth = max(labelWidth, len(row[0]))
	}
	label := lipgloss.NewStyle().Foreground(m.styles.Gold).Bold(true).Width(labelWidth + 2)
	value := lipgloss.NewStyle().Width(width - labelWidth - 2)
	rule := lipgloss.NewStyle().Foreground(m.styles.Gold).Render(strings.Repeat("─", width))
	lines := []string{rule, "", m.styles.StatusHeader.Render(result.Title), ""}
	for _, row := range result.Rows {
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, label.Render(row[0]), value.Render(row[1])))
	}
	if len(result.Notes) > 0 {
		lines = append(lines, "")
		for _, note := range result.Notes {
			lines = append(lines, lipgloss.NewStyle().Width(width).Render(note))
		}
	}
	if len(result.Commands) > 0 {
		lines = append(lines, "")
		command := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		for _, line := range result.Commands {
			lines = append(lines, command.Render(line))
		}
	}
	return strings.Join(append(lines, "", rule), "\n")
}

// runInstallWithProgress drives the installation behind the progress view on
// the controlling terminal. Input stays cooked so Ctrl-C still delivers SIGINT
// to the installer's own handler, which cancels and rolls back as before.
func runInstallWithProgress(ctx context.Context, opts *Options, inv *Inventory, migration *Migration, dark bool) error {
	tty, _, _, err := openInstallerTTY()
	if err != nil {
		result, err := runInstall(ctx, opts, inv, migration)
		if err == nil {
			fmt.Print(result.plain())
		}
		return err
	}
	defer tty.Close()
	program := tea.NewProgram(newProgressModel(dark), tea.WithInput(nil), tea.WithOutput(tty), tea.WithoutSignalHandler())
	stepSink = func(text string) { program.Send(progressStepMsg(text)) }
	warningSink = func(text string) { program.Send(progressWarningMsg(text)) }
	defer func() { stepSink, warningSink = nil, nil }()
	var installErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		var result installResult
		result, installErr = runInstall(ctx, opts, inv, migration)
		program.Send(progressDoneMsg{result: result, err: installErr})
	}()
	_, runErr := program.Run()
	<-done
	if runErr != nil && !errors.Is(runErr, tea.ErrProgramKilled) {
		return errors.Join(installErr, fmt.Errorf("progress display: %w", runErr))
	}
	return installErr
}
