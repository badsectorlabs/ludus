package ludusapi

import (
	"fmt"
	"os"
	"regexp"

	"github.com/gin-gonic/gin"
	yaml "sigs.k8s.io/yaml"
)

func GetIPForVMFromConfig(c *gin.Context, vmName string) string {
	// Get the user object to get their range number
	usersRange, err := GetRangeObject(c)
	if err != nil {
		return "null"
	}

	// Read the user's range config file
	user, err := GetUserObject(c)
	if err != nil {
		return "null"
	}

	configBytes, err := os.ReadFile(ludusInstallPath + "/users/" + user.ProxmoxUsername + "/range-config.yml")
	if err != nil {
		return "null"
	}

	// LudusConfig is a struct that represents the range config file
	// It is defined in yaml_validate.go
	var config LudusConfig
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		return "null"
	}

	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)

	// Loop through VMs looking for a match
	for _, vm := range config.Ludus {
		vmNameToCompare := rangeIDTemplateRegex.ReplaceAllString(vm.VMName, usersRange.UserID)
		if vmNameToCompare == vmName && vm.ForceIP {
			return fmt.Sprintf("10.%d.%d.%d", usersRange.RangeNumber, vm.VLAN, vm.IPLastOctet)
		}
	}

	return "null"
}
