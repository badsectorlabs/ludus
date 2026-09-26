package localgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWireguard(t *testing.T) {
	dir := t.TempDir()
	cfg := WGConfig{
		Dir:        dir,
		ListenPort: 51820,
		ServerIP:   "198.51.100.1/24",
	}
	if err := EnsureWireguard(cfg); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"server-private-key", "server-public-key", "wg0.conf"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("missing %s: %v", f, err)
		}
	}
	conf, _ := os.ReadFile(filepath.Join(dir, "wg0.conf"))
	if !strings.Contains(string(conf), "ListenPort = 51820") {
		t.Fatalf("port missing:\n%s", conf)
	}
	if !strings.Contains(string(conf), "Address = 198.51.100.1/24") {
		t.Fatalf("address missing:\n%s", conf)
	}
	// Idempotent: priv key unchanged on second run
	pk1, _ := os.ReadFile(filepath.Join(dir, "server-private-key"))
	_ = EnsureWireguard(cfg)
	pk2, _ := os.ReadFile(filepath.Join(dir, "server-private-key"))
	if string(pk1) != string(pk2) {
		t.Fatal("private key regenerated")
	}
	// wg0.conf must be preserved on second run (imported configs contain [Peer] blocks)
	confPath := filepath.Join(dir, "wg0.conf")
	if err := os.WriteFile(confPath, []byte("[Interface]\n# imported\n[Peer]\nPublicKey = x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = EnsureWireguard(cfg)
	conf2, _ := os.ReadFile(confPath)
	if !strings.Contains(string(conf2), "[Peer]") {
		t.Fatalf("wg0.conf was overwritten on second run:\n%s", conf2)
	}
}
