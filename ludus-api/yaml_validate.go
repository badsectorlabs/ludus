package ludusapi

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/goforj/godump"
	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
	"github.com/xeipuuv/gojsonschema"
	yaml "sigs.k8s.io/yaml"
)

func checkYAML(a *[]byte, isJSON bool) error {
	if len(*a) == 0 {
		return fmt.Errorf("input must not be empty")
	}

	if !utf8.Valid(*a) {
		return fmt.Errorf("input must be valid UTF-8")
	}

	// attempt to parse JSON first
	var any interface{}
	err := json.Unmarshal(*a, &any)

	// input is valid JSON
	if err == nil {
		return nil
	}

	// exit condition: flagged as JSON but error found
	if isJSON {
		return fmt.Errorf("invalid JSON: %s", err.Error())
	}

	// not JSON
	json, err := yaml.YAMLToJSON(*a)
	if err != nil {
		return fmt.Errorf("invalid YAML: %s", err.Error())
	}

	// successful conversion
	*a = json

	return nil
}

func validateBytes(bytes []byte, schemabytes []byte) error {

	err := checkYAML(&bytes, false)
	if err != nil {
		return fmt.Errorf("can't parse input: %s", err.Error())
	}

	var obj interface{}
	if err = json.Unmarshal(bytes, &obj); err != nil {
		return fmt.Errorf("can't unmarshal data: %s", err.Error())
	}

	if len(schemabytes) > 0 {
		schemaLoader := gojsonschema.NewStringLoader(string(schemabytes))
		documentLoader := gojsonschema.NewStringLoader(string(bytes))

		result, err := gojsonschema.Validate(schemaLoader, documentLoader)
		if err != nil {
			return fmt.Errorf("can't validate YAML: %s", err.Error())
		}

		if !result.Valid() {
			var report string
			for i, desc := range result.Errors() {
				if i > 0 {
					report += "; "
				}
				report += desc.String()
			}
			return fmt.Errorf("invalid YAML: %s", report)
		}
	} else {
		logger.Debug("Yaml validate: checking syntax only")
	}

	return nil
}

func validateFile(e *core.RequestEvent, path string, schema string) error {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("can't read %s: %v", path, err)
	}

	yamlBytes, err := loadYaml(schema)
	if err != nil {
		return fmt.Errorf("can't parse schema: %s", err.Error())
	}

	err = validateBytes(bytes, yamlBytes)
	if err != nil {
		return err
	}
	return validateRangeYAML(e, bytes)

}

func loadYaml(schema string) ([]byte, error) {
	var yamlBytes []byte
	var err error
	if len(schema) > 0 {
		yamlBytes, err = os.ReadFile(schema)
		if err != nil {
			return nil, fmt.Errorf("can't read schema: %s", err.Error())
		}

		schemaIsJSON := strings.HasSuffix(schema, ".json")
		err = checkYAML(&yamlBytes, schemaIsJSON)
		if err != nil {
			return nil, fmt.Errorf("can't parse schema: %s", err.Error())
		}
	}

	return yamlBytes, nil
}

// validateTargetNode checks if a target_node exists in the cluster
// Returns nil if targetNode is empty (will be auto-selected) or if it exists
func validateTargetNode(e *core.RequestEvent, targetNode string) error {
	if targetNode == "" {
		return nil // Will be auto-selected
	}

	// Check if we have cached the nodes for this request
	cachedNodes := e.Get("clusterNodes")
	if cachedNodes != nil {
		for _, node := range cachedNodes.(goproxmox.NodeStatuses) {
			if node.Node == targetNode {
				return nil
			}
		}
	}

	client, err := getRootGoProxmoxClient()
	if err != nil {
		return fmt.Errorf("failed to get proxmox client: %w", err)
	}

	nodes, err := GetClusterNodes(client)
	logger.Debug(fmt.Sprintf("Got cluster nodes: %s", godump.DumpStr(nodes)))
	if err != nil {
		return fmt.Errorf("failed to get cluster nodes: %w", err)
	}

	e.Set("clusterNodes", nodes)

	for _, node := range nodes {
		if node.Node == targetNode {
			return nil
		}
	}
	return fmt.Errorf("target_node '%s' does not exist in the cluster", targetNode)
}

