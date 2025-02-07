package ludusapi

import (
	"fmt"
	"log"

	"github.com/spf13/viper"
)

const ludusInstallPath string = "/opt/ludus"
const LudusInstallPath = ludusInstallPath // Export the path for use in plugins

// Configurations exported
type Configuration struct {
	ProxmoxNode            string `mapstructure:"proxmox_node" yaml:"proxmox_node"`
	ProxmoxInterface       string `mapstructure:"proxmox_interface" yaml:"proxmox_interface"`
	ProxmoxInvalidCert     bool   `mapstructure:"proxmox_invalid_cert" yaml:"proxmox_invalid_cert"`
	ProxmoxURL             string `mapstructure:"proxmox_url" yaml:"proxmox_url"`
	ProxmoxHostname        string `mapstructure:"proxmox_hostname" yaml:"proxmox_hostname"`
	ProxmoxLocalIP         string `mapstructure:"proxmox_local_ip" yaml:"proxmox_local_ip"`
	ProxmoxPublicIP        string `mapstructure:"proxmox_public_ip" yaml:"proxmox_public_ip"`
	ProxmoxGateway         string `mapstructure:"proxmox_gateway" yaml:"proxmox_gateway"`
	ProxmoxNetmask         string `mapstructure:"proxmox_netmask" yaml:"proxmox_netmask"`
	ProxmoxVMStoragePool   string `mapstructure:"proxmox_vm_storage_pool" yaml:"proxmox_vm_storage_pool"`
	ProxmoxVMStorageFormat string `mapstructure:"proxmox_vm_storage_format" yaml:"proxmox_vm_storage_format"`
	ProxmoxISOStoragePool  string `mapstructure:"proxmox_iso_storage_pool" yaml:"proxmox_iso_storage_pool"`
	LudusNATInterface      string `mapstructure:"ludus_nat_interface" yaml:"ludus_nat_interface"`
	PreventUserAnsibleAdd  bool   `mapstructure:"prevent_user_ansible_add" yaml:"prevent_user_ansible_add"`
	LicenseKey             string `mapstructure:"license_key" yaml:"license_key"`
	ExposeAdminPort        bool   `mapstructure:"expose_admin_port" yaml:"expose_admin_port"`
}

var ServerConfiguration Configuration

func (s *Server) ParseConfig() {
	// Set the file name of the configurations file
	viper.SetConfigName("config")

	// Set the path to look for the configurations file
	viper.AddConfigPath(ludusInstallPath)

	// Enable viper to read Environment Variables
	viper.AutomaticEnv()

	viper.SetConfigType("yaml")

	// Set defaults
	viper.SetDefault("proxmox_invalid_cert", true)
	viper.SetDefault("proxmox_url", "https://127.0.0.1:8006")
	viper.SetDefault("proxmox_public_ip", GetPublicIPviaAPI())
	viper.SetDefault("proxmox_vm_storage_pool", "local")
	viper.SetDefault("proxmox_vm_storage_format", "qcow2")
	viper.SetDefault("proxmox_iso_storage_pool", "local")
	viper.SetDefault("ludus_nat_interface", "vmbr0") // Backwards compatibility for < v1.0.4
	viper.SetDefault("prevent_user_ansible_add", false)

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	err := viper.Unmarshal(&ServerConfiguration)
	if err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}
	// By default hostname is the node name, but not always
	if ServerConfiguration.ProxmoxHostname == "" {
		ServerConfiguration.ProxmoxHostname = ServerConfiguration.ProxmoxNode
	}
	// If there is no license in the config, set it to community
	if ServerConfiguration.LicenseKey == "" || ServerConfiguration.LicenseKey == "community" {
		s.LicenseType = "community"
		s.LicenseValid = true
		s.LicenseMessage = ""
	} else {
		s.LicenseType = "enterprise"
		s.LicenseMessage = ""
		s.LicenseKey = ServerConfiguration.LicenseKey
		s.checkLicense()
	}
	fmt.Println("Using configuration file: ", viper.ConfigFileUsed())
}
