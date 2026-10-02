package ludusapi

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
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

func TestGalaxyCollectionInstallScope(t *testing.T) {
	if _, err := exec.LookPath("ansible-galaxy"); err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("CI requires ansible-galaxy for collection scope regression coverage")
		}
		t.Skip("requires ansible-galaxy")
	}

	server := httptest.NewServer(http.FileServer(http.Dir("../ludus-server/ci/fixtures")))
	t.Cleanup(server.Close)
	tests := []struct {
		name             string
		global           bool
		preinstallGlobal bool
		force            bool
		wantScopes       []string
	}{
		{name: "user", force: true, wantScopes: []string{"user"}},
		{name: "explicit global", global: true, force: true, wantScopes: []string{"global"}},
		{name: "reuse global", preinstallGlobal: true, wantScopes: []string{"global"}},
		{name: "force user copy", preinstallGlobal: true, force: true, wantScopes: []string{"global", "user"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "user", ".ansible")
			global := filepath.Join(root, "global-collections")
			t.Setenv("ANSIBLE_HOME", filepath.Join(root, "service-home"))
			t.Setenv("ANSIBLE_LOCAL_TEMP", filepath.Join(root, "tmp"))
			// The LXC service exports this global search path. It must not
			// redirect a per-user install or prevent an explicit global install.
			t.Setenv("ANSIBLE_COLLECTIONS_PATH", global)
			t.Setenv("ANSIBLE_FORCE_COLOR", "false")

			install := func(scopePath string, force bool) {
				t.Helper()
				cmd := galaxyInstallCmd(galaxyCollection, []string{"collection", "install"},
					[]string{server.URL + "/ludus_ci-http_archive-1.0.0.tar.gz?token=ci"},
					force, scopePath, home)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("collection install: %v\n%s", err, output)
				}
			}
			if test.preinstallGlobal {
				install(global, true)
			}
			scopePath := ""
			if test.global {
				scopePath = global
			}
			install(scopePath, test.force)
			var want []InstalledCollection
			for _, scope := range test.wantScopes {
				want = append(want, InstalledCollection{FQCN: "ludus_ci.http_archive", Version: "1.0.0", Scope: scope})
			}
			if got := scanInstalledCollections([]string{home}, global); !reflect.DeepEqual(got, want) {
				t.Fatalf("installed collections = %#v, want %#v", got, want)
			}
		})
	}
}

func TestPackerWinRMShellCompatibility(t *testing.T) {
	ansible, err := exec.LookPath("ansible-inventory")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("CI requires ansible-inventory for Packer WinRM compatibility coverage")
		}
		t.Skip("requires ansible-inventory")
	}
	plugins, err := filepath.Abs("../ludus-server/packer/ansible/vars_plugins")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("ANSIBLE_HOME", filepath.Join(root, "home"))
	t.Setenv("ANSIBLE_LOCAL_TEMP", filepath.Join(root, "tmp"))
	t.Setenv("ANSIBLE_VARS_PLUGINS", plugins)
	t.Setenv("ANSIBLE_VARS_ENABLED", "host_group_vars")
	t.Setenv("ANSIBLE_NOCOLOR", "true")
	t.Setenv("PYTHONDONTWRITEBYTECODE", "1")
	config := filepath.Join(root, "ansible.cfg")
	if err := os.WriteFile(config, []byte("[defaults]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANSIBLE_CONFIG", config)
	inventory := filepath.Join(root, "inventory.ini")
	// Packer generates these connection and shell variables directly on hosts.
	// Include grouped inventory and mixed transports to catch cross-host leakage.
	if err := os.WriteFile(inventory, []byte(`[all]
winrm ansible_connection=winrm ansible_shell_type=powershell
winrm_builtin ansible_connection=ansible.builtin.winrm ansible_shell_type=powershell
winrm_legacy ansible_connection=ansible.legacy.winrm ansible_shell_type=powershell
winrm_cmd ansible_connection=winrm ansible_shell_type=cmd
winrm_default ansible_connection=winrm
windows_ssh ansible_connection=ssh ansible_shell_type=powershell
linux_ssh ansible_connection=ssh ansible_shell_type=sh
psrp ansible_connection=psrp ansible_shell_type=powershell

[windows]
inherited
ssh_override ansible_connection=ssh ansible_shell_type=powershell

[windows:vars]
ansible_connection=winrm
ansible_shell_type=powershell
`), 0600); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"winrm":         "cmd",
		"winrm_builtin": "cmd",
		"winrm_legacy":  "cmd",
		"winrm_cmd":     "cmd",
		"winrm_default": "",
		"windows_ssh":   "powershell",
		"linux_ssh":     "sh",
		"psrp":          "powershell",
		"inherited":     "cmd",
		"ssh_override":  "powershell",
	}
	for _, stage := range []string{"start", "demand"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("ANSIBLE_RUN_VARS_PLUGINS", stage)
			for _, override := range []bool{false, true} {
				args := []string{"-i", inventory, "--list"}
				if override {
					args = append(args, "--extra-vars", "ansible_shell_type=powershell")
				}
				output, err := exec.Command(ansible, args...).Output()
				if err != nil {
					if exit, ok := err.(*exec.ExitError); ok {
						t.Fatalf("ansible-inventory: %v\n%s", err, exit.Stderr)
					}
					t.Fatal(err)
				}
				var got struct {
					Meta struct {
						Hostvars map[string]map[string]interface{} `json:"hostvars"`
					} `json:"_meta"`
				}
				if err := json.Unmarshal(output, &got); err != nil {
					t.Fatalf("decoding Ansible inventory: %v\n%s", err, output)
				}
				for host, want := range expected {
					if override {
						want = "powershell"
					}
					variables, ok := got.Meta.Hostvars[host]
					if !ok {
						t.Fatalf("missing inventory host %q", host)
					}
					shell, _ := variables["ansible_shell_type"].(string)
					if shell != want {
						t.Errorf("%s shell (explicit override=%v) = %q, want %q", host, override, shell, want)
					}
				}
			}
		})
	}
}
