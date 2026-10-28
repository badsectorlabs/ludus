package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// These tests exercise the gate with synthetic text, not legal acceptance of
// the real agreement, and never open a tty or invoke installation.
func syntheticLicenseModel() *licenseModel {
	return &licenseModel{width: 80, height: 24, contentWidth: 50, contentHeight: 8, lines: strings.Split(strings.Repeat("test line\n", 24), "\n")}
}

func TestLicenseRequiresBottomAndExplicitAcceptance(t *testing.T) {
	m := syntheticLicenseModel()
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.confirmed || !m.showWarningModal {
		t.Fatal("accepting before the bottom must show a warning, not confirm")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.confirmed || m.showWarningModal {
		t.Fatal("dismissing the warning must not accept")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	if !m.hasScrolledToBottom || m.confirmed {
		t.Fatal("reaching the bottom must unlock, but not activate, acceptance")
	}
	if m.scrollPosition != len(m.lines)-m.contentHeight {
		t.Fatal("scrolling beyond the bottom must clamp to the last visible page")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	if !m.hasScrolledToBottom || m.scrollPosition != 0 {
		t.Fatal("the gate remembers that the complete document was viewed")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.confirmed || m.quitting {
		t.Fatal("explicit acceptance after viewing the bottom must confirm")
	}
}

func TestLicenseCanDeclineWithoutScrolling(t *testing.T) {
	m := syntheticLicenseModel()
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.confirmed || !m.quitting {
		t.Fatal("declining must not require scrolling")
	}
}

func TestLicenseCancellationAlwaysWorks(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{escKey, ctrlCKey} {
		for _, tiny := range []bool{false, true} {
			m := syntheticLicenseModel()
			m.showWarningModal = true
			if tiny {
				m.width, m.height = 40, 10
			}
			m.Update(key)
			if m.confirmed || !m.quitting {
				t.Fatal("cancellation must work in warning modal and undersized terminal")
			}
		}
	}
}

func TestLicenseCannotAcceptWhileTerminalTooSmall(t *testing.T) {
	m := syntheticLicenseModel()
	m.hasScrolledToBottom = true
	m.width, m.height = 53, 19
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.confirmed {
		t.Fatal("an unusable terminal must not accept even after earlier scrolling")
	}
}

func TestLicenseFitsUsableTerminalAndRewraps(t *testing.T) {
	m := &licenseModel{content: clickThroughLicense, darkBackground: true}
	for _, size := range [][2]int{{120, 40}, {54, 20}, {80, 24}} {
		if err := m.resize(size[0], size[1]); err != nil {
			t.Fatal(err)
		}
		view := m.View().Content
		if lipgloss.Width(view) > size[0] || lipgloss.Height(view) > size[1] {
			t.Fatalf("license view exceeds %dx%d: got %dx%d", size[0], size[1], lipgloss.Width(view), lipgloss.Height(view))
		}
		if m.hasScrolledToBottom || m.confirmed {
			t.Fatal("resizing the beginning of the real agreement must not accept or unlock it")
		}
	}
}
