package ludusapi

import (
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
)

type AllowPayload struct {
	Domains []string `json:"domains,omitempty"`
	Ips     []string `json:"ips,omitempty"`
}

func removeEmptyStrings(s []string) []string {
	var r []string
	for _, str := range s {
		if str != "" {
			r = append(r, str)
		}
	}
	return r
}

// Allow - allows the domain and CRL domains and the ips they resolve to
func Allow(c *gin.Context) {

	var thisAllowPayload AllowPayload
	var returnArray []string

	type errorStruct struct {
		Item   string `json:"item"`
		Reason string `json:"reason"`
	}

	var errorArray []errorStruct

	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	if !usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "testing not enabled for range " + usersRange.UserID})
		return
	}

	err = c.BindJSON(&thisAllowPayload)
	if err != nil {
		c.JSON(http.StatusNoContent, gin.H{"error": "improperly formatted allow payload"})
		return
	}

	allowDomains := removeEmptyStrings(thisAllowPayload.Domains)
	allowIPs := removeEmptyStrings(thisAllowPayload.Ips)
	for _, domain := range allowDomains {
		// First, check if this domain is already allowed
		// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
		if containsSubstring(usersRange.AllowedDomains, domain+" (") {
			errorArray = append(errorArray, errorStruct{domain, "already allowed"})
		} else {
			// This is a new domain to allow, so allow it
			domainIP, err := GetIPFromDomain(domain)
			if err != nil {
				errorArray = append(errorArray, errorStruct{domain, err.Error()})
				continue
			}
			extraVars := map[string]interface{}{"domain": domain, "domainIP": domainIP}
			output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-domain", false)
			if err != nil {
				errorArray = append(errorArray, errorStruct{domain, output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			usersRange.AllowedDomains = append(usersRange.AllowedDomains, fmt.Sprintf("%s (%s)", domain, domainIP))
			db.Save(&usersRange)
			returnArray = append(returnArray, domain)

			// Get all the CRL domains for this domain and allow those too
			// We could do this with a recursive function and bool flag but its only a few extra duplicates lines
			crlDomains := getCRLDomainsFromDomain(domain)
			for _, crlDomain := range crlDomains {
				// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
				if containsSubstring(usersRange.AllowedDomains, crlDomain+" (") {
					continue // Don't return the CRL domain in an "error" to the client as they didn't ask for it to be allowed
				} else {
					domainIP, err := GetIPFromDomain(crlDomain)
					if err != nil {
						errorArray = append(errorArray, errorStruct{crlDomain, err.Error()})
						continue
					}
					extraVars := map[string]interface{}{"domain": crlDomain, "domainIP": domainIP}
					output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-domain", false)
					if err != nil {
						errorArray = append(errorArray, errorStruct{crlDomain, output})
						continue
					}
					// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
					usersRange.AllowedDomains = append(usersRange.AllowedDomains, fmt.Sprintf("%s (%s)", crlDomain, domainIP))
					db.Save(&usersRange)
					returnArray = append(returnArray, crlDomain)
				}
			}
		}
	}
	for _, ip := range allowIPs {
		if slices.Contains(usersRange.AllowedIPs, ip) {
			errorArray = append(errorArray, errorStruct{ip, "already allowed"})
		} else {
			extraVars := map[string]interface{}{"action_ip": ip}
			output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-ip", false)
			if err != nil {
				errorArray = append(errorArray, errorStruct{ip, output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			usersRange.AllowedIPs = append(usersRange.AllowedIPs, ip)
			db.Save(&usersRange)
			returnArray = append(returnArray, ip)
		}
	}

	c.JSON(http.StatusOK, gin.H{"allowed": returnArray, "errors": errorArray})
}

func Deny(c *gin.Context) {
	var thisDenyPayload AllowPayload
	var returnArray []string

	type errorStruct struct {
		Item   string
		Reason string
	}

	var errorArray []errorStruct

	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	if !usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "testing not enabled for range " + usersRange.UserID})
		return
	}

	err = c.BindJSON(&thisDenyPayload)
	if err != nil {
		c.JSON(http.StatusNoContent, gin.H{"error": "improperly formatted deny payload"})
		return
	}

	denyDomains := removeEmptyStrings(thisDenyPayload.Domains)
	denyIPs := removeEmptyStrings(thisDenyPayload.Ips)
	for _, domain := range denyDomains {
		// First, check if this domain is currently allowed
		if !containsSubstring(usersRange.AllowedDomains, domain+" (") {
			errorArray = append(errorArray, errorStruct{domain, "not allowed"})
		} else {
			// Extract the pinned IP from the AllowedDomains string that contains this domain
			domainIP := getDomainIPString(usersRange.AllowedDomains, domain)
			extraVars := map[string]interface{}{"domain": domain, "domainIP": domainIP}
			output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "deny-domain", false)
			if err != nil {
				errorArray = append(errorArray, errorStruct{domain, output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
			newAllowedDomains := removeElementThatContainsString(usersRange.AllowedDomains, domain+" (")
			usersRange.AllowedDomains = newAllowedDomains
			db.Save(&usersRange)
			returnArray = append(returnArray, domain)

			// We aren't going to deal with CRL domains, as they could be shared between explicitly allowed domains.
		}
	}
	for _, ip := range denyIPs {
		if !slices.Contains(usersRange.AllowedIPs, ip) {
			returnArray = append(returnArray, fmt.Sprintf("%s NOT denied as it is not allowed", ip))
		} else {
			extraVars := map[string]interface{}{"action_ip": ip}
			output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "deny-ip", false)
			if err != nil {
				errorArray = append(errorArray, errorStruct{ip, output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			newAllowedIPs := removeStringExact(usersRange.AllowedIPs, ip)
			usersRange.AllowedIPs = newAllowedIPs
			db.Save(&usersRange)
			returnArray = append(returnArray, ip)
		}
	}

	c.JSON(http.StatusOK, gin.H{"denied": returnArray, "errors": errorArray})
}

// StartTesting - snapshot and enter testing state
func StartTesting(c *gin.Context) {
	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	if usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"result": "testing already enabled"})
		return
	}

	output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, nil, "start-testing", false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}
	// Update the testing state in the DB
	db.Model(&usersRange).Update("testing_enabled", true)

	c.JSON(http.StatusOK, gin.H{"result": "testing started"})
}

// StopTesting - revert and exit testing state
func StopTesting(c *gin.Context) {
	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	if !usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "testing not enabled"})
		return
	}

	type StopTestingBody struct {
		Force bool `json:"force"`
	}
	var stopTestingBody StopTestingBody
	c.Bind(&stopTestingBody) // If this errors Force will have the zero value which is false - which is what we want as a default

	extraVars := map[string]interface{}{"force_stop": stopTestingBody.Force}
	output, err := RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "stop-testing", false)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": output})
		return
	}
	// Update the testing state in the DB as well as allowed domains and ips
	usersRange.TestingEnabled = false
	usersRange.AllowedDomains = []string{}
	usersRange.AllowedIPs = []string{}
	db.Save(&usersRange)

	c.JSON(http.StatusOK, gin.H{"result": "testing stopped"})
}

// UpdateVMs - update a VM/group of VMs based on a name provided in the POST body
func UpdateVMs(c *gin.Context) {
	usersRange, err := getRangeObject(c)
	if err != nil {
		return // JSON set in getRangeObject
	}

	if usersRange.TestingEnabled {
		c.JSON(http.StatusConflict, gin.H{"error": "testing is enabled; stop testing to update VMs"})
		return
	}

	type UpdatePayload struct {
		Name string `json:"name"`
	}
	var thisUpdatePayload UpdatePayload

	err = c.BindJSON(&thisUpdatePayload)
	if err != nil {
		c.JSON(http.StatusNoContent, gin.H{"error": "improperly formatted update payload"})
		return
	}

	extraVars := map[string]interface{}{"update_host": thisUpdatePayload.Name}
	go RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/update.yml"}, nil, extraVars, "update", false)

	c.JSON(http.StatusOK, gin.H{"result": "update process started"})
}
