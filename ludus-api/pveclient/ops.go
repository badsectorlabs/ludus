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
