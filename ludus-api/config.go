package ludusapi

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
)

const ludusInstallPath string = "/opt/ludus"
const LudusInstallPath = ludusInstallPath // Export the path for use in plugins

// Default listen ports used when config.yml omits the corresponding keys
const (
	DefaultPort      = 8080
	DefaultAdminPort = 8081
)

// Configurations exported
type Configuration struct {
	ProxmoxNode               string        `mapstructure:"proxmox_node" yaml:"proxmox_node"`
	ProxmoxInterface          string        `mapstructure:"proxmox_interface" yaml:"proxmox_interface"`
	ProxmoxInvalidCert        bool          `mapstructure:"proxmox_invalid_cert" yaml:"proxmox_invalid_cert"`
	ProxmoxURL                string        `mapstructure:"proxmox_url" yaml:"proxmox_url"`
	ProxmoxHostname           string        `mapstructure:"proxmox_hostname" yaml:"proxmox_hostname"`
	ProxmoxLocalIP            string        `mapstructure:"proxmox_local_ip" yaml:"proxmox_local_ip"`
	ProxmoxPublicIP           string        `mapstructure:"proxmox_public_ip" yaml:"proxmox_public_ip"`
	ProxmoxGateway            string        `mapstructure:"proxmox_gateway" yaml:"proxmox_gateway"`
	ProxmoxNetmask            string        `mapstructure:"proxmox_netmask" yaml:"proxmox_netmask"`
	ProxmoxVMStoragePool      string        `mapstructure:"proxmox_vm_storage_pool" yaml:"proxmox_vm_storage_pool"`
	ProxmoxVMStorageFormat    string        `mapstructure:"proxmox_vm_storage_format" yaml:"proxmox_vm_storage_format"`
	ProxmoxISOStoragePool     string        `mapstructure:"proxmox_iso_storage_pool" yaml:"proxmox_iso_storage_pool"`
	LudusNATInterface         string        `mapstructure:"ludus_nat_interface" yaml:"ludus_nat_interface"`
	PreventUserAnsibleAdd     bool          `mapstructure:"prevent_user_ansible_add" yaml:"prevent_user_ansible_add"`
	LicenseKey                string        `mapstructure:"license_key" yaml:"license_key"`
	ExposeAdminPort           bool          `mapstructure:"expose_admin_port" yaml:"expose_admin_port"`
	Port                      int           `mapstructure:"port" yaml:"port"`
	AdminPort                 int           `mapstructure:"admin_port" yaml:"admin_port"`
	ReservedRangeNumbers      []int32       `mapstructure:"reserved_range_numbers" yaml:"reserved_range_numbers"`
	DataDirectory             string        `mapstructure:"data_directory" yaml:"data_directory"`
	DatabaseEncryptionKey     string        `mapstructure:"database_encryption_key" yaml:"database_encryption_key"`
	WireguardPort             int           `mapstructure:"wireguard_port" yaml:"wireguard_port"`
	MaxLogHistory             int           `mapstructure:"max_log_history" yaml:"max_log_history"` // Max number of log history entries to keep per range/user (default: 100)
	InactivityShutdownTimeout time.Duration `mapstructure:"inactivity_shutdown_timeout" yaml:"inactivity_shutdown_timeout"`
	// Cluster mode settings
	ClusterMode  bool   `mapstructure:"cluster_mode" yaml:"cluster_mode"`     // Auto-detected via API during startup, can be overridden
	SDNZone      string `mapstructure:"sdn_zone" yaml:"sdn_zone"`             // The SDN zone name for Ludus networking (default: "ludus")
	VXLANTagBase int    `mapstructure:"vxlan_tag_base" yaml:"vxlan_tag_base"` // Base VXLAN tag (VNI) added to range number (default: 0)
	// Quota defaults - applied to users who don't have explicit quotas or group defaults
	// 0 means unlimited
	DefaultQuotaRAM    int `mapstructure:"default_quota_ram" yaml:"default_quota_ram"`
	DefaultQuotaCPU    int `mapstructure:"default_quota_cpu" yaml:"default_quota_cpu"`
	DefaultQuotaVMs    int `mapstructure:"default_quota_vms" yaml:"default_quota_vms"`
	DefaultQuotaRanges int `mapstructure:"default_quota_ranges" yaml:"default_quota_ranges"`
}

