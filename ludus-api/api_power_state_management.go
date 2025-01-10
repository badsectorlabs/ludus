package ludusapi

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

func PowerAction(c *gin.Context, action string) {
	type PowerBody struct {
		Machines []string `json:"machines"`
	}
	var powerBody PowerBody
	c.Bind(&powerBody)

	// Get the proxmox client for the user here to check if the ROOT API key is being used
	// and fail early if it is
	proxmoxClient, err := GetProxmoxClientForUser(c)
	if err != nil {
		return // JSON set in getProxmoxClientForUser
	}

	if len(powerBody.Machines) == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "you must specify a VM, comma separated list of VMs, or 'all'"})
		return
	} else if len(powerBody.Machines) == 1 && powerBody.Machines[0] == "all" {
		if action == "off" {
			go server.RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/power.yml"}, nil, nil, "stop-range", false, "")
		} else {
			go server.RunAnsiblePlaybookWithVariables(c, []string{ludusInstallPath + "/ansible/range-management/power.yml"}, nil, nil, "startup-range", false, "")
		}
		c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Full range power %s in progress", action)})
		return
	} else {
		// One or more machine names passed in
		for _, machineName := range powerBody.Machines {
			thisVmRef, err := proxmoxClient.GetVmRefByName(machineName)
			if err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if action == "off" {
				proxmoxClient.StopVm(thisVmRef)
			} else {
				proxmoxClient.StartVm(thisVmRef)
			}
		}
	}
	if len(powerBody.Machines) > 1 {
		c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Powered %s %d VMs", action, len(powerBody.Machines))})
	} else {
		c.JSON(http.StatusOK, gin.H{"result": fmt.Sprintf("Powered %s VM: %s", action, powerBody.Machines[0])})
	}
}

// PowerOffRange - powers off all range VMs
func PowerOffRange(c *gin.Context) {
	PowerAction(c, "off")
}

// PowerOnRange - powers on all range VMs
func PowerOnRange(c *gin.Context) {
	PowerAction(c, "on")
}
