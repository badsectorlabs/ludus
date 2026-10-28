package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestInitialAdminRejectsValuesLudusWouldRefuse(t *testing.T) {
	inv := &Inventory{ProxmoxUsers: map[string]bool{"jane-doe@pve": true}, Pools: map[string]bool{"JD": true}}
	opts := &Options{AdminPassword: "correct-horse"}
	for _, test := range []struct{ key, value string }{
		{"ADMIN_NAME", ""},
		{"ADMIN_NAME", " Padded"},
		{"ADMIN_NAME", "Jane Doe"}, // jane-doe@pve already exists
		{"ADMIN_NAME", "a:b"},
		{"ADMIN_EMAIL", "not-an-email"},
		{"ADMIN_EMAIL", "Jane <jane@example.com>"},
		{"ADMIN_EMAIL", "a@localhost"}, // PocketBase rejects this at first boot
		{"ADMIN_USERID", "1JD"},
		{"ADMIN_USERID", "root"},
		{"ADMIN_USERID", "JD"}, // the user's pool would collide
		{"ADMIN_USERID", strings.Repeat("A", 22)},
		{"ADMIN_PASSWORD", "short"},
		{"ADMIN_PASSWORD_CONFIRM", "correct-horsE"},
	} {
		if err := validateField(test.key, test.value, opts, inv); err == nil {
			t.Errorf("%s accepted %q", test.key, test.value)
		}
	}
	for _, test := range []struct{ key, value string }{
		{"ADMIN_NAME", "John Smith"},
		{"ADMIN_EMAIL", "john@example.com"},
		{"ADMIN_USERID", "JS"},
		{"ADMIN_USERID", "A" + strings.Repeat("1", 20)},
		{"ADMIN_PASSWORD", "12345678"},
		{"ADMIN_PASSWORD_CONFIRM", "correct-horse"},
	} {
		if err := validateField(test.key, test.value, opts, inv); err != nil {
			t.Errorf("%s rejected %q: %v", test.key, test.value, err)
		}
	}
}

func TestInitialAdminPageIsRequiredOnlyForFreshInstalls(t *testing.T) {
	m, _ := installerUIFixture(t, nil)
	focusInstallerUIField(m, uiCACertificate)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.review || m.focusedIndex() != uiAdminName {
		t.Fatal("a fresh install must continue to the admin page")
	}
	fillInitialAdmin(m)
	focusInstallerUIField(m, uiAdminPasswordConfirm)
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.review || m.errorText != "" {
		t.Fatalf("a complete admin page must enter review: %s", m.errorText)
	}
	review := ansi.Strip(strings.Join(m.settingLines(200, -1, false), "\n"))
	if strings.Contains(review+m.View().Content, "correct-horse") || strings.Contains(review, "Confirm admin password") || !strings.Contains(review, "Admin user ID: JD") || !strings.Contains(review, "Admin password: ********") {
		t.Fatalf("review must show the admin ID and never the password:\n%s", review)
	}
	if o := m.candidate(); o.AdminUserID != "JD" || o.AdminPassword != "correct-horse" || o.AdminEmail != "jane@example.com" {
		t.Fatalf("admin details lost: %+v", o)
	}

	for name, change := range map[string]func(*Options, *Inventory){
		"migration":    func(o *Options, _ *Inventory) { o.MigrateHost = true },
		"state import": func(o *Options, _ *Inventory) { o.ImportDB = "/tmp/state.tar.gz" },
	} {
		m, _ := installerUIFixture(t, change)
		focusInstallerUIField(m, uiCACertificate)
		m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		if !m.review || m.errorText != "" || m.candidate().AdminUserID != "" || strings.Contains(m.View().Content, "Admin") {
			t.Fatalf("%s must keep existing users and skip the admin page: %s", name, m.errorText)
		}
	}
}
