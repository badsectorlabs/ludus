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

// GetRouterVMName returns the router VM name for the given range
func GetRouterVMName(c *gin.Context) (string, error) {
	// Get the user object to get their range number
	usersRange, err := GetRangeObject(c)
	if err != nil {
		return "", err
	}

	// Read the user's range config file
	user, err := GetUserObject(c)
	if err != nil {
		return "", err
	}

	configBytes, err := os.ReadFile(ludusInstallPath + "/users/" + user.ProxmoxUsername + "/range-config.yml")
	if err != nil {
		// If no config file exists, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	// Parse the YAML to get router configuration
	var config map[string]interface{}
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		// If parsing fails, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	// Check if router section exists
	routerSection, exists := config["router"]
	if !exists {
		// If no router section, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	router, ok := routerSection.(map[string]interface{})
	if !ok {
		// If router section is not a map, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	// Get router VM name
	vmName, exists := router["vm_name"]
	if !exists {
		// If no vm_name specified, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	vmNameStr, ok := vmName.(string)
	if !ok {
		// If vm_name is not a string, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", usersRange.UserID), nil
	}

	// Replace range_id template with actual range ID
	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)
	routerVMName := rangeIDTemplateRegex.ReplaceAllString(vmNameStr, usersRange.UserID)

	return routerVMName, nil
}
