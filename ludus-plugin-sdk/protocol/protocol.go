// Package protocol is the wire contract between the Ludus host and plugin
// processes. The host and the plugin SDK both import it. It depends only on
// hashicorp/go-plugin and the standard library.
package protocol

import "time"

const (
	// PluginName is the go-plugin dispense name.
	PluginName = "ludus"
	// ProtocolVersion stays 1 until release; breaking changes are made in place.
	ProtocolVersion = 1
	// MaxBodyBytes limits Request.Body and Response.Body.
	MaxBodyBytes = 256 << 20
)

// CallID identifies one host → plugin call. The plugin passes it back on every
// host-service call made on behalf of that call. It expires when the call returns.
type CallID string

// Level decides what a plugin may do inside Ludus.
type Level string

const (
	LevelStandard Level = "standard"
	LevelSystem   Level = "system"
)

// RunAs decides the OS user of the plugin process and the service that hosts it.
type RunAs string

const (
	RunAsLudus RunAs = "ludus"
	RunAsRoot  RunAs = "root"
)

// Source records how a package was installed.
type Source string

const (
	SourceUploaded Source = "uploaded"
	SourceServer   Source = "server"
	SourceLicensed Source = "licensed"
)

// RouteScope decides where a route is mounted.
type RouteScope string

const (
	// ScopePlugin mounts at /api/v2/plugins/{resourceID}/rpc/<Pattern>.
	ScopePlugin RouteScope = "plugin"
	// ScopeAPI mounts at /api/v2/<Pattern>. System level only.
	ScopeAPI RouteScope = "api"
)

type Route struct {
	Name    string     `json:"name"`
	Method  string     `json:"method"`
	Pattern string     `json:"pattern"`
	Scope   RouteScope `json:"scope,omitempty"` // empty means ScopePlugin
}

// EffectiveScope returns the route scope with the default applied.
func (r Route) EffectiveScope() RouteScope {
	if r.Scope == "" {
		return ScopePlugin
	}
	return r.Scope
}

// Metadata is what a running plugin reports. It must match the package's
// plugin.json (see Manifest.CheckMetadata); the host refuses to start it otherwise.
type Metadata struct {
	ID                       string
	Name                     string
	Version                  string
	Description              string
	Author                   string
	Level                    Level
	RunAs                    RunAs
	Routes                   []Route
	VMHooks                  VMHookCapabilities
	ForwardCallerCredentials bool
}

// VMHookCapabilities advertises VM lifecycle operations. System level only.
type VMHookCapabilities struct {
	Select       bool `json:"select,omitempty"`
	Start        bool `json:"start,omitempty"`
	Stop         bool `json:"stop,omitempty"`
	Status       bool `json:"status,omitempty"`
	BeforeDelete bool `json:"beforeDelete,omitempty"`
	Address      bool `json:"address,omitempty"`
}

// Any reports whether any capability is set.
func (c VMHookCapabilities) Any() bool {
	return c != VMHookCapabilities{}
}

type VMHookOperation string

const (
	VMHookSelect       VMHookOperation = "select"
	VMHookStart        VMHookOperation = "start"
	VMHookStop         VMHookOperation = "stop"
	VMHookStatus       VMHookOperation = "status"
	VMHookBeforeDelete VMHookOperation = "before-delete"
	VMHookAddress      VMHookOperation = "address"
)

type VMHookRequest struct {
	Call      CallID
	Operation VMHookOperation
	Source    string
	RangeID   string
	VMID      int
	VMName    string
	Node      string
	Pool      string
	Status    string
}

type VMHookDecision uint8

const (
	VMHookContinue VMHookDecision = iota
	VMHookHandled
)

type VMStatus string

const (
	VMStatusRunning  VMStatus = "running"
	VMStatusStopped  VMStatus = "stopped"
	VMStatusStarting VMStatus = "starting"
	VMStatusStopping VMStatus = "stopping"
)

type VMHookResponse struct {
	Decision VMHookDecision
	Selected bool
	Status   VMStatus
	Ready    bool
	Address  string
}

type InitializeRequest struct {
	Call CallID
	// HostService is the MuxBroker stream ID of the host service. The plugin
	// must dial it within the broker's 5-second accept window. Zero means the
	// host serves no host service (metadata-only test harnesses).
	HostService uint32
	Env         Environment
}

// WebApplication is the private loopback endpoint of the Guacamole application.
// Ludus injects Token into proxied requests; it never goes to the browser.
type WebApplication struct {
	URL   string
	Token string
}

type InitializeResponse struct {
	WebApplication *WebApplication
	Jobs           []ScheduledJob
}

// MinJobInterval is the shortest scheduled-job interval the host accepts.
const MinJobInterval = time.Second

type ScheduledJob struct {
	Name     string
	Interval time.Duration
}

type JobRequest struct {
	Call CallID
	Name string
}

// Environment is the host context a plugin receives at Initialize. It never
// carries database credentials, PocketBase tokens, the root Proxmox token, or
// the database encryption key.
type Environment struct {
	LudusVersion string
	ResourceID   string // plugin_resources record ID
	Level        Level
	RunAs        RunAs
	Source       Source
	// InstallPath is the Ludus install tree. Only system-level plugins write
	// into it, through the SDK's InstallFiles.
	InstallPath string
	// StateDir is <data_directory>/plugin-state/<plugin id>, created 0700 and
	// owned by the run-as user.
	StateDir string
	License  License
	Settings Settings
	API      APIEndpoint
	// LicenseKey is set only for the licensed ludus-enterprise package.
	LicenseKey string
}

