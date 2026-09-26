// Package pveclient wraps github.com/luthermonson/go-proxmox with
// multi-endpoint failover and fills gaps not covered by that library.
// It is the single owner of Proxmox HTTP interaction in Ludus.
package pveclient

import (
	"log/slog"
	"time"
)

type Config struct {
	Endpoints   []string // ordered; failover left-to-right
	TokenID     string   // e.g. "root@pam!ludus"
	TokenSecret string
	InsecureTLS bool
	Timeout     time.Duration // per-request; default 30s
	Logger      *slog.Logger  // nil → slog.Default()
}

type Version struct {
	Version string `json:"version"` // e.g. "8.2.4"
	Release string `json:"release"`
	RepoID  string `json:"repoid"`
}

type Storage struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Status  string `json:"status"` // "available" | "disabled" | ...
	Total   int64  `json:"total"`
	Used    int64  `json:"used"`
	Avail   int64  `json:"avail"`
	Content string `json:"content"`
}

type SDNZone struct {
	Zone string `json:"zone"`
	Type string `json:"type"`
}

type NodeStatus struct {
	CPU     float64  `json:"cpu"` // 0.0-1.0
	Memory  Memory   `json:"memory"`
	Uptime  int64    `json:"uptime"`
	LoadAvg []string `json:"loadavg"`
}

type Memory struct {
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
	Free  int64 `json:"free"`
}

type Token struct {
	FullTokenID string `json:"full-tokenid"` // user@realm!name
	Value       string `json:"value"`        // secret
}

// apiResp is the standard Proxmox envelope.
type apiResp[T any] struct {
	Data T `json:"data"`
}
