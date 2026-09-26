package localgen

import (
	"fmt"
	"os"
	"path/filepath"
)

type DnsmasqConfig struct {
	BindIP    string
	Gateway   string
	PoolLow   string
	PoolHigh  string
	IfName    string
	Upstreams []string
}

const dnsmasqDefaults = `# Managed by Ludus bootstrap — do not edit
CONFIG_DIR=/etc/dnsmasq.d,.dpkg-dist,.dpkg-old,.dpkg-new
IGNORE_RESOLVCONF=yes
`

func RenderDnsmasq(c DnsmasqConfig) string {
	out := fmt.Sprintf(`# Managed by Ludus bootstrap — do not edit
bind-interfaces
interface=%s
listen-address=%s
dhcp-range=%s,%s,12h
dhcp-option=option:router,%s
dhcp-option=option:dns-server,%s
`, c.IfName, c.BindIP, c.PoolLow, c.PoolHigh, c.Gateway, c.BindIP)
	if len(c.Upstreams) > 0 {
		out += "no-resolv\n"
		out += "filter-AAAA\n"
		for _, upstream := range c.Upstreams {
			out += fmt.Sprintf("server=%s\n", upstream)
		}
	}
	return out
}

func WriteDnsmasq(path string, c DnsmasqConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(RenderDnsmasq(c)), 0644)
}

func WriteDnsmasqDefaults(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(dnsmasqDefaults), 0644)
}
