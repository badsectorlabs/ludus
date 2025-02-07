package main

import (
	"encoding/json"
	"fmt"
	"log"
	ludusapi "ludusapi"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v2"
)

// get interface information for the machine, and create a config automatically
// useful for CI/CD tests
func automatedConfigGenerator(writeToFile bool) {
	// Name this node with the Pipeline ID if we are in CI
	// Make sure this is not all numbers or it will be interpreted
	// as an IP address
	if fileExists("/usr/bin/pveversion") && nodeName != "" {
		log.Fatal("Cannot specify a node name when running on Proxmox. Remove the node name from the command line.")
	} else if fileExists("/usr/bin/pveversion") { // On proxmox installs, the node name is the hostname
		nodeName = strings.TrimSpace(Run("hostname", false, false))
		// Make sure vmbr1000 is not currently in use
		type InterfaceConfig struct {
			Iface string `json:"iface"`
			// Other fields omitted for brevity
		}
		// We must set the locale to en_US.UTF-8 to get the correct output from pvesh, exporting it via the install script doesn't affect this sub-shell
		ifaceJSONString := strings.TrimSpace(Run(fmt.Sprintf("LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8 LANG=en_US.UTF-8 LC_CTYPE=en_US.UTF-8 pvesh get /nodes/%s/network --output-format json", nodeName), false, false))
		var configs []InterfaceConfig
		err := json.Unmarshal([]byte(ifaceJSONString), &configs)
		if err != nil {
			log.Fatal("Error unmarshaling JSON while getting networks:", err)
		}

		vmbr1000Found := false
		for _, config := range configs {
			if strings.EqualFold(config.Iface, "vmbr1000") {
				vmbr1000Found = true
				break
			}
		}

		if vmbr1000Found {
			log.Fatal("The 'vmbr1000' interface was found on this server already. Specify a nonexistent vmbr value for 'ludus_nat_interface' in /opt/ludus/config.yml")
		}
	} else {
		nodeName = "ludus"
	}

	interfaces, _ := net.Interfaces()
	_, localhost, _ := net.ParseCIDR("127.0.0.0/8")
	for _, inter := range interfaces {
		addrs, _ := inter.Addrs()
		for _, ipaddr := range addrs {
			ipv4, ipnet, _ := net.ParseCIDR(ipaddr.String())
			isIPv4 := ipv4.To4()
			if isIPv4 != nil && !localhost.Contains(ipv4) {
				// TODO clean this up/do it in Go. Since we know we will be on a Debian 12 box, it's ok for now
				gateway := strings.Trim(Run("ip route show | grep default | grep -Po '(?<=via )[^ ]*'", false, true), "\n")
				if writeToFile {
					f, err := os.Create(fmt.Sprintf("%s/config.yml", ludusPath))
					if err != nil {
						log.Fatal(err)
					}
					defer f.Close()
					_, err = f.WriteString("---\n")
					if err != nil {
						log.Fatal(err)
					}
					f.WriteString(fmt.Sprintf("proxmox_node: %s\n", nodeName))
					f.WriteString(fmt.Sprintf("proxmox_interface: %s\n", inter.Name))
					f.WriteString(fmt.Sprintf("proxmox_local_ip: %s\n", ipv4.String()))
					f.WriteString(fmt.Sprintf("proxmox_public_ip: %s\n", ipv4.String()))
					f.WriteString(fmt.Sprintf("proxmox_gateway: %s\n", gateway))
					f.WriteString(fmt.Sprintf("proxmox_netmask: %d.%d.%d.%d\n", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3]))
					f.WriteString("proxmox_vm_storage_pool: local\n")
					f.WriteString("proxmox_vm_storage_format: qcow2\n")
					f.WriteString("proxmox_iso_storage_pool: local\n")
					f.WriteString("ludus_nat_interface: vmbr1000\n")
					f.WriteString("prevent_user_ansible_add: false\n")
					f.WriteString("license_key: community\n")
					f.WriteString("expose_admin_port: false\n")
				} else {
					config.ProxmoxNode = nodeName
					config.ProxmoxInterface = inter.Name
					config.ProxmoxLocalIP = ipv4.String()
					config.ProxmoxPublicIP = ipv4.String()
					config.ProxmoxGateway = gateway
					config.ProxmoxNetmask = fmt.Sprintf("%d.%d.%d.%d", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3])
					config.ProxmoxVMStoragePool = "local"
					config.ProxmoxVMStorageFormat = "qcow2"
					config.ProxmoxISOStoragePool = "local"
					config.LudusNATInterface = "vmbr1000"
					config.PreventUserAnsibleAdd = false
					config.ProxmoxInvalidCert = true
					config.ProxmoxURL = "https://127.0.0.1:8006"
					config.LicenseKey = "community"
					config.ExposeAdminPort = false
				}
				return
			}
		}
	}
}

// Load the config file from disk into the config struct
func loadConfig() {
	// Read the config file
	data, err := os.ReadFile(fmt.Sprintf("%s/config.yml", ludusInstallPath))
	if err != nil {
		log.Fatalf("Error opening config: %v", err)
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("Error unmarshalling config: %v", err)
	}
}

func writeConfigToFile(config ludusapi.Configuration, filename string) error {
	// Marshal the struct to YAML
	data, err := yaml.Marshal(&config)
	if err != nil {
		return fmt.Errorf("error marshaling YAML: %v", err)
	}

	// Write the YAML to a file
	err = os.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing file: %v", err)
	}

	return nil
}
