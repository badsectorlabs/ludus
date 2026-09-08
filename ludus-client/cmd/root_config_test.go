package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
)

func TestInitConfigResolvesConnectionSettings(t *testing.T) {
	t.Cleanup(func() {
		resetRootConfigForTest(t)
	})

	tests := []struct {
		name       string
		config     string
		envURL     string
		envProxy   string
		envVerify  string
		flags      map[string]string
		wantURL    string
		wantProxy  string
		wantVerify bool
	}{
		{
			name:      "flag defaults",
			config:    "{}\n",
			wantURL:   "https://198.51.100.1:8080",
			wantProxy: "",
		},
		{
			name:       "config file",
			config:     "url: https://config.example:8080\nproxy: http://config-proxy.example:3128\nverify: true\n",
			wantURL:    "https://config.example:8080",
			wantProxy:  "http://config-proxy.example:3128",
			wantVerify: true,
		},
		{
			name:       "environment overrides config file",
			config:     "url: https://config.example:8080\nproxy: http://config-proxy.example:3128\nverify: false\n",
			envURL:     "https://env.example:8080",
			envProxy:   "http://env-proxy.example:3128",
			envVerify:  "true",
			wantURL:    "https://env.example:8080",
			wantProxy:  "http://env-proxy.example:3128",
			wantVerify: true,
		},
		{
			name:      "flags override environment and config file",
			config:    "url: https://config.example:8080\nproxy: http://config-proxy.example:3128\nverify: true\n",
			envURL:    "https://env.example:8080",
			envProxy:  "http://env-proxy.example:3128",
			envVerify: "true",
			flags: map[string]string{
				"url":    "https://flag.example:8080",
				"proxy":  "http://flag-proxy.example:3128",
				"verify": "false",
			},
			wantURL:   "https://flag.example:8080",
			wantProxy: "http://flag-proxy.example:3128",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetRootConfigForTest(t)
			t.Setenv("LUDUS_API_KEY", "test.key")
			t.Setenv("LUDUS_URL", test.envURL)
			t.Setenv("LUDUS_PROXY", test.envProxy)
			t.Setenv("LUDUS_VERIFY", test.envVerify)

			configPath := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(configPath, []byte(test.config), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfgFile = configPath

			for name, value := range test.flags {
				if err := rootCmd.PersistentFlags().Set(name, value); err != nil {
					t.Fatalf("set --%s: %v", name, err)
				}
			}

			initConfig()

			if url != test.wantURL {
				t.Errorf("url = %q, want %q", url, test.wantURL)
			}
			if proxy != test.wantProxy {
				t.Errorf("proxy = %q, want %q", proxy, test.wantProxy)
			}
			if verify != test.wantVerify {
				t.Errorf("verify = %t, want %t", verify, test.wantVerify)
			}
		})
	}
}

func resetRootConfigForTest(t *testing.T) {
	t.Helper()

	viper.Reset()
	for _, name := range []string{"verbose", "json", "url", "proxy", "verify", "user"} {
		flag := rootCmd.PersistentFlags().Lookup(name)
		if err := flag.Value.Set(flag.DefValue); err != nil {
			t.Fatalf("reset --%s: %v", name, err)
		}
		flag.Changed = false
		if err := viper.BindPFlag(name, flag); err != nil {
			t.Fatalf("bind --%s: %v", name, err)
		}
	}

	cfgFile = ""
	apiKey = ""
	rangeID = ""
}
