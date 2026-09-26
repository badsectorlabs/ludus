package localgen

import (
	"os"
	"strings"
	"testing"
)

func TestRenderDnsmasq(t *testing.T) {
	got := RenderDnsmasq(DnsmasqConfig{
		BindIP:    "192.0.2.253",
		Gateway:   "192.0.2.254",
		PoolLow:   "192.0.2.50",
		PoolHigh:  "192.0.2.100",
		IfName:    "eth1",
		Upstreams: []string{"1.1.1.1", "8.8.8.8"},
	})
	for _, want := range []string{
		"interface=eth1",
		"listen-address=192.0.2.253",
		"dhcp-range=192.0.2.50,192.0.2.100,12h",
		"dhcp-option=option:router,192.0.2.254",
		"no-resolv",
		"filter-AAAA",
		"server=1.1.1.1",
		"server=8.8.8.8",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWriteDnsmasq(t *testing.T) {
	p := t.TempDir() + "/ludus.conf"
	if err := WriteDnsmasq(p, DnsmasqConfig{BindIP: "192.0.2.253", Gateway: "192.0.2.254", PoolLow: "192.0.2.50", PoolHigh: "192.0.2.100", IfName: "eth1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}

func TestWriteDnsmasqDefaultsDisablesResolvconfOverride(t *testing.T) {
	path := t.TempDir() + "/dnsmasq"
	if err := WriteDnsmasqDefaults(path); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CONFIG_DIR=/etc/dnsmasq.d,.dpkg-dist,.dpkg-old,.dpkg-new",
		"IGNORE_RESOLVCONF=yes",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("dnsmasq defaults missing %q:\n%s", want, content)
		}
	}
}
