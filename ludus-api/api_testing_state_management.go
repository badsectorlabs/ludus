package ludusapi

import (
	"fmt"
	"ludusapi/dto"
	"net/http"
	"slices"

	"github.com/pocketbase/pocketbase/core"
)

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
func Allow(e *core.RequestEvent) error {

	var thisAllowPayload dto.AllowRequest
	var returnArray []string

	var errorArray []dto.AllowResponseErrorsItem

	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	if !usersRange.TestingEnabled() {
		return JSONError(e, http.StatusConflict, "testing not enabled for range "+usersRange.RangeId())
	}

	e.BindBody(&thisAllowPayload)

	allowDomains := removeEmptyStrings(thisAllowPayload.Domains)
	allowIPs := removeEmptyStrings(thisAllowPayload.Ips)
	for _, domain := range allowDomains {
		// First, check if this domain is already allowed
		// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
		if containsSubstring(usersRange.AllowedDomains(), domain+" (") {
			errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: domain, Reason: "already allowed"})
		} else {
			// This is a new domain to allow, so allow it
			domainIP, err := GetIPFromDomain(domain)
			if err != nil {
				errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: domain, Reason: err.Error()})
				continue
			}
			extraVars := map[string]interface{}{"domain": domain, "domainIP": domainIP}
			output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-domain", false, "")
			if err != nil {
				errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: domain, Reason: output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			usersRange.SetAllowedDomains(append(usersRange.AllowedDomains(), fmt.Sprintf("%s (%s)", domain, domainIP)))
			e.App.Save(usersRange)
			returnArray = append(returnArray, domain)

			// Get all the CRL domains for this domain and allow those too
			// We could do this with a recursive function and bool flag but its only a few extra duplicates lines
			crlDomains := getCRLDomainsFromDomain(domain)
			for _, crlDomain := range crlDomains {
				// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
				if containsSubstring(usersRange.AllowedDomains(), crlDomain+" (") {
					continue // Don't return the CRL domain in an "error" to the client as they didn't ask for it to be allowed
				} else {
					domainIP, err := GetIPFromDomain(crlDomain)
					if err != nil {
						errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: crlDomain, Reason: err.Error()})
						continue
					}
					extraVars := map[string]interface{}{"domain": crlDomain, "domainIP": domainIP}
					output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-domain", false, "")
					if err != nil {
						errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: crlDomain, Reason: output})
						continue
					}
					// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
					usersRange.SetAllowedDomains(append(usersRange.AllowedDomains(), fmt.Sprintf("%s (%s)", crlDomain, domainIP)))
					e.App.Save(usersRange)
					returnArray = append(returnArray, crlDomain)
				}
			}
		}
	}
	for _, ip := range allowIPs {
		if slices.Contains(usersRange.AllowedIps(), ip) {
			errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: ip, Reason: "already allowed"})
		} else {
			extraVars := map[string]interface{}{"action_ip": ip}
			output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "allow-ip", false, "")
			if err != nil {
				errorArray = append(errorArray, dto.AllowResponseErrorsItem{Item: ip, Reason: output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			usersRange.SetAllowedIps(append(usersRange.AllowedIps(), ip))
			e.App.Save(usersRange)
			returnArray = append(returnArray, ip)
		}
	}

	response := dto.AllowResponse{
		Allowed: returnArray,
		Errors:  errorArray,
	}
	return e.JSON(http.StatusOK, response)

}

