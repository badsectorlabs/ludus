package ludusapi

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func loadConfigFromYAML(t *testing.T, yaml string) (Configuration, error) {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatalf("read: %v", err)
	}
	var c Configuration
	if err := v.Unmarshal(&c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c, c.ApplyShimAndValidate()
}

func TestConfig_ProxmoxURLShim(t *testing.T) {
	c, err := loadConfigFromYAML(t, `
proxmox_url: https://10.0.0.5:8006
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.ProxmoxEndpoints) != 1 || c.ProxmoxEndpoints[0] != "https://10.0.0.5:8006" {
		t.Fatalf("shim failed: %v", c.ProxmoxEndpoints)
	}
}

func TestConfig_RejectLocalhostEndpoint(t *testing.T) {
	_, err := loadConfigFromYAML(t, `
proxmox_endpoints: ["https://127.0.0.1:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
`)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("expected localhost rejection, got: %v", err)
	}
}

func TestConfig_PublicIPShimToWGEndpoint(t *testing.T) {
	c, err := loadConfigFromYAML(t, `
proxmox_endpoints: ["https://10.0.0.5:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
proxmox_public_ip: 203.0.113.9
`)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if c.WireguardEndpoint != "203.0.113.9" {
		t.Fatalf("wg endpoint shim failed: %q", c.WireguardEndpoint)
	}
}

func TestConfig_RealmDefault(t *testing.T) {
	c, _ := loadConfigFromYAML(t, `
proxmox_endpoints: ["https://10.0.0.5:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
`)
	if c.ProxmoxUserRealm != "pve" {
		t.Fatalf("expected default realm pve, got %q", c.ProxmoxUserRealm)
	}
}

func TestConfig_RejectMissingScheme(t *testing.T) {
	_, err := loadConfigFromYAML(t, `
proxmox_endpoints: ["10.0.0.5:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
`)
	if err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("expected scheme rejection, got: %v", err)
	}
}

func TestConfig_DeprecatedURLLocalhostRejected(t *testing.T) {
	_, err := loadConfigFromYAML(t, `
proxmox_url: https://127.0.0.1:8006
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
`)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("expected shimmed legacy localhost to be rejected, got: %v", err)
	}
}

func TestConfig_LudusDNSServer(t *testing.T) {
	c, err := loadConfigFromYAML(t, `
proxmox_endpoints: ["https://10.0.0.5:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
ludus_dns_server: 10.20.30.40
`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.LudusDNSServer != "10.20.30.40" {
		t.Fatalf("unexpected Ludus DNS server: %q", c.LudusDNSServer)
	}
}

func TestConfig_RejectInvalidLudusDNSServer(t *testing.T) {
	_, err := loadConfigFromYAML(t, `
proxmox_endpoints: ["https://10.0.0.5:8006"]
proxmox_token_id: root@pam!ludus
proxmox_token_secret: abc
ludus_dns_server: not-an-ip
`)
	if err == nil || !strings.Contains(err.Error(), "ludus_dns_server") {
		t.Fatalf("expected invalid Ludus DNS server rejection, got: %v", err)
	}
}
