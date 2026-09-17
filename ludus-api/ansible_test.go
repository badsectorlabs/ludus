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
			configs, err := ansibleConfigExtraVars(root, rangePath)
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
