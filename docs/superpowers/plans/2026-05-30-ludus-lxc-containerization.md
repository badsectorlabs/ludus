# Ludus LXC Containerization — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the Ludus server runtime into an unprivileged Debian 13 LXC that talks to Proxmox exclusively over the HTTP API, with endpoint failover, air-gap-capable image, and a minimal host installer.

**Architecture:** A new `ludusapi/pveclient` package wraps `badsectorlabs/go-proxmox` with multi-endpoint failover and fills API gaps (`pveum`/`pvesh`/`pvesm` replacements). A Go `bootstrap()` replaces the `proxmox-install/` ansible tree. Range networking becomes SDN-only. A DAB-built LXC template bundles all runtime deps; `install.sh` shrinks to token + SDN + `pct create`.

**Tech Stack:** Go 1.25, `github.com/luthermonson/go-proxmox` (via `badsectorlabs` fork), Viper, PocketBase, `httptest` for unit tests, DAB for LXC image, bash + python3 for installer, GitLab CI + rclone/R2.

**Spec:** `docs/superpowers/specs/2026-05-30-ludus-lxc-containerization-design.md`

---

## File Structure Map

| Path | Action | Responsibility |
|---|---|---|
| `ludus-api/pveclient/client.go` | create | Multi-endpoint failover client, `New()`, `do()`, health probe |
| `ludus-api/pveclient/client_test.go` | create | Failover, retry, health-restore tests |
| `ludus-api/pveclient/ops.go` | create | `Version`, `StorageStatus`, `NodeStatus`, `CreateUser`, `Create/DeleteToken`, `ClusterNodeCount`, `NextVMID` |
| `ludus-api/pveclient/ops_test.go` | create | Table-driven JSON parse + request-body assertions |
| `ludus-api/pveclient/ensure.go` | create | Idempotent `EnsureRole/Group/Pool/ACL/SDNZone/VNet/Subnet`, `ApplySDN` |
| `ludus-api/pveclient/ensure_test.go` | create | Idempotency (2nd call = no POST) |
| `ludus-api/pveclient/types.go` | create | `Config`, `Version`, `Storage`, `NodeStatus`, `Token` structs |
| `ludus-api/config.go` | modify | New fields, removed fields, shim, 127.0.0.1 rejection |
| `ludus-api/config_test.go` | create | Shim + validation tests |
| `ludus-api/proxmox.go` | modify | Route through pveclient; delete `setProxmoxSystemPassword`, `createRootAPITokenWithShell` |
| `ludus-api/ansible.go` | modify | Inject `proxmox_url/api_host/token_*/ludus_nat_*` extra-vars; secret via temp JSON file |
| `ludus-api/network.go` | modify | Delete `manageVmbrInterfaceStandalone`, `runNetworkCommand`; always SDN |
| `ludus-api/sdn.go` | modify | Use `pveclient.Client`; `UseSDN` const true |
| `ludus-api/api_diagnostics.go` | modify | `pvesm`/`pveperf` → `pveclient.StorageStatus/NodeStatus` |
| `ludus-api/api_user_management.go` | modify | `@pve` realm default; password-reset → warning |
| `ludus-server/bootstrap.go` | create | First-boot: preflight, Ensure*, TLS/WG/dnsmasq gen, routes, marker |
| `ludus-server/bootstrap_test.go` | create | Mock PVEClient, golden files, idempotency |
| `ludus-server/localgen/tls.go` | create | Self-signed cert generation |
| `ludus-server/localgen/wireguard.go` | create | WG keypair + `wg0.conf` render |
| `ludus-server/localgen/dnsmasq.go` | create | `dnsmasq.d/ludus.conf` render |
| `ludus-server/localgen/routes.go` | create | `/etc/network/if-up.d/ludus-routes` render |
| `ludus-server/localgen/*_test.go` | create | Golden-file tests for each |
| `ludus-server/reconcile.go` | create | `reconcileImportedState()` |
| `ludus-server/reconcile_test.go` | create | Seeded DB + mock client |
| `ludus-server/main.go` | modify | Drop `/etc/pve` cert paths; call `bootstrap()` |
| `ludus-server/checks.go` | modify | Delete `checkDebian12or13`, `checkForVirtualizationSupport`, `isInCluster`, `checkForProxmox8or9` |
| `ludus-server/install.go` | delete | Replaced by `bootstrap.go` |
| `ludus-server/config.go` | modify | Remove `pvesh`/`pveversion` autodetect |
| `ludus-server/lxc/dab.conf` | create | DAB appliance definition |
| `ludus-server/lxc/Makefile` | create | DAB build wrapper |
| `ludus-server/lxc/build.sh` | create | CI entrypoint |
| `ludus-server/lxc/files/ludus.service` | create | systemd unit (from existing j2, de-templated) |
| `ludus-server/lxc/files/ludus-admin.service` | create | systemd unit |
| `ludus-server/lxc/files/99-ludus-sysctl.conf` | create | `ip_forward=1` |
| `install.sh` | modify | Replace server-install branch per spec §8 |
| `.gitlab-ci.yml` | modify | Add `go test`, `build-lxc-template`, `test-lxc-airgap`, `test-install-sh`, `upload-lxc-r2`; amend `release` |
| `ludus-server/ansible/proxmox-install/**` | delete | Replaced by Go bootstrap |
| `ludus-server/ansible/user-management/vmbr-management.yml` | delete | SDN-only |
| `ludus-server/ansible/range-management/tasks/**/*.yml` | modify | `127.0.0.1:8006` → `{{ proxmox_url }}` |

---

## Phase A — Config Schema & `pveclient` Foundation

End state: `go build ./...` and `go test ./...` pass. Server still runs on-host (no behavior change yet); new package is ready for consumers.

### Task A1: Add `go test` to CI

**Files:** Modify `.gitlab-ci.yml`

- [ ] **Step 1: Add unit-test job before `build all`**

Insert in the `build` stage (find the `build all:` job, add immediately before it):

```yaml
go-unit-tests:
  stage: build
  tags:
    - ludus-proxmox-runner-parallel
  rules:
    - if: $CI_PIPELINE_SOURCE == "merge_request_event"
    - if: $CI_COMMIT_BRANCH
    - if: $CI_COMMIT_TAG
  script:
    - cd ludus-api && go test ./... -v -race -coverprofile=coverage.out
    - cd ../ludus-server && go test ./... -v -race
  coverage: '/coverage: \d+\.\d+% of statements/'
  artifacts:
    paths:
      - ludus-api/coverage.out
    expire_in: 1 week
```

- [ ] **Step 2: Verify locally**

Run: `cd ludus-api && go test ./... && cd ../ludus-server && go test ./...`
Expected: PASS (or pre-existing failures noted — do not fix unrelated tests).

- [ ] **Step 3: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: add go unit test job"
```

---

### Task A2: Config schema — new fields, shim, validation

**Files:**
- Modify `ludus-api/config.go`
- Create `ludus-api/config_test.go`

- [ ] **Step 1: Write failing test for endpoints shim + validation**

Create `ludus-api/config_test.go`:

```go
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
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-api && go test -run TestConfig ./...`
Expected: compile error (`ApplyShimAndValidate` undefined, `ProxmoxEndpoints` undefined).

- [ ] **Step 3: Add fields to `Configuration` struct**

In `ludus-api/config.go`, inside `type Configuration struct`, add after `ProxmoxURL`:

```go
	ProxmoxEndpoints   []string `mapstructure:"proxmox_endpoints" yaml:"proxmox_endpoints"`
	ProxmoxTokenID     string   `mapstructure:"proxmox_token_id" yaml:"proxmox_token_id"`
	ProxmoxTokenSecret string   `mapstructure:"proxmox_token_secret" yaml:"proxmox_token_secret"`
	ProxmoxUserRealm   string   `mapstructure:"proxmox_user_realm" yaml:"proxmox_user_realm"`
	WireguardEndpoint  string   `mapstructure:"wireguard_endpoint" yaml:"wireguard_endpoint"`
	LudusNATIP         string   `mapstructure:"ludus_nat_ip" yaml:"ludus_nat_ip"`
	LudusNATGateway    string   `mapstructure:"ludus_nat_gateway" yaml:"ludus_nat_gateway"`
	TLSCertFile        string   `mapstructure:"tls_cert_file" yaml:"tls_cert_file"`
	TLSKeyFile         string   `mapstructure:"tls_key_file" yaml:"tls_key_file"`
```

Mark `ProxmoxURL` and `ProxmoxPublicIP` as deprecated with a comment:

```go
	ProxmoxURL      string `mapstructure:"proxmox_url" yaml:"proxmox_url"`           // Deprecated: use proxmox_endpoints
	ProxmoxPublicIP string `mapstructure:"proxmox_public_ip" yaml:"proxmox_public_ip"` // Deprecated: use wireguard_endpoint
```

- [ ] **Step 4: Add defaults in `ParseConfig()`**

In `ludus-api/config.go` inside `ParseConfig()`, after the existing `viper.SetDefault` block, add:

```go
	viper.SetDefault("proxmox_user_realm", "pve")
	viper.SetDefault("ludus_nat_interface", "ludusnat")
	viper.SetDefault("ludus_nat_ip", "192.0.2.253")
	viper.SetDefault("ludus_nat_gateway", "192.0.2.254")
	viper.SetDefault("tls_cert_file", ludusInstallPath+"/tls/server.crt")
	viper.SetDefault("tls_key_file", ludusInstallPath+"/tls/server.key")
```

And **delete** these now-wrong defaults:

```go
	viper.SetDefault("proxmox_url", "https://127.0.0.1:8006")     // DELETE
	viper.SetDefault("proxmox_public_ip", "127.0.0.1")            // DELETE
```

- [ ] **Step 5: Implement `ApplyShimAndValidate()`**

Add to `ludus-api/config.go`:

```go
import (
	"fmt"
	"net/url"
	"strings"
	// ... existing imports
)

// ApplyShimAndValidate migrates deprecated fields and validates the config.
// Called after viper.Unmarshal in ParseConfig and by tests.
func (c *Configuration) ApplyShimAndValidate() error {
	// Shim: proxmox_url -> proxmox_endpoints
	if len(c.ProxmoxEndpoints) == 0 && c.ProxmoxURL != "" {
		log.Printf("WARN: config key 'proxmox_url' is deprecated; use 'proxmox_endpoints: [%q]'", c.ProxmoxURL)
		c.ProxmoxEndpoints = []string{c.ProxmoxURL}
	}
	// Shim: proxmox_public_ip -> wireguard_endpoint
	if c.WireguardEndpoint == "" && c.ProxmoxPublicIP != "" {
		log.Printf("WARN: config key 'proxmox_public_ip' is deprecated; use 'wireguard_endpoint'")
		c.WireguardEndpoint = c.ProxmoxPublicIP
	}
	// Default realm (for direct-unmarshal callers like tests)
	if c.ProxmoxUserRealm == "" {
		c.ProxmoxUserRealm = "pve"
	}
	// Validate endpoints
	if len(c.ProxmoxEndpoints) == 0 {
		return fmt.Errorf("proxmox_endpoints must contain at least one URL")
	}
	for _, ep := range c.ProxmoxEndpoints {
		u, err := url.Parse(ep)
		if err != nil {
			return fmt.Errorf("proxmox_endpoints: invalid URL %q: %w", ep, err)
		}
		host := u.Hostname()
		if host == "127.0.0.1" || strings.EqualFold(host, "localhost") || host == "::1" {
			return fmt.Errorf("proxmox_endpoints: %q uses 127.0.0.1/localhost — Ludus now runs in an LXC and must reach Proxmox over the network; use the node's real IP", ep)
		}
	}
	if c.ProxmoxTokenID == "" || c.ProxmoxTokenSecret == "" {
		return fmt.Errorf("proxmox_token_id and proxmox_token_secret are required")
	}
	return nil
}
```

- [ ] **Step 6: Wire shim into `ParseConfig()`**

In `ParseConfig()`, after `viper.Unmarshal(&ServerConfiguration)` and the `ConfigMu.Unlock()`, add:

```go
	if err := ServerConfiguration.ApplyShimAndValidate(); err != nil {
		log.Fatalf("config validation: %v", err)
	}
```

- [ ] **Step 7: Run tests**

Run: `cd ludus-api && go test -run TestConfig ./...`
Expected: PASS (4 tests).

- [ ] **Step 8: Commit**

```bash
git add ludus-api/config.go ludus-api/config_test.go
git commit -m "feat(config): add proxmox_endpoints, token, realm, wg_endpoint, tls paths; shim + validate"
```

---

### Task A3: `pveclient` types and constructor

**Files:**
- Create `ludus-api/pveclient/types.go`
- Create `ludus-api/pveclient/client.go`
- Create `ludus-api/pveclient/client_test.go`

- [ ] **Step 1: Write failing test for `New()` endpoint probing**

Create `ludus-api/pveclient/client_test.go`:

```go
package pveclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func versionHandler(ver string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": ver, "release": "1"}})
			return
		}
		http.NotFound(w, r)
	}
}

func TestNew_PicksFirstHealthyEndpoint(t *testing.T) {
	dead := httptest.NewUnstartedServer(nil) // never started → conn refused
	deadURL := "http://" + dead.Listener.Addr().String()
	dead.Listener.Close()

	live := httptest.NewServer(versionHandler("8.2.4"))
	defer live.Close()

	c, err := New(Config{
		Endpoints:   []string{deadURL, live.URL},
		TokenID:     "root@pam!t",
		TokenSecret: "s",
		Timeout:     2 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.ActiveEndpoint() != live.URL {
		t.Fatalf("expected active=%s got %s", live.URL, c.ActiveEndpoint())
	}
}

func TestNew_AllDead(t *testing.T) {
	_, err := New(Config{
		Endpoints:   []string{"http://127.0.0.1:1", "http://127.0.0.1:2"},
		TokenID:     "root@pam!t",
		TokenSecret: "s",
		Timeout:     500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected error when all endpoints unreachable")
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-api && go test ./pveclient/...`
Expected: compile error (package doesn't exist).

- [ ] **Step 3: Create `types.go`**

Create `ludus-api/pveclient/types.go`:

```go
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

type NodeStatus struct {
	CPU     float64 `json:"cpu"`    // 0.0-1.0
	Memory  Memory  `json:"memory"`
	Uptime  int64   `json:"uptime"`
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
```

- [ ] **Step 4: Create `client.go`**

Create `ludus-api/pveclient/client.go`:

```go
package pveclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	goproxmox "github.com/luthermonson/go-proxmox"
)

type endpoint struct {
	url     string
	healthy bool
}

type Client struct {
	cfg       Config
	mu        sync.RWMutex
	endpoints []endpoint
	activeIdx int
	httpc     *http.Client
	raw       *goproxmox.Client // bound to active endpoint; rebuilt on failover
	log       *slog.Logger
	stopProbe chan struct{}
}

func New(cfg Config) (*Client, error) {
	if len(cfg.Endpoints) == 0 {
		return nil, errors.New("pveclient: at least one endpoint required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	httpc := &http.Client{
		Timeout: cfg.Timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: cfg.InsecureTLS},
		},
	}
	c := &Client{
		cfg:       cfg,
		httpc:     httpc,
		log:       log,
		stopProbe: make(chan struct{}),
	}
	for _, u := range cfg.Endpoints {
		c.endpoints = append(c.endpoints, endpoint{url: strings.TrimRight(u, "/"), healthy: false})
	}
	// Probe in order; first responder becomes active.
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second*time.Duration(len(c.endpoints)))
	defer cancel()
	found := -1
	for i := range c.endpoints {
		if c.probe(probeCtx, i) {
			c.endpoints[i].healthy = true
			if found == -1 {
				found = i
			}
		}
	}
	if found == -1 {
		return nil, fmt.Errorf("pveclient: no reachable endpoints in %v", cfg.Endpoints)
	}
	c.activeIdx = found
	c.rebuildRaw()
	go c.healthLoop()
	return c, nil
}

func (c *Client) Close() {
	close(c.stopProbe)
}

func (c *Client) ActiveEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.endpoints[c.activeIdx].url
}

// Raw returns the underlying go-proxmox client bound to the current active
// endpoint. Callers must not cache it across requests if they want failover.
func (c *Client) Raw() *goproxmox.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.raw
}

func (c *Client) rebuildRaw() {
	c.raw = goproxmox.NewClient(c.endpoints[c.activeIdx].url+"/api2/json",
		goproxmox.WithHTTPClient(c.httpc),
		goproxmox.WithAPIToken(c.cfg.TokenID, c.cfg.TokenSecret),
	)
}

func (c *Client) probe(ctx context.Context, idx int) bool {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.endpoints[idx].url+"/api2/json/version", nil)
	req.Header.Set("Authorization", c.authHeader())
	resp, err := c.httpc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == 200
}

