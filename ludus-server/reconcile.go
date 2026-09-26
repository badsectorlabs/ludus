package main

import (
	"context"
	"fmt"

	"ludusapi"
	"ludusapi/pveclient"

	"ludus-server/localgen"
)

type importedRange struct{ Number int }
type importedUser struct{ ProxmoxUsername string }

// ReconcilePVE is the subset of pveclient used by reconciliation.
type ReconcilePVE interface {
	SDNZoneType(ctx context.Context, name string) (string, error)
	EnsureVNet(ctx context.Context, zone, name string, tag int, vlanaware bool) error
	ApplySDN(context.Context) error
	UserExists(context.Context, string) (bool, error)
}

var _ ReconcilePVE = (*pveclient.Client)(nil)

func reconcileImportedState(ctx context.Context, pc ReconcilePVE, cfg ludusapi.Configuration,
	ranges []importedRange, users []importedUser, routesPath string) ([]string, error) {

	var warns []string
	var rangeNums []int
	if len(ranges) > 0 {
		zoneType, err := pc.SDNZoneType(ctx, cfg.SDNZone)
		if err != nil {
			return warns, fmt.Errorf("sdn zone type: %w", err)
		}
		for _, r := range ranges {
			name := fmt.Sprintf("r%d", r.Number)
			tag, vlanaware := ludusapi.RangeVNetOptionsForZone(zoneType, cfg.VXLANTagBase, r.Number)
			if err := pc.EnsureVNet(ctx, cfg.SDNZone, name, tag, vlanaware); err != nil {
				return warns, fmt.Errorf("vnet %s: %w", name, err)
			}
			rangeNums = append(rangeNums, r.Number)
		}
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
