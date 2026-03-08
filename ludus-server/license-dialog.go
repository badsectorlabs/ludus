package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// License page model for scrollable license text
type licenseModel struct {
	content             string
	renderedContent     string // Pre-rendered markdown content
	scrollPosition      int
	confirmed           bool
	quitting            bool
	height              int
	width               int
	selectedButton      int  // 0 for Accept, 1 for Decline
	hasScrolledToBottom bool // Tracks if user has viewed the bottom
	showWarningModal    bool // Shows modal when user tries to accept too early
}

func (m licenseModel) Init() tea.Cmd {
	return nil
}

func (m licenseModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If warning modal is showing, only accept Enter to dismiss it
		if m.showWarningModal {
			switch msg.String() {
			case "enter", " ":
				m.showWarningModal = false
			}
			return m, nil
		}

		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			if m.scrollPosition > 0 {
				m.scrollPosition--
			}
			m.checkIfAtBottom()
		case "down", "j":
			// maxScroll will be calculated based on wrapped lines
			m.scrollPosition++
			m.checkIfAtBottom()
		case "pgup":
			// Calculate approximate page height for page up
			maxDialogHeight := int(float64(m.height) * 0.8)
			if maxDialogHeight < 20 {
				maxDialogHeight = 20
			}
			contentHeight := maxDialogHeight - 16
			if contentHeight < 10 {
				contentHeight = 10
			}
			m.scrollPosition -= contentHeight
			if m.scrollPosition < 0 {
				m.scrollPosition = 0
			}
			m.checkIfAtBottom()
		case "pgdown":
			// Calculate approximate page height for page down
			maxDialogHeight := int(float64(m.height) * 0.8)
			if maxDialogHeight < 20 {
				maxDialogHeight = 20
			}
			contentHeight := maxDialogHeight - 16
			if contentHeight < 10 {
				contentHeight = 10
			}
			m.scrollPosition += contentHeight
			m.checkIfAtBottom()
		case "home":
			m.scrollPosition = 0
			m.checkIfAtBottom()
		case "end":
			// Will be clamped based on content
			m.scrollPosition = 999999
			m.checkIfAtBottom()
		case "tab":
			m.selectedButton = (m.selectedButton + 1) % 2
		case "shift+tab":
			m.selectedButton = (m.selectedButton + 1) % 2
		case "left":
			m.selectedButton = 0
		case "right":
			m.selectedButton = 1
		case "enter", " ":
			if m.selectedButton == 0 {
				// Check if user has scrolled to the bottom
				if !m.hasScrolledToBottom {
					m.showWarningModal = true
					return m, nil
				}
				m.confirmed = true
			} else {
				m.quitting = true
			}
			return m, tea.Quit
		case "esc":
			m.quitting = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.checkIfAtBottom()
	}
	return m, nil
}

