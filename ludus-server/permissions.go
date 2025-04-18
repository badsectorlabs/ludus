package main

import (
	"fmt"
	"os"
)

func checkAndCreateNetworkAccessRole() error {
	// Check if NetworkAccess role exists
	roleCheck := Run("pveum role list | grep AccessNetwork", false, false)
	if roleCheck == "Command processed (no output)." {
		// Create NetworkAccess role with Sys.AccessNetwork privilege
		Run("pveum role add AccessNetwork --privs \"Sys.AccessNetwork\"", false, true)
	}

	// Get hostname for path
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("error getting hostname: %v", err)
	}

	Run(fmt.Sprintf("pveum acl modify /nodes/%s -group ludus_users -role AccessNetwork", hostname), false, false)
	Run(fmt.Sprintf("pveum acl modify /nodes/%s -group ludus_users -role ResourceAudit", hostname), false, false)
	return nil
}
