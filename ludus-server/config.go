package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"strings"
)

// get interface information for the machine, and create a config automatically
// useful for CI/CD tests
func automatedConfigGenerator() {
	f, err := os.Create("config.yml")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	// Name this node with the Pipeline ID if we are in CI
	// Make sure this is not all numbers or it will be interpreted
	// as an IP address
	var nodeName string
	if len(os.Args) > 2 {
		nodeName = os.Args[2]
	} else if fileExists("/usr/bin/pveversion") { // On proxmox installs, the node name is the hostname
		nodeName = strings.TrimSpace(Run("hostname", false, false))
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
				_, err = f.WriteString("---\n")
				if err != nil {
					log.Fatal(err)
				}
				f.WriteString(fmt.Sprintf("proxmox_node: %s\n", nodeName))
				f.WriteString(fmt.Sprintf("proxmox_interface: %s\n", inter.Name))
				f.WriteString(fmt.Sprintf("proxmox_local_ip: %s\n", ipv4.String()))
				f.WriteString(fmt.Sprintf("proxmox_public_ip: %s\n", ipv4.String()))
				// TODO clean this up/do it in Go. Since we know we will be on a Debian 12 box, it's ok for now
				gateway := strings.Trim(Run("ip route show | grep default | grep -Po '(?<=via )[^ ]*'", false, true), "\n")
				f.WriteString(fmt.Sprintf("proxmox_gateway: %s\n", gateway))
				f.WriteString(fmt.Sprintf("proxmox_netmask: %d.%d.%d.%d\n", ipnet.Mask[0], ipnet.Mask[1], ipnet.Mask[2], ipnet.Mask[3]))
				f.WriteString("proxmox_vm_storage_pool: local\n")
				f.WriteString("proxmox_vm_storage_format: qcow2\n")
				f.WriteString("proxmox_iso_storage_pool: local\n")
				f.WriteString("ludus_nat_interface: ludus\n")
				return
			}
		}
	}
}
