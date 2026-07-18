package localgen

import (
	"os"
	"strings"
	"testing"
)

func TestRenderNftables(t *testing.T) {
	got := RenderNftables(NftablesConfig{NATCIDR: "192.0.2.0/24", OutIfName: "eth0"})
	for _, want := range []string{
		"table ip ludus_nat",
		"ip saddr 192.0.2.0/24 oifname \"eth0\" masquerade",
		"type nat hook postrouting priority srcnat",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWriteNftables(t *testing.T) {
	p := t.TempDir() + "/nftables.conf"
	if err := WriteNftables(p, NftablesConfig{NATCIDR: "192.0.2.0/24", OutIfName: "eth0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal(err)
	}
}
