package main

// Version is supplied by the release build. An explicit --version selects the
// matching appliance when running a locally built installer.
var Version = "dev"

// Options is shared by the interactive form and unattended installation. Paths
// refer to the Proxmox host unless the option belongs to a state utility mode.
type Options struct {
	Version            string
	Channel            string
	ReleaseBase        string
	ApplianceBase      string
	Offline            bool
	NoPrompt           bool
	MigrateHost        bool
	SkipVerification   bool
	VerifyProxmoxTLS   bool
	TemplateFile       string
	ISODirectory       string
	LicenseFile        string
	BgInfoFile         string
	EnterprisePlugin   string
	CACertificate      string
	ChecksumFile       string
	ChecksumSignature  string
	ChecksumPublicKey  string
	ImportDB           string
	TokenID            string
	TokenSecret        string
	Endpoints          string
	VMID               int
	Hostname           string
	Storage            string
	RootFSSize         int
	Bridge             string
	VLANTag            string
	IP                 string
	Gateway            string
	Nameserver         string
	WireGuardEndpoint  string
	WireGuardPort      int
	NoWireGuardForward bool
	VMStorage          string
	VMStorageFormat    string
	ISOStorage         string
	License            string
	// The initial administrator is collected only by the interactive form for
	// fresh installs; there are deliberately no flags carrying a password.
	AdminName          string
	AdminEmail         string
	AdminUserID        string
	AdminPassword      string
	Node               string
	NATIP              string
	NATGateway         string
	SDNZone            string
	APIPort            int
	AdminPort          int
	HostManagedNetwork bool
	MigrationInfo      bool
	ExportState        string
	ExportLive         string
	ImportState        string
	Provided           map[string]bool
}

type ClusterNode struct {
	Name  string
	IP    string
	Local bool
}

type StorageInfo struct {
	Name      string
	Type      string
	Content   string
	Active    bool
	Shared    bool
	Available int64
}

type Inventory struct {
	Node      string
	LocalIP   string
	Nodes     []ClusterNode
	Endpoints []string
	NextVMID  int
	VMIDs     map[int]bool
	Storages  []StorageInfo
	Bridges   []string
	// Existing Proxmox user IDs (name@realm) and pool IDs, which a new Ludus
	// user must not collide with.
	ProxmoxUsers map[string]bool
	Pools        map[string]bool
}
