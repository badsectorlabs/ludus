package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSSOConfigDefaultAndOptOut(t *testing.T) {
	originalConfig, originalPath := config, ludusPath
	t.Cleanup(func() {
		config, ludusPath = originalConfig, originalPath
	})
	ludusPath = t.TempDir()
	configPath := filepath.Join(ludusPath, "config.yml")

	for _, scenario := range []struct {
		name    string
		content string
		want    bool
	}{
		{"existing config without setting", "proxmox_node: test\n", true},
		{"explicit opt out", "sso_require_existing_user: false\n", false},
		{"removing opt out restores secure default", "proxmox_node: test\n", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(scenario.content), 0600); err != nil {
				t.Fatal(err)
			}
			checkConfig()
			if config.SSORequireExistingUser != scenario.want {
				t.Fatalf("SSO existing-account requirement = %v, want %v", config.SSORequireExistingUser, scenario.want)
			}

			// Rewriting an older config must not disable the default restriction,
			// and an explicit false must survive an installer save and reload.
			if err := writeConfigToFile(config, configPath); err != nil {
				t.Fatal(err)
			}
			config.SSORequireExistingUser = !scenario.want
			checkConfig()
			if config.SSORequireExistingUser != scenario.want {
				t.Fatalf("config rewrite changed the SSO policy: got %v, want %v", config.SSORequireExistingUser, scenario.want)
			}
		})
	}
}
