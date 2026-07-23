package ludusapi

import (
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	"ludusapi/pveclient"
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
	ProxmoxInvalidCert        bool          `mapstructure:"proxmox_invalid_cert" yaml:"proxmox_invalid_cert"`
	ProxmoxURL                string        `mapstructure:"proxmox_url" yaml:"proxmox_url"`           // Deprecated: use proxmox_endpoints
	ProxmoxEndpoints          []string      `mapstructure:"proxmox_endpoints" yaml:"proxmox_endpoints"`
	ProxmoxTokenID            string        `mapstructure:"proxmox_token_id" yaml:"proxmox_token_id"`
	ProxmoxTokenSecret        string        `mapstructure:"proxmox_token_secret" yaml:"proxmox_token_secret"`
	ProxmoxUserRealm          string        `mapstructure:"proxmox_user_realm" yaml:"proxmox_user_realm"`
	ProxmoxHostname           string        `mapstructure:"proxmox_hostname" yaml:"proxmox_hostname"`
	ProxmoxPublicIP           string        `mapstructure:"proxmox_public_ip" yaml:"proxmox_public_ip"` // Deprecated: use wireguard_endpoint
	WireguardEndpoint         string        `mapstructure:"wireguard_endpoint" yaml:"wireguard_endpoint"`
	LudusNATIP                string        `mapstructure:"ludus_nat_ip" yaml:"ludus_nat_ip"`
	LudusNATGateway           string        `mapstructure:"ludus_nat_gateway" yaml:"ludus_nat_gateway"`
	TLSCertFile               string        `mapstructure:"tls_cert_file" yaml:"tls_cert_file"`
	TLSKeyFile                string        `mapstructure:"tls_key_file" yaml:"tls_key_file"`
	ProxmoxVMStoragePool      string        `mapstructure:"proxmox_vm_storage_pool" yaml:"proxmox_vm_storage_pool"`
	ProxmoxVMStorageFormat    string        `mapstructure:"proxmox_vm_storage_format" yaml:"proxmox_vm_storage_format"`
	ProxmoxISOStoragePool     string        `mapstructure:"proxmox_iso_storage_pool" yaml:"proxmox_iso_storage_pool"`
	LudusNATInterface         string        `mapstructure:"ludus_nat_interface" yaml:"ludus_nat_interface"`
	PreventUserAnsibleAdd     bool          `mapstructure:"prevent_user_ansible_add" yaml:"prevent_user_ansible_add"`
	AirgappedInstall          bool          `mapstructure:"airgapped_install" yaml:"airgapped_install"`
	LicenseKey                string        `mapstructure:"license_key" yaml:"license_key"`
	ExposeAdminPort           bool          `mapstructure:"expose_admin_port" yaml:"expose_admin_port"`
	RegisterDefaultSource     bool          `mapstructure:"register_default_source" yaml:"register_default_source"`
	SyncSourcesOnStartup      bool          `mapstructure:"sync_sources_on_startup" yaml:"sync_sources_on_startup"`
	Port                      int           `mapstructure:"port" yaml:"port"`
	AdminPort                 int           `mapstructure:"admin_port" yaml:"admin_port"`
	ReservedRangeNumbers      []int32       `mapstructure:"reserved_range_numbers" yaml:"reserved_range_numbers"`
	DataDirectory             string        `mapstructure:"data_directory" yaml:"data_directory"`
	DatabaseEncryptionKey     string        `mapstructure:"database_encryption_key" yaml:"database_encryption_key"`
	WireguardPort             int           `mapstructure:"wireguard_port" yaml:"wireguard_port"`
	MaxLogHistory             int           `mapstructure:"max_log_history" yaml:"max_log_history"` // Max number of log history entries to keep per range/user (default: 100)
	InactivityShutdownTimeout time.Duration `mapstructure:"inactivity_shutdown_timeout" yaml:"inactivity_shutdown_timeout"`
	// SDN settings
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
	viper.SetDefault("proxmox_vm_storage_pool", "local")
	viper.SetDefault("proxmox_vm_storage_format", "qcow2")
	viper.SetDefault("proxmox_iso_storage_pool", "local")
	viper.SetDefault("ludus_nat_interface", "ludusnat")
	viper.SetDefault("proxmox_user_realm", "pve")
	viper.SetDefault("ludus_nat_ip", "192.0.2.253")
	viper.SetDefault("ludus_nat_gateway", "192.0.2.254")
	viper.SetDefault("tls_cert_file", ludusInstallPath+"/tls/server.crt")
	viper.SetDefault("tls_key_file", ludusInstallPath+"/tls/server.key")
	viper.SetDefault("prevent_user_ansible_add", false)
	viper.SetDefault("airgapped_install", false)
	viper.SetDefault("register_default_source", true)
	viper.SetDefault("sync_sources_on_startup", true)
	viper.SetDefault("data_directory", "/opt/ludus/db")
	viper.SetDefault("database_encryption_key", "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48")
	viper.SetDefault("wireguard_port", 51820)
	viper.SetDefault("port", DefaultPort)
	viper.SetDefault("admin_port", DefaultAdminPort)
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
	ServerConfiguration.ApplyDefaults()
	if err := ServerConfiguration.ApplyShimAndValidate(); err != nil {
		log.Fatalf("config validation: %v", err)
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
		if err := viper.Unmarshal(&ServerConfiguration); err != nil {
			log.Printf("Error reloading config: %v", err)
			ConfigMu.Unlock()
			return
		}
		log.Println("Configuration reloaded from file")
		ConfigMu.Unlock()
		if err := ServerConfiguration.ApplyShimAndValidate(); err != nil {
			log.Printf("ERROR: hot-reloaded config is invalid: %v (config left in inconsistent state; fix and save again)", err)
		}
	})
}

