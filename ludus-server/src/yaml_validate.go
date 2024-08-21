package ludusapi

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
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
		log.Println("Yaml validate: checking syntax only")
	}

	return nil
}

func validateFile(c *gin.Context, path string, schema string) error {
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
	return validateRangeYAML(c, bytes)

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
	Roles interface{} `yaml:"roles"`
}

type LudusConfig struct {
	Ludus []VM `json:"ludus"`
}

// validateRangeYAML checks for duplicate vlan and ip_last_octet combinations, templates exist on the server, and unique hostname
// also checks each role to see if it exists on the server and creates the user-defined-roles.yml file.
func validateRangeYAML(c *gin.Context, yamlData []byte) error {
	var config LudusConfig
	err := yaml.Unmarshal(yamlData, &config)
	if err != nil {
		return err
	}

	// Get a list of all the built templates on the system
	templateSlice, err := getTemplateNameArray(c, true)
	if err != nil {
		return err
	}

	usersRange, err := getRangeObject(c)
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
			hostname := rangeIDTemplateRegex.ReplaceAllString(vm.Hostname, usersRange.UserID)
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
		// Check the roles (if any)
		if vm.Roles != nil {
			switch roles := vm.Roles.(type) {
			case []interface{}:
				for _, role := range roles {
					switch r := role.(type) {
					case string:
						exists, err := checkRoleExists(c, r)
						if err != nil {
							return fmt.Errorf("error checking if role exists on the server: %s", err)
						}
						if !exists {
							return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", role, usersRange.UserID)
						} else {
							c.Set("userHasRoles", true)
						}
					case map[string]interface{}:
						log.Println(role)
						if name, ok := r["name"].(string); ok {
							exists, err := checkRoleExists(c, name)
							if err != nil {
								return fmt.Errorf("error checking if role exists on the server: %s", err)
							}
							if !exists {
								return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", name, usersRange.UserID)
							} else {
								c.Set("userHasRoles", true)
							}
							if dependsOn, ok := r["depends_on"].([]interface{}); ok {
								for _, dep := range dependsOn {
									if depMap, ok := dep.(map[string]interface{}); ok {
										if role, ok := depMap["role"].(string); ok {
											exists, err := checkRoleExists(c, role)
											if err != nil {
												return fmt.Errorf("error checking if role exists on the server: %s", err)
											}
											if !exists {
												return fmt.Errorf("the role '%s' does not exist on the Ludus server for user %s", role, usersRange.UserID)
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	return nil
}
