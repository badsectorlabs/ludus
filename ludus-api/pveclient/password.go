package pveclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrPasswordAuthentication = errors.New("Proxmox rejected the stored password; restore valid credentials before rotating it")
	ErrPasswordMFA            = errors.New("Proxmox requires additional authentication; unattended password rotation is unavailable for this account")
	ErrPasswordRejected       = errors.New("Proxmox rejected the password change")
	ErrPasswordUnknown        = errors.New("the Proxmox password change outcome is unknown")
	ErrPasswordUnsupported    = errors.New("unsupported Proxmox password rotation")
)

// PasswordSession is a short-lived, node-pinned user session. Password updates
// forbid API tokens and must never use the generic PUT failover/retry transport.
type PasswordSession struct {
	client                                   *Client
	endpoint, userID, password, ticket, csrf string
}

// PasswordEndpoint selects the node before recording a rotation. PVE passwords
// are cluster-wide; PAM passwords are local to each node. A cluster-wide PAM
// change needs a separate, per-node workflow, so reject it before changing any node.
func (c *Client) PasswordEndpoint(ctx context.Context, realm string) (string, error) {
	endpoint := c.ActiveEndpoint()
	switch realm {
	case "pve":
		return endpoint, nil
	case "pam":
		data, _, err := c.passwordRequest(ctx, endpoint, http.MethodGet, "/cluster/status", nil, nil, true)
		if err != nil {
			return "", errors.New("cannot determine Proxmox cluster membership for PAM password rotation")
		}
		var status apiResp[[]clusterStatusItem]
		if json.Unmarshal(data, &status) != nil {
			return "", errors.New("invalid Proxmox cluster membership response")
		}
		count := 0
		for _, node := range status.Data {
			if node.Type == "node" {
				count++
			}
		}
		if count != 1 {
			return "", fmt.Errorf("%w: PAM requires a single-node Proxmox host; multi-node PAM accounts must be updated on every node", ErrPasswordUnsupported)
		}
		return endpoint, nil
	default:
		return "", fmt.Errorf("%w: only the pve and pam realms are supported", ErrPasswordUnsupported)
	}
}

// AuthenticatePassword always performs a fresh password login, never a ticket
// renewal. No ticket, password, response body, or request URL is logged in errors.
func (c *Client) AuthenticatePassword(ctx context.Context, endpoint, userID, password string) (*PasswordSession, error) {
	data, status, err := c.passwordRequest(ctx, endpoint, http.MethodPost, "/access/ticket",
		url.Values{"username": {userID}, "password": {password}}, nil, false)
	if status == http.StatusUnauthorized {
		return nil, ErrPasswordAuthentication
	}
	if err != nil {
		return nil, err
	}
	var result apiResp[struct {
		Username string `json:"username"`
		Ticket   string `json:"ticket"`
		CSRF     string `json:"CSRFPreventionToken"`
	}]
	if json.Unmarshal(data, &result) != nil {
		return nil, errors.New("invalid Proxmox password authentication response")
	}
	if strings.Contains(result.Data.Ticket, "!tfa!") || result.Data.CSRF == "" {
		return nil, ErrPasswordMFA
	}
	if result.Data.Username != userID || !strings.HasPrefix(result.Data.Ticket, "PVE:") {
		return nil, errors.New("Proxmox did not issue a full user authentication ticket")
	}
	return &PasswordSession{client: c, endpoint: endpoint, userID: userID, password: password,
		ticket: result.Data.Ticket, csrf: result.Data.CSRF}, nil
}

// ChangePassword submits exactly once. Any ambiguous outcome must be reconciled
// with a fresh login using the new password, not by replaying the mutation.
func (s *PasswordSession) ChangePassword(ctx context.Context, password string) error {
	_, status, err := s.client.passwordRequest(ctx, s.endpoint, http.MethodPut, "/access/password",
		url.Values{"userid": {s.userID}, "password": {password}, "confirmation-password": {s.password}}, s, false)
	if err == nil {
		return nil
	}
	switch status {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return ErrPasswordRejected
	default:
		return ErrPasswordUnknown
	}
}

func (c *Client) passwordRequest(ctx context.Context, endpoint, method, path string, form url.Values, session *PasswordSession, token bool) ([]byte, int, error) {
	c.mu.RLock()
	configured := false
	for _, candidate := range c.endpoints {
		if candidate.url == endpoint {
			configured = true
			break
		}
	}
	c.mu.RUnlock()
	if !configured {
		return nil, 0, errors.New("the password rotation endpoint is no longer configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint+"/api2/json"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, errors.New("cannot create Proxmox password request")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if session != nil {
		req.AddCookie(&http.Cookie{Name: "PVEAuthCookie", Value: session.ticket})
		req.Header.Set("CSRFPreventionToken", session.csrf)
	}
	if token {
		req.Header.Set("Authorization", c.authHeader())
	}
	// Redirects can forward credentials to another host or node. Disable them
	// without modifying the client's shared HTTP configuration.
	httpc := *c.httpc
	httpc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, 0, errors.New("Proxmox password request could not be completed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("Proxmox password request failed (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, resp.StatusCode, errors.New("cannot read Proxmox password response")
	}
	return data, resp.StatusCode, nil
}