type License struct {
	Valid        bool
	Message      string
	Name         string
	Expiry       *time.Time
	Entitlements []string
}

// Settings are explicit, non-secret server settings. Add a field when a plugin
// needs one.
type Settings struct {
	ProxmoxVMStoragePool  string
	ProxmoxISOStoragePool string
	LudusNATInterface     string
	LudusNATGateway       string
	LudusDNSServer        string
	LudusNATIP            string

	ProxmoxNode               string
	WireguardEndpoint         string
	InactivityShutdownTimeout time.Duration
	DefaultQuotaRAM           int
	DefaultQuotaCPU           int
	DefaultQuotaVMs           int
	DefaultQuotaRanges        int
	DataDirectory             string
}

// APIEndpoint locates the local Ludus API for plugins that act as a user
// through /api/v2 (see Metadata.ForwardCallerCredentials).
type APIEndpoint struct {
	URL               string
	CertificateSHA256 string
}

// Request is one HTTP request routed to a plugin, with the caller already
// authenticated and the effective user and range already resolved.
type Request struct {
	GuacAppBasePath string
	Call            CallID
	Route           string
	Method          string
	Path            string
	RawQuery        string
	Header          map[string][]string // Authorization, X-API-KEY, and Cookie removed unless Metadata.ForwardCallerCredentials
	Body            []byte
	RemoteAddr      string
	Auth            Principal
	User            *User  // effective user; nil for PocketBase superusers
	Range           *Range // range resolved by middleware; nil when none
}

type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// Principal is the authenticated identity of a request.
type Principal struct {
	RecordID    string
	UserID      string
	Name        string
	IsAdmin     bool
	IsSuperuser bool
}

// User is the effective user of a request. It differs from Principal when an
// admin impersonates another user with ?userID=.
type User struct {
	RecordID        string
	UserID          string
	Name            string
	ProxmoxUsername string
	DefaultRangeID  string
	IsAdmin         bool
}

type Range struct {
	RecordID       string // "" for the ROOT pseudo-range
	RangeID        string
	Name           string
	Number         int
	TestingEnabled bool
	State          string
	RouterVMName   string
	// InactivityShutdownTimeout is the raw per-range override: "" uses the
	// server default, "0" disables, otherwise a Go duration string.
	InactivityShutdownTimeout string
	LastActive                time.Time
}

type VM struct {
	OSType      string
	RecordID    string
	RangeID     string
	RangeNumber int
	Name        string
	IP          string
	ProxmoxID   int
	PoweredOn   bool
	IsRouter    bool
	CPU         int
	RAM         int
}

type LogEntry struct {
	ID       string
	Created  time.Time
	Status   string
	RangeID  string
	Template string
	Archived bool
	// Tail holds the end of the archived log when requested.
	Tail []byte
}

type ProxmoxCredentials struct {
	Endpoints   []string
	TokenID     string
	TokenSecret string
	InsecureTLS bool
}

type QuotaStatus struct {
	LimitRAM    int
	LimitCPU    int
	LimitVMs    int
	LimitRanges int
	UsedRAM     int
	UsedCPU     int
	UsedVMs     int
	UsedRanges  int
}

type UserQuotaStatus struct {
	UserID string
	Name   string
	QuotaStatus
	// Source of each limit: "U" user, "G" group default, "S" system default, "" unlimited.
	SourceRAM    string
	SourceCPU    string
	SourceVMs    string
	SourceRanges string
}

type GroupQuota struct {
	Name               string
	DefaultQuotaRAM    int
	DefaultQuotaCPU    int
	DefaultQuotaVMs    int
	DefaultQuotaRanges int
	MemberCount        int
}

// QuotaPatch sets the fields whose Set flag is true. Values must be
// non-negative. The flags exist because gob does not transmit zero values:
// a *int pointing at 0 would arrive as nil and "set to 0" would be lost.
type QuotaPatch struct {
	RAM       int
	CPU       int
	VMs       int
	Ranges    int
	SetRAM    bool
	SetCPU    bool
	SetVMs    bool
	SetRanges bool
}

// NewQuotaPatch builds a patch from optional values; nil leaves a field unchanged.
func NewQuotaPatch(ram, cpu, vms, ranges *int) QuotaPatch {
	var patch QuotaPatch
	if ram != nil {
		patch.RAM, patch.SetRAM = *ram, true
	}
	if cpu != nil {
		patch.CPU, patch.SetCPU = *cpu, true
	}
	if vms != nil {
		patch.VMs, patch.SetVMs = *vms, true
	}
	if ranges != nil {
		patch.Ranges, patch.SetRanges = *ranges, true
	}
	return patch
}

type RangeFile string

const (
	RangeFileConfig     RangeFile = "range-config"
	RangeFileAnsibleLog RangeFile = "ansible-log"
)

type PlaybookRoot string

const (
	// PlaybookRootStateDir resolves paths under Environment.StateDir.
	PlaybookRootStateDir PlaybookRoot = "state-dir"
	// PlaybookRootRangeManagement resolves paths under
	// <install>/ansible/range-management. System level only.
	PlaybookRootRangeManagement PlaybookRoot = "range-management"
)

type PlaybookRequest struct {
	Call           CallID
	Root           PlaybookRoot
	Playbook       string   // filepath.IsLocal, relative to Root
	ExtraVarsFiles []string // same rule as Playbook
	ExtraVarsJSON  []byte   // JSON object merged over the host vars
	Tags           string
	Limit          string
	Verbose        bool
}

type PlaybookResult struct {
	Output string
	Failed bool
	Errors []string // GetErrorsFromAnsiblePlaybook output when the run fails
	LogID  string
}
