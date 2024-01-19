package ludusapi

import (
	"fmt"
	"log"

	"github.com/spf13/viper"
)

const ludusInstallPath string = "/opt/ludus"

// Configurations exported
type Configuration struct {
	ProxmoxNode            string `mapstructure:"proxmox_node" yaml:"proxmox_node"`
	ProxmoxInvalidCert     bool   `mapstructure:"proxmox_invalid_cert" yaml:"proxmox_invalid_cert"`
	ProxmoxURL             string `mapstructure:"proxmox_url" yaml:"proxmox_url"`
	ProxmoxHostname        string `mapstructure:"proxmox_hostname" yaml:"proxmox_hostname"`
	ProxmoxPublicIP        string `mapstructure:"proxmox_public_ip" yaml:"proxmox_public_ip"`
	ProxmoxVMStoragePool   string `mapstructure:"proxmox_vm_storage_pool" yaml:"proxmox_vm_storage_pool"`
	ProxmoxVMStorageFormat string `mapstructure:"proxmox_vm_storage_format" yaml:"proxmox_vm_storage_format"`
	ProxmoxISOStoragePool  string `mapstructure:"proxmox_iso_storage_pool" yaml:"proxmox_iso_storage_pool"`
}

var ServerConfiguration Configuration

func ParseConfig() {
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
	fmt.Println("Using configuration file: ", viper.ConfigFileUsed())
}
