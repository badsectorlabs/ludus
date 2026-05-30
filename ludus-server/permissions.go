package main

import (
	"context"

	"ludusapi/pveclient"
)

// migratePermissions is called from update.go on upgrade paths to ensure Proxmox
// roles/groups/pools/ACLs/SDN match what the current Ludus version expects.
// Permissions are now ensured by bootstrap() via pveclient.Ensure*; this is a
// thin re-run of the same idempotent calls against the live cluster.
func migratePermissions() error {
	loadConfig()
	pc, err := pveclient.New(config.PVEClientConfig())
	if err != nil {
		return err
	}
	defer pc.Close()
	return bootstrapProxmoxObjects(context.Background(), pc, config, ludusInstallPath)
}