// checkIfAtBottom checks if the user has scrolled to the bottom of the content
func (m *licenseModel) checkIfAtBottom() {
	// Calculate content dimensions (same logic as in View)
	maxDialogHeight := int(float64(m.height) * 0.8)
	if maxDialogHeight < 20 {
		maxDialogHeight = 20
	}
	contentHeight := maxDialogHeight - 16
	if contentHeight < 10 {
		contentHeight = 10
	}

	// Get wrapped lines
	var wrappedLines []string
	if m.renderedContent != "" {
		wrappedLines = strings.Split(strings.TrimRight(m.renderedContent, "\n"), "\n")
	} else {
		wrappedLines = strings.Split(strings.TrimRight(m.content, "\n"), "\n")
	}

	// Calculate max scroll position
	maxScroll := len(wrappedLines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Clamp scroll position
	displayScrollPos := m.scrollPosition
	if displayScrollPos > maxScroll {
		displayScrollPos = maxScroll
	}
	if displayScrollPos < 0 {
		displayScrollPos = 0
	}

	// Check if at bottom
	if displayScrollPos >= maxScroll {
		m.hasScrolledToBottom = true
	}
}

func (m licenseModel) View() string {
	if m.quitting {
		return ""
	}

	physicalWidth, physicalHeight, _ := term.GetSize(int(os.Stdout.Fd()))
	if physicalWidth > 0 {
		m.width = physicalWidth
	}
	if physicalHeight > 0 {
		m.height = physicalHeight
	}

	// License dialog styles
	licenseBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#BF9000")).
		Padding(1, 2).
		BorderTop(true).
		BorderLeft(true).
		BorderRight(true).
		BorderBottom(true)

	scrollAreaStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("#666666")).
		Padding(1, 1).
		Margin(0, 0)

	buttonStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFF7DB")).
		Background(lipgloss.Color("#888B7E")).
		Padding(0, 3).
		Margin(1, 2)

	activeButtonStyle := buttonStyle.
		Foreground(lipgloss.Color("#FFF7DB")).
		Background(lipgloss.Color("#02BF87")).
		Padding(0, 3).
		Margin(1, 2).
		Underline(true)

	titleStyle := lipgloss.NewStyle().
		Foreground(gold).
		Bold(true).
		Align(lipgloss.Center).
		MarginBottom(0)

	helpStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#666666")).
		Align(lipgloss.Center).
		MarginTop(0)

	// Calculate max dialog dimensions (80% of terminal width, 90% of terminal height)
	maxDialogWidth := int(float64(m.width) * 0.8)
	if maxDialogWidth < 60 {
		maxDialogWidth = 60
	}

	maxDialogHeight := int(float64(m.height) * 0.8)
	if maxDialogHeight < 20 {
		maxDialogHeight = 20
	}

	// Calculate content area dimensions
	// Account for: border (4), padding (4), scroll border (4) = 12 total width overhead
	contentWidth := maxDialogWidth - 12
	// Account for: border (4), padding (2), title (1), spacers (3), buttons+margin (3), help (1), scroll padding (2) = 16 total height overhead
	contentHeight := maxDialogHeight - 16

	// Ensure minimum dimensions
	if contentWidth < 40 {
		contentWidth = 40
	}
	if contentHeight < 10 {
		contentHeight = 10
	}

	// Use pre-rendered content and wrap it to current width
	// Re-wrap the content if needed (glamour handles wrapping, but we need to split into lines)
	var wrappedLines []string
	if m.renderedContent != "" {
		wrappedLines = strings.Split(strings.TrimRight(m.renderedContent, "\n"), "\n")
	} else {
		// Fallback: split plain content if rendering failed
		wrappedLines = strings.Split(strings.TrimRight(m.content, "\n"), "\n")
	}

	// Calculate max scroll position based on wrapped lines
	maxScroll := len(wrappedLines) - contentHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Clamp scroll position for display (don't modify m.scrollPosition in View!)
	displayScrollPos := m.scrollPosition
	if displayScrollPos > maxScroll {
		displayScrollPos = maxScroll
	}
	if displayScrollPos < 0 {
		displayScrollPos = 0
	}

	// Get visible lines from wrapped lines
	var visibleLines []string
	if len(wrappedLines) <= contentHeight {
		visibleLines = wrappedLines
	} else {
		start := displayScrollPos
		end := start + contentHeight
		if end > len(wrappedLines) {
			end = len(wrappedLines)
		}
		visibleLines = wrappedLines[start:end]
	}

	// Pad visible lines to ensure consistent height
	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}

	// Create scrollable content
	content := strings.Join(visibleLines, "\n")
	scrollArea := scrollAreaStyle.Width(contentWidth).Height(contentHeight).Render(content)

	// Create buttons based on selection
	var acceptButton, declineButton string
	if m.selectedButton == 0 {
		acceptButton = activeButtonStyle.Render("Accept")
		declineButton = buttonStyle.Render("Decline")
	} else {
		acceptButton = buttonStyle.Render("Accept")
		declineButton = activeButtonStyle.Render("Decline")
	}
	buttons := lipgloss.JoinHorizontal(lipgloss.Center, acceptButton, declineButton)

	// Create help text
	helpText := helpStyle.Render("↑↓/PgUp/PgDn: Scroll • Tab/←→: Switch • Enter: Confirm • Esc: Decline")

	// Combine all elements with spacing
	title := titleStyle.Width(contentWidth).Render("License Agreement")
	spacer := ""
	ui := lipgloss.JoinVertical(lipgloss.Center, title, spacer, scrollArea, spacer, buttons, spacer, helpText)

	// Render the dialog with the box style
	renderedDialog := licenseBoxStyle.Render(ui)

	// Center the dialog within terminal bounds
	dialog := lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		renderedDialog,
		lipgloss.WithWhitespaceChars("剣闘"),
		lipgloss.WithWhitespaceForeground(lipgloss.Color("#383838")),
	)

	// If warning modal is showing, overlay it on top of the dialog
	if m.showWarningModal {
		return m.renderWarningModal(dialog)
	}

	return dialog
}

