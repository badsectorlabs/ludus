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
	if len(groups) == 0 && len(users) == 0 {
		return fmt.Errorf("EnsureACL: at least one group or user required")
	}
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
			// Proxmox subnet IDs are "<zone>-<ip>-<prefix>", e.g. "ludus-192.0.2.0-24".
			// Anchor on the dash-prefixed suffix to avoid "/1" matching "/10".
			if strings.HasSuffix(s.Subnet, "-"+strings.ReplaceAll(cidr, "/", "-")) {
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