// ApplyDefaults backfills zero-value fields with the same defaults Viper would
// apply in ParseConfig. Used by ludus-server which loads config via plain yaml.
func (c *Configuration) ApplyDefaults() {
	if c.ProxmoxUserRealm == "" {
		c.ProxmoxUserRealm = "pve"
	}
	if c.LudusNATInterface == "" {
		c.LudusNATInterface = "ludusnat"
	}
	if c.LudusNATIP == "" {
		c.LudusNATIP = "192.0.2.253"
	}
	if c.LudusNATGateway == "" {
		c.LudusNATGateway = "192.0.2.254"
	}
	if c.TLSCertFile == "" {
		c.TLSCertFile = ludusInstallPath + "/tls/server.crt"
	}
	if c.TLSKeyFile == "" {
		c.TLSKeyFile = ludusInstallPath + "/tls/server.key"
	}
	if c.SDNZone == "" {
		c.SDNZone = "ludus"
	}
	if c.DataDirectory == "" {
		c.DataDirectory = ludusInstallPath + "/db"
	}
	if c.WireguardPort == 0 {
		c.WireguardPort = 51820
	}
	if c.ProxmoxVMStoragePool == "" {
		c.ProxmoxVMStoragePool = "local"
	}
	if c.ProxmoxVMStorageFormat == "" {
		c.ProxmoxVMStorageFormat = "qcow2"
	}
	if c.ProxmoxISOStoragePool == "" {
		c.ProxmoxISOStoragePool = "local"
	}
}

// ApplyShimAndValidate migrates deprecated fields and validates the config.
// Called after viper.Unmarshal in ParseConfig and by tests.
func (c *Configuration) ApplyShimAndValidate() error {
	// Shim: proxmox_url -> proxmox_endpoints
	if len(c.ProxmoxEndpoints) == 0 && c.ProxmoxURL != "" {
		log.Printf("WARN: config key 'proxmox_url' is deprecated; use 'proxmox_endpoints: [%q]'", c.ProxmoxURL)
		c.ProxmoxEndpoints = []string{c.ProxmoxURL}
	}
	// Shim: proxmox_public_ip -> wireguard_endpoint
	if c.WireguardEndpoint == "" && c.ProxmoxPublicIP != "" {
		log.Printf("WARN: config key 'proxmox_public_ip' is deprecated; use 'wireguard_endpoint'")
		c.WireguardEndpoint = c.ProxmoxPublicIP
	}
	// Default realm (for direct-unmarshal callers like tests)
	if c.ProxmoxUserRealm == "" {
		c.ProxmoxUserRealm = "pve"
	}
	// Validate endpoints
	if len(c.ProxmoxEndpoints) == 0 {
		return fmt.Errorf("proxmox_endpoints must contain at least one URL")
	}
	for _, ep := range c.ProxmoxEndpoints {
		if !strings.Contains(ep, "://") {
			return fmt.Errorf("proxmox_endpoints: %q must include scheme (e.g. https://%s)", ep, ep)
		}
		u, err := url.Parse(ep)
		if err != nil {
			return fmt.Errorf("proxmox_endpoints: invalid URL %q: %w", ep, err)
		}
		if u.Scheme != "https" && u.Scheme != "http" {
			return fmt.Errorf("proxmox_endpoints: %q must include scheme (e.g. https://%s)", ep, ep)
		}
		host := u.Hostname()
		if host == "127.0.0.1" || strings.EqualFold(host, "localhost") || host == "::1" {
			return fmt.Errorf("proxmox_endpoints: %q uses 127.0.0.1/localhost — Ludus now runs in an LXC and must reach Proxmox over the network; use the node's real IP", ep)
		}
	}
	// Reverse-shim: populate deprecated ProxmoxURL from the first endpoint so
	// legacy call sites (per-user clients, packer, ansible-inventory) keep working.
	if c.ProxmoxURL == "" {
		c.ProxmoxURL = c.ProxmoxEndpoints[0]
	}
	if c.ProxmoxTokenID == "" || c.ProxmoxTokenSecret == "" {
		return fmt.Errorf("proxmox_token_id and proxmox_token_secret are required")
	}
	return nil
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

// PVEClientConfig returns a pveclient.Config populated from this Configuration.
// It satisfies pveclient.Builder, enabling pveclient.FromBuilder(cfg) call sites.
func (c *Configuration) PVEClientConfig() pveclient.Config {
	return pveclient.Config{
		Endpoints:   c.ProxmoxEndpoints,
		TokenID:     c.ProxmoxTokenID,
		TokenSecret: c.ProxmoxTokenSecret,
		InsecureTLS: c.ProxmoxInvalidCert,
	}
}