// renderWarningModal renders the warning modal overlay
func (m licenseModel) renderWarningModal(baseDialog string) string {
	modalBoxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#FF0000")).
		Padding(1, 2).
		Background(lipgloss.Color("#1a1a1a")).
		BorderTop(true).
		BorderLeft(true).
		BorderRight(true).
		BorderBottom(true)

	modalContentWidth := 56

	modalTextStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#1a1a1a")).
		Align(lipgloss.Center).
		Width(modalContentWidth).
		MarginBottom(1)

	modalButtonContainerStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("#1a1a1a")).
		Width(modalContentWidth).
		Align(lipgloss.Center)

	modalButtonStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFF7DB")).
		Background(lipgloss.Color("#02BF87")).
		Padding(0, 3).
		Underline(true)

	// Modal content
	modalText := modalTextStyle.Render("Sorry, our lawyers made us force you to\nat least scroll to the bottom of the agreement 📜🧑‍⚖️")
	modalButton := modalButtonStyle.Render("Sigh... Fine...")
	modalButtonContainer := modalButtonContainerStyle.Render(modalButton)
	modalContent := lipgloss.JoinVertical(lipgloss.Left, modalText, modalButtonContainer)

	// Render modal box
	modal := modalBoxStyle.Render(modalContent)

	// Split base dialog and modal into lines for overlay
	baseLines := strings.Split(baseDialog, "\n")
	modalLines := strings.Split(modal, "\n")

	// Calculate center position for modal
	modalHeight := len(modalLines)
	modalWidth := 0
	for _, line := range modalLines {
		// Use visible width (strip ANSI codes for accurate measurement)
		width := lipgloss.Width(line)
		if width > modalWidth {
			modalWidth = width
		}
	}

	startRow := (m.height - modalHeight) / 2
	startCol := (m.width - modalWidth) / 2

	// Overlay modal lines onto base dialog lines
	result := make([]string, 0, len(baseLines))
	result = append(result, baseLines...)

	for i, modalLine := range modalLines {
		targetRow := startRow + i
		if targetRow >= 0 && targetRow < len(result) {
			baseLine := result[targetRow]
			baseLineWidth := lipgloss.Width(baseLine)

			// Calculate padding needed
			leftPad := startCol
			if leftPad < 0 {
				leftPad = 0
			}

			// Build the overlaid line
			var overlaidLine string
			if leftPad < baseLineWidth {
				// Extract the left part of base line (up to where modal starts)
				leftPart := truncateToWidth(baseLine, leftPad)
				overlaidLine = leftPart + modalLine

				// Add right part if modal doesn't extend to end of line
				modalLineWidth := lipgloss.Width(modalLine)
				rightStart := leftPad + modalLineWidth
				if rightStart < baseLineWidth {
					rightPart := skipToWidth(baseLine, rightStart)
					overlaidLine += rightPart
				}
			} else {
				// Modal starts beyond the base line, just pad and add modal
				overlaidLine = baseLine + strings.Repeat(" ", leftPad-baseLineWidth) + modalLine
			}

			result[targetRow] = overlaidLine
		}
	}

	return strings.Join(result, "\n")
}

// truncateToWidth returns the prefix of s that fits within the given display width
func truncateToWidth(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	currentWidth := 0
	inEscape := false
	result := strings.Builder{}

	for _, r := range s {
		result.WriteRune(r)

		// Handle ANSI escape sequences (don't count toward width)
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}

		// Count regular characters
		currentWidth++
		if currentWidth >= maxWidth {
			break
		}
	}

	return result.String()
}

// skipToWidth skips the first n display characters and returns the rest
func skipToWidth(s string, skipWidth int) string {
	if skipWidth <= 0 {
		return s
	}
	currentWidth := 0
	inEscape := false
	var result strings.Builder

	skipping := true
	for _, r := range s {
		// Handle ANSI escape sequences (don't count toward width)
		if r == '\x1b' {
			inEscape = true
			if !skipping {
				result.WriteRune(r)
			}
			continue
		}
		if inEscape {
			if !skipping {
				result.WriteRune(r)
			}
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEscape = false
			}
			continue
		}

		// Count regular characters
		if skipping {
			if currentWidth >= skipWidth {
				skipping = false
				result.WriteRune(r)
			}
			currentWidth++
		} else {
			result.WriteRune(r)
		}
	}

	return result.String()
}

func showLicense(licenseText string) bool {
	physicalWidth, physicalHeight, _ := term.GetSize(int(os.Stdout.Fd()))

	// Pre-render the markdown content once with a reasonable width for wrapping
	maxDialogWidth := int(float64(physicalWidth) * 0.8)
	if maxDialogWidth < 60 {
		maxDialogWidth = 60
	}
	contentWidth := maxDialogWidth - 12 // Account for borders and padding
	if contentWidth < 40 {
		contentWidth = 40
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(contentWidth),
	)

	var renderedContent string
	if err != nil {
		// Fallback to plain text if glamour fails
		renderedContent = licenseText
	} else {
		rendered, err := renderer.Render(licenseText)
		if err != nil {
			// Fallback to plain text if rendering fails
			renderedContent = licenseText
		} else {
			renderedContent = rendered
		}
	}

	initialModel := licenseModel{
		content:             licenseText,
		renderedContent:     renderedContent,
		scrollPosition:      0,
		confirmed:           false,
		quitting:            false,
		height:              physicalHeight,
		width:               physicalWidth,
		selectedButton:      0,
		hasScrolledToBottom: false,
		showWarningModal:    false,
	}

	// Check if already at bottom (e.g., all content fits on screen)
	initialModel.checkIfAtBottom()

	p := tea.NewProgram(initialModel)

	m, err := p.Run()
	if err != nil {
		fmt.Println("Error running license dialog:", err)
		os.Exit(1)
	}

	finalModel := m.(licenseModel)
	return finalModel.confirmed
}

