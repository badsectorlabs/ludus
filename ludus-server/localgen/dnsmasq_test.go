package localgen

import (
	"os"
	"strings"
	"testing"
)

func TestRenderDnsmasq(t *testing.T) {
	got := RenderDnsmasq(DnsmasqConfig{
		BindIP:   "192.0.2.253",
		Gateway:  "192.0.2.254",
		PoolLow:  "192.0.2.50",
		PoolHigh: "192.0.2.100",
		IfName:   "eth1",
	})
	for _, want := range []string{
		"interface=eth1",
		"listen-address=192.0.2.253",
		"dhcp-range=192.0.2.50,192.0.2.100,12h",
		"dhcp-option=option:router,192.0.2.254",
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
