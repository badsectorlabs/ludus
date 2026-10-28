package main

import (
	"image/color"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// Styles and customHuhTheme retain the native legacy installer's theme.
// Brand colors follow the terminal background reported to Bubble Tea.
type Styles struct {
	Base, HeaderText, Status, StatusHeader, ErrorHeaderText, Help lipgloss.Style
	Gold, Green, Red                                              color.Color
}

func NewStyles(isDark bool) *Styles {
	lightDark := lipgloss.LightDark(isDark)
	s := Styles{
		Gold:  lightDark(lipgloss.Color("#D8AE2D"), lipgloss.Color("#BF9000")),
		Green: lightDark(lipgloss.Color("#02BA84"), lipgloss.Color("#02BF87")),
		Red:   lipgloss.Color("#A70000"),
	}
	s.Base = lipgloss.NewStyle().Padding(1, 4, 0, 1)
	// Right padding separates the text from the "////" rule; the left edge
	// aligns with the body content.
	s.HeaderText = lipgloss.NewStyle().Foreground(s.Gold).Bold(true).PaddingRight(1)
	s.Status = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.Gold).PaddingLeft(1).MarginTop(1)
	s.StatusHeader = lipgloss.NewStyle().Foreground(s.Green).Bold(true)
	s.ErrorHeaderText = s.HeaderText.Foreground(s.Red)
	s.Help = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	return &s
}

// customHuhTheme uses only the terminal's own palette, so it is identical on
// light and dark backgrounds.
func customHuhTheme(isDark bool) *huh.Styles {
	t := huh.ThemeBase(isDark)
	t.Focused.Base = t.Focused.Base.BorderForeground(lipgloss.Color("8"))
	t.Focused.Title = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	t.Focused.NoteTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	t.Focused.Directory = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	t.Focused.Description = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(lipgloss.Color("9"))
	t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(lipgloss.Color("9"))
	t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(lipgloss.Color("3"))
	t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(lipgloss.Color("3"))
	t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(lipgloss.Color("3"))
	t.Focused.Option = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	t.Focused.MultiSelectSelector = t.Focused.MultiSelectSelector.Foreground(lipgloss.Color("3"))
	t.Focused.SelectedOption = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	t.Focused.SelectedPrefix = t.Focused.SelectedPrefix.Foreground(lipgloss.Color("2"))
	t.Focused.UnselectedOption = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("2"))
	t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(lipgloss.Color("7")).Background(lipgloss.Color("0"))

	t.Blurred = t.Focused
	t.Blurred.Base = t.Blurred.Base.BorderStyle(lipgloss.HiddenBorder())
	t.Blurred.NoteTitle = t.Blurred.NoteTitle.Foreground(lipgloss.Color("8"))
	t.Blurred.Title = t.Blurred.NoteTitle.Foreground(lipgloss.Color("8"))
	t.Blurred.TextInput.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	t.Blurred.TextInput.Text = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()
	return t
}
