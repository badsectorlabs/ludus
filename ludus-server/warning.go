package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}

	dialogBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#BF9000")).
			Padding(1, 0).
			BorderTop(true).
			BorderLeft(true).
			BorderRight(true).
			BorderBottom(true)

	buttonStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 3).
			Margin(1, 2)

	activeButtonStyle = buttonStyle.
				Foreground(lipgloss.Color("#FFF7DB")).
				Background(lipgloss.Color("#02BF87")).
				Padding(0, 3).
				Margin(1, 2).
				Underline(true)

	docStyle = lipgloss.NewStyle().Padding(1, 2, 1, 2)

	dialogMessage     = "Message"
	confirmButtonText = "Confirm"
	cancelButtonText  = "Cancel"
	dialogWidth       = 50
	backgroundHeight  = 12
)

type model struct {
	activeButton int
	confirmed    bool
	quitting     bool
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "left", "h":
			m.activeButton = 1
		case "right", "l":
			m.activeButton = 0
		case "enter", " ":
			m.confirmed = m.activeButton == 1
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	if m.quitting || m.confirmed {
		return ""
	}

	physicalWidth, _, _ := term.GetSize(int(os.Stdout.Fd()))
	doc := strings.Builder{}

	okButton := buttonStyle.Render(confirmButtonText)
	cancelButton := buttonStyle.Render(cancelButtonText)

	if m.activeButton == 1 {
		okButton = activeButtonStyle.Render(confirmButtonText)
	} else {
		cancelButton = activeButtonStyle.Render(cancelButtonText)
	}

	question := lipgloss.NewStyle().Width(dialogWidth).Align(lipgloss.Center).Render(dialogMessage)
	buttons := lipgloss.JoinHorizontal(lipgloss.Top, okButton, cancelButton)
	ui := lipgloss.JoinVertical(lipgloss.Center, question, buttons)

	dialog := lipgloss.Place(physicalWidth, backgroundHeight,
		lipgloss.Center, lipgloss.Center,
		dialogBoxStyle.Render(ui),
		lipgloss.WithWhitespaceChars("剣闘"),
		lipgloss.WithWhitespaceForeground(subtle),
	)

	doc.WriteString(dialog + "\n\n")

	if physicalWidth > 0 {
		docStyle = docStyle.MaxWidth(physicalWidth)
	}
	return docStyle.Render(doc.String())
}

func showWarning(dialogMessagePassedIn string,
	confirmButtonTextPassedIn string,
	cancelButtonTextPassedIn string,
	dialogWidthPassedIn int,
	backgroundHeightPassedIn int) {

	dialogMessage = dialogMessagePassedIn
	confirmButtonText = confirmButtonTextPassedIn
	cancelButtonText = cancelButtonTextPassedIn
	dialogWidth = dialogWidthPassedIn
	backgroundHeight = backgroundHeightPassedIn

	p := tea.NewProgram(model{})
	m, err := p.Run()
	if err != nil {
		fmt.Println("Error running warning dialog:", err)
		os.Exit(1)
	}

	finalModel := m.(model)
	if !finalModel.confirmed {
		fmt.Println("Exiting")
		os.Exit(1)
	}
}
