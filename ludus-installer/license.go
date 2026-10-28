package main

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
)

type licenseModel struct {
	content                     string
	lines                       []string
	width, height               int
	contentWidth, contentHeight int
	scrollPosition              int
	selectedButton              int // 0: Accept, 1: Decline, as in the legacy dialog.
	hasScrolledToBottom         bool
	showWarningModal            bool
	confirmed, quitting         bool
	darkBackground              bool
	err                         error
}

func (m *licenseModel) Init() tea.Cmd { return tea.RequestBackgroundColor }

func (m *licenseModel) resize(width, height int) error {
	m.width, m.height = width, height
	if width < minimumUIWidth || height < minimumUIHeight {
		return nil
	}
	// Use the original rounded dialog, inner scroll border and button margins,
	// but constrain every frame to the real tty, including at 54 x 20.
	dialogWidth := min(width-2, max(52, width*8/10))
	dialogHeight := min(height-2, max(18, height*8/10))
	contentWidth, contentHeight := dialogWidth-10, dialogHeight-12
	if contentWidth != m.contentWidth || len(m.lines) == 0 {
		style := "dark"
		if !m.darkBackground {
			style = "light"
		}
		renderer, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(contentWidth))
		if err != nil {
			return fmt.Errorf("preparing license agreement: %w", err)
		}
		rendered, err := renderer.Render(m.content)
		if err != nil {
			return fmt.Errorf("rendering license agreement: %w", err)
		}
		m.lines = strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	}
	m.contentWidth, m.contentHeight = contentWidth, contentHeight
	m.checkIfAtBottom()
	return nil
}

func (m *licenseModel) checkIfAtBottom() {
	if m.width < minimumUIWidth || m.height < minimumUIHeight || m.contentHeight <= 0 || len(m.lines) == 0 {
		return
	}
	maxScroll := max(0, len(m.lines)-m.contentHeight)
	m.scrollPosition = max(0, min(m.scrollPosition, maxScroll))
	if m.scrollPosition == maxScroll {
		m.hasScrolledToBottom = true
	}
}

func (m *licenseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		if msg.IsDark() != m.darkBackground {
			// Re-render the agreement with the matching Glamour style.
			m.darkBackground, m.lines = msg.IsDark(), nil
			if err := m.resize(m.width, m.height); err != nil {
				m.err = err
				return m, tea.Quit
			}
		}
	case tea.WindowSizeMsg:
		if err := m.resize(msg.Width, msg.Height); err != nil {
			m.err = err
			return m, tea.Quit
		}
	case tea.KeyPressMsg:
		// Cancellation must work even in the warning modal or an undersized tty.
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.quitting = true
			return m, tea.Quit
		}
		if m.width < minimumUIWidth || m.height < minimumUIHeight {
			return m, nil
		}
		if m.showWarningModal {
			if msg.String() == "enter" || msg.String() == "space" {
				m.showWarningModal = false
			}
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			m.scrollPosition--
		case "down", "j":
			m.scrollPosition++
		case "pgup":
			m.scrollPosition -= m.contentHeight
		case "pgdown":
			m.scrollPosition += m.contentHeight
		case "home":
			m.scrollPosition = 0
		case "end":
			m.scrollPosition = len(m.lines)
		case "tab", "shift+tab":
			m.selectedButton = (m.selectedButton + 1) % 2
		case "left":
			m.selectedButton = 0
		case "right":
			m.selectedButton = 1
		case "enter", "space":
			if m.selectedButton == 1 {
				m.quitting = true
				return m, tea.Quit
			}
			if !m.hasScrolledToBottom {
				m.showWarningModal = true
				return m, nil
			}
			m.confirmed = true
			return m, tea.Quit
		}
		m.checkIfAtBottom()
	}
	return m, nil
}