// VM represents a virtual machine configuration
// Why json tags for yaml? https://github.com/kubernetes-sigs/yaml#introduction
// "it effectively reuses the JSON struct tags as well as the custom JSON methods MarshalJSON and UnmarshalJSON unlike go-yaml"
type VM struct {
	VMName      string `json:"vm_name"`
	Hostname    string `json:"hostname"`
	Template    string `json:"template"`
	VLAN        int    `json:"vlan"`
	IPLastOctet int    `json:"ip_last_octet"`
	Domain      struct {
		FQDN string `json:"fqdn"`
		Role string `json:"role"`
	} `json:"domain"`
	Roles      interface{} `yaml:"roles"`
	ForceIP    bool        `json:"force_ip,omitempty"`
	TargetNode string      `json:"target_node,omitempty"` // Per-VM node selection for cluster deployments
}

// Router represents the router section of the range config.
type Router struct {
	TargetNode string `json:"target_node,omitempty"` // Proxmox node for the router VM; overrides range default (cluster only)
}

type Defaults struct {
	TargetNode string `json:"target_node,omitempty"` // Range-level default node for cluster deployments
}

type LudusConfig struct {
	Ludus    []VM      `json:"ludus"`
	Router   *Router   `json:"router,omitempty"`
	Defaults *Defaults `json:"defaults,omitempty"`
}

