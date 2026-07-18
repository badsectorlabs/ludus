package pveclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
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
	rawHTTP   *http.Client
	raw       *goproxmox.Client // failover-aware go-proxmox client
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
		c.mu.RLock()
		rawHTTP := c.rawHTTP
		c.mu.RUnlock()
		if rawHTTP != nil {
			rawHTTP.CloseIdleConnections()
		}
	})
}

func (c *Client) ActiveEndpoint() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.endpoints[c.activeIdx].url
}

// Raw returns the underlying go-proxmox client. Its transport rewrites requests
// to the active endpoint and retries once on gateway failures.
func (c *Client) Raw() *goproxmox.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.raw
}

func (c *Client) rebuildRaw() {
	oldRawHTTP := c.rawHTTP
	c.rawHTTP = &http.Client{
		Timeout: c.cfg.Timeout,
		Transport: &failoverTransport{
			client: c,
			transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: c.cfg.InsecureTLS},
			},
		},
	}
	c.raw = goproxmox.NewClient(c.endpoints[c.activeIdx].url+"/api2/json",
		goproxmox.WithHTTPClient(c.rawHTTP),
		goproxmox.WithAPIToken(c.cfg.TokenID, c.cfg.TokenSecret),
	)
	if oldRawHTTP != nil {
		oldRawHTTP.CloseIdleConnections()
	}
}

type failoverTransport struct {
	client    *Client
	transport http.RoundTripper
}

func (t *failoverTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var bodyBytes []byte
	if req.Body != nil && req.GetBody == nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
	}

	for attempt := 0; attempt < 2; attempt++ {
		t.client.mu.RLock()
		base := t.client.endpoints[t.client.activeIdx].url
		t.client.mu.RUnlock()

		outReq, err := cloneRequestForEndpoint(req, base, bodyBytes)
		if err != nil {
			return nil, err
		}

		resp, err := t.transport.RoundTrip(outReq)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		if !shouldFailover(err, status) {
			return resp, err
		}
		if attempt == 1 {
			if resp != nil {
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				return nil, fmt.Errorf("proxmox gateway failure after failover: %d", status)
			}
			return nil, err
		}
		if resp != nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		t.client.advance(base)
	}
	return nil, errors.New("pveclient: unreachable") // not hit
}

func cloneRequestForEndpoint(req *http.Request, endpoint string, bodyBytes []byte) (*http.Request, error) {
	endpointURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	outReq := req.Clone(req.Context())
	outURL := *req.URL
	outURL.Scheme = endpointURL.Scheme
	outURL.Host = endpointURL.Host
	outReq.URL = &outURL

	if req.Body != nil {
		if bodyBytes != nil {
			outReq.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			outReq.ContentLength = int64(len(bodyBytes))
		} else {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			outReq.Body = body
		}
	}
	return outReq, nil
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
			rdr = bytes.NewReader(bodyBytes)
		}
		c.mu.RLock()
		base := c.endpoints[c.activeIdx].url
		c.mu.RUnlock()

		req, err := http.NewRequestWithContext(ctx, method, base+path, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", c.authHeader())
		if rdr != nil {
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
		c.advance(base)
	}
	return errors.New("pveclient: unreachable") // not hit
}

func (c *Client) advance(failedURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.endpoints[c.activeIdx].url != failedURL {
		// Another goroutine already failed over; don't double-mark.
		return
	}
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

// Builder is satisfied by ludusapi.Configuration; avoids import cycle.
type Builder interface {
	PVEClientConfig() Config
}

// FromBuilder constructs a Client from anything that knows how to produce a Config.
func FromBuilder(b Builder) (*Client, error) {
	return New(b.PVEClientConfig())
}
