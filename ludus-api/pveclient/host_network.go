package pveclient

import (
	"context"
	"fmt"
)

type existingVNet struct {
	Name      string `json:"vnet"`
	Zone      string `json:"zone"`
	VLANAware int    `json:"vlanaware"`
}

func (c *Client) existingVNet(ctx context.Context, zone, name string, vlanaware bool) (bool, error) {
	var response apiResp[[]existingVNet]
	if err := c.do(ctx, "GET", "/api2/json/cluster/sdn/vnets", nil, &response); err != nil {
		return false, err
	}
	for _, vnet := range response.Data {
		if vnet.Name != name {
			continue
		}
		if vnet.Zone != zone {
			return false, fmt.Errorf("host-managed VNet %s belongs to zone %s, not %s", name, vnet.Zone, zone)
		}
		if vlanaware && vnet.VLANAware != 1 {
			return false, fmt.Errorf("host-managed range VNet %s must remain VLAN-aware", name)
		}
		return true, nil
	}
	return false, nil
}

// RequireVNet validates an installer-owned network without changing live VLANs,
// VNI tags or cluster-wide gateway configuration.
func (c *Client) RequireVNet(ctx context.Context, zone, name string, vlanaware bool) error {
	found, err := c.existingVNet(ctx, zone, name, vlanaware)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("host-managed VNet %s is missing from zone %s", name, zone)
	}
	return nil
}

// EnsureVNetPreservingTag creates new range networks but never retags an existing
// migrated network while workload VMs are attached to it.
func (c *Client) EnsureVNetPreservingTag(ctx context.Context, zone, name string, tag int, vlanaware bool) error {
	found, err := c.existingVNet(ctx, zone, name, vlanaware)
	if err != nil || found {
		return err
	}
	return c.EnsureVNet(ctx, zone, name, tag, vlanaware)
}