// validateRangeYAML checks for duplicate vlan and ip_last_octet combinations, templates exist on the server, and unique hostname
// also checks each role to see if it exists on the server and creates the user-defined-roles.yml file.
// Additionally validates target_node settings for cluster deployments.
func validateRangeYAML(e *core.RequestEvent, yamlData []byte) error {
	var config LudusConfig
	err := yaml.Unmarshal(yamlData, &config)
	if err != nil {
		return err
	}

	// Validate range-level target_node if specified
	if config.Defaults != nil && config.Defaults.TargetNode != "" {
		if err := validateTargetNode(e, config.Defaults.TargetNode); err != nil {
			return fmt.Errorf("range-level target_node error: %w", err)
		}
	}

	// Validate router target_node if specified
	if config.Router != nil && config.Router.TargetNode != "" {
		if err := validateTargetNode(e, config.Router.TargetNode); err != nil {
			return fmt.Errorf("router target_node error: %w", err)
		}
	}

	// Get a list of all the built templates on the system
	templateSlice, err := getTemplateNameArray(e, true)
	if err != nil {
		return err
	}

	targetRange, err := GetRange(e)
	if err != nil {
		return err
	}
	// Check for duplicate vlan and ip_last_octet combinations
	seenVLANAndIP := make(map[string]bool)
	// Check that all vm_names and hostnames are unique
	seenVMNames := make(map[string]bool)
	seenHostnames := make(map[string]bool)
	// Check that NETBIOS are unique per domain
	seenNETBIOSnames := make(map[string]string)
	rangeIDTemplateRegex := regexp.MustCompile(`{{\s*range_id\s*}}`)

	var NETBIOSnameKey string
	e.Set("rangeHasRoles", false)
	for _, vm := range config.Ludus {
		vlanIPKey := fmt.Sprintf("vlan: %d, ip_last_octet: %d", vm.VLAN, vm.IPLastOctet)
		vmNameKey := vm.VMName
		vmHostnameKey := vm.Hostname
		if seenVLANAndIP[vlanIPKey] {
			return fmt.Errorf("duplicate vlan and ip_last_octet combination found: %s for VM: %s", vlanIPKey, vm.VMName)
		}
		if seenVMNames[vmNameKey] {
			return fmt.Errorf("duplicate VM name found: %s", vmNameKey)
		}
		if seenHostnames[vmHostnameKey] {
			return fmt.Errorf("duplicate hostname name found: %s", vmHostnameKey)
		}
		// We only care about this for VMs in a domain
		if vm.Domain.FQDN != "" {
			// "Windows doesn't permit computer names that exceed 15 characters"
			// https://learn.microsoft.com/en-us/troubleshoot/windows-server/active-directory/naming-conventions-for-computer-domain-site-ou
			// First we have to replace any range_id template strings
			hostname := rangeIDTemplateRegex.ReplaceAllString(vm.Hostname, targetRange.RangeId())
			// If the hostname is more than 15 chars, chop it down
			if len(hostname) >= 15 {
				NETBIOSnameKey = hostname[:15]
			} else {
				NETBIOSnameKey = hostname
			}
			// Check to see if we have seen this 15 char or less hostname in this domain
			if seenNETBIOSnames[vm.Domain.FQDN] == NETBIOSnameKey {
				return fmt.Errorf("duplicate Windows hostname name found: %s\nWindows hostnames are truncated to 15 characters so the first 15 characters must be unique", vm.Hostname)
			}
			// Store this hostname for this domain to check against in the future
			seenNETBIOSnames[vm.Domain.FQDN] = NETBIOSnameKey
		}
		seenVLANAndIP[vlanIPKey] = true
		seenHostnames[vmHostnameKey] = true
		seenVMNames[vmNameKey] = true
		// Check the template
		if !slices.Contains(templateSlice, vm.Template) {
			return fmt.Errorf("template not found or not built on this server: %s for VM: %s", vm.Template, vm.VMName)
		}
		// Validate VM-level target_node if specified
		if vm.TargetNode != "" {
			if err := validateTargetNode(e, vm.TargetNode); err != nil {
				return fmt.Errorf("VM '%s' target_node error: %w", vm.VMName, err)
			}
		}
		// Check the roles (if any)
		if vm.Roles != nil {
			switch roles := vm.Roles.(type) {
			case []interface{}:
				for _, role := range roles {
					switch r := role.(type) {
					case string:
						exists, err := checkRoleExists(e, r)
						if err != nil {
							return fmt.Errorf("error checking if role exists on the server: %s", err)
						}
						if !exists {
							return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", role, targetRange.RangeId())
						} else {
							e.Set("rangeHasRoles", true)
						}
					case map[string]interface{}:
						logger.Debug("Yaml validate: checking role: " + godump.DumpStr(role))
						if name, ok := r["name"].(string); ok {
							exists, err := checkRoleExists(e, name)
							if err != nil {
								return fmt.Errorf("error checking if role exists on the server: %s", err)
							}
							if !exists {
								return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", name, targetRange.RangeId())
							} else {
								e.Set("rangeHasRoles", true)
							}
							if dependsOn, ok := r["depends_on"].([]interface{}); ok {
								for _, dep := range dependsOn {
									if depMap, ok := dep.(map[string]interface{}); ok {
										if role, ok := depMap["role"].(string); ok {
											exists, err := checkRoleExists(e, role)
											if err != nil {
												return fmt.Errorf("error checking if role exists on the server: %s", err)
											}
											if !exists {
												return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", role, targetRange.RangeId())
											}
										}
									}
								}
							}
						}
					}
				}
			}
		} else {
			// Remove the user-defined-roles.yml file in the event the range previously had a config with roles defined
			_, err = os.Stat(fmt.Sprintf("%s/ranges/%s/user-defined-roles.yml", ludusInstallPath, targetRange.RangeId()))
			if err == nil {
				err = os.Remove(fmt.Sprintf("%s/ranges/%s/user-defined-roles.yml", ludusInstallPath, targetRange.RangeId()))
				if err != nil {
					return fmt.Errorf("failed to remove user-defined-roles.yml: %v", err)
				}
			}
		}
	}

	return nil
}
