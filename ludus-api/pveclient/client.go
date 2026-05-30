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
	closeOnce sync.Once
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
	c.closeOnce.Do(func() {
		close(c.stopProbe)
		c.httpc.CloseIdleConnections()
	})
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
			// Snapshot unhealthy endpoints under read lock.
			c.mu.RLock()
			var toProbe []int
			for i := range c.endpoints {
				if !c.endpoints[i].healthy {
					toProbe = append(toProbe, i)
				}
			}
			c.mu.RUnlock()
			if len(toProbe) == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			for _, i := range toProbe {
				if c.probe(ctx, i) {
					c.mu.Lock()
					c.endpoints[i].healthy = true
					c.mu.Unlock()
					c.log.Info("pveclient: endpoint restored", "url", c.endpoints[i].url)
				}
			}
			cancel()
		}
	}
}

// shouldFailover returns true for transport errors and gateway HTTP codes.
func shouldFailover(err error, status int) bool {
	if err != nil {
		// http.Client.Do wraps transport errors in *url.Error, which
		// implements net.Error; this also covers context.DeadlineExceeded
		// and *net.OpError.
		var ne net.Error
		return errors.As(err, &ne)
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
		var respBody string
		if resp != nil {
			status = resp.StatusCode
			b, _ := io.ReadAll(resp.Body)
			respBody = string(b)
			resp.Body.Close()
		}
		if !shouldFailover(err, status) || attempt == 1 {
			if err != nil {
				return err
			}
			return fmt.Errorf("proxmox %s %s: %d: %s", method, path, status, respBody)
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
