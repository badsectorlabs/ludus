package localgen

import (
	"fmt"
	"os"
	"path/filepath"
)

type DnsmasqConfig struct {
	BindIP   string
	Gateway  string
	PoolLow  string
	PoolHigh string
	IfName   string
}

func RenderDnsmasq(c DnsmasqConfig) string {
	return fmt.Sprintf(`# Managed by Ludus bootstrap — do not edit
bind-interfaces
interface=%s
listen-address=%s
dhcp-range=%s,%s,12h
dhcp-option=option:router,%s
dhcp-option=option:dns-server,%s
`, c.IfName, c.BindIP, c.PoolLow, c.PoolHigh, c.Gateway, c.BindIP)
}

func WriteDnsmasq(path string, c DnsmasqConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(RenderDnsmasq(c)), 0644)
}