func (m *licenseModel) warningView() string {
	width := min(56, m.width-10)
	background := lipgloss.Color("#1a1a1a")
	text := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(background).
		Align(lipgloss.Center).Width(width).MarginBottom(1).
		Render("Sorry, our lawyers made us force you to at least scroll to the bottom of the agreement. 📜🧑‍⚖️")
	button := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF7DB")).
		Background(lipgloss.Color("#02BF87")).Padding(0, 3).Underline(true).Render("Sigh... Fine...")
	button = lipgloss.NewStyle().Background(background).Width(width).Align(lipgloss.Center).Render(button)
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#FF0000")).
		Padding(1, 2).Background(background).Render(text + "\n" + button)
}

func (m *licenseModel) View() tea.View {
	view := tea.NewView(m.render())
	view.AltScreen = true
	return view
}

func (m *licenseModel) render() string {
	if m.quitting || m.confirmed || m.err != nil {
		return ""
	}
	if m.width < minimumUIWidth || m.height < minimumUIHeight {
		return smallTerminalView()
	}
	if m.showWarningModal {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.warningView())
	}
	start := max(0, min(m.scrollPosition, max(0, len(m.lines)-m.contentHeight)))
	content := strings.Join(m.lines[start:min(len(m.lines), start+m.contentHeight)], "\n")
	scroll := lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("#666666")).
		Padding(0, 1).Width(m.contentWidth + 2).Height(m.contentHeight).Render(content)
	buttonStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFF7DB")).
		Background(lipgloss.Color("#888B7E")).Padding(0, 3).Margin(1, 2)
	activeStyle := buttonStyle.Background(lipgloss.Color("#02BF87")).Underline(true)
	accept, decline := buttonStyle, buttonStyle
	if m.selectedButton == 0 {
		accept = activeStyle
	} else {
		decline = activeStyle
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, accept.Render("Accept"), decline.Render("Decline"))
	title := lipgloss.NewStyle().Foreground(NewStyles(m.darkBackground).Gold).Bold(true).Align(lipgloss.Center).
		Width(m.contentWidth).Render("License Agreement")
	help := lipgloss.NewStyle().Foreground(lipgloss.Color("#666666")).Align(lipgloss.Center).
		Render("↑↓/PgUp/PgDn scroll; Tab/←→ select\nEnter confirm; Esc decline")
	body := lipgloss.JoinVertical(lipgloss.Center, title, scroll, buttons, help)
	dialog := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#BF9000")).
		Padding(1, 2).Render(body)
	return licenseBackdrop(m.width, m.height, dialog)
}

func licenseBackdrop(width, height int, dialog string) string {
	// Lip Gloss's whitespace renderer can overfill an odd-width gap with a
	// double-width rune. Keep the legacy texture, padding odd cells with a space.
	texture := func(columns int) string {
		text := strings.Repeat("剣闘", columns/4)
		if columns%4 >= 2 {
			text += "剣"
		}
		if columns%2 != 0 {
			text += " "
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#383838")).Render(text)
	}
	lines := strings.Split(dialog, "\n")
	dialogWidth := lipgloss.Width(dialog)
	leftWidth := max(0, (width-dialogWidth)/2)
	left := texture(leftWidth)
	right := texture(max(0, width-dialogWidth-leftWidth))
	blank := texture(width)
	top := max(0, (height-len(lines))/2)
	var view strings.Builder
	for row := range height {
		if row > 0 {
			view.WriteByte('\n')
		}
		if row < top || row >= top+len(lines) {
			view.WriteString(blank)
		} else {
			view.WriteString(left)
			view.WriteString(lines[row-top])
			view.WriteString(right)
		}
	}
	return view.String()
}

func runLicenseDialog(ctx context.Context) (bool, error) {
	tty, width, height, err := openInstallerTTY()
	if err != nil {
		return false, err
	}
	defer tty.Close()
	// Bubble Tea reports the real background after Init; dark is its default.
	model := &licenseModel{content: clickThroughLicense, darkBackground: true}
	if err := model.resize(width, height); err != nil {
		return false, err
	}
	result, err := tea.NewProgram(model, tea.WithInput(tty), tea.WithOutput(tty), tea.WithContext(ctx)).Run()
	if err != nil {
		return false, fmt.Errorf("license dialog: %w", err)
	}
	final := result.(*licenseModel)
	if final.err != nil {
		return false, final.err
	}
	return final.confirmed && !final.quitting, nil
}
