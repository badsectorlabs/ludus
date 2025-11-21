package dto

type AddTemplateFromTarRequest struct {
	Force bool   `json:"force,omitempty"`
	File  string `json:"file,omitempty"`
}
type AddUserRequest struct {
	UserID   string `json:"userID"`
	IsAdmin  bool   `json:"isAdmin"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}
type AllowRequest struct {
	Ips     []string `json:"ips,omitempty"`
	Domains []string `json:"domains,omitempty"`
}
type AntiSandboxEnableRequest struct {
	VmIDs                  string `json:"vmIDs,omitempty"`
	RegisteredOrganization string `json:"registeredOrganization,omitempty"`
	Vendor                 string `json:"vendor,omitempty"`
	DropFiles              bool   `json:"dropFiles,omitempty"`
	ProcessorName          string `json:"processorName,omitempty"`
	ProcessorVendor        string `json:"processorVendor,omitempty"`
	ProcessorIdentifier    string `json:"processorIdentifier,omitempty"`
	SystemBiosVersion      string `json:"systemBiosVersion,omitempty"`
	RegisteredOwner        string `json:"registeredOwner,omitempty"`
	ProcessorSpeed         string `json:"processorSpeed,omitempty"`
	Persist                bool   `json:"persist,omitempty"`
}
type BuildTemplatesRequest struct {
	Templates []string `json:"templates,omitempty"`
	Parallel  int      `json:"parallel,omitempty"`
}
type CreateGroupRequest struct {
	Description string `json:"description,omitempty"`
	Name        string `json:"name"`
}
type CreateRangeRequest struct {
	RangeID     string `json:"rangeID,omitempty"`
	Description string `json:"description,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	UserID      string `json:"userID,omitempty"`
	RangeNumber int64  `json:"rangeNumber,omitempty"`
	Name        string `json:"name"`
}
type DenyRequest struct {
	Domains []string `json:"domains,omitempty"`
	Ips     []string `json:"ips,omitempty"`
}
type DeployRangeRequest struct {
	Tags      string   `json:"tags,omitempty"`
	Force     bool     `json:"force,omitempty"`
	OnlyRoles []string `json:"only_roles,omitempty"`
	Limit     string   `json:"limit,omitempty"`
}
type InstallCollectionRequest struct {
	Collection string `json:"collection,omitempty"`
	Version    string `json:"version,omitempty"`
	Force      bool   `json:"force,omitempty"`
}
type InstallRoleRequest struct {
	Role    string `json:"role,omitempty"`
	Version string `json:"version,omitempty"`
	Force   bool   `json:"force,omitempty"`
	Action  string `json:"action,omitempty"`
	Global  bool   `json:"global,omitempty"`
}
type InstallRoleTarRequest struct {
	File  string `json:"file,omitempty"`
	Force bool   `json:"force,omitempty"`
}
type KmsLicenseRequest struct {
	ProductKey string  `json:"productKey,omitempty"`
	Vmids      []int32 `json:"vmids,omitempty"`
}
type PasswordResetRequest struct {
	UserID string `json:"userID,omitempty"`
}
type PostCredentialsRequest struct {
	ProxmoxPassword string `json:"proxmoxPassword,omitempty"`
	UserID          string `json:"userID,omitempty"`
}
type PostDefaultRangeIDRequest struct {
	DefaultRangeID string `json:"defaultRangeID"`
}
type PowerOffRangeRequest struct {
	Machines []string `json:"machines,omitempty"`
}
type PowerOnRangeRequest struct {
	Machines []string `json:"machines,omitempty"`
}
type PutConfigRequest struct {
	Force bool   `json:"force,omitempty"`
	File  string `json:"file,omitempty"`
}
type SnapshotsRemoveRequest struct {
	Name  string  `json:"name,omitempty"`
	Vmids []int32 `json:"vmids,omitempty"`
}
type SnapshotsRollbackRequest struct {
	Name  string  `json:"name,omitempty"`
	Vmids []int32 `json:"vmids,omitempty"`
}
type SnapshotsTakeRequest struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Vmids       []int  `json:"vmids,omitempty"`
	IncludeRAM  bool   `json:"includeRAM,omitempty"`
}
type StopTestingRequest struct {
	Force bool `json:"force,omitempty"`
}
type UpdateRequest struct {
	Name string `json:"name,omitempty"`
}
