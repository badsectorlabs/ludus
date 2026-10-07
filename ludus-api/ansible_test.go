package ludusapi

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/apenella/go-ansible/pkg/playbook"
)

func TestAnsibleConfigDefaults(t *testing.T) {
	ansible, err := exec.LookPath("ansible-inventory")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("CI requires ansible-inventory for defaults regression coverage")
		}
		t.Skip("requires ansible-inventory")
	}

	root := t.TempDir()
	t.Setenv("ANSIBLE_HOME", filepath.Join(root, "home"))
	t.Setenv("ANSIBLE_LOCAL_TEMP", filepath.Join(root, "tmp"))
	t.Setenv("ANSIBLE_HASH_BEHAVIOUR", "replace")
	t.Setenv("ANSIBLE_NOCOLOR", "true")
	if err := os.MkdirAll(filepath.Join(root, "ansible"), 0700); err != nil {
		t.Fatal(err)
	}
	writeConfig := func(t *testing.T, path string, value map[string]interface{}) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	serverDefaults := map[string]interface{}{
		"ad_domain_admin":          "serveradmin",
		"ad_domain_admin_password": "server-password",
		"timezone":                 "America/New_York",
		"snapshot_with_RAM":        true,
		"stale_hours":              float64(7),
		"use_cert_auth":            true,
		"cert_auth_linux_user":     "root",
	}
	writeConfig(t, filepath.Join(root, "config.yml"), map[string]interface{}{"precedence": "config"})
	writeConfig(t, filepath.Join(root, "ansible", "server-config.yml"), map[string]interface{}{"defaults": serverDefaults})

	tests := []struct {
		name          string
		filename      string
		defaults      map[string]interface{}
		callerDefault bool
	}{
		{name: "timezone only", filename: "range-config.yml", defaults: map[string]interface{}{"timezone": "Europe/Zurich"}},
		{name: "temporary config", filename: ".tmp-range-config.yml", defaults: map[string]interface{}{"timezone": "Asia/Tokyo"}},
		{name: "empty defaults", filename: "range-config.yml", defaults: map[string]interface{}{}},
		{name: "defaults omitted", filename: "range-config.yml"},
		{name: "no range config"},
		{name: "false and zero", filename: "range-config.yml", defaults: map[string]interface{}{"snapshot_with_RAM": false, "stale_hours": float64(0)}},
		{name: "disable inherited cert auth", filename: "range-config.yml", defaults: map[string]interface{}{"use_cert_auth": false}},
		{name: "complete defaults", filename: "range-config.yml", defaults: map[string]interface{}{
			"ad_domain_admin":          "rangeadmin",
			"ad_domain_admin_password": "range-password",
			"timezone":                 "Europe/Zurich",
			"snapshot_with_RAM":        false,
			"stale_hours":              float64(1),
		}},
		{name: "caller file retains precedence", filename: "range-config.yml", defaults: map[string]interface{}{"timezone": "Europe/Zurich"}, callerDefault: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rangePath := ""
			wantPrecedence := "config"
			if test.filename != "" {
				rangePath = filepath.Join(root, test.filename)
				config := map[string]interface{}{"precedence": "range"}
				if test.defaults != nil {
					config["defaults"] = test.defaults
				}
				writeConfig(t, rangePath, config)
				wantPrecedence = "range"
			}
			wantDefaults := maps.Clone(serverDefaults)
			maps.Copy(wantDefaults, test.defaults)
			configs, _, err := ansibleConfigExtraVars(root, rangePath)
			if err != nil {
				t.Fatal(err)
			}
			if test.callerDefault {
				wantDefaults = map[string]interface{}{"timezone": "Pacific/Auckland"}
				callerPath := filepath.Join(root, "caller.yml")
				writeConfig(t, callerPath, map[string]interface{}{"defaults": wantDefaults})
				configs = append(configs, "@"+callerPath)
			}
			options := &playbook.AnsiblePlaybookOptions{
				Inventory:     "localhost,",
				ExtraVarsFile: configs,
				ExtraVars:     map[string]interface{}{"precedence": "inline"},
			}
			args, err := options.GenerateCommandOptions()
			if err != nil {
				t.Fatal(err)
			}
			// Inventory inspection uses Ansible's real extra-vars loader without
			// requiring a VM or worker processes to execute a playbook.
			output, err := exec.Command(ansible, append(args, "--host", "localhost")...).Output()
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					t.Fatalf("ansible-inventory: %v\n%s", err, exit.Stderr)
				}
				t.Fatal(err)
			}
			var got struct {
				Defaults   map[string]interface{} `json:"defaults"`
				Precedence string                 `json:"precedence"`
			}
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("decoding Ansible variables: %v\n%s", err, output)
			}
			if !reflect.DeepEqual(got.Defaults, wantDefaults) {
				t.Errorf("defaults visible to Ansible = %#v, want %#v", got.Defaults, wantDefaults)
			}
			if got.Precedence != wantPrecedence {
				t.Errorf("unrelated variable precedence = %q, want %q", got.Precedence, wantPrecedence)
			}
		})
	}
}