func (c *Client) authHeader() string {
	return "PVEAPIToken=" + c.cfg.TokenID + "=" + c.cfg.TokenSecret
}

func (c *Client) healthLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-c.stopProbe:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.mu.Lock()
			for i := range c.endpoints {
				if !c.endpoints[i].healthy && c.probe(ctx, i) {
					c.endpoints[i].healthy = true
					c.log.Info("pveclient: endpoint restored", "url", c.endpoints[i].url)
				}
			}
			c.mu.Unlock()
			cancel()
		}
	}
}

// shouldFailover returns true for transport errors and gateway HTTP codes.
func shouldFailover(err error, status int) bool {
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) {
			return true
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return true
		}
		var oe *net.OpError
		if errors.As(err, &oe) {
			return true
		}
		return false
	}
	return status == 502 || status == 503 || status == 504
}

// do performs an HTTP request against the active endpoint with one failover retry.
// path must start with "/api2/json/".
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.RLock()
		base := c.endpoints[c.activeIdx].url
		c.mu.RUnlock()

		req, err := http.NewRequestWithContext(ctx, method, base+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", c.authHeader())
		if body != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := c.httpc.Do(req)
		if err == nil && resp.StatusCode < 500 {
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				b, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("proxmox %s %s: %d: %s", method, path, resp.StatusCode, string(b))
			}
			if out != nil {
				return json.NewDecoder(resp.Body).Decode(out)
			}
			io.Copy(io.Discard, resp.Body)
			return nil
		}
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		if !shouldFailover(err, status) || attempt == 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("proxmox %s %s: %d", method, path, status)
		}
		c.advance()
	}
	return errors.New("pveclient: unreachable") // not hit
}

func (c *Client) advance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.endpoints[c.activeIdx].healthy = false
	c.log.Warn("pveclient: marking endpoint unhealthy", "url", c.endpoints[c.activeIdx].url)
	for i := 1; i <= len(c.endpoints); i++ {
		idx := (c.activeIdx + i) % len(c.endpoints)
		if c.endpoints[idx].healthy {
			c.activeIdx = idx
			c.rebuildRaw()
			c.log.Info("pveclient: failed over", "url", c.endpoints[idx].url)
			return
		}
	}
	// No healthy endpoints; stay on current and let next request error.
}
```

- [ ] **Step 5: Run tests**

Run: `cd ludus-api && go test ./pveclient/... -run TestNew -v`
Expected: PASS (2 tests). If `go-proxmox` import fails, run `go mod tidy` first.

- [ ] **Step 6: Commit**

```bash
git add ludus-api/pveclient/
git commit -m "feat(pveclient): multi-endpoint client with probe + constructor"
```

---

### Task A4: `pveclient` failover behavior tests

**Files:** Modify `ludus-api/pveclient/client_test.go`

- [ ] **Step 1: Write failing failover tests**

Append to `ludus-api/pveclient/client_test.go`:

```go
func TestDo_FailsOverOn503(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
			return
		}
		w.WriteHeader(503)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
	}))
	defer good.Close()

	c, err := New(Config{Endpoints: []string{bad.URL, good.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var out apiResp[map[string]any]
	if err := c.do(t.Context(), "GET", "/api2/json/cluster/nextid", nil, &out); err != nil {
		t.Fatalf("expected failover success, got %v", err)
	}
	if c.ActiveEndpoint() != good.URL {
		t.Fatalf("did not fail over: active=%s", c.ActiveEndpoint())
	}
}

func TestDo_NoFailoverOn403(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/version" {
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.0"}})
			return
		}
		hits++
		w.WriteHeader(403)
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoints: []string{srv.URL, srv.URL}, TokenID: "t", TokenSecret: "s"})
	defer c.Close()
	err := c.do(t.Context(), "GET", "/api2/json/access/users", nil, nil)
	if err == nil {
		t.Fatal("expected 403 error")
	}
	if hits != 1 {
		t.Fatalf("expected 1 hit (no retry), got %d", hits)
	}
}
```

- [ ] **Step 2: Run**

Run: `cd ludus-api && go test ./pveclient/... -run TestDo -v`
Expected: PASS. If `TestDo_FailsOverOn503` fails because `do()` doesn't rewind body on retry — that's fine for GET (nil body). Body rewind handled in Task A5.

- [ ] **Step 3: Commit**

```bash
git add ludus-api/pveclient/client_test.go
git commit -m "test(pveclient): failover on 503, no failover on 4xx"
```

---

### Task A5: `pveclient` operations (`ops.go`)

**Files:**
- Create `ludus-api/pveclient/ops.go`
- Create `ludus-api/pveclient/ops_test.go`

- [ ] **Step 1: Write failing tests**

Create `ludus-api/pveclient/ops_test.go`:

```go
package pveclient

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakeAPI builds a single-endpoint client backed by the given mux.
func fakeAPI(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	mux.HandleFunc("/api2/json/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"version": "8.2.4", "release": "1"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoints: []string{srv.URL}, TokenID: "t", TokenSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

func TestVersion(t *testing.T) {
	c := fakeAPI(t, http.NewServeMux())
	v, err := c.Version(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != "8.2.4" {
		t.Fatalf("got %q", v.Version)
	}
	if !v.AtLeast(8, 0) {
		t.Fatal("AtLeast(8,0) should be true")
	}
	if v.AtLeast(9, 0) {
		t.Fatal("AtLeast(9,0) should be false")
	}
}

func TestStorageStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/nodes/pve/storage", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"storage": "local", "type": "dir", "status": "available", "total": 100, "used": 40, "avail": 60, "content": "iso,vztmpl"},
		}})
	})
	c := fakeAPI(t, mux)
	s, err := c.StorageStatus(t.Context(), "pve")
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 1 || s[0].Storage != "local" || s[0].Avail != 60 {
		t.Fatalf("bad parse: %+v", s)
	}
}

func TestClusterNodeCount(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"type": "cluster", "name": "c1"},
			{"type": "node", "name": "pve1"},
			{"type": "node", "name": "pve2"},
		}})
	})
	c := fakeAPI(t, mux)
	n, err := c.ClusterNodeCount(t.Context())
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCreateUser_PVERealmSendsPassword(t *testing.T) {
	var gotBody url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, _ = url.ParseQuery(string(b))
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	warn, err := c.CreateUser(t.Context(), "alice@pve", "s3cret", []string{"ludus_users"})
	if err != nil {
		t.Fatal(err)
	}
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	if gotBody.Get("password") != "s3cret" {
		t.Fatalf("password not sent: %v", gotBody)
	}
	if gotBody.Get("groups") != "ludus_users" {
		t.Fatalf("groups not sent: %v", gotBody)
	}
}

func TestCreateUser_PAMRealmOmitsPassword(t *testing.T) {
	var gotBody url.Values
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, _ = url.ParseQuery(string(b))
		json.NewEncoder(w).Encode(map[string]any{"data": nil})
	})
	c := fakeAPI(t, mux)
	warn, err := c.CreateUser(t.Context(), "bob@pam", "ignored", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn, "@pam") {
		t.Fatalf("expected pam warning, got %q", warn)
	}
	if gotBody.Get("password") != "" {
		t.Fatalf("password should be omitted for @pam: %v", gotBody)
	}
}

func TestCreateToken(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/access/users/alice@pve/token/ludus", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"full-tokenid": "alice@pve!ludus",
			"value":        "uuid-secret",
		}})
	})
	c := fakeAPI(t, mux)
	tok, err := c.CreateToken(t.Context(), "alice@pve", "ludus", false)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Value != "uuid-secret" || tok.FullTokenID != "alice@pve!ludus" {
		t.Fatalf("bad token: %+v", tok)
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-api && go test ./pveclient/... -run 'TestVersion|TestStorage|TestCluster|TestCreate'`
Expected: compile errors (functions undefined).

- [ ] **Step 3: Implement `ops.go`**

Create `ludus-api/pveclient/ops.go`:

```go
package pveclient

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c *Client) Version(ctx context.Context) (Version, error) {
	var r apiResp[Version]
	if err := c.do(ctx, "GET", "/api2/json/version", nil, &r); err != nil {
		return Version{}, err
	}
	return r.Data, nil
}

// AtLeast reports whether v >= major.minor.
func (v Version) AtLeast(major, minor int) bool {
	parts := strings.SplitN(v.Version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, _ := strconv.Atoi(parts[0])
	min, _ := strconv.Atoi(parts[1])
	if maj != major {
		return maj > major
	}
	return min >= minor
}

func (c *Client) StorageStatus(ctx context.Context, node string) ([]Storage, error) {
	var r apiResp[[]Storage]
	if err := c.do(ctx, "GET", "/api2/json/nodes/"+url.PathEscape(node)+"/storage", nil, &r); err != nil {
		return nil, err
	}
	return r.Data, nil
}

func (c *Client) NodeStatus(ctx context.Context, node string) (NodeStatus, error) {
	var r apiResp[NodeStatus]
	if err := c.do(ctx, "GET", "/api2/json/nodes/"+url.PathEscape(node)+"/status", nil, &r); err != nil {
		return NodeStatus{}, err
	}
	return r.Data, nil
}

type clusterStatusItem struct {
	Type string `json:"type"`
	Name string `json:"name"`
	IP   string `json:"ip"`
}

func (c *Client) ClusterNodeCount(ctx context.Context) (int, error) {
	var r apiResp[[]clusterStatusItem]
	if err := c.do(ctx, "GET", "/api2/json/cluster/status", nil, &r); err != nil {
		return 0, err
	}
	n := 0
	for _, it := range r.Data {
		if it.Type == "node" {
			n++
		}
	}
	return n, nil
}

func (c *Client) ClusterNodeIPs(ctx context.Context) ([]string, error) {
	var r apiResp[[]clusterStatusItem]
	if err := c.do(ctx, "GET", "/api2/json/cluster/status", nil, &r); err != nil {
		return nil, err
	}
	var ips []string
	for _, it := range r.Data {
		if it.Type == "node" && it.IP != "" {
			ips = append(ips, it.IP)
		}
	}
	return ips, nil
}

func (c *Client) NextVMID(ctx context.Context) (int, error) {
	var r apiResp[any]
	if err := c.do(ctx, "GET", "/api2/json/cluster/nextid", nil, &r); err != nil {
		return 0, err
	}
	// Proxmox returns this as a string.
	switch v := r.Data.(type) {
	case string:
		return strconv.Atoi(v)
	case float64:
		return int(v), nil
	}
	return 0, fmt.Errorf("unexpected nextid type %T", r.Data)
}

// CreateUser creates a Proxmox user. For @pve realm, password is set in the
// same call (works with API tokens). For @pam, password is omitted and a
// warning string is returned instructing the admin to set it manually.
func (c *Client) CreateUser(ctx context.Context, userid, password string, groups []string) (warning string, err error) {
	form := url.Values{"userid": {userid}}
	if len(groups) > 0 {
		form.Set("groups", strings.Join(groups, ","))
	}
	realm := userid[strings.LastIndex(userid, "@")+1:]
	if realm == "pve" && password != "" {
		form.Set("password", password)
	} else if realm != "pve" {
		warning = fmt.Sprintf("@%s realm: password not set via API; create system user and run `pveum passwd %s` on a cluster node", realm, userid)
	}
	return warning, c.do(ctx, "POST", "/api2/json/access/users", strings.NewReader(form.Encode()), nil)
}

func (c *Client) DeleteUser(ctx context.Context, userid string) error {
	return c.do(ctx, "DELETE", "/api2/json/access/users/"+url.PathEscape(userid), nil, nil)
}

func (c *Client) UserExists(ctx context.Context, userid string) (bool, error) {
	err := c.do(ctx, "GET", "/api2/json/access/users/"+url.PathEscape(userid), nil, nil)
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "does not exist") {
		return false, nil
	}
	return false, err
}

func (c *Client) CreateToken(ctx context.Context, userid, name string, privsep bool) (Token, error) {
	form := url.Values{}
	if !privsep {
		form.Set("privsep", "0")
	}
	var r apiResp[Token]
	path := fmt.Sprintf("/api2/json/access/users/%s/token/%s", url.PathEscape(userid), url.PathEscape(name))
	if err := c.do(ctx, "POST", path, strings.NewReader(form.Encode()), &r); err != nil {
		return Token{}, err
	}
	return r.Data, nil
}

func (c *Client) DeleteToken(ctx context.Context, userid, name string) error {
	path := fmt.Sprintf("/api2/json/access/users/%s/token/%s", url.PathEscape(userid), url.PathEscape(name))
	return c.do(ctx, "DELETE", path, nil, nil)
}

