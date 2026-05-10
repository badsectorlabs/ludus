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
	RangeID     string   `json:"rangeID,omitempty"`
	Description string   `json:"description,omitempty"`
	Purpose     string   `json:"purpose,omitempty"`
	UserID      []string `json:"userID,omitempty"`
	RangeNumber int      `json:"rangeNumber,omitempty"`
	Name        string   `json:"name"`
	BlueprintID string   `json:"blueprintID,omitempty"`
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
type ProvisionOAuth2UserRequest struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	UserID          string `json:"userID"`
	Password        string `json:"password"`
	ProxmoxUsername string `json:"proxmoxUsername"`
	IsAdmin         bool   `json:"isAdmin"`
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
type InstallSubscriptionRolesRequest struct {
	Roles  []string `json:"roles"`
	Global bool     `json:"global"`
	Force  bool     `json:"force"`
}
type GetRoleVarsRequest struct {
	Roles []string `json:"roles"`
}
type MoveRoleScopeRequest struct {
	Roles  []string `json:"roles"`
	Global bool     `json:"global"`
	Copy   bool     `json:"copy,omitempty"` // If true, keep source; if false, remove source (move)
}
type BulkAddUsersToGroupRequest struct {
	UserIDs  []string `json:"userIDs"`
	Managers []string `json:"managers,omitempty"` // Optional: userIDs that should be managers
}
type BulkRemoveUsersFromGroupRequest struct {
	UserIDs []string `json:"userIDs"`
}
type BulkAddRangesToGroupRequest struct {
	RangeIDs []string `json:"rangeIDs"`
}
type BulkRemoveRangesFromGroupRequest struct {
	RangeIDs []string `json:"rangeIDs"`
}
type CreateBlueprintFromRangeRequest struct {
	BlueprintID string `json:"blueprintID,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	RangeID     string `json:"rangeID,omitempty"`
}
// CreateBlueprintRequest is the body for POST /blueprints — creating a
// blueprint from scratch (empty or seeded range-config). For other create
// modes use POST /blueprints/from-range, /blueprints/{id}/copy, or
// /blueprints/import.
type CreateBlueprintRequest struct {
	BlueprintID     string   `json:"blueprintID"`
	Name            string   `json:"name,omitempty"`
	Description     string   `json:"description,omitempty"`
	Version         string   `json:"version,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	MinLudusVersion string   `json:"min_ludus_version,omitempty"`
	// Config is optional YAML for range-config.yml. Empty means `ludus: []`.
	Config string `json:"config,omitempty"`
}
type CopyBlueprintRequest struct {
	BlueprintID string `json:"blueprintID,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}
type ApplyBlueprintRequest struct {
	RangeID string `json:"rangeID,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

type UpdateBlueprintConfigRequest struct {
	Config string `json:"config"`
}
type BulkShareBlueprintWithGroupsRequest struct {
	GroupNames []string `json:"groupNames"`
}
type BulkUnshareBlueprintWithGroupsRequest struct {
	GroupNames []string `json:"groupNames"`
}
type BulkShareBlueprintWithUsersRequest struct {
	UserIDs []string `json:"userIDs"`
}
type BulkUnshareBlueprintWithUsersRequest struct {
	UserIDs []string `json:"userIDs"`
}

type SetUserQuotaRequest struct {
	UserIDs     []string `json:"userIDs"`
	QuotaRAM    *int     `json:"quotaRAM"`
	QuotaCPU    *int     `json:"quotaCPU"`
	QuotaVMs    *int     `json:"quotaVMs"`
	QuotaRanges *int     `json:"quotaRanges"`
}

type SetGroupQuotaRequest struct {
	GroupNames         []string `json:"groupNames"`
	DefaultQuotaRAM    *int     `json:"defaultQuotaRAM"`
	DefaultQuotaCPU    *int     `json:"defaultQuotaCPU"`
	DefaultQuotaVMs    *int     `json:"defaultQuotaVMs"`
	DefaultQuotaRanges *int     `json:"defaultQuotaRanges"`
}

type AutoShutdownRequest struct {
	AutoShutdownTimeout string `json:"autoShutdownTimeout"`
}
type CreateSourceRequest struct {
	ID          string `json:"id,omitempty" form:"id"`
	Type        string `json:"type" form:"type"`
	URL         string `json:"url,omitempty" form:"url"`
	Ref         string `json:"ref,omitempty" form:"ref"`
	GlobalRoles bool   `json:"globalRoles,omitempty" form:"globalRoles"`
	Force       bool   `json:"force,omitempty" form:"force"`
	DryRun      bool   `json:"dryRun,omitempty" form:"dryRun"`
}
type UpdateSourceRequest struct {
	Ref         string `json:"ref"`
	GlobalRoles bool   `json:"globalRoles,omitempty"`
	Force       bool   `json:"force,omitempty"`
}
type SyncSourceRequest struct {
	GlobalRoles bool `json:"globalRoles,omitempty" form:"globalRoles"`
	Force       bool `json:"force,omitempty" form:"force"`
	DryRun      bool `json:"dryRun,omitempty" form:"dryRun"`
}
type DeleteSourceRequest struct {
	Purge bool `json:"purge,omitempty"`
}
type InstallBlueprintDepsRequest struct {
	GlobalRoles bool `json:"globalRoles,omitempty"`
	ForceRoles  bool `json:"forceRoles,omitempty"`
}