func TestRangeAnsibleRuntimeSelection(t *testing.T) {
	// An unset override is different from an explicitly empty override.
	t.Setenv("LUDUS_ANSIBLE_BINARY", "")
	if err := os.Unsetenv("LUDUS_ANSIBLE_BINARY"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "runtimes", "ansible-windows-ssh", "bin", "ansible-playbook")
	if err := os.MkdirAll(filepath.Dir(binary), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		config      string
		useCertAuth bool
		wantModern  bool
	}{
		{"password Windows", "ludus:\n  - windows: {}", false, false},
		{"Linux only", "ludus:\n  - linux: true", true, false},
		{"macOS only", "ludus:\n  - macos: true", true, false},
		{"Windows mapping", "ludus:\n  - windows:\n      sysprep: false", true, true},
		{"Windows empty key", "ludus:\n  - windows:", true, true},
		{"Windows legacy boolean", "ludus:\n  - windows: true", true, true},
		{"Windows disabled", "ludus:\n  - windows: false\n    linux: true", true, false},
		{"Windows unmanaged", "ludus:\n  - windows: {}\n    unmanaged: true", true, false},
		{"mixed range", "ludus:\n  - linux: true\n  - windows: {}\n    unmanaged: false", true, true},
		{"managed after unmanaged", "ludus:\n  - windows: {}\n    unmanaged: true\n  - windows: {}", true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "range-config.yml")
			if err := os.WriteFile(path, []byte(test.config), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := rangeAnsibleBinary(root, path, test.useCertAuth)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if test.wantModern {
				want = binary
			}
			if got != want {
				t.Fatalf("selected runtime %q, want %q", got, want)
			}
		})
	}
	if got, err := rangeAnsibleBinary(root, "", true); err != nil || got != "" {
		t.Fatalf("controller-only run selected %q, error %v", got, err)
	}
}

func TestRangeAnsibleRuntimeMissingAndOverride(t *testing.T) {
	t.Setenv("LUDUS_ANSIBLE_BINARY", "")
	if err := os.Unsetenv("LUDUS_ANSIBLE_BINARY"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "range-config.yml")
	if err := os.WriteFile(path, []byte("ludus:\n  - windows: {}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := rangeAnsibleBinary(root, path, true); err == nil {
		t.Fatal("Windows SSH silently fell back to legacy Ansible without its runtime")
	}
	if got, err := rangeAnsibleBinary(root, path, false); err != nil || got != "" {
		t.Fatalf("password mode required isolated runtime: binary %q, error %v", got, err)
	}
	t.Setenv("LUDUS_ANSIBLE_BINARY", "/custom/bin/ansible-playbook")
	if got, err := rangeAnsibleBinary(root, path, true); err != nil || got != "/custom/bin/ansible-playbook" {
		t.Fatalf("explicit binary override lost precedence: binary %q, error %v", got, err)
	}
	t.Setenv("LUDUS_ANSIBLE_BINARY", "")
	if got, err := rangeAnsibleBinary(root, path, true); err != nil || got != "" {
		t.Fatalf("explicit empty override lost precedence: binary %q, error %v", got, err)
	}
}