func Deny(e *core.RequestEvent) error {
	var thisDenyPayload dto.DenyRequest
	var returnArray []string

	var errorArray []dto.DenyResponseErrorsItem

	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	if !usersRange.TestingEnabled() {
		return JSONError(e, http.StatusConflict, "Testing not enabled for range "+usersRange.RangeId())
	}

	e.BindBody(&thisDenyPayload)

	denyDomains := removeEmptyStrings(thisDenyPayload.Domains)
	denyIPs := removeEmptyStrings(thisDenyPayload.Ips)
	for _, domain := range denyDomains {
		// First, check if this domain is currently allowed
		if !containsSubstring(usersRange.AllowedDomains(), domain+" (") {
			errorArray = append(errorArray, dto.DenyResponseErrorsItem{Item: domain, Reason: "not allowed"})
		} else {
			// Extract the pinned IP from the AllowedDomains string that contains this domain
			domainIP := getDomainIPString(usersRange.AllowedDomains(), domain)
			extraVars := map[string]interface{}{"domain": domain, "domainIP": domainIP}
			output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "deny-domain", false, "")
			if err != nil {
				errorArray = append(errorArray, dto.DenyResponseErrorsItem{Item: domain, Reason: output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			// The `+" ("` is to ensure that subdomains aren't matched incorrectly - a.com won't match a.company.com because of the added space and paren
			newAllowedDomains := removeElementThatContainsString(usersRange.AllowedDomains(), domain+" (")
			usersRange.SetAllowedDomains(newAllowedDomains)
			e.App.Save(usersRange)
			returnArray = append(returnArray, domain)

			// We aren't going to deal with CRL domains, as they could be shared between explicitly allowed domains.
		}
	}
	for _, ip := range denyIPs {
		if !slices.Contains(usersRange.AllowedIps(), ip) {
			returnArray = append(returnArray, fmt.Sprintf("%s NOT denied as it is not allowed", ip))
		} else {
			extraVars := map[string]interface{}{"action_ip": ip}
			output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "deny-ip", false, "")
			if err != nil {
				errorArray = append(errorArray, dto.DenyResponseErrorsItem{Item: ip, Reason: output})
				continue
			}
			// Save allowed value in the DB for each success - in the event one fails, the DB will reflect the correct state
			newAllowedIPs := removeStringExact(usersRange.AllowedIps(), ip)
			usersRange.SetAllowedIps(newAllowedIPs)
			e.App.Save(usersRange)
			returnArray = append(returnArray, ip)
		}
	}

	response := dto.DenyResponse{
		Denied: returnArray,
		Errors: errorArray,
	}
	return e.JSON(http.StatusOK, response)
}

// StartTesting - snapshot and enter testing state
func StartTesting(e *core.RequestEvent) error {
	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	if usersRange.TestingEnabled() {
		return JSONError(e, http.StatusConflict, "Testing already enabled")
	}

	output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, nil, "start-testing", false, "")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, output)
	}
	// Update the testing state in the DB
	usersRange.SetTestingEnabled(true)
	e.App.Save(usersRange)

	return JSONResult(e, http.StatusOK, "Testing started")
}

// StopTesting - revert and exit testing state
func StopTesting(e *core.RequestEvent) error {
	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	if !usersRange.TestingEnabled() {
		return JSONError(e, http.StatusConflict, "Testing not enabled")
	}

	var stopTestingBody dto.StopTestingRequest
	e.BindBody(&stopTestingBody) // If this errors Force will have the zero value which is false - which is what we want as a default

	extraVars := map[string]interface{}{"force_stop": stopTestingBody.Force}
	output, err := server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/testing.yml"}, nil, extraVars, "stop-testing", false, "")
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, output)
	}
	// Update the testing state in the DB as well as allowed domains and ips
	usersRange.SetTestingEnabled(false)
	usersRange.SetAllowedDomains([]string{})
	usersRange.SetAllowedIps([]string{})
	e.App.Save(usersRange)

	return JSONResult(e, http.StatusOK, "Testing stopped")
}

// UpdateVMs - update a VM/group of VMs based on a name provided in the POST body
func UpdateVMs(e *core.RequestEvent) error {
	usersRange, err := GetRange(e)
	if err != nil {
		return err
	}

	if usersRange.TestingEnabled() {
		return JSONError(e, http.StatusConflict, "Testing is enabled; stop testing to update VMs")
	}

	var thisUpdatePayload dto.UpdateRequest

	e.BindBody(&thisUpdatePayload)

	extraVars := map[string]interface{}{"update_host": thisUpdatePayload.Name}
	go server.RunAnsiblePlaybookWithVariables(e, []string{ludusInstallPath + "/ansible/range-management/update.yml"}, nil, extraVars, "update", false, "")

	return JSONResult(e, http.StatusOK, "Update process started")
}
