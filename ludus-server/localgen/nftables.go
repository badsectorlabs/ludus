package localgen

import (
	"fmt"
	"os"
	"path/filepath"
)

type NftablesConfig struct {
	NATCIDR   string
	OutIfName string
}

func RenderNftables(c NftablesConfig) string {
	return fmt.Sprintf(`# Managed by Ludus bootstrap — do not edit
flush ruleset

table inet filter {
	chain input {
		type filter hook input priority filter; policy accept;
	}

	chain forward {
		type filter hook forward priority filter; policy accept;
	}

	chain output {
		type filter hook output priority filter; policy accept;
	}
}

table ip ludus_nat {
	chain postrouting {
		type nat hook postrouting priority srcnat; policy accept;
		ip saddr %s oifname "%s" masquerade
	}
}
`, c.NATCIDR, c.OutIfName)
}

func WriteNftables(path string, c NftablesConfig) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(RenderNftables(c)), 0644)
}