func showLicenseDialog() {
	clickThroughLicense := `# Ludus Self-Hosted Software License

## The Short Version (In Plain English)

We, Bad Sector Labs, Inc., have created Ludus to be a powerful tool. A significant portion of Ludus is built on open-source software and we believe in the power of community-driven development.
Here's the deal:

- You get a license to use our Ludus software, and we're using the "open core" model.
- The core open-source parts of Ludus are licensed under the **GNU Affero General Public License version 3 (AGPLv3)**. This means you are free to use, modify, and distribute these parts as long as you adhere to the terms of the AGPLv3.
- However, the pro/enterprise plugins and any other features we've developed that are not explicitly licensed as open-source are our proprietary, closed-source code. You do not own them, and your license gives you the right to use them but not to redistribute them. We have to pay the bills somehow!

## The Details (The Legal Stuff)

This is a legal agreement between you (either an individual or a single entity) and **Bad Sector Labs, Inc.** for the Ludus software product, which includes the core software and may include associated media, printed materials, and "online" or electronic documentation ("Software").

By installing, copying, or otherwise using the Software, you agree to be bound by the terms of this Agreement. If you do not agree, do not install or use the Software.

### 1. Your License to Use Ludus

Bad Sector Labs, Inc. grants you a **non-exclusive, non-transferable** license to install and use the Ludus software on your own servers for your internal business purposes.

### 2. The Open Source Parts of Ludus (AGPLv3)

The core Ludus software and any components explicitly identified as "open source" are licensed to you under the terms of the **GNU Affero General Public License, version 3 (AGPLv3)**. The full text of the AGPLv3 is provided with the Software. The AGPLv3 gives you certain rights, including the right to use, copy, modify, and distribute the open-source code, provided you comply with its terms. One key aspect of the AGPLv3 is that if you modify the open-source code and make it available to others over a network, you must also make your modified source code available to them under the same license.

### 3. The Closed-Source Enterprise Plugins and Features

Ludus also includes proprietary, closed-source enterprise and pro plugins and features ("Enterprise Components"). These are owned exclusively by Bad Sector Labs, Inc. and are not licensed under the AGPLv3. Your license to use the Enterprise Components is limited to their use as part of the Ludus software.

You agree that you will **not**:

- Copy, modify, create derivative works of, or reverse engineer the Enterprise Components.
- Sell, rent, lease, sublicense, or otherwise transfer your rights to the Enterprise Components.
- Remove or alter any copyright or other proprietary notices on the Enterprise Components.

All rights in and to the Enterprise Components not expressly granted to you in this Agreement are reserved by Bad Sector Labs, Inc.

### 4. Ownership

You acknowledge that Bad Sector Labs, Inc. retains all right, title, and interest in and to the Ludus software, including all intellectual property rights in the Enterprise Components. Your license gives you the right to use the software but does not transfer any ownership to you.

### 5. Disclaimer of Warranty

The Ludus software is provided **"as is,"** without warranty of any kind. Bad Sector Labs, Inc. disclaims all warranties, whether express, implied, or statutory, including but not limited to any implied warranties of merchantability, fitness for a particular purpose, and non-infringement.

### 6. Limitation of Liability

In no event shall Bad Sector Labs, Inc. be liable for any special, incidental, indirect, or consequential damages whatsoever (including, without limitation, damages for loss of business profits, business interruption, loss of business information, or any other pecuniary loss) arising out of the use of or inability to use the Ludus software, even if Bad Sector Labs, Inc. has been advised of the possibility of such damages.

### 7. Termination

This Agreement is effective until terminated. Your rights under this Agreement will terminate automatically without notice from Bad Sector Labs, Inc. if you fail to comply with any term(s) of this Agreement. Upon termination, you must cease all use of the Ludus software and destroy all copies, full or partial, of the software.

### 8. Governing Law

This Agreement will be governed by and construed in accordance with the laws of the State of Delaware, without regard to its conflict of laws principles.

---

By clicking **"Accept,"** you are acknowledging that you have read and understood this Agreement and agree to be bound by its terms and conditions.`

	accepted := showLicense(clickThroughLicense)
	if !accepted {
		fmt.Println("License not accepted. Exiting.")
		os.Exit(1)
	}
	fmt.Println("License accepted. Continuing...")
}
