package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

func checkAndCreateNetworkAccessRole() error {
	// Check if NetworkAccess role exists
	roleCheck := Run("pveum role list --output-format json | jq .[].roleid | grep AccessNetwork", false, false)
	if roleCheck == "Command processed (no output)." {
		// Create NetworkAccess role with Sys.AccessNetwork privilege
		log.Println("Creating NetworkAccess role with Sys.AccessNetwork privilege")
		Run("pveum role add AccessNetwork --privs \"Sys.AccessNetwork\"", false, true)

		// Get hostname for path
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("error getting hostname: %v", err)
		}
		log.Println("Adding ludus_users to NetworkAccess role to allow direct PVE download of ISOs")
		Run(fmt.Sprintf("pveum acl modify /nodes/%s -group ludus_users -role AccessNetwork", hostname), false, false)
		Run(fmt.Sprintf("pveum acl modify /nodes/%s -group ludus_users -role ResourceAudit", hostname), false, false)
	}

	// Admin users need Pool.Audit to query the API to get VMs in the ADMIN pool
	adminPoolCheck := Run("pveum acl list --output-format json | jq -e 'any( (.ugid == \"ludus_admins\") and (.roleid == \"PVEPoolAdmin\") and (.path == \"/pool/ADMIN\") )'", false, false)
	if strings.Trim(adminPoolCheck, "\n") == "false" {
		log.Println("Adding PVEPoolAdmin role to ludus_admins to allow querying the API to get VMs in the ADMIN pool")
		Run("pveum acl modify /pool/ADMIN -group ludus_admins -role PVEPoolAdmin", false, false)
	}
	return nil
}
