package localgen

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/curve25519"
)

type WGConfig struct {
	Dir        string // e.g. /etc/wireguard
	ListenPort int
	ServerIP   string // e.g. 198.51.100.1/24
}

func EnsureWireguard(cfg WGConfig) error {
	if err := os.MkdirAll(cfg.Dir, 0700); err != nil {
		return err
	}
	privPath := filepath.Join(cfg.Dir, "server-private-key")
	pubPath := filepath.Join(cfg.Dir, "server-public-key")
	confPath := filepath.Join(cfg.Dir, "wg0.conf")

	var privB64 string
	if b, err := os.ReadFile(privPath); err == nil {
		privB64 = string(b)
	} else {
		var priv [32]byte
		if _, err := rand.Read(priv[:]); err != nil {
			return err
		}
		// Clamp per RFC 7748
		priv[0] &= 248
		priv[31] &= 127
		priv[31] |= 64
		privB64 = base64.StdEncoding.EncodeToString(priv[:])
		if err := os.WriteFile(privPath, []byte(privB64), 0600); err != nil {
			return err
		}
		pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
		if err != nil {
			return err
		}
		if err := os.WriteFile(pubPath, []byte(base64.StdEncoding.EncodeToString(pub)), 0644); err != nil {
			return err
		}
	}

	conf := fmt.Sprintf(`[Interface]
Address = %s
ListenPort = %d
PrivateKey = %s
SaveConfig = false
`, cfg.ServerIP, cfg.ListenPort, privB64)
	return os.WriteFile(confPath, []byte(conf), 0600)
}