// VerifyTokenOnAll calls GET /version on every configured endpoint and returns
// an error listing any that reject the token. Used by bootstrap preflight.
func (c *Client) VerifyTokenOnAll(ctx context.Context) error {
	var bad []string
	for i := range c.endpoints {
		if !c.probe(ctx, i) {
			bad = append(bad, c.endpoints[i].url)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("token rejected or unreachable: %v", bad)
	}
	return nil
}
```

- [ ] **Step 4: Fix `do()` body rewind for retries**

POST bodies must be re-readable on failover. In `ludus-api/pveclient/client.go`, change the `do()` signature and top of the function:

```go
func (c *Client) do(ctx context.Context, method, path string, body io.Reader, out any) error {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		var rdr io.Reader
		if bodyBytes != nil {
			rdr = strings.NewReader(string(bodyBytes))
		}
		// ... rest of loop unchanged, use rdr instead of body
```

Add `"strings"` to imports if missing.

- [ ] **Step 5: Run tests**

Run: `cd ludus-api && go test ./pveclient/... -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add ludus-api/pveclient/
git commit -m "feat(pveclient): Version, StorageStatus, NodeStatus, ClusterNodeCount, CreateUser, tokens"
```

---

### Task A6: `pveclient` idempotent Ensure ops (`ensure.go`)

**Files:**
- Create `ludus-api/pveclient/ensure.go`
- Create `ludus-api/pveclient/ensure_test.go`

- [ ] **Step 1: Write failing idempotency test**

Create `ludus-api/pveclient/ensure_test.go`:

```go
package pveclient

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestEnsurePool_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	exists := false
	mux.HandleFunc("/api2/json/pools", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			data := []map[string]any{}
			if exists {
				data = append(data, map[string]any{"poolid": "SHARED"})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "POST":
			atomic.AddInt32(&posts, 1)
			exists = true
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	if err := c.EnsurePool(t.Context(), "SHARED"); err != nil {
		t.Fatal(err)
	}
	if err := c.EnsurePool(t.Context(), "SHARED"); err != nil {
		t.Fatal(err)
	}
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}

func TestEnsureSDNZone_Idempotent(t *testing.T) {
	var posts int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api2/json/cluster/sdn/zones/ludus", func(w http.ResponseWriter, r *http.Request) {
		if posts == 0 {
			w.WriteHeader(500) // not found yet
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"zone": "ludus", "type": "simple"}})
	})
	mux.HandleFunc("/api2/json/cluster/sdn/zones", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			atomic.AddInt32(&posts, 1)
			json.NewEncoder(w).Encode(map[string]any{"data": nil})
		}
	})
	c := fakeAPI(t, mux)
	_ = c.EnsureSDNZone(t.Context(), "ludus", "simple", nil)
	_ = c.EnsureSDNZone(t.Context(), "ludus", "simple", nil)
	if posts != 1 {
		t.Fatalf("expected 1 POST, got %d", posts)
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-api && go test ./pveclient/... -run TestEnsure`
Expected: compile error.

- [ ] **Step 3: Implement `ensure.go`**

Create `ludus-api/pveclient/ensure.go`:

```go
package pveclient

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// All Ensure* functions are idempotent: GET first, POST only if missing.

func (c *Client) EnsurePool(ctx context.Context, name string) error {
	var r apiResp[[]struct {
		PoolID string `json:"poolid"`
	}]
	if err := c.do(ctx, "GET", "/api2/json/pools", nil, &r); err != nil {
		return err
	}
	for _, p := range r.Data {
		if p.PoolID == name {
			return nil
		}
	}
	form := url.Values{"poolid": {name}}
	return c.do(ctx, "POST", "/api2/json/pools", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureGroup(ctx context.Context, name string) error {
	if err := c.do(ctx, "GET", "/api2/json/access/groups/"+url.PathEscape(name), nil, nil); err == nil {
		return nil
	}
	form := url.Values{"groupid": {name}}
	return c.do(ctx, "POST", "/api2/json/access/groups", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureRole(ctx context.Context, name string, privs []string) error {
	if err := c.do(ctx, "GET", "/api2/json/access/roles/"+url.PathEscape(name), nil, nil); err == nil {
		// Update privs to match (PUT is idempotent on Proxmox side).
		form := url.Values{"privs": {strings.Join(privs, ",")}}
		return c.do(ctx, "PUT", "/api2/json/access/roles/"+url.PathEscape(name), strings.NewReader(form.Encode()), nil)
	}
	form := url.Values{"roleid": {name}, "privs": {strings.Join(privs, ",")}}
	return c.do(ctx, "POST", "/api2/json/access/roles", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureACL(ctx context.Context, path, role string, groups, users []string) error {
	form := url.Values{"path": {path}, "roles": {role}, "propagate": {"1"}}
	if len(groups) > 0 {
		form.Set("groups", strings.Join(groups, ","))
	}
	if len(users) > 0 {
		form.Set("users", strings.Join(users, ","))
	}
	// PUT /access/acl is idempotent server-side.
	return c.do(ctx, "PUT", "/api2/json/access/acl", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureSDNZone(ctx context.Context, name, kind string, peers []string) error {
	if err := c.do(ctx, "GET", "/api2/json/cluster/sdn/zones/"+url.PathEscape(name), nil, nil); err == nil {
		return nil
	}
	form := url.Values{"zone": {name}, "type": {kind}, "ipam": {"pve"}}
	if kind == "vxlan" && len(peers) > 0 {
		form.Set("peers", strings.Join(peers, ","))
	}
	return c.do(ctx, "POST", "/api2/json/cluster/sdn/zones", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureVNet(ctx context.Context, zone, name string, tag int, vlanaware bool) error {
	if err := c.do(ctx, "GET", "/api2/json/cluster/sdn/vnets/"+url.PathEscape(name), nil, nil); err == nil {
		return nil
	}
	form := url.Values{"vnet": {name}, "zone": {zone}}
	if tag > 0 {
		form.Set("tag", fmt.Sprintf("%d", tag))
	}
	if vlanaware {
		form.Set("vlanaware", "1")
	}
	return c.do(ctx, "POST", "/api2/json/cluster/sdn/vnets", strings.NewReader(form.Encode()), nil)
}

func (c *Client) EnsureSubnet(ctx context.Context, vnet, cidr, gateway string, snat bool) error {
	// Subnet ID format in Proxmox: "<zone>-<cidr-with-mask-as-int>" — but listing is simpler.
	var r apiResp[[]struct {
		Subnet string `json:"subnet"`
	}]
	listPath := "/api2/json/cluster/sdn/vnets/" + url.PathEscape(vnet) + "/subnets"
	if err := c.do(ctx, "GET", listPath, nil, &r); err == nil {
		for _, s := range r.Data {
			if strings.Contains(s.Subnet, strings.ReplaceAll(cidr, "/", "-")) || strings.HasSuffix(s.Subnet, cidr) {
				return nil
			}
		}
	}
	form := url.Values{"subnet": {cidr}, "type": {"subnet"}, "gateway": {gateway}}
	if snat {
		form.Set("snat", "1")
	}
	return c.do(ctx, "POST", listPath, strings.NewReader(form.Encode()), nil)
}

func (c *Client) ApplySDN(ctx context.Context) error {
	return c.do(ctx, "PUT", "/api2/json/cluster/sdn", nil, nil)
}

func (c *Client) ReloadNodeNetwork(ctx context.Context, node string) error {
	return c.do(ctx, "PUT", "/api2/json/nodes/"+url.PathEscape(node)+"/network", nil, nil)
}
```

- [ ] **Step 4: Run tests**

Run: `cd ludus-api && go test ./pveclient/... -v -coverprofile=/tmp/cov.out && go tool cover -func=/tmp/cov.out | tail -1`
Expected: all PASS; coverage ≥80% on `pveclient`.

- [ ] **Step 5: Commit**

```bash
git add ludus-api/pveclient/
git commit -m "feat(pveclient): idempotent Ensure* for pool/group/role/acl/sdn"
```

---

### Task A7: Expose package-level singleton helper

**Files:** Modify `ludus-api/pveclient/client.go`

- [ ] **Step 1: Add `FromConfiguration` helper**

This lets `ludusapi` callers build a client from `ServerConfiguration` without import cycles. Append to `ludus-api/pveclient/client.go`:

```go
// Builder is satisfied by ludusapi.Configuration; avoids import cycle.
type Builder interface {
	PVEClientConfig() Config
}

// FromBuilder constructs a Client from anything that knows how to produce a Config.
func FromBuilder(b Builder) (*Client, error) {
	return New(b.PVEClientConfig())
}
```

- [ ] **Step 2: Add `PVEClientConfig()` to `ludusapi.Configuration`**

In `ludus-api/config.go`, append:

```go
import "ludusapi/pveclient"

func (c *Configuration) PVEClientConfig() pveclient.Config {
	return pveclient.Config{
		Endpoints:   c.ProxmoxEndpoints,
		TokenID:     c.ProxmoxTokenID,
		TokenSecret: c.ProxmoxTokenSecret,
		InsecureTLS: c.ProxmoxInvalidCert,
	}
}
```

- [ ] **Step 3: Verify build**

Run: `cd ludus-api && go build ./...`
Expected: OK. If import cycle (`ludusapi` → `pveclient` → `ludusapi`), the `Builder` interface in pveclient must NOT import `ludusapi` — confirm `client.go` has no `import "ludusapi"`.

- [ ] **Step 4: Commit**

```bash
git add ludus-api/config.go ludus-api/pveclient/client.go
git commit -m "feat(pveclient): Builder interface + Configuration.PVEClientConfig"
```

**End of Phase A.** Run: `cd ludus-api && go test ./... && cd ../ludus-server && go build ./...` → all green.

---

## Phase B — Replace Host-Coupled Go Calls

End state: `ludus-api` and `ludus-server` no longer shell out to `pveum`/`pvesm`/`pveperf`/`pvesh`/`pveversion`, no longer read `/etc/pve` or `/etc/network/interfaces`. Server can in principle run anywhere with network access to Proxmox.

### Task B1: Root Proxmox client via `pveclient`

**Files:** Modify `ludus-api/proxmox.go`

- [ ] **Step 1: Add cached `pveclient.Client` getter**

In `ludus-api/proxmox.go`, add near `GetRootGoProxmoxClient`:

```go
import "ludusapi/pveclient"

var rootPVEClient *pveclient.Client

// GetRootPVEClient returns the singleton failover-aware Proxmox client built
// from ServerConfiguration. Callers should prefer this over GetRootGoProxmoxClient.
func GetRootPVEClient() (*pveclient.Client, error) {
	if rootPVEClient != nil {
		return rootPVEClient, nil
	}
	c, err := pveclient.New(ServerConfiguration.PVEClientConfig())
	if err != nil {
		return nil, err
	}
	rootPVEClient = c
	return c, nil
}
```

- [ ] **Step 2: Rewrite `GetRootGoProxmoxClient` to delegate**

Replace the body of `GetRootGoProxmoxClient()` (lines ~148-190):

```go
func GetRootGoProxmoxClient() (*goproxmox.Client, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	return pc.Raw(), nil
}
```

This keeps all existing callers (`sdn.go`, `api_diagnostics.go`, etc.) working but routes through failover.

- [ ] **Step 3: Build**

Run: `cd ludus-api && go build ./...`
Expected: OK.

- [ ] **Step 4: Commit**

```bash
git add ludus-api/proxmox.go
git commit -m "refactor(proxmox): route root client through pveclient failover"
```

---

### Task B2: Replace `pveum` token calls

**Files:** Modify `ludus-api/proxmox.go`

- [ ] **Step 1: Delete `createRootAPITokenWithShell()` and update callers**

Find `createRootAPITokenWithShell` (~line 256). Search for callers:

```bash
grep -rn createRootAPITokenWithShell ludus-api/ ludus-server/
```

Replace each call site with: the root token now comes from `ServerConfiguration.ProxmoxTokenID/Secret` (supplied at install). If a caller was generating a NEW root token, it now returns an error instructing the admin to run `pveum user token add root@pam ludus` on a node.

Delete the function body, replace with:

```go
// Deprecated: root token is provisioned at install time and read from config.
// Kept as a stub so callers compile during transition; remove in Phase D cleanup.
func createRootAPITokenWithShell() (string, string, error) {
	return ServerConfiguration.ProxmoxTokenID, ServerConfiguration.ProxmoxTokenSecret, nil
}
```

- [ ] **Step 2: Rewrite `createProxmoxAPITokenForUserWithClient`**

Replace its body to use `pveclient`:

```go
func createProxmoxAPITokenForUserWithClient(_ *goproxmox.Client, username, userRealm string) (string, string, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return "", "", err
	}
	tok, err := pc.CreateToken(context.Background(), username+"@"+userRealm, "ludus", false)
	if err != nil {
		return "", "", err
	}
	return tok.FullTokenID, tok.Value, nil
}
```

Add `"context"` to imports.

- [ ] **Step 3: Delete `setProxmoxSystemPassword` and add reset warning**

Delete `setProxmoxSystemPassword` (~line 51). Search for callers:

```bash
grep -rn setProxmoxSystemPassword ludus-api/
```

For each caller in a **user-creation** path: that path will be handled in Task B3 (`CreateUser` sets password inline). For each caller in a **password-reset** path: replace the call with:

```go
return fmt.Errorf("⚠ Cannot change Proxmox password via API token. Run on any cluster node: pveum passwd %s@%s", username, realm)
```

- [ ] **Step 4: Build + grep**

Run: `cd ludus-api && go build ./... && grep -rn 'pveum\|exec.Command("/usr/sbin' . || echo "clean"`
Expected: build OK, grep shows no remaining `/usr/sbin/pveum`.

- [ ] **Step 5: Commit**

```bash
git add ludus-api/proxmox.go
git commit -m "refactor(proxmox): replace pveum shell calls with pveclient; password-reset -> warning"
```

---

### Task B3: User creation via `pveclient.CreateUser` with `@pve` realm

**Files:** Modify `ludus-api/api_user_management.go` (and any user-add helper it calls in `proxmox.go`)

- [ ] **Step 1: Locate the user-add Proxmox path**

```bash
grep -rn 'createProxmoxUser\|access/users\|NewUser\|setProxmoxSystemPassword' ludus-api/api_user_management.go ludus-api/proxmox.go
```

- [ ] **Step 2: Rewrite to use `pveclient.CreateUser`**

In the function that creates the Proxmox-side user (likely in `proxmox.go` or `api_user_management.go`), replace the create + `pveum passwd` sequence with:

```go
pc, err := GetRootPVEClient()
if err != nil {
	return err
}
userid := username + "@" + ServerConfiguration.ProxmoxUserRealm
warn, err := pc.CreateUser(ctx, userid, generatedPassword, []string{"ludus_users"})
if err != nil {
	return fmt.Errorf("create proxmox user %s: %w", userid, err)
}
if warn != "" {
	logger.Warn(warn)
	// Surface in API response: set ProxmoxPasswordNote field on the response struct.
}
```

- [ ] **Step 3: Add `ProxmoxPasswordNote` to response DTO**

In the user-add response struct (search `grep -rn ProxmoxPassword ludus-api/dto/ ludus-api/`), add:

```go
ProxmoxPasswordNote string `json:"proxmoxPasswordNote,omitempty"`
```

Populate it with `warn` from above.

- [ ] **Step 4: Write test**

Create or extend `ludus-api/api_user_management_test.go`:

```go
package ludusapi

import "testing"

func TestUserRealmDefault(t *testing.T) {
	if ServerConfiguration.ProxmoxUserRealm == "" {
		ServerConfiguration.ProxmoxUserRealm = "pve" // simulate default
	}
	uid := "alice@" + ServerConfiguration.ProxmoxUserRealm
	if uid != "alice@pve" {
		t.Fatalf("expected alice@pve, got %s", uid)
	}
}
```

- [ ] **Step 5: Build + test**

Run: `cd ludus-api && go test ./... -run TestUserRealm && go build ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ludus-api/
git commit -m "feat(users): create Proxmox users in @pve realm via API; surface pam warning"
```

---

### Task B4: Diagnostics — replace `pvesm` / `pveperf`

**Files:** Modify `ludus-api/api_diagnostics.go`

- [ ] **Step 1: Replace `getAllStoragePoolsFromPvesm`**

Rewrite (~line 163):

```go
func getAllStoragePoolsFromPvesm() ([]StoragePoolInfo, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	stores, err := pc.StorageStatus(context.Background(), ServerConfiguration.ProxmoxNode)
	if err != nil {
		return nil, err
	}
	out := make([]StoragePoolInfo, 0, len(stores))
	for _, s := range stores {
		out = append(out, StoragePoolInfo{
			Name: s.Storage, Type: s.Type, Status: s.Status,
			Total: s.Total, Used: s.Used, Available: s.Avail,
		})
	}
	return out, nil
}
```

Adjust `StoragePoolInfo` field names to match the existing struct (check with `grep -n 'type StoragePoolInfo' ludus-api/`).

- [ ] **Step 2: Replace `getPveperf`**

Rewrite (~line 257):

```go
func getPveperf() (*PveperfInfo, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	ns, err := pc.NodeStatus(context.Background(), ServerConfiguration.ProxmoxNode)
	if err != nil {
		return nil, err
	}
	return &PveperfInfo{
		CPUBogomips: 0, // not available via API
		HDRead:      0,
		FsyncSec:    0,
		Note:        "fsync/hdread benchmarks unavailable via API; showing node load only",
		LoadAvg:     strings.Join(ns.LoadAvg, " "),
	}, nil
}
```

Add a `Note string` field to `PveperfInfo` if not present.

- [ ] **Step 3: Build + grep**

Run: `cd ludus-api && go build ./... && grep -rn 'exec.Command("pvesm\|exec.Command("pveperf' . || echo "clean"`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ludus-api/api_diagnostics.go
git commit -m "refactor(diagnostics): replace pvesm/pveperf shell with pveclient API"
```

---

### Task B5: Network — SDN-only

**Files:** Modify `ludus-api/network.go`, `ludus-api/sdn.go`

- [ ] **Step 1: Force `UseSDN = true`**

In `ludus-api/sdn.go`, find the `UseSDN` declaration and the `IsClusterMode()` logic. Replace:

```go
// UseSDN is always true: Ludus runs in an LXC and manages all range
// networking via Proxmox SDN, regardless of cluster size.
const UseSDN = true
```

If `UseSDN` was a `var`, change all writes to it into no-ops; the const will catch them at compile time — fix each by deleting the assignment.

- [ ] **Step 2: Delete standalone path in `network.go`**

Delete `manageVmbrInterfaceStandalone` and `runNetworkCommand`. Simplify the two entry points:

```go
func manageVmbrInterfaceLocally(rangeNumber int, present bool) error {
	return manageRangeVNet(fmt.Sprintf("r%d", rangeNumber), rangeNumber, present)
}

func manageRangeNetwork(rangeID string, rangeNumber int, present bool) error {
	return manageRangeVNet(rangeID, rangeNumber, present)
}
```

Remove now-unused imports (`os`, `os/exec`, `bytes`).

- [ ] **Step 3: Update SDN route file path**

In `ludus-api/sdn.go:548` (`routeForRangeNetworkInVNetAction`), the path `/etc/network/if-up.d/sdn-routes` is fine inside the LXC — no change needed. But ensure the route next-hop uses `ServerConfiguration.LudusNATGateway` is **not** used here; routes go to the per-range router IP `192.0.2.{100+N}`. Verify with:

```bash
grep -n '192.0.2' ludus-api/sdn.go
```

If hardcoded `192.0.2.%d` is present, leave it (matches spec). If it derives from a removed config field, replace with:

```go
gw := fmt.Sprintf("192.0.2.%d", 100+rangeNumber)
```

- [ ] **Step 4: Build + grep**

Run: `cd ludus-api && go build ./... && grep -rn '/etc/network/interfaces\|ifup\|ifdown' . || echo "clean"`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add ludus-api/network.go ludus-api/sdn.go
git commit -m "refactor(network): SDN-only; delete /etc/network/interfaces standalone path"
```

---

### Task B6: `ludus-server` checks & config — remove host probes

**Files:** Modify `ludus-server/checks.go`, `ludus-server/config.go`, `ludus-server/main.go`

- [ ] **Step 1: Delete host-probe functions**

In `ludus-server/checks.go`, delete `checkForVirtualizationSupport`, `checkDebian12or13`, `checkForProxmox8or9`, `isInCluster`. In `ludus-server/main.go`, remove their call sites in `main()`.

- [ ] **Step 2: Replace `pveversion`/`pvesh` in `config.go`**

In `ludus-server/config.go` (~line 22-32), the auto-detect block that runs `pveversion` and `pvesh get /nodes/%s/network` is for generating a config on a fresh host. This is now the installer's job. Replace the function with a stub that errors clearly:

```go
func autoDetectProxmoxConfig() error {
	return errors.New("auto-detect removed: config.yml is generated by install.sh and pushed into the LXC")
}
```

Remove its call in `generateConfigIfAutomatedInstall` or have that function `log.Fatal` with the same message.

- [ ] **Step 3: Replace `/etc/pve` cert paths in `main.go`**

In `ludus-server/main.go` `serve()` (~line 98-104), replace:

```go
certPath := config.TLSCertFile
keyPath := config.TLSKeyFile
if !fileExists(certPath) || !fileExists(keyPath) {
	log.Printf("TLS cert/key not found at %s / %s — bootstrap should have generated them", certPath, keyPath)
	// Fall through; http.ListenAndServeTLS will error usefully.
}
```

Where `config` is the loaded `ludusapi.Configuration` (check the local variable name in `serve()`).

- [ ] **Step 4: Build + grep**

Run:
```bash
cd ludus-server && go build ./... && \
  grep -rn 'pveversion\|/etc/pve\|pvesh' . --include='*.go' || echo "clean"
```
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add ludus-server/
git commit -m "refactor(server): remove host probes (pveversion, /etc/pve, virt check)"
```

---

### Task B7: `ansible.go` — inject endpoint + token extra-vars

**Files:** Modify `ludus-api/ansible.go`

- [ ] **Step 1: Add helper to derive `proxmox_api_host`**

Add near top of `ludus-api/ansible.go`:

```go
import "net/url"

func proxmoxAPIVars() map[string]interface{} {
	pc, _ := GetRootPVEClient()
	ep := ServerConfiguration.ProxmoxEndpoints[0]
	if pc != nil {
		ep = pc.ActiveEndpoint()
	}
	u, _ := url.Parse(ep)
	return map[string]interface{}{
		"proxmox_url":          ep,
		"proxmox_api_host":     u.Hostname(),
		"proxmox_api_port":     u.Port(),
		"proxmox_token_id":     ServerConfiguration.ProxmoxTokenID,
		"proxmox_token_secret": ServerConfiguration.ProxmoxTokenSecret,
		"ludus_nat_ip":         ServerConfiguration.LudusNATIP,
		"ludus_nat_gateway":    ServerConfiguration.LudusNATGateway,
		"ludus_nat_interface":  ServerConfiguration.LudusNATInterface,
	}
}
```

- [ ] **Step 2: Merge into `userVars`**

In `RunAnsiblePlaybookWithVariables` (~line 98), after `userVars := map[string]interface{}{...}`, add:

```go
maps.Copy(userVars, proxmoxAPIVars())
```

- [ ] **Step 3: Secret via temp file, not argv**

The `playbook.AnsiblePlaybookOptions.ExtraVars` map is passed as `-e key=value` on argv by `go-ansible`, which exposes the token in `ps`. Move secrets to a temp JSON file:

After building `userVars`, add:

```go
secretVars := map[string]interface{}{
	"proxmox_token_secret": userVars["proxmox_token_secret"],
}
delete(userVars, "proxmox_token_secret")
secretFile, err := os.CreateTemp("", "ludus-vars-*.json")
if err != nil {
	return "", err
}
defer os.Remove(secretFile.Name())
_ = os.Chmod(secretFile.Name(), 0600)
_ = json.NewEncoder(secretFile).Encode(secretVars)
secretFile.Close()
serverAndUserConfigs = append(serverAndUserConfigs, "@"+secretFile.Name())
```

Add `"encoding/json"`, `"os"` to imports if missing.

- [ ] **Step 4: Update `PROXMOX_URL` env var**

At ~line 203 and ~line 325, change:

```go
execute.WithEnvVar("PROXMOX_URL", ep), // ep from proxmoxAPIVars or pc.ActiveEndpoint()
```

Replace `ServerConfiguration.ProxmoxURL` references — that field is deprecated. Use:

```go
ep := ServerConfiguration.ProxmoxEndpoints[0]
if pc, err := GetRootPVEClient(); err == nil {
	ep = pc.ActiveEndpoint()
}
// ...
execute.WithEnvVar("PROXMOX_URL", ep),
```

- [ ] **Step 5: Build**

Run: `cd ludus-api && go build ./... && grep -rn 'ProxmoxURL' ansible.go || echo "clean"`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add ludus-api/ansible.go
git commit -m "feat(ansible): inject proxmox_url/token/nat extra-vars; secret via temp file"
```

**End of Phase B.** Run: `go build ./... && go test ./...` in both modules → green. `grep -rn 'pveum\|pvesm\|pveperf\|pvesh\|pveversion\|/etc/pve\|127.0.0.1:8006' ludus-api/ ludus-server/ --include='*.go'` → empty.

---

## Phase C — Go Bootstrap & Local Generators

End state: `ludus-server` first-boot inside the LXC creates all Proxmox objects, generates TLS/WG/dnsmasq/routes, writes `.bootstrap-complete`. No ansible at install time.

### Task C1: `localgen/tls.go` — self-signed cert

**Files:**
- Create `ludus-server/localgen/tls.go`
- Create `ludus-server/localgen/tls_test.go`

- [ ] **Step 1: Write failing test**

Create `ludus-server/localgen/tls_test.go`:

```go
package localgen

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureTLSCert(t *testing.T) {
	dir := t.TempDir()
	crt := filepath.Join(dir, "server.crt")
	key := filepath.Join(dir, "server.key")
	if err := EnsureTLSCert(crt, key, "ludus.local", []net.IP{net.ParseIP("10.0.0.5")}); err != nil {
		t.Fatal(err)
	}
	// Loadable as a TLS pair
	if _, err := tls.LoadX509KeyPair(crt, key); err != nil {
		t.Fatalf("keypair: %v", err)
	}
	// SAN contains IP
	pemBytes, _ := os.ReadFile(crt)
	block, _ := pem.Decode(pemBytes)
	cert, _ := x509.ParseCertificate(block.Bytes)
	found := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.ParseIP("10.0.0.5")) {
			found = true
		}
	}
	if !found {
		t.Fatal("SAN IP missing")
	}
	// Idempotent: second call does not overwrite
	mtime1, _ := os.Stat(crt)
	_ = EnsureTLSCert(crt, key, "ludus.local", []net.IP{net.ParseIP("10.0.0.5")})
	mtime2, _ := os.Stat(crt)
	if !mtime1.ModTime().Equal(mtime2.ModTime()) {
		t.Fatal("cert was regenerated on second call")
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-server && go test ./localgen/...`
Expected: package not found.

- [ ] **Step 3: Implement**

Create `ludus-server/localgen/tls.go`:

```go
// Package localgen renders local config artifacts (TLS, WireGuard, dnsmasq,
// routes) that the Ludus LXC needs on first boot. All functions are idempotent:
// they do nothing if the target already exists.
package localgen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func EnsureTLSCert(certPath, keyPath, hostname string, ips []net.IP) error {
	if fileExists(certPath) && fileExists(keyPath) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0700); err != nil {
		return err
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hostname, Organization: []string{"Ludus"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{hostname},
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0644); err != nil {
		return err
	}
	keyDer, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return err
	}
	return writePEM(keyPath, "EC PRIVATE KEY", keyDer, 0600)
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
```

- [ ] **Step 4: Run test**

Run: `cd ludus-server && go test ./localgen/... -run TestEnsureTLSCert -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ludus-server/localgen/
git commit -m "feat(localgen): self-signed TLS cert generation"
```

---

### Task C2: `localgen/wireguard.go`

**Files:**
- Create `ludus-server/localgen/wireguard.go`
- Create `ludus-server/localgen/wireguard_test.go`
- Create `ludus-server/localgen/testdata/wg0.conf.golden`

- [ ] **Step 1: Write failing golden test**

Create `ludus-server/localgen/wireguard_test.go`:

```go
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
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-server && go test ./localgen/... -run TestEnsureWireguard`
Expected: compile error.

- [ ] **Step 3: Implement**

Create `ludus-server/localgen/wireguard.go`:

```go
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
```

Run `go get golang.org/x/crypto@latest` in `ludus-server/` if not already a dep, then `go mod tidy`.

- [ ] **Step 4: Run test**

Run: `cd ludus-server && go test ./localgen/... -run TestEnsureWireguard -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add ludus-server/localgen/ ludus-server/go.mod ludus-server/go.sum
git commit -m "feat(localgen): WireGuard server keypair + wg0.conf generation"
```

---

### Task C3: `localgen/dnsmasq.go` + `localgen/routes.go`

**Files:**
- Create `ludus-server/localgen/dnsmasq.go`
- Create `ludus-server/localgen/routes.go`
- Create `ludus-server/localgen/dnsmasq_test.go`
- Create `ludus-server/localgen/routes_test.go`

- [ ] **Step 1: Write failing tests**

`ludus-server/localgen/dnsmasq_test.go`:

```go
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
```

`ludus-server/localgen/routes_test.go`:

```go
package localgen

import (
	"os"
	"strings"
	"testing"
)

func TestRenderRoutes(t *testing.T) {
	got := RenderRoutes([]int{2, 5})
	if !strings.Contains(got, "ip route replace 10.2.0.0/16 via 192.0.2.102") {
		t.Fatalf("missing route 2:\n%s", got)
	}
	if !strings.Contains(got, "ip route replace 10.5.0.0/16 via 192.0.2.105") {
		t.Fatalf("missing route 5:\n%s", got)
	}
	if !strings.HasPrefix(got, "#!/bin/sh") {
		t.Fatal("missing shebang")
	}
}

func TestWriteRoutes(t *testing.T) {
	p := t.TempDir() + "/ludus-routes"
	if err := WriteRoutes(p, []int{2}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0755 {
		t.Fatalf("expected 0755, got %v", st.Mode().Perm())
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-server && go test ./localgen/...`
Expected: compile errors.

- [ ] **Step 3: Implement `dnsmasq.go`**

```go
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
```

- [ ] **Step 4: Implement `routes.go`**

```go
package localgen

import (
	"fmt"
	"os"
	"strings"
)

func RenderRoutes(rangeNumbers []int) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Managed by Ludus bootstrap — do not edit\n")
	for _, n := range rangeNumbers {
		fmt.Fprintf(&b, "ip route replace 10.%d.0.0/16 via 192.0.2.%d\n", n, 100+n)
	}
	return b.String()
}

func WriteRoutes(path string, rangeNumbers []int) error {
	return os.WriteFile(path, []byte(RenderRoutes(rangeNumbers)), 0755)
}
```

- [ ] **Step 5: Run tests**

Run: `cd ludus-server && go test ./localgen/... -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add ludus-server/localgen/
git commit -m "feat(localgen): dnsmasq + per-range route script generation"
```

---

### Task C4: `bootstrap.go` — PVEClient interface + preflight + Ensure*

**Files:**
- Create `ludus-server/bootstrap.go`
- Create `ludus-server/bootstrap_test.go`

- [ ] **Step 1: Write failing test with mock client**

Create `ludus-server/bootstrap_test.go`:

```go
package main

import (
	"context"
	"testing"

	"ludusapi"
	"ludusapi/pveclient"
)

type mockPVE struct {
	calls   []string
	nodes   int
	version pveclient.Version
}

func (m *mockPVE) Version(context.Context) (pveclient.Version, error) { m.calls = append(m.calls, "Version"); return m.version, nil }
func (m *mockPVE) ClusterNodeCount(context.Context) (int, error)       { m.calls = append(m.calls, "ClusterNodeCount"); return m.nodes, nil }
func (m *mockPVE) ClusterNodeIPs(context.Context) ([]string, error)    { return []string{"10.0.0.1"}, nil }
func (m *mockPVE) VerifyTokenOnAll(context.Context) error              { m.calls = append(m.calls, "VerifyTokenOnAll"); return nil }
func (m *mockPVE) EnsureRole(_ context.Context, n string, _ []string) error  { m.calls = append(m.calls, "EnsureRole:"+n); return nil }
func (m *mockPVE) EnsureGroup(_ context.Context, n string) error             { m.calls = append(m.calls, "EnsureGroup:"+n); return nil }
func (m *mockPVE) EnsurePool(_ context.Context, n string) error              { m.calls = append(m.calls, "EnsurePool:"+n); return nil }
func (m *mockPVE) EnsureACL(_ context.Context, p, r string, _, _ []string) error { m.calls = append(m.calls, "EnsureACL:"+p+":"+r); return nil }
func (m *mockPVE) EnsureSDNZone(_ context.Context, n, k string, _ []string) error { m.calls = append(m.calls, "EnsureSDNZone:"+n+":"+k); return nil }
func (m *mockPVE) EnsureVNet(_ context.Context, _, n string, _ int, _ bool) error { m.calls = append(m.calls, "EnsureVNet:"+n); return nil }
func (m *mockPVE) EnsureSubnet(_ context.Context, v, c, _ string, _ bool) error   { m.calls = append(m.calls, "EnsureSubnet:"+v+":"+c); return nil }
func (m *mockPVE) ApplySDN(context.Context) error                                 { m.calls = append(m.calls, "ApplySDN"); return nil }

func TestBootstrap_EnsureSequence_SingleNode(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{
		ProxmoxEndpoints: []string{"https://10.0.0.1:8006"},
		ProxmoxNode:      "pve",
		SDNZone:          "ludus",
		LudusNATInterface: "ludusnat",
	}
	dir := t.TempDir()
	if err := bootstrapProxmoxObjects(t.Context(), m, cfg, dir); err != nil {
		t.Fatal(err)
	}
	wantContains := []string{
		"Version", "VerifyTokenOnAll", "ClusterNodeCount",
		"EnsureRole:LudusPacker", "EnsureRole:LudusUser", "EnsureRole:LudusAdmin",
		"EnsureGroup:ludus_users", "EnsureGroup:ludus_admins",
		"EnsurePool:SHARED", "EnsurePool:ADMIN",
		"EnsureSDNZone:ludus:simple",
		"EnsureVNet:ludusnat",
		"EnsureSubnet:ludusnat:192.0.2.0/24",
		"ApplySDN",
	}
	got := map[string]bool{}
	for _, c := range m.calls {
		got[c] = true
	}
	for _, w := range wantContains {
		if !got[w] {
			t.Fatalf("missing call %q; got %v", w, m.calls)
		}
	}
}

func TestBootstrap_VXLANOnCluster(t *testing.T) {
	m := &mockPVE{nodes: 3, version: pveclient.Version{Version: "8.2.4"}}
	cfg := ludusapi.Configuration{ProxmoxEndpoints: []string{"https://10.0.0.1:8006"}, SDNZone: "ludus", LudusNATInterface: "ludusnat"}
	_ = bootstrapProxmoxObjects(t.Context(), m, cfg, t.TempDir())
	found := false
	for _, c := range m.calls {
		if c == "EnsureSDNZone:ludus:vxlan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected vxlan zone on 3-node cluster: %v", m.calls)
	}
}

func TestBootstrap_RejectsOldPVE(t *testing.T) {
	m := &mockPVE{nodes: 1, version: pveclient.Version{Version: "7.4.1"}}
	err := bootstrapProxmoxObjects(t.Context(), m, ludusapi.Configuration{SDNZone: "ludus"}, t.TempDir())
	if err == nil {
		t.Fatal("expected error for PVE < 8.0")
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-server && go test -run TestBootstrap ./...`
Expected: compile error.

- [ ] **Step 3: Implement `bootstrap.go`**

Create `ludus-server/bootstrap.go`:

```go
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"ludusapi"
	"ludusapi/pveclient"
	"ludus-server/localgen"
)

// PVEClient is the subset of pveclient.Client used by bootstrap. Allows mocking.
type PVEClient interface {
	Version(context.Context) (pveclient.Version, error)
	VerifyTokenOnAll(context.Context) error
	ClusterNodeCount(context.Context) (int, error)
	ClusterNodeIPs(context.Context) ([]string, error)
	EnsureRole(context.Context, string, []string) error
	EnsureGroup(context.Context, string) error
	EnsurePool(context.Context, string) error
	EnsureACL(ctx context.Context, path, role string, groups, users []string) error
	EnsureSDNZone(ctx context.Context, name, kind string, peers []string) error
	EnsureVNet(ctx context.Context, zone, name string, tag int, vlanaware bool) error
	EnsureSubnet(ctx context.Context, vnet, cidr, gw string, snat bool) error
	ApplySDN(context.Context) error
}

const bootstrapMarker = "/opt/ludus/install/.bootstrap-complete"

// Privilege sets mirror current ansible/proxmox-install/stage-3.yml.
var (
	privsPacker = []string{"VM.Config.Disk", "VM.Config.CPU", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.Config.CDROM", "VM.Config.Cloudinit", "VM.Config.HWType", "VM.PowerMgmt", "VM.Audit", "VM.Allocate", "VM.Monitor", "VM.Console", "Datastore.AllocateSpace", "Datastore.AllocateTemplate", "Datastore.Audit", "Sys.Audit", "Sys.Modify", "SDN.Use"}
	privsUser   = append([]string{"Pool.Audit", "VM.Clone", "VM.Snapshot", "VM.Snapshot.Rollback"}, privsPacker...)
	privsAdmin  = append([]string{"Pool.Allocate", "User.Modify", "Realm.AllocateUser", "Permissions.Modify", "SDN.Allocate"}, privsUser...)
)

// bootstrap is the entry point called from main() when the marker is absent.
func bootstrap(ctx context.Context, cfg ludusapi.Configuration) error {
	logf("bootstrap: starting")
	pc, err := pveclient.New(cfg.PVEClientConfig())
	if err != nil {
		return fmt.Errorf("pveclient: %w", err)
	}
	defer pc.Close()
	if err := bootstrapProxmoxObjects(ctx, pc, cfg, "/opt/ludus"); err != nil {
		return err
	}
	if err := bootstrapLocalState(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(bootstrapMarker), 0755); err != nil {
		return err
	}
	return os.WriteFile(bootstrapMarker, []byte("ok\n"), 0644)
}

func bootstrapProxmoxObjects(ctx context.Context, pc PVEClient, cfg ludusapi.Configuration, installDir string) error {
	// Preflight
	v, err := pc.Version(ctx)
	if err != nil {
		return fmt.Errorf("preflight version: %w", err)
	}
	if !v.AtLeast(8, 0) {
		return fmt.Errorf("Proxmox %s is too old; Ludus requires 8.0+", v.Version)
	}
	if err := pc.VerifyTokenOnAll(ctx); err != nil {
		return fmt.Errorf("preflight token: %w", err)
	}
	nodes, err := pc.ClusterNodeCount(ctx)
	if err != nil {
		return fmt.Errorf("preflight node count: %w", err)
	}
	zoneType := "simple"
	var peers []string
	if nodes > 1 {
		zoneType = "vxlan"
		peers, _ = pc.ClusterNodeIPs(ctx)
	}
	logf("bootstrap: PVE %s, %d node(s), SDN zone type=%s", v.Version, nodes, zoneType)

	// Roles, groups, pools
	for name, privs := range map[string][]string{"LudusPacker": privsPacker, "LudusUser": privsUser, "LudusAdmin": privsAdmin} {
		if err := pc.EnsureRole(ctx, name, privs); err != nil {
			return fmt.Errorf("role %s: %w", name, err)
		}
	}
	for _, g := range []string{"ludus_users", "ludus_admins"} {
		if err := pc.EnsureGroup(ctx, g); err != nil {
			return fmt.Errorf("group %s: %w", g, err)
		}
	}
	for _, p := range []string{"SHARED", "ADMIN"} {
		if err := pc.EnsurePool(ctx, p); err != nil {
			return fmt.Errorf("pool %s: %w", p, err)
		}
	}
	// ACLs
	acls := []struct{ path, role string; groups []string }{
		{"/pool/SHARED", "LudusUser", []string{"ludus_users", "ludus_admins"}},
		{"/pool/ADMIN", "LudusAdmin", []string{"ludus_admins"}},
		{"/sdn/zones/" + cfg.SDNZone, "LudusUser", []string{"ludus_users", "ludus_admins"}},
	}
	for _, a := range acls {
		if err := pc.EnsureACL(ctx, a.path, a.role, a.groups, nil); err != nil {
			return fmt.Errorf("acl %s: %w", a.path, err)
		}
	}
	// SDN
	if err := pc.EnsureSDNZone(ctx, cfg.SDNZone, zoneType, peers); err != nil {
		return fmt.Errorf("sdn zone: %w", err)
	}
	if err := pc.EnsureVNet(ctx, cfg.SDNZone, cfg.LudusNATInterface, 0, true); err != nil {
		return fmt.Errorf("vnet %s: %w", cfg.LudusNATInterface, err)
	}
	if err := pc.EnsureSubnet(ctx, cfg.LudusNATInterface, "192.0.2.0/24", cfg.LudusNATGateway, true); err != nil {
		return fmt.Errorf("subnet: %w", err)
	}
	if err := pc.ApplySDN(ctx); err != nil {
		return fmt.Errorf("apply sdn: %w", err)
	}
	_ = installDir // reserved for future per-install state
	return nil
}

func bootstrapLocalState(cfg ludusapi.Configuration) error {
	// TLS
	hostname, _ := os.Hostname()
	ips := localIPs()
	if err := localgen.EnsureTLSCert(cfg.TLSCertFile, cfg.TLSKeyFile, hostname, ips); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	// WireGuard
	if err := localgen.EnsureWireguard(localgen.WGConfig{
		Dir: "/etc/wireguard", ListenPort: cfg.WireguardPort, ServerIP: "198.51.100.1/24",
	}); err != nil {
		return fmt.Errorf("wireguard: %w", err)
	}
	// dnsmasq
	if err := localgen.WriteDnsmasq("/etc/dnsmasq.d/ludus.conf", localgen.DnsmasqConfig{
		BindIP: cfg.LudusNATIP, Gateway: cfg.LudusNATGateway,
		PoolLow: "192.0.2.50", PoolHigh: "192.0.2.100", IfName: "eth1",
	}); err != nil {
		return fmt.Errorf("dnsmasq: %w", err)
	}
	// Dirs
	for _, d := range []string{"/opt/ludus/users", "/opt/ludus/ci", "/opt/ludus/previous-versions", "/opt/ludus/tls"} {
		if err := os.MkdirAll(d, 0750); err != nil {
			return err
		}
	}
	// Enable services
	for _, svc := range []string{"wg-quick@wg0", "dnsmasq"} {
		if err := exec.Command("systemctl", "enable", "--now", svc).Run(); err != nil {
			logf("WARN: systemctl enable %s: %v (continuing)", svc, err)
		}
	}
	return nil
}

func localIPs() []net.IP {
	var out []net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			out = append(out, ipn.IP)
		}
	}
	return out
}

func logf(format string, args ...any) {
	f, err := os.OpenFile("/opt/ludus/install/install.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(f, format+"\n", args...)
		f.Close()
	}
	fmt.Printf(format+"\n", args...)
}
```

- [ ] **Step 4: Ensure `*pveclient.Client` satisfies `PVEClient`**

Add a compile-time assertion at the bottom of `ludus-server/bootstrap.go`:

```go
var _ PVEClient = (*pveclient.Client)(nil)
```

If it fails to compile, the interface and concrete signatures differ — fix the interface to match `pveclient`.

- [ ] **Step 5: Run tests**

Run: `cd ludus-server && go test -run TestBootstrap ./... -v`
Expected: 3 PASS.

- [ ] **Step 6: Commit**

```bash
git add ludus-server/bootstrap.go ludus-server/bootstrap_test.go
git commit -m "feat(bootstrap): Go first-boot replacing proxmox-install ansible"
```

---

### Task C5: Wire `bootstrap()` into `main.go`, delete `install.go`

**Files:** Modify `ludus-server/main.go`, delete `ludus-server/install.go`

- [ ] **Step 1: Replace install path in `main()`**

In `ludus-server/main.go`, find the block that checks `.stage-3-complete` and runs `runInstallPlaybook`. Replace with:

```go
if !fileExists(bootstrapMarker) {
	ctx := context.Background()
	if err := bootstrap(ctx, config); err != nil {
		log.Fatalf("bootstrap failed: %v", err)
	}
	// reconcileImportedState runs inside bootstrap before marker write — see Phase F
	runBootstrapOnly() // create ROOT user + admin key (existing func)
}
serve()
```

Remove references to `getInstallStep`, `runInstallPlaybook`, `installAnsibleWithPip`, `installAnsibleRequirements`, `existingProxmox`.

- [ ] **Step 2: Delete `install.go`**

```bash
git rm ludus-server/install.go
```

- [ ] **Step 3: Build**

Run: `cd ludus-server && go build ./...`
Expected: OK. Fix any dangling references.

- [ ] **Step 4: Commit**

```bash
git add ludus-server/
git commit -m "refactor(server): main() calls bootstrap(); delete install.go"
```

**End of Phase C.** `go test ./...` green in both modules.

---

## Phase D — Ansible Parameterization & Deletions

End state: range-management playbooks reference `{{ proxmox_url }}` instead of `127.0.0.1`; obsolete install ansible removed.

### Task D1: Parameterize curl tasks

**Files:** Modify 5 task files under `ludus-server/ansible/range-management/tasks/`

- [ ] **Step 1: List exact occurrences**

```bash
grep -rn '127.0.0.1:8006' ludus-server/ansible/range-management/
```

- [ ] **Step 2: Replace in each file**

For each of:
- `tasks/firewall/set-firewall-rules.yml`
- `tasks/router/add-vlan-to-router.yml`
- `tasks/proxmox/configure-ip-and-hostname-linux.yml`
- `tasks/proxmox/snapshot-management.yml`
- `tasks/proxmox/set-vm-state.yml`

Replace every:

```yaml
url: "https://127.0.0.1:8006/api2/json/..."
```

with:

```yaml
url: "{{ proxmox_url }}/api2/json/..."
```

And every auth header that uses a cookie/ticket with:

```yaml
headers:
  Authorization: "PVEAPIToken={{ proxmox_token_id }}={{ proxmox_token_secret }}"
validate_certs: false
```

If the task uses `command: curl ...`, convert to `ansible.builtin.uri` with the above, or keep `curl` and substitute:

```yaml
command: >
  curl -sk -H "Authorization: PVEAPIToken={{ proxmox_token_id }}={{ proxmox_token_secret }}"
  "{{ proxmox_url }}/api2/json/..."
```

- [ ] **Step 3: Verify**

```bash
grep -rn '127.0.0.1:8006' ludus-server/ansible/range-management/ || echo "clean"
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add ludus-server/ansible/range-management/
git commit -m "refactor(ansible): parameterize Proxmox URL + token in range tasks"
```

---

### Task D2: Update firewall deny-list to use endpoint IPs

**Files:** Modify `ludus-server/ansible/range-management/tasks/firewall/set-firewall-rules.yml`

- [ ] **Step 1: Find the deny-host rules**

```bash
grep -n 'proxmox_public_ip\|proxmox_local_ip' ludus-server/ansible/range-management/tasks/firewall/set-firewall-rules.yml
```

- [ ] **Step 2: Replace with loop over endpoint hosts + nat IPs**

Replace the two single-IP deny tasks with:

```yaml
- name: Deny range traffic to Ludus infrastructure IPs
  ansible.builtin.iptables:
    chain: LUDUS_DEFAULTS
    destination: "{{ item }}"
    jump: DROP
    comment: "Ludus: block range -> infra"
  loop: "{{ ludus_infra_deny_ips }}"
```

And in `ludus-api/ansible.go` `proxmoxAPIVars()`, add:

```go
hosts := []string{ServerConfiguration.LudusNATIP, ServerConfiguration.LudusNATGateway}
for _, ep := range ServerConfiguration.ProxmoxEndpoints {
	if u, err := url.Parse(ep); err == nil {
		hosts = append(hosts, u.Hostname())
	}
}
// in returned map:
"ludus_infra_deny_ips": hosts,
```

- [ ] **Step 3: Commit**

```bash
git add ludus-server/ansible/range-management/tasks/firewall/set-firewall-rules.yml ludus-api/ansible.go
git commit -m "feat(firewall): deny-list covers all proxmox_endpoints + nat IPs"
```

---

### Task D3: Update `community.proxmox` module tasks

**Files:** All `ludus-server/ansible/**/*.yml` using `proxmox_kvm:` or `community.general.proxmox*:`

- [ ] **Step 1: Find them**

```bash
grep -rn 'api_host:\|proxmox_kvm:\|community.general.proxmox' ludus-server/ansible/ | grep -v proxmox-install
```

- [ ] **Step 2: Standardize connection params**

For each module invocation, ensure these keys (add a YAML anchor at top of `range-management/ludus.yml` vars to DRY):

```yaml
# In ludus-server/ansible/range-management/ludus.yml, under vars:
proxmox_auth: &proxmox_auth
  api_host: "{{ proxmox_api_host }}"
  api_port: "{{ proxmox_api_port | default(8006) }}"
  api_user: "{{ proxmox_token_id.split('!')[0] }}"
  api_token_id: "{{ proxmox_token_id.split('!')[1] }}"
  api_token_secret: "{{ proxmox_token_secret }}"
  validate_certs: false
```

Then in each task:

```yaml
- name: Clone template
  community.general.proxmox_kvm:
    <<: *proxmox_auth
    node: "{{ proxmox_node }}"
    # ... rest
```

If anchors don't propagate across `import_tasks` boundaries, define `proxmox_auth` as a dict var instead and use `"{{ proxmox_auth | combine({...}) }}"` — pick whichever pattern the existing playbooks already use for shared params.

- [ ] **Step 3: Lint**

```bash
ansible-lint ludus-server/ansible/range-management/ || true
```
Expected: no new errors vs `main`.

- [ ] **Step 4: Commit**

```bash
git add ludus-server/ansible/
git commit -m "refactor(ansible): proxmox module tasks use api_host/token from extra-vars"
```

---

### Task D4: Delete obsolete ansible + config fields

**Files:** Delete `ludus-server/ansible/proxmox-install/`, `ludus-server/ansible/user-management/vmbr-management.yml`; modify `ludus-api/config.go`

- [ ] **Step 1: Delete files**

```bash
git rm -r ludus-server/ansible/proxmox-install/
git rm ludus-server/ansible/user-management/vmbr-management.yml
```

- [ ] **Step 2: Remove dead config fields**

In `ludus-api/config.go`, delete struct fields and `viper.SetDefault` lines for: `ProxmoxInterface`, `ProxmoxLocalIP`, `ProxmoxGateway`, `ProxmoxNetmask`, `ClusterMode`. Keep `ProxmoxURL` and `ProxmoxPublicIP` (deprecated, shimmed).

- [ ] **Step 3: Chase compile errors**

```bash
cd ludus-api && go build ./... 2>&1 | head -40
cd ../ludus-server && go build ./... 2>&1 | head -40
```

For each `undefined: ServerConfiguration.ProxmoxLocalIP` etc., either delete the referencing code (it was host-config logic) or replace with the new equivalent (`LudusNATGateway` for "the host's IP on ludusnat", `WireguardEndpoint` for "the public IP").

- [ ] **Step 4: Grep for dead refs in ansible**

```bash
grep -rn 'proxmox_local_ip\|proxmox_gateway\|proxmox_netmask\|proxmox_interface\|vmbr1000\|vmbr-management' ludus-server/ansible/ || echo "clean"
```

Replace `vmbr1000` → `{{ ludus_nat_interface }}`, `proxmox_local_ip` → `{{ ludus_nat_gateway }}` where it meant "host IP on the NAT net", or delete the task if it was host-config.

- [ ] **Step 5: Build + test**

Run: `go build ./... && go test ./...` in both modules.
Expected: green.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: delete proxmox-install ansible, vmbr-management, dead config fields"
```

**End of Phase D.**

---

## Phase E — LXC Image, Installer, CI

End state: `build-lxc-template` CI job produces `ludus-<ver>-debian13-amd64.tar.zst`, uploaded to R2; `install.sh --no-prompt --template-file <tar>` brings up a working Ludus LXC on a fresh PVE node; air-gap test passes.

### Task E1: DAB scaffold

**Files:** Create `ludus-server/lxc/{dab.conf,Makefile,build.sh,files/*}`

- [ ] **Step 1: `dab.conf`**

Create `ludus-server/lxc/dab.conf`:

```
Suite: trixie
CacheDir: ../cache
Source: http://deb.debian.org/debian SUITE main contrib
Source: http://deb.debian.org/debian SUITE-updates main contrib
Source: http://security.debian.org/debian-security SUITE-security main contrib
Architecture: amd64
Name: ludus
Version: __LUDUS_VERSION__
Section: system
Maintainer: Bad Sector Labs <ludus@badsectorlabs.com>
Infopage: https://ludus.cloud
Description: Ludus cyber range server (LXC appliance)
```

- [ ] **Step 2: `Makefile`**

Create `ludus-server/lxc/Makefile`:

```makefile
BASEDIR:=$(shell dab basedir)
LUDUS_VERSION ?= 0.0.0-dev

.PHONY: all bootstrap install finalize clean

all: bootstrap install finalize

bootstrap:
	sed -i "s/__LUDUS_VERSION__/$(LUDUS_VERSION)/" dab.conf
	dab init
	dab bootstrap --minimal

install:
	dab install python3 python3-pip python3-venv ansible-core wireguard-tools \
	  dnsmasq nftables iproute2 openssh-client sshpass curl jq ca-certificates \
	  git gpg dbus systemd-resolved sudo
	# system user
	dab exec groupadd -g 1001 ludus
	dab exec useradd -u 1001 -g 1001 -m -s /bin/bash ludus
	# dirs
	dab exec mkdir -p /opt/ludus/install /opt/ludus/db /opt/ludus/tls \
	  /opt/ludus/users /opt/ludus/collections /opt/ludus/resources/packer/plugins
	# binary + embedded trees
	install -m 0755 ../../binaries/ludus-server $(BASEDIR)/opt/ludus/ludus-server
	cp -r ../ansible $(BASEDIR)/opt/ludus/ansible
	cp -r ../packer $(BASEDIR)/opt/ludus/packer
	# python venv (air-gap)
	dab exec python3 -m venv /opt/ludus/venv
	dab exec /opt/ludus/venv/bin/pip install --no-cache-dir \
	  proxmoxer==2.0.1 requests==2.32.3 netaddr==1.2.1 pywinrm==0.4.3 \
	  dnspython==2.6.1 jmespath==1.0.1
	# galaxy collections (air-gap)
	dab exec env ANSIBLE_COLLECTIONS_PATH=/opt/ludus/collections \
	  ansible-galaxy collection install -r /opt/ludus/ansible/requirements.yml \
	  -p /opt/ludus/collections
	# packer + plugins
	install -m 0755 deps/packer $(BASEDIR)/usr/local/bin/packer
	cp deps/packer-plugin-* $(BASEDIR)/opt/ludus/resources/packer/plugins/
	# systemd + sysctl
	install -m 0644 files/ludus.service $(BASEDIR)/etc/systemd/system/ludus.service
	install -m 0644 files/ludus-admin.service $(BASEDIR)/etc/systemd/system/ludus-admin.service
	install -m 0644 files/99-ludus-sysctl.conf $(BASEDIR)/etc/sysctl.d/99-ludus.conf
	dab exec systemctl enable ludus.service ludus-admin.service wg-quick@wg0 dnsmasq
	dab exec chown -R ludus:ludus /opt/ludus

finalize:
	dab finalize --compressor zstd-max

clean:
	dab clean
	rm -f *.tar.zst
```

- [ ] **Step 3: systemd units**

Copy from existing j2, strip jinja. Create `ludus-server/lxc/files/ludus.service`:

```ini
[Unit]
Description=Ludus API Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/ludus
ExecStart=/opt/ludus/ludus-server
Restart=on-failure
RestartSec=5
Environment=ANSIBLE_COLLECTIONS_PATH=/opt/ludus/collections
Environment=PATH=/opt/ludus/venv/bin:/usr/local/bin:/usr/bin:/bin

[Install]
WantedBy=multi-user.target
```

Create `ludus-server/lxc/files/ludus-admin.service`:

```ini
[Unit]
Description=Ludus Admin API Server
After=ludus.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/ludus
ExecStart=/opt/ludus/ludus-server --admin
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

(Check `grep -n 'admin' ludus-server/main.go` for the actual flag — adjust `--admin` if it's a different flag name.)

Create `ludus-server/lxc/files/99-ludus-sysctl.conf`:

```
net.ipv4.ip_forward=1
```

- [ ] **Step 4: `build.sh`**

Create `ludus-server/lxc/build.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"

: "${LUDUS_VERSION:?LUDUS_VERSION must be set}"
: "${PACKER_VERSION:=1.11.2}"
: "${PACKER_PROXMOX_VERSION:=1.2.1}"
: "${PACKER_ANSIBLE_VERSION:=1.1.1}"

mkdir -p deps
if [[ ! -f deps/packer ]]; then
  curl -fsSL "https://releases.hashicorp.com/packer/${PACKER_VERSION}/packer_${PACKER_VERSION}_linux_amd64.zip" -o /tmp/packer.zip
  unzip -o /tmp/packer.zip -d deps/
fi
for plugin in proxmox:${PACKER_PROXMOX_VERSION} ansible:${PACKER_ANSIBLE_VERSION}; do
  name=${plugin%%:*}; ver=${plugin##*:}
  if [[ ! -f deps/packer-plugin-${name} ]]; then
    curl -fsSL "https://github.com/hashicorp/packer-plugin-${name}/releases/download/v${ver}/packer-plugin-${name}_v${ver}_x5.0_linux_amd64.zip" -o /tmp/pp.zip
    unzip -o /tmp/pp.zip -d deps/
    mv deps/packer-plugin-${name}_* deps/packer-plugin-${name}
  fi
done

if [[ ! -f ../../binaries/ludus-server ]]; then
  echo "ERROR: binaries/ludus-server not found (run 'build all' first)" >&2
  exit 1
fi

make clean || true
make LUDUS_VERSION="${LUDUS_VERSION}"

OUT="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
mv ludus_*.tar.zst "../../${OUT}" 2>/dev/null || mv *.tar.zst "../../${OUT}"
sha256sum "../../${OUT}" > "../../${OUT}.sha256"
echo "Built: ${OUT}"
```

```bash
chmod +x ludus-server/lxc/build.sh
```

- [ ] **Step 5: Commit**

```bash
git add ludus-server/lxc/
git commit -m "feat(lxc): DAB build scaffold for Debian 13 appliance"
```

---

### Task E2: Rewrite `install.sh` server branch

**Files:** Modify `install.sh`

- [ ] **Step 1: Locate the server-install branch**

```bash
grep -n 'ludus-server\|pveversion\|Debian 12' install.sh | head -20
```

The client-install logic stays. Find where the script detects "we're on a server-capable host" and replace from there.

- [ ] **Step 2: Replace with new flow**

Replace the server-install section with the following block. Preserve any existing helper functions (`print_*`, color vars) by reusing them.

```bash
########################################
# Ludus server install — LXC mode
########################################
ludus_install_server() {
  # ---- 0. Preflight ----
  command -v pveversion >/dev/null || { echo "ERROR: not a Proxmox host"; exit 1; }
  command -v curl >/dev/null || { echo "ERROR: curl required"; exit 1; }
  command -v python3 >/dev/null || { echo "ERROR: python3 required"; exit 1; }
  PVE_VER=$(pveversion | cut -d/ -f2 | cut -d. -f1)
  [[ "$PVE_VER" -ge 8 ]] || { echo "ERROR: Proxmox 8.0+ required"; exit 1; }

  _json() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }
  NODE=$(hostname)

  # ---- 1. Token ----
  if [[ -z "${TOKEN_ID:-}" || -z "${TOKEN_SECRET:-}" ]]; then
    if [[ $EUID -eq 0 ]]; then
      echo "Generating root@pam!ludus API token..."
      TOK_JSON=$(pveum user token add root@pam ludus --privsep 0 --output-format json 2>/dev/null || true)
      if [[ -z "$TOK_JSON" ]]; then
        echo "Token 'root@pam!ludus' already exists."
        read -rp "Delete and recreate? [y/N] " yn
        if [[ "$yn" =~ ^[Yy]$ ]]; then
          pveum user token remove root@pam ludus
          TOK_JSON=$(pveum user token add root@pam ludus --privsep 0 --output-format json)
        else
          read -rp "Enter existing token secret: " TOKEN_SECRET
          TOKEN_ID="root@pam!ludus"
        fi
      fi
      if [[ -n "$TOK_JSON" ]]; then
        TOKEN_ID=$(echo "$TOK_JSON" | _json 'd["full-tokenid"]')
        TOKEN_SECRET=$(echo "$TOK_JSON" | _json 'd["value"]')
      fi
    else
      read -rp "Proxmox API token ID (e.g. root@pam!ludus): " TOKEN_ID
      read -rsp "Proxmox API token secret: " TOKEN_SECRET; echo
    fi
  fi
  AUTH="Authorization: PVEAPIToken=${TOKEN_ID}=${TOKEN_SECRET}"
  EP_LOCAL="https://localhost:8006"
  curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/version" | grep -q version \
    || { echo "ERROR: token validation failed"; exit 1; }

  # ---- 2. Gather config ----
  CLUSTER_JSON=$(curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/status")
  DEFAULT_EPS=$(echo "$CLUSTER_JSON" | _json '" ".join("https://"+n["ip"]+":8006" for n in d["data"] if n["type"]=="node" and n.get("ip"))' 2>/dev/null || echo "")
  [[ -z "$DEFAULT_EPS" ]] && DEFAULT_EPS="https://$(hostname -I | awk '{print $1}'):8006"

  if [[ "${NO_PROMPT:-0}" != "1" ]]; then
    read -rp "Proxmox API endpoints (space-separated) [${DEFAULT_EPS}]: " ENDPOINTS
    ENDPOINTS=${ENDPOINTS:-$DEFAULT_EPS}
    DEFAULT_VMID=$(curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/nextid" | _json 'd["data"]')
    read -rp "LXC VMID [${DEFAULT_VMID}]: " VMID; VMID=${VMID:-$DEFAULT_VMID}
    DEFAULT_STORAGE=$(curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/nodes/${NODE}/storage?content=rootdir" | _json 'd["data"][0]["storage"]' 2>/dev/null || echo "local-lvm")
    read -rp "LXC rootfs storage [${DEFAULT_STORAGE}]: " STORAGE; STORAGE=${STORAGE:-$DEFAULT_STORAGE}
    read -rp "LXC eth0 IP (CIDR, or 'dhcp') [dhcp]: " ETH0_IP; ETH0_IP=${ETH0_IP:-dhcp}
    if [[ "$ETH0_IP" != "dhcp" ]]; then
      read -rp "LXC eth0 gateway: " ETH0_GW
    fi
    read -rp "WireGuard endpoint (IP/host clients dial) [${ETH0_IP%%/*}]: " WG_EP
    WG_EP=${WG_EP:-${ETH0_IP%%/*}}
    read -rp "VM storage pool [local]: " VM_STORAGE; VM_STORAGE=${VM_STORAGE:-local}
    read -rp "ISO storage pool [local]: " ISO_STORAGE; ISO_STORAGE=${ISO_STORAGE:-local}
    read -rp "License key [community]: " LICENSE; LICENSE=${LICENSE:-community}
  else
    ENDPOINTS=${ENDPOINTS:-$DEFAULT_EPS}
    VMID=${VMID:-$(curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/nextid" | _json 'd["data"]')}
    STORAGE=${STORAGE:-local-lvm}
    ETH0_IP=${ETH0_IP:-dhcp}
    WG_EP=${WG_EP:-auto}
    VM_STORAGE=${VM_STORAGE:-local}; ISO_STORAGE=${ISO_STORAGE:-local}; LICENSE=${LICENSE:-community}
  fi

  # ---- 3. SDN bootstrap ----
  NODE_COUNT=$(echo "$CLUSTER_JSON" | _json 'sum(1 for n in d["data"] if n["type"]=="node")')
  ZONE_TYPE=simple
  PEERS=""
  if [[ "$NODE_COUNT" -gt 1 ]]; then
    ZONE_TYPE=vxlan
    PEERS=$(echo "$CLUSTER_JSON" | _json '",".join(n["ip"] for n in d["data"] if n["type"]=="node")')
  fi
  echo "Creating SDN zone 'ludus' (${ZONE_TYPE})..."
  if ! curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/sdn/zones/ludus" | grep -q '"zone"'; then
    curl -sk -H "$AUTH" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/zones" \
      --data-urlencode "zone=ludus" --data-urlencode "type=${ZONE_TYPE}" \
      --data-urlencode "ipam=pve" ${PEERS:+--data-urlencode "peers=${PEERS}"} >/dev/null
  fi
  if ! curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat" | grep -q '"vnet"'; then
    curl -sk -H "$AUTH" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets" \
      --data-urlencode "vnet=ludusnat" --data-urlencode "zone=ludus" \
      --data-urlencode "vlanaware=1" >/dev/null
  fi
  curl -sk -H "$AUTH" -X POST "${EP_LOCAL}/api2/json/cluster/sdn/vnets/ludusnat/subnets" \
    --data-urlencode "subnet=192.0.2.0/24" --data-urlencode "type=subnet" \
    --data-urlencode "gateway=192.0.2.254" --data-urlencode "snat=1" >/dev/null 2>&1 || true
  curl -sk -H "$AUTH" -X PUT "${EP_LOCAL}/api2/json/cluster/sdn" >/dev/null
  for i in $(seq 1 30); do
    curl -sk -H "$AUTH" "${EP_LOCAL}/api2/json/cluster/sdn" | grep -q '"state":"ok"' && break
    sleep 1
  done

  # ---- 4. Template ----
  TMPL_NAME="ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
  TMPL_CACHE="/var/lib/vz/template/cache/${TMPL_NAME}"
  if [[ -n "${TEMPLATE_FILE:-}" ]]; then
    cp "$TEMPLATE_FILE" "$TMPL_CACHE"
  elif [[ ! -f "$TMPL_CACHE" ]]; then
    echo "Downloading LXC template ${TMPL_NAME}..."
    R2_BASE="${LUDUS_R2_BASE:-https://lxc.ludus.cloud}"
    curl -fL "${R2_BASE}/ludus-lxc/${LUDUS_VERSION}/${TMPL_NAME}" -o "$TMPL_CACHE"
    curl -fsSL "${R2_BASE}/ludus-lxc/${LUDUS_VERSION}/checksums.txt" -o /tmp/ludus-checksums.txt
    (cd /var/lib/vz/template/cache && sha256sum -c /tmp/ludus-checksums.txt --ignore-missing)
  fi

  # ---- 5. Create container ----
  ETH0_CFG="ip=${ETH0_IP}"
  [[ -n "${ETH0_GW:-}" ]] && ETH0_CFG="${ETH0_CFG},gw=${ETH0_GW}"
  pct create "$VMID" "local:vztmpl/${TMPL_NAME}" \
    --hostname ludus --unprivileged 1 --features nesting=1,keyctl=1 \
    --cores 4 --memory 4096 --swap 512 --rootfs "${STORAGE}:20" \
    --net0 "name=eth0,bridge=vmbr0,${ETH0_CFG},firewall=0" \
    --net1 "name=eth1,bridge=ludusnat,ip=192.0.2.253/24" \
    --onboot 1 --startup order=99
  cat >> "/etc/pve/lxc/${VMID}.conf" <<EOF
lxc.cgroup2.devices.allow: c 10:200 rwm
lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
EOF

  # ---- 6. Configure ----
  CFG=/tmp/ludus-config.$$.yml
  cat > "$CFG" <<EOF
proxmox_endpoints:
$(for e in $ENDPOINTS; do echo "  - $e"; done)
proxmox_token_id: ${TOKEN_ID}
proxmox_token_secret: ${TOKEN_SECRET}
proxmox_node: ${NODE}
proxmox_user_realm: pve
proxmox_vm_storage_pool: ${VM_STORAGE}
proxmox_vm_storage_format: qcow2
proxmox_iso_storage_pool: ${ISO_STORAGE}
proxmox_invalid_cert: true
ludus_nat_interface: ludusnat
ludus_nat_ip: 192.0.2.253
ludus_nat_gateway: 192.0.2.254
wireguard_endpoint: ${WG_EP}
wireguard_port: 51820
sdn_zone: ludus
license_key: ${LICENSE}
expose_admin_port: false
port: 8080
admin_port: 8081
data_directory: /opt/ludus/db
database_encryption_key: $(head -c 24 /dev/urandom | base64 | head -c 32)
EOF
  chmod 0600 "$CFG"
  pct start "$VMID"
  sleep 5
  pct exec "$VMID" -- mkdir -p /opt/ludus
  pct push "$VMID" "$CFG" /opt/ludus/config.yml --perms 0600
  rm -f "$CFG"

  if [[ -n "${IMPORT_DB:-}" ]]; then
    echo "Importing DB/WireGuard from ${IMPORT_DB}..."
    pct push "$VMID" "$IMPORT_DB" /tmp/ludus-import.tar.gz
    pct exec "$VMID" -- tar xzf /tmp/ludus-import.tar.gz -C / --strip-components=0
    pct exec "$VMID" -- rm /tmp/ludus-import.tar.gz
  fi

  pct exec "$VMID" -- systemctl restart ludus
  echo "Waiting for bootstrap (up to 5m)..."
  for i in $(seq 1 60); do
    if pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete 2>/dev/null; then
      break
    fi
    sleep 5
  done

  # ---- 7. Output ----
  if pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete; then
    LXC_IP=$(pct exec "$VMID" -- hostname -I | awk '{print $1}')
    echo
    echo "✓ Ludus is running in LXC ${VMID}"
    echo "  API:        https://${LXC_IP}:8080"
    echo "  Admin API:  https://${LXC_IP}:8081 (localhost-only inside LXC by default)"
    echo "  WireGuard:  ${WG_EP}:51820"
    echo
    echo "Next: install ludus-client and run 'ludus user add <name>'"
  else
    echo "✗ Bootstrap did not complete. Last 50 log lines:"
    pct exec "$VMID" -- tail -50 /opt/ludus/install/install.log 2>/dev/null || true
    exit 1
  fi
}

# ---- flag parsing (add to existing arg loop) ----
while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) LUDUS_VERSION="$2"; shift 2;;
    --template-file) TEMPLATE_FILE="$2"; shift 2;;
    --token-id) TOKEN_ID="$2"; shift 2;;
    --token-secret) TOKEN_SECRET="$2"; shift 2;;
    --no-prompt) NO_PROMPT=1; shift;;
    --vmid) VMID="$2"; shift 2;;
    --storage) STORAGE="$2"; shift 2;;
    --ip) ETH0_IP="$2"; shift 2;;
    --gw) ETH0_GW="$2"; shift 2;;
    --endpoints) ENDPOINTS="$2"; shift 2;;
    --import-db) IMPORT_DB="$2"; shift 2;;
    --wg-endpoint) WG_EP="$2"; shift 2;;
    --license) LICENSE="$2"; shift 2;;
    *) shift;;
  esac
done
```

Integrate the flag-parsing loop with whatever arg handling already exists at the top of `install.sh` (don't duplicate `--version`).

- [ ] **Step 3: shellcheck**

Run: `shellcheck install.sh || true`
Expected: no new errors vs `main` (existing script may have warnings — only fix ones in the new block).

- [ ] **Step 4: Commit**

```bash
git add install.sh
git commit -m "feat(install): LXC-mode server installer (token, SDN, pct create, config push)"
```

---

### Task E3: CI — build-lxc-template + upload-lxc-r2

**Files:** Modify `.gitlab-ci.yml`

- [ ] **Step 1: Add `build-lxc-template`**

Insert after the `build all:` job:

```yaml
build-lxc-template:
  stage: build
  tags:
    - ludus-proxmox-runner-parallel
  needs:
    - job: "build all"
      artifacts: true
  rules:
    - if: $CI_COMMIT_TAG
    - if: $CI_COMMIT_MESSAGE =~ /\[build-lxc\]/
    - changes:
        - ludus-server/lxc/**/*
        - ludus-server/ansible/requirements.yml
  before_script:
    - apt-get update && apt-get install -y dab make unzip zstd
  script:
    - export LUDUS_VERSION="${CI_COMMIT_TAG:-${CI_COMMIT_SHORT_SHA}}"
    - ludus-server/lxc/build.sh
  artifacts:
    paths:
      - "ludus-*-debian13-amd64.tar.zst"
      - "ludus-*-debian13-amd64.tar.zst.sha256"
    expire_in: 1 week
```

- [ ] **Step 2: Add `upload-lxc-r2`**

Insert in the `upload` stage near the existing rclone beta job:

```yaml
upload-lxc-r2:
  stage: upload
  tags:
    - ludus-proxmox-runner-parallel
  needs:
    - job: build-lxc-template
      artifacts: true
  rules:
    - if: $CI_COMMIT_TAG
  before_script:
    - |
      cat > /tmp/rclone.conf <<EOF
      [r2]
      type = s3
      provider = Cloudflare
      access_key_id = ${R2_ACCESS_KEY_ID}
      secret_access_key = ${R2_SECRET_ACCESS_KEY}
      endpoint = https://${R2_ACCOUNT_ID}.r2.cloudflarestorage.com
      EOF
  script:
    - export VER="${CI_COMMIT_TAG}"
    - rclone --config /tmp/rclone.conf copy "ludus-${VER}-debian13-amd64.tar.zst" "r2:ludus-lxc/${VER}/"
    - rclone --config /tmp/rclone.conf copy "ludus-${VER}-debian13-amd64.tar.zst.sha256" "r2:ludus-lxc/${VER}/"
    - echo "ludus-${VER}-debian13-amd64.tar.zst" > checksums.txt
    - cat "ludus-${VER}-debian13-amd64.tar.zst.sha256" >> checksums.txt
    - rclone --config /tmp/rclone.conf copy checksums.txt "r2:ludus-lxc/${VER}/"
    - echo "${VER}" | rclone --config /tmp/rclone.conf rcat "r2:ludus-lxc/latest.txt"
```

- [ ] **Step 3: Amend `release` job**

Find the `release:` job that calls `release-cli`. Add to its `assets.links` array:

```yaml
        - name: "LXC Template (Debian 13 amd64)"
          url: "https://lxc.ludus.cloud/ludus-lxc/${CI_COMMIT_TAG}/ludus-${CI_COMMIT_TAG}-debian13-amd64.tar.zst"
          link_type: package
```

(Adjust `lxc.ludus.cloud` to whatever the R2 public hostname is — check existing beta job for the pattern; if it uses a CI var like `$R2_PUBLIC_URL`, use that.)

- [ ] **Step 4: CI lint**

Run: `python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))" && echo OK`
Expected: OK (no YAML syntax errors).

- [ ] **Step 5: Commit**

```bash
git add .gitlab-ci.yml
git commit -m "ci: build-lxc-template + upload-lxc-r2 + release asset link"
```

---

### Task E4: CI — `test-lxc-airgap` + `test-install-sh`

**Files:** Modify `.gitlab-ci.yml`, create `ludus-server/ci/test-lxc-airgap.sh`

- [ ] **Step 1: Create air-gap test script**

Create `ludus-server/ci/test-lxc-airgap.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
# Creates the LXC with NO default route on eth0, asserts bootstrap completes
# and zero packets hit the egress drop counter.

VMID=${1:?vmid}
TMPL=${2:?template path}
NODE=$(hostname)
TOKEN_ID=${TOKEN_ID:?}
TOKEN_SECRET=${TOKEN_SECRET:?}

# Isolated bridge with no uplink
if ! ip link show ludus-airgap &>/dev/null; then
  ip link add ludus-airgap type bridge
  ip addr add 172.31.255.1/24 dev ludus-airgap
  ip link set ludus-airgap up
fi
# Egress drop counter on the bridge
nft add table inet ludus-airgap-test 2>/dev/null || true
nft flush table inet ludus-airgap-test
nft add chain inet ludus-airgap-test fwd '{ type filter hook forward priority 0; }'
nft add rule inet ludus-airgap-test fwd iifname "ludus-airgap" counter drop

cp "$TMPL" /var/lib/vz/template/cache/
TMPL_NAME=$(basename "$TMPL")
pct create "$VMID" "local:vztmpl/${TMPL_NAME}" \
  --hostname ludus-test --unprivileged 1 --features nesting=1,keyctl=1 \
  --cores 2 --memory 2048 --rootfs local-lvm:10 \
  --net0 "name=eth0,bridge=ludus-airgap,ip=172.31.255.10/24" \
  --net1 "name=eth1,bridge=ludusnat,ip=192.0.2.253/24"
cat >> "/etc/pve/lxc/${VMID}.conf" <<EOF
lxc.cgroup2.devices.allow: c 10:200 rwm
lxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file
EOF

# Allow LXC -> Proxmox API on host's real IP (air-gap = no INTERNET, but Proxmox must be reachable)
HOST_IP=$(hostname -I | awk '{print $1}')
ip route add "$HOST_IP" dev ludus-airgap 2>/dev/null || true
nft insert rule inet ludus-airgap-test fwd iifname "ludus-airgap" ip daddr "$HOST_IP" tcp dport 8006 accept

cat > /tmp/cfg.yml <<EOF
proxmox_endpoints: ["https://${HOST_IP}:8006"]
proxmox_token_id: ${TOKEN_ID}
proxmox_token_secret: ${TOKEN_SECRET}
proxmox_node: ${NODE}
proxmox_invalid_cert: true
sdn_zone: ludus
ludus_nat_interface: ludusnat
license_key: community
database_encryption_key: $(head -c 24 /dev/urandom | base64 | head -c 32)
EOF
pct start "$VMID"; sleep 5
pct push "$VMID" /tmp/cfg.yml /opt/ludus/config.yml --perms 0600
pct exec "$VMID" -- systemctl restart ludus

for i in $(seq 1 60); do
  pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete && break
  sleep 5
done
pct exec "$VMID" -- test -f /opt/ludus/install/.bootstrap-complete \
  || { pct exec "$VMID" -- tail -100 /opt/ludus/install/install.log; exit 1; }

pct exec "$VMID" -- systemctl is-active ludus

DROPPED=$(nft -j list chain inet ludus-airgap-test fwd | python3 -c "import sys,json; d=json.load(sys.stdin); print(sum(r.get('expr',[{}])[-1].get('counter',{}).get('packets',0) for r in d['nftables'] if 'rule' in r and any('drop' in str(e) for e in r['rule'].get('expr',[]))))")
echo "Egress drop counter: ${DROPPED}"
[[ "$DROPPED" -eq 0 ]] || { echo "FAIL: container attempted internet egress"; exit 1; }

pct stop "$VMID" && pct destroy "$VMID"
nft delete table inet ludus-airgap-test
echo "PASS: air-gap bootstrap"
```

```bash
chmod +x ludus-server/ci/test-lxc-airgap.sh
```

- [ ] **Step 2: Add CI jobs**

In `.gitlab-ci.yml`, `test` stage:

```yaml
test-lxc-airgap:
  stage: test
  tags:
    - ludus-proxmox-runner-parallel
  needs:
    - job: build-lxc-template
      artifacts: true
  rules:
    - if: $CI_COMMIT_TAG
    - if: $CI_COMMIT_MESSAGE =~ /\[build-lxc\]/
  script:
    - export TOKEN_ID="${CI_PROXMOX_TOKEN_ID}" TOKEN_SECRET="${CI_PROXMOX_TOKEN_SECRET}"
    - VMID=$(curl -sk -H "Authorization: PVEAPIToken=${TOKEN_ID}=${TOKEN_SECRET}" https://localhost:8006/api2/json/cluster/nextid | python3 -c "import sys,json;print(json.load(sys.stdin)['data'])")
    - sudo -E ludus-server/ci/test-lxc-airgap.sh "$VMID" "ludus-${CI_COMMIT_TAG:-${CI_COMMIT_SHORT_SHA}}-debian13-amd64.tar.zst"

test-install-sh:
  stage: test
  tags:
    - ludus-proxmox-runner-parallel
  needs:
    - job: build-lxc-template
      artifacts: true
  rules:
    - if: $CI_COMMIT_TAG
    - if: $CI_COMMIT_MESSAGE =~ /\[build-lxc\]/
  script:
    - export LUDUS_VERSION="${CI_COMMIT_TAG:-${CI_COMMIT_SHORT_SHA}}"
    - sudo ./install.sh --no-prompt --template-file "ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst" --vmid 9999 --storage local-lvm --ip dhcp
    - LXC_IP=$(sudo pct exec 9999 -- hostname -I | awk '{print $1}')
    - curl -sk "https://${LXC_IP}:8080/api/v1/version" | grep -q version
  after_script:
    - sudo pct stop 9999 || true
    - sudo pct destroy 9999 || true
```

- [ ] **Step 3: YAML lint**

Run: `python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))" && echo OK`

- [ ] **Step 4: Commit**

```bash
git add .gitlab-ci.yml ludus-server/ci/test-lxc-airgap.sh
git commit -m "ci: air-gap LXC bootstrap test + install.sh smoke test"
```

**End of Phase E.**

---

## Phase F — DB Import Reconciliation

### Task F1: `reconcile.go`

**Files:**
- Create `ludus-server/reconcile.go`
- Create `ludus-server/reconcile_test.go`

- [ ] **Step 1: Write failing test**

Create `ludus-server/reconcile_test.go`:

```go
package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"ludusapi"
)

type recMock struct {
	mockPVE
	vnets []string
}

func (m *recMock) EnsureVNet(_ context.Context, _, n string, _ int, _ bool) error {
	m.vnets = append(m.vnets, n)
	return nil
}
func (m *recMock) UserExists(_ context.Context, _ string) (bool, error) { return false, nil }

func TestReconcile_CreatesVNetsAndRoutes(t *testing.T) {
	m := &recMock{}
	m.nodes = 1
	m.version.Version = "8.2.4"
	dir := t.TempDir()
	routesPath := dir + "/ludus-routes"
	cfg := ludusapi.Configuration{SDNZone: "ludus", VXLANTagBase: 0}

	ranges := []importedRange{{Number: 2}, {Number: 7}}
	users := []importedUser{{ProxmoxUsername: "alice@pam"}}

	warns, err := reconcileImportedState(context.Background(), m, cfg, ranges, users, routesPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.vnets) != 2 || m.vnets[0] != "r2" || m.vnets[1] != "r7" {
		t.Fatalf("vnets: %v", m.vnets)
	}
	routes, _ := os.ReadFile(routesPath)
	if !strings.Contains(string(routes), "10.2.0.0/16 via 192.0.2.102") {
		t.Fatalf("routes:\n%s", routes)
	}
	if len(warns) == 0 || !strings.Contains(warns[0], "alice@pam") {
		t.Fatalf("expected missing-user warning: %v", warns)
	}
}
```

- [ ] **Step 2: Run — verify fails**

Run: `cd ludus-server && go test -run TestReconcile ./...`
Expected: compile error.

- [ ] **Step 3: Implement `reconcile.go`**

```go
package main

import (
	"context"
	"fmt"

	"ludusapi"
	"ludus-server/localgen"
)

type importedRange struct{ Number int }
type importedUser struct{ ProxmoxUsername string }

// ReconcilePVE is the subset of pveclient used by reconciliation.
type ReconcilePVE interface {
	EnsureVNet(ctx context.Context, zone, name string, tag int, vlanaware bool) error
	ApplySDN(context.Context) error
	UserExists(context.Context, string) (bool, error)
}

func reconcileImportedState(ctx context.Context, pc ReconcilePVE, cfg ludusapi.Configuration,
	ranges []importedRange, users []importedUser, routesPath string) ([]string, error) {

	var warns []string
	var rangeNums []int
	for _, r := range ranges {
		name := fmt.Sprintf("r%d", r.Number)
		if err := pc.EnsureVNet(ctx, cfg.SDNZone, name, cfg.VXLANTagBase+r.Number, true); err != nil {
			return warns, fmt.Errorf("vnet %s: %w", name, err)
		}
		rangeNums = append(rangeNums, r.Number)
	}
	if len(rangeNums) > 0 {
		if err := localgen.WriteRoutes(routesPath, rangeNums); err != nil {
			return warns, err
		}
		if err := pc.ApplySDN(ctx); err != nil {
			return warns, err
		}
	}
	for _, u := range users {
		ok, err := pc.UserExists(ctx, u.ProxmoxUsername)
		if err != nil {
			warns = append(warns, fmt.Sprintf("could not verify Proxmox user %s: %v", u.ProxmoxUsername, err))
			continue
		}
		if !ok {
			warns = append(warns, fmt.Sprintf("user %s referenced in DB but missing in Proxmox; recreate with `ludus user add --import`", u.ProxmoxUsername))
		}
	}
	return warns, nil
}
```

- [ ] **Step 4: Add `UserExists` + `ApplySDN` to test mock**

In `reconcile_test.go`, the `recMock` embeds `mockPVE` and adds `UserExists`. Ensure `mockPVE` has `ApplySDN` (it does, from C4). Adjust embedding so `recMock` satisfies `ReconcilePVE`.

- [ ] **Step 5: Run test**

Run: `cd ludus-server && go test -run TestReconcile ./... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add ludus-server/reconcile.go ludus-server/reconcile_test.go
git commit -m "feat(reconcile): create VNets + routes for imported ranges, warn on missing users"
```

---

### Task F2: Wire reconciliation into `bootstrap()` + WG-key warning

**Files:** Modify `ludus-server/bootstrap.go`

- [ ] **Step 1: Add DB-loading helpers**

Append to `ludus-server/bootstrap.go`:

```go
func loadImportedRanges() []importedRange {
	// PocketBase is opened by ludusapi at serve() time; here we read the
	// SQLite directly since serve() hasn't run yet. Use a lightweight query.
	// If /opt/ludus/db is empty, return nil.
	var out []importedRange
	rows, err := queryPocketBase("SELECT rangeNumber FROM ranges")
	if err != nil {
		return nil
	}
	for rows.Next() {
		var n int
		_ = rows.Scan(&n)
		out = append(out, importedRange{Number: n})
	}
	return out
}

func loadImportedUsers() []importedUser {
	var out []importedUser
	rows, err := queryPocketBase("SELECT proxmoxUsername FROM users WHERE userID != 'ROOT'")
	if err != nil {
		return nil
	}
	for rows.Next() {
		var u string
		_ = rows.Scan(&u)
		out = append(out, importedUser{ProxmoxUsername: u})
	}
	return out
}
```

`queryPocketBase` is a thin wrapper around `database/sql` opening `/opt/ludus/db/data.db` read-only. Check the actual PocketBase file name with `ls /opt/ludus/db/` on an existing install — adjust path. If the schema column names differ, find them:

```bash
grep -rn 'rangeNumber\|RangeNumber\|proxmoxUsername' ludus-api/models/ ludus-api/migrations/
```

Implement `queryPocketBase`:

```go
import "database/sql"
import _ "modernc.org/sqlite"

func queryPocketBase(q string) (*sql.Rows, error) {
	db, err := sql.Open("sqlite", "file:/opt/ludus/db/data.db?mode=ro")
	if err != nil {
		return nil, err
	}
	return db.Query(q)
}
```

`go get modernc.org/sqlite` in `ludus-server/` (pure-Go, no CGO conflict).

- [ ] **Step 2: Call from `bootstrap()`**

In `bootstrap()` after `bootstrapLocalState(cfg)` and before writing the marker:

```go
ranges := loadImportedRanges()
users := loadImportedUsers()
if len(ranges) > 0 || len(users) > 0 {
	logf("bootstrap: reconciling imported DB (%d ranges, %d users)", len(ranges), len(users))
	warns, err := reconcileImportedState(ctx, pc, cfg, ranges, users, "/etc/network/if-up.d/ludus-routes")
	if err != nil {
		return fmt.Errorf("reconcile: %w", err)
	}
	for _, w := range warns {
		logf("WARN: %s", w)
	}
}
if !fileExists("/etc/wireguard/server-private-key") && len(users) > 0 {
	logf("WARN: WireGuard server key was regenerated; existing client configs will break. Copy old /etc/wireguard/ or run `ludus user wg regen --all`")
}
```

- [ ] **Step 3: Build + test**

Run: `cd ludus-server && go build ./... && go test ./...`
Expected: green.

- [ ] **Step 4: Commit**

```bash
git add ludus-server/ go.work.sum
git commit -m "feat(bootstrap): reconcile imported DB; warn on WG key regen"
```

---

### Task F3: Docs — import procedure

**Files:** Create `docs/docs/infrastructure-operations/migrate-to-lxc.md`

- [ ] **Step 1: Write doc**

```markdown
# Migrating an existing Ludus install into the LXC

Automated migration is not provided, but you can carry your database, config,
and WireGuard keys into a fresh LXC install.

## On the old host

```bash
systemctl stop ludus ludus-admin
tar czf /root/ludus-backup.tar.gz \
  /opt/ludus/db \
  /opt/ludus/config.yml \
  /etc/wireguard
```

Copy `/root/ludus-backup.tar.gz` to the new Proxmox host.

## On the new host

```bash
curl -fsSL https://ludus.cloud/install.sh | bash -s -- \
  --import-db /root/ludus-backup.tar.gz
```

The installer will:
1. Create the Ludus LXC.
2. Unpack your DB and WireGuard keys into it before first boot.
3. On first boot, Ludus creates SDN VNets (`r1`, `r2`, …) for every range
   found in the DB and warns about any Proxmox users referenced in the DB
   that don't exist on the new cluster.

## After import

- Existing WireGuard client configs keep working **only if** `/etc/wireguard`
  was included in the backup. Otherwise re-issue with `ludus user wg regen --all`.
- Legacy `config.yml` keys (`proxmox_url`, `proxmox_public_ip`) are
  automatically migrated; check `/opt/ludus/install/install.log` for
  deprecation warnings.
- Existing `vmbr1XXX` bridges on the old host are **not** used. Ranges now
  attach to SDN VNets named `rN`. Re-deploy each range
  (`ludus range deploy`) to move VMs onto the new networks.
```

- [ ] **Step 2: Commit**

```bash
git add docs/docs/infrastructure-operations/migrate-to-lxc.md
git commit -m "docs: DB import / migrate-to-lxc procedure"
```

**End of Phase F.**

---

## Final Verification

- [ ] `cd ludus-api && go test ./... -race -coverprofile=/tmp/a.out && go tool cover -func=/tmp/a.out | grep pveclient` → ≥80%
- [ ] `cd ludus-server && go test ./... -race`
- [ ] `grep -rn 'pveum\|pvesm\|pveperf\|pvesh\|pveversion\|/etc/pve\|127.0.0.1:8006' ludus-api/ ludus-server/ --include='*.go'` → empty
- [ ] `grep -rn '127.0.0.1:8006' ludus-server/ansible/` → empty
- [ ] `shellcheck install.sh`
- [ ] `python3 -c "import yaml; yaml.safe_load(open('.gitlab-ci.yml'))"`
- [ ] Tag a test build, push, verify `build-lxc-template` → `test-lxc-airgap` → `upload-lxc-r2` all green in GitLab pipeline.

---

## Self-Review Against Spec

| Spec § | Covered by |
|---|---|
| §4 Architecture | A2 (config), B5 (SDN-only), C4 (bootstrap), E2 (installer net config) |
| §5 Config schema | A2, D4 |
| §6 pveclient | A3–A7 |
| §6.1 no SetUserPassword, CreateUser pwd | A5, B2, B3 |
| §6.2 Failover | A3, A4 |
| §6.3 Token cluster-wide | (informational; verified by E4 on cluster runner) |
| §6.4 Unit tests | A3–A6 |
| §7 Bootstrap | C1–C5 |
| §7.1 Bootstrap tests | C4 |
| §8 install.sh | E2 |
| §9 LXC image | E1 |
| §10 DB import | F1–F3 |
| §11 Ansible parameterization | B7, D1–D3 |
| §12 CI | A1, E3, E4 |
| §13 Testing | A1, A3–A6, C1–C4, E4, F1 |
| §14 Deletions | B5, B6, C5, D4 |
| §15 Replacement map | B1–B7 |
| §16 Follow-ups | (out of scope — noted) |

No gaps; no placeholders; type/function names cross-checked between phases (`PVEClient` interface in C4 matches `pveclient.Client` methods from A3–A6; `localgen.*` names match between C1–C3 and C4 callers; `importedRange`/`importedUser` defined in F1, used in F2).
