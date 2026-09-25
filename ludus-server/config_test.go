package main

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v2"
)

func TestUserProvisioningConfigDefaults(t *testing.T) {
	originalConfig, originalPath := config, ludusPath
	t.Cleanup(func() {
		config, ludusPath = originalConfig, originalPath
	})
	ludusPath = t.TempDir()
	configPath := filepath.Join(ludusPath, "config.yml")
	const platformConfig = "proxmox_endpoints: [https://10.0.0.5:8006]\nproxmox_token_id: root@pam!test\nproxmox_token_secret: test-secret\n"

	for _, scenario := range []struct {
		name      string
		content   string
		wantSSO   bool
		wantRange bool
	}{
		{"existing config without settings", "proxmox_node: test\n", true, true},
		{"SSO opt out preserves default ranges", "sso_require_existing_user: false\n", false, true},
		{"range opt out preserves SSO restriction", "create_default_range: false\n", true, false},
		{"SSO provisioning without default ranges", "sso_require_existing_user: false\ncreate_default_range: false\n", false, false},
		{"removing opt outs restores defaults", "proxmox_node: test\n", true, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := os.WriteFile(configPath, []byte(platformConfig+scenario.content), 0600); err != nil {
				t.Fatal(err)
			}
			checkConfig()
			if config.SSORequireExistingUser != scenario.wantSSO || config.CreateDefaultRange != scenario.wantRange {
				t.Fatalf("loaded policies: SSO requirement = %v, default range = %v; want %v, %v",
					config.SSORequireExistingUser, config.CreateDefaultRange, scenario.wantSSO, scenario.wantRange)
			}

			// Rewriting older configs must preserve defaults, and explicit opt outs
			// must survive a YAML round trip and reload.
			data, err := yaml.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			config.SSORequireExistingUser = !scenario.wantSSO
			config.CreateDefaultRange = !scenario.wantRange
			checkConfig()
			if config.SSORequireExistingUser != scenario.wantSSO || config.CreateDefaultRange != scenario.wantRange {
				t.Fatalf("rewritten policies: SSO requirement = %v, default range = %v; want %v, %v",
					config.SSORequireExistingUser, config.CreateDefaultRange, scenario.wantSSO, scenario.wantRange)
			}
		})
	}
}
