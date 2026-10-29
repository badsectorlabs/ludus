package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func updateTestWrite(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAnsiblePayloadUpdateReplacesDefaults(t *testing.T) {
	bundled, err := embeddedAnsbileDir.ReadFile("ansible/server-config.yml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		existing   bool
		defaults   string
		wantNotice bool
	}{
		{name: "customized", existing: true, defaults: "defaults:\n  ad_domain_admin_password: custom-secret-not-for-logs\n", wantNotice: true},
		{name: "unchanged", existing: true, defaults: string(bundled)},
		{name: "empty", existing: true, wantNotice: true},
		{name: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			filename := filepath.Join(root, "ansible/server-config.yml")
			backupFilename := filepath.Join(root, "previous-versions", "update-regression", "ansible", "server-config.yml")
			if test.existing {
				updateTestWrite(t, root, "ansible/server-config.yml", test.defaults)
			}
			updateTestWrite(t, root, "ansible/obsolete-playbook.yml", "legacy code")
			var output bytes.Buffer
			previousWriter := log.Writer()
			log.SetOutput(&output)
			t.Cleanup(func() { log.SetOutput(previousWriter) })

			if err := replaceAnsibleFiles(root, "update-regression"); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filename)
			if err != nil || !bytes.Equal(content, bundled) {
				t.Fatalf("release defaults not installed: %v", err)
			}
			if test.existing {
				backup, err := os.ReadFile(backupFilename)
				if err != nil || string(backup) != test.defaults {
					t.Fatalf("pre-update defaults not backed up intact: %v", err)
				}
				st, err := os.Stat(backupFilename)
				if err != nil || st.Mode().Perm() != 0600 {
					t.Fatal("backup changed configuration permissions")
				}
			} else if _, err := os.Stat(backupFilename); !os.IsNotExist(err) {
				t.Fatalf("unexpected defaults backup for a missing file: %v", err)
			}
			message := output.String()
			if got := strings.Contains(message, backupFilename); got != test.wantNotice {
				t.Fatalf("backup location reported = %v, want %v", got, test.wantNotice)
			}
			if test.wantNotice && !strings.Contains(message, filename) {
				t.Fatal("notification omitted the configuration restore destination")
			}
			if strings.Contains(message, "custom-secret-not-for-logs") {
				t.Fatal("notification exposed configuration values")
			}
			if _, err := os.Stat(filepath.Join(root, "ansible/obsolete-playbook.yml")); !os.IsNotExist(err) {
				t.Fatal("legacy executable payload survived replacement")
			}
		})
	}
}

func TestAnsibleDefaultsUpdateRejectsSymlinkBeforeReplacingPayload(t *testing.T) {
	root := t.TempDir()
	updateTestWrite(t, root, "private-defaults.yml", "defaults:\n  snapshot: false\n")
	updateTestWrite(t, root, "ansible/old-playbook.yml", "original payload")
	if err := os.Symlink(filepath.Join(root, "private-defaults.yml"), filepath.Join(root, "ansible/server-config.yml")); err != nil {
		t.Fatal(err)
	}
	if err := replaceAnsibleFiles(root, "rejected-update"); err == nil {
		t.Fatal("update accepted linked global configuration")
	}
	content, err := os.ReadFile(filepath.Join(root, "ansible/old-playbook.yml"))
	if err != nil || string(content) != "original payload" {
		t.Fatal("failed configuration preflight replaced existing playbooks")
	}
	content, err = os.ReadFile(filepath.Join(root, "private-defaults.yml"))
	if err != nil || string(content) != "defaults:\n  snapshot: false\n" {
		t.Fatal("failed update modified linked administrator configuration")
	}
}
