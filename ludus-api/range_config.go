package ludusapi

import (
	"fmt"
	"ludusapi/models"
	"os"
	"regexp"

	yaml "sigs.k8s.io/yaml"
)

func GetIPForVMFromConfig(targetRange *models.Range, vmName string) string {
	configBytes, err := os.ReadFile(ludusInstallPath + "/ranges/" + targetRange.RangeId() + "/range-config.yml")
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
		vmNameToCompare := rangeIDTemplateRegex.ReplaceAllString(vm.VMName, targetRange.RangeId())
		if vmNameToCompare == vmName && vm.ForceIP {
			return fmt.Sprintf("10.%d.%d.%d", targetRange.RangeNumber(), vm.VLAN, vm.IPLastOctet)
		}
	}

	return "null"
}

// GetOsTypeForVMFromConfig returns the OS type string ("linux", "windows", "macos", "other")
// for a VM by looking up its template in the range config, then inferring OS from the template name.
// Falls back to "linux" if the config cannot be read or the VM is not found.
func GetOsTypeForVMFromConfig(targetRange *models.Range, vmName string) string {
	configBytes, err := os.ReadFile(ludusInstallPath + "/ranges/" + targetRange.RangeId() + "/range-config.yml")
	if err != nil {
		return string(osFromTemplateName(vmName))
	}

	var config LudusConfig
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		return string(osFromTemplateName(vmName))
	}

	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)

	for _, vm := range config.Ludus {
		vmNameToCompare := rangeIDTemplateRegex.ReplaceAllString(vm.VMName, targetRange.RangeId())
		if vmNameToCompare == vmName {
			return string(osFromTemplateName(vm.Template))
		}
	}

	// Fallback: infer from VM name directly
	return string(osFromTemplateName(vmName))
}

// GetStaticIPForVMFromConfig returns the deterministic static IP for a VM
// computed from its VLAN and ip_last_octet in the range config.
// This is used when the guest agent hasn't reported an IP yet (VM powered off).
// Returns "null" if the VM is not found in the config.
func GetStaticIPForVMFromConfig(targetRange *models.Range, vmName string) string {
	configBytes, err := os.ReadFile(ludusInstallPath + "/ranges/" + targetRange.RangeId() + "/range-config.yml")
	if err != nil {
		return "null"
	}

	var config LudusConfig
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		return "null"
	}

	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)

	for _, vm := range config.Ludus {
		vmNameToCompare := rangeIDTemplateRegex.ReplaceAllString(vm.VMName, targetRange.RangeId())
		if vmNameToCompare == vmName && vm.VLAN > 0 && vm.IPLastOctet > 0 {
			return fmt.Sprintf("10.%d.%d.%d", targetRange.RangeNumber(), vm.VLAN, vm.IPLastOctet)
		}
	}

	return "null"
}

// GetRouterVMName returns the router VM name for the given range
// TODO parse the vm descriptions to get the router and not depend on the config file or name
func GetRouterVMName(targetRange *models.Range) (string, error) {

	configBytes, err := os.ReadFile(ludusInstallPath + "/ranges/" + targetRange.RangeId() + "/range-config.yml")
	if err != nil {
		// If no config file exists, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	// Parse the YAML to get router configuration
	var config map[string]interface{}
	err = yaml.Unmarshal(configBytes, &config)
	if err != nil {
		// If parsing fails, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	// Check if router section exists
	routerSection, exists := config["router"]
	if !exists {
		// If no router section, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	router, ok := routerSection.(map[string]interface{})
	if !ok {
		// If router section is not a map, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	// Get router VM name
	vmName, exists := router["vm_name"]
	if !exists {
		// If no vm_name specified, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	vmNameStr, ok := vmName.(string)
	if !ok {
		// If vm_name is not a string, return default router name
		return fmt.Sprintf("%s-router-debian11-x64", targetRange.RangeId()), nil
	}

	// Replace range_id template with actual range ID
	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)
	routerVMName := rangeIDTemplateRegex.ReplaceAllString(vmNameStr, targetRange.RangeId())

	return routerVMName, nil
}