var ServerConfiguration Configuration
var ConfigMu sync.RWMutex

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
	viper.SetDefault("proxmox_public_ip", "127.0.0.1")
	viper.SetDefault("proxmox_vm_storage_pool", "local")
	viper.SetDefault("proxmox_vm_storage_format", "qcow2")
	viper.SetDefault("proxmox_iso_storage_pool", "local")
	viper.SetDefault("ludus_nat_interface", "vmbr1000")
	viper.SetDefault("prevent_user_ansible_add", false)
	viper.SetDefault("data_directory", "/opt/ludus/db")
	viper.SetDefault("database_encryption_key", "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48")
	viper.SetDefault("wireguard_port", 51820)
	viper.SetDefault("port", DefaultPort)
	viper.SetDefault("admin_port", DefaultAdminPort)
	// Do not set a default for cluster_mode to force viper to leave it unset unless provided,
	// so we can detect if user has explicitly set it or not and fallback to API if unset.
	// (See IsClusterMode in sdn.go for logic)
	viper.SetDefault("sdn_zone", "ludus") // Default SDN zone name
	viper.SetDefault("vxlan_tag_base", 0) // Base VXLAN tag added to range number
	viper.SetDefault("default_quota_ram", 0)
	viper.SetDefault("default_quota_cpu", 0)
	viper.SetDefault("default_quota_vms", 0)
	viper.SetDefault("default_quota_ranges", 0)
	viper.SetDefault("max_log_history", 100)           // Max log history entries per range/user
	viper.SetDefault("inactivity_shutdown_timeout", 0) // Disabled by default
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	// Hold the write lock around the initial unmarshal for symmetry with
	// OnConfigChange — the scheduler hasn't started yet, but future concurrent
	// readers during ParseConfig wouldn't have a safe time-window without this.
	ConfigMu.Lock()
	err := viper.Unmarshal(&ServerConfiguration)
	ConfigMu.Unlock()
	if err != nil {
		log.Fatalf("Unable to decode into struct, %v", err)
	}
	// By default hostname is the node name, but not always
	if ServerConfiguration.ProxmoxHostname == "" {
		ServerConfiguration.ProxmoxHostname = ServerConfiguration.ProxmoxNode
	}
	// Make sure the database encryption key is 32 characters long
	if len(ServerConfiguration.DatabaseEncryptionKey) != 32 {
		log.Fatalf("Database encryption key must be 32 characters long")
	}
	if err := ServerConfiguration.ApplyPortDefaultsAndValidate(); err != nil {
		log.Fatalf("%v", err)
	}
	// If there is no license in the config, set it to community
	if ServerConfiguration.LicenseKey == "" || ServerConfiguration.LicenseKey == "community" {
		s.Entitlements = []string{}
		s.LicenseValid = true
		s.LicenseMessage = "community license"
	} else {
		s.LicenseMessage = ""
		s.LicenseKey = ServerConfiguration.LicenseKey
		s.checkLicense()
	}
	log.Println("Using configuration file: ", viper.ConfigFileUsed())

	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		ConfigMu.Lock()
		defer ConfigMu.Unlock()
		if err := viper.Unmarshal(&ServerConfiguration); err != nil {
			log.Printf("Error reloading config: %v", err)
		} else {
			log.Println("Configuration reloaded from file")
		}
	})
}

// ApplyPortDefaultsAndValidate backfills DefaultPort / DefaultAdminPort for
// unset values (zero) and validates that both ports are in range 1-65535 and
// distinct. Callers across ludus-api and ludus-server share this to keep the
// three config load paths (Viper, plain yaml.Decode, plain yaml.Unmarshal) in sync.
func (c *Configuration) ApplyPortDefaultsAndValidate() error {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.AdminPort == 0 {
		c.AdminPort = DefaultAdminPort
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535, got %d", c.Port)
	}
	if c.AdminPort < 1 || c.AdminPort > 65535 {
		return fmt.Errorf("admin_port must be between 1 and 65535, got %d", c.AdminPort)
	}
	if c.Port == c.AdminPort {
		return fmt.Errorf("port and admin_port must differ (got %d for both)", c.Port)
	}
	return nil
}
