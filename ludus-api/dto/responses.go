package dto

import (
	"time"
)

type AbortTemplatesResponse struct {
	Result string `json:"result,omitempty"`
}
type AddTemplateFromTarResponse struct {
	Result string `json:"result,omitempty"`
}
type AddUserResponse struct {
	DateCreated     time.Time `json:"dateCreated,omitempty"`
	DateLastActive  time.Time `json:"dateLastActive,omitempty"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername,omitempty"`
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	ApiKey          string    `json:"apiKey,omitempty"`
}
type AllowResponse struct {
	Allowed []string                  `json:"allowed,omitempty"`
	Errors  []AllowResponseErrorsItem `json:"errors,omitempty"`
}
type AllowResponseErrorsItem struct {
	Item   string `json:"item,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type AntiSandboxEnableResponse struct {
	Success []string                              `json:"success,omitempty"`
	Errors  []AntiSandboxEnableResponseErrorsItem `json:"errors,omitempty"`
}
type AntiSandboxEnableResponseErrorsItem struct {
	Item   string `json:"item,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type AntiSandboxInstallCustomResponse struct {
	Result string `json:"result,omitempty"`
}
type AntiSandboxInstallStandardResponse struct {
	Result string `json:"result,omitempty"`
}
type BuildTemplatesResponse struct {
	Result string `json:"result,omitempty"`
}
type CreateGroupResponse struct {
	Result string `json:"result,omitempty"`
}
type CreateRangeResponse struct {
	Result *CreateRangeResponseResult `json:"result,omitempty"`
}
type CreateRangeResponseResult struct {
	RangeNumber    int32                              `json:"rangeNumber"`
	Description    string                             `json:"description,omitempty"`
	Purpose        string                             `json:"purpose,omitempty"`
	LastDeployment time.Time                          `json:"lastDeployment"`
	TestingEnabled bool                               `json:"testingEnabled"`
	VMs            []CreateRangeResponseResultVMsItem `json:"VMs"`
	UserID         string                             `json:"userID,omitempty"`
	Name           string                             `json:"name,omitempty"`
	NumberOfVMs    int32                              `json:"numberOfVMs"`
	AllowedIPs     []string                           `json:"allowedIPs,omitempty"`
	AllowedDomains []string                           `json:"allowedDomains,omitempty"`
	RangeState     string                             `json:"rangeState,omitempty"`
}
type CreateRangeResponseResultVMsItem struct {
	ID          int32  `json:"ID"`
	ProxmoxID   int32  `json:"proxmoxID"`
	RangeNumber int32  `json:"rangeNumber"`
	Name        string `json:"name"`
	PoweredOn   bool   `json:"poweredOn"`
	Ip          string `json:"ip,omitempty"`
	IsRouter    bool   `json:"isRouter,omitempty"`
	CPU         int32  `json:"cpu"`
	RAM         int32  `json:"ram"`
}
type DeleteRangeResponse struct {
	Result string `json:"result,omitempty"`
}
type DeleteRangeVMsResponse struct {
	Result string `json:"result,omitempty"`
}
type DeleteTemplateResponse struct {
	Result string `json:"result,omitempty"`
}
type DenyResponse struct {
	Denied []string                 `json:"denied,omitempty"`
	Errors []DenyResponseErrorsItem `json:"errors,omitempty"`
}
type DenyResponseErrorsItem struct {
	Item   string `json:"item,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type GetAPIKeyResponse struct {
	Result *GetAPIKeyResponseResult `json:"result,omitempty"`
}
type GetAPIKeyResponseResult struct {
	ApiKey string `json:"apiKey,omitempty"`
	UserID string `json:"userID,omitempty"`
}
type GetAnsibleInventoryResponse struct {
	Result string `json:"result,omitempty"`
}
type GetConfigExampleResponse struct {
	Result interface{} `json:"result,omitempty"`
}
type GetConfigResponse struct {
	Result interface{} `json:"result,omitempty"`
}
type GetCredentialsResponse struct {
	Result *GetCredentialsResponseResult `json:"result,omitempty"`
}
type GetCredentialsResponseResult struct {
	ProxmoxUsername string `json:"proxmoxUsername,omitempty"`
	ProxmoxRealm    string `json:"proxmoxRealm,omitempty"`
	ProxmoxPassword string `json:"proxmoxPassword,omitempty"`
	LudusEmail      string `json:"ludusEmail,omitempty"`
}
type GetOrPostDefaultRangeIDResponse struct {
	DefaultRangeID string `json:"defaultRangeID"`
}
type GetEtcHostsResponse struct {
	Result string `json:"result,omitempty"`
}
type GetLicenseResponse struct {
	LicensedTo   string    `json:"licensed_to,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Active       bool      `json:"active,omitempty"`
	Message      string    `json:"message,omitempty"`
	Entitlements []string  `json:"entitlements,omitempty"`
}
type GetLogsResponse struct {
	Result string `json:"result,omitempty"`
	Cursor int64  `json:"cursor,omitempty"`
}
type GetPackerLogsResponse struct {
	Result string `json:"result,omitempty"`
	Cursor int64  `json:"cursor,omitempty"`
}
type GetRolesAndCollectionsResponse struct {
	Result []GetRolesAndCollectionsResponseItem `json:"result,omitempty"`
}
type GetRolesAndCollectionsResponseItem struct {
	Global  bool   `json:"global,omitempty"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	Type    string `json:"type,omitempty"`
}
type GetSSHConfigResponse struct {
	Result string `json:"result,omitempty"`
}
type GetSnapshotsResponse struct {
	Snapshots []GetSnapshotsResponseSnapshotsItem `json:"snapshots,omitempty"`
	Errors    []GetSnapshotsResponseErrorsItem    `json:"errors,omitempty"`
}
type GetSnapshotsResponseErrorsItem struct {
	Vmid   int32  `json:"vmid,omitempty"`
	Vmname string `json:"vmname,omitempty"`
	Error  string `json:"error,omitempty"`
}
type GetSnapshotsResponseSnapshotsItem struct {
	Name        string `json:"name,omitempty"`
	IncludesRAM bool   `json:"includesRAM,omitempty"`
	Description string `json:"description,omitempty"`
	Snaptime    int64  `json:"snaptime,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Vmid        int32  `json:"vmid,omitempty"`
	Vmname      string `json:"vmname,omitempty"`
}
type GetTemplatesResponse struct {
	Value []GetTemplatesResponseItem `json:"-"`
}
type GetTemplatesResponseItem struct {
	Name   string `json:"name,omitempty"`
	Built  bool   `json:"built,omitempty"`
	Status string `json:"status,omitempty"`
	OS     string `json:"os,omitempty"`
}
type GetTemplatesStatusResponse struct {
	Value []GetTemplatesStatusResponseItem `json:"-"`
}
type GetTemplatesStatusResponseItem struct {
	User     string `json:"user,omitempty"`
	Template string `json:"template,omitempty"`
}
type GetWireguardConfigResponse struct {
	Result *GetWireguardConfigResponseResult `json:"result,omitempty"`
}
type GetWireguardConfigResponseResult struct {
	WireGuardConfig string `json:"wireGuardConfig,omitempty"`
}
type IndexResponse struct {
	Result string `json:"result,omitempty"`
}
type InstallCollectionResponse struct {
	Result string `json:"result,omitempty"`
}
type InstallRoleResponse struct {
	Result string `json:"result,omitempty"`
}
type InstallRoleTarResponse struct {
	Result string `json:"result,omitempty"`
}
type KmsInstallResponse struct {
	Result string `json:"result,omitempty"`
}
type KmsLicenseResponse struct {
	Success []int64                        `json:"success,omitempty"`
	Errors  []KmsLicenseResponseErrorsItem `json:"errors,omitempty"`
}
type KmsLicenseResponseErrorsItem struct {
	Item   string `json:"item,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ListAllRangeResponseItem struct {
	VMs            []ListAllRangeResponseItemVMsItem `json:"VMs"`
	RangeID        string                            `json:"rangeID,omitempty"`
	Name           string                            `json:"name,omitempty"`
	NumberOfVMs    int32                             `json:"numberOfVMs"`
	AllowedIPs     []string                          `json:"allowedIPs,omitempty"`
	AllowedDomains []string                          `json:"allowedDomains,omitempty"`
	RangeState     string                            `json:"rangeState,omitempty"`
	RangeNumber    int32                             `json:"rangeNumber"`
	Description    string                            `json:"description,omitempty"`
	Purpose        string                            `json:"purpose,omitempty"`
	ThumbnailUrl   string                            `json:"thumbnailUrl,omitempty"`
	LastDeployment time.Time                         `json:"lastDeployment"`
	TestingEnabled bool                              `json:"testingEnabled"`
}
type ListAllRangeResponseItemVMsItem struct {
	Ip            string `json:"ip,omitempty"`
	IsRouter      bool   `json:"isRouter,omitempty"`
	ID            int32  `json:"ID"`
	ProxmoxID     int32  `json:"proxmoxID"`
	RangeNumber   int32  `json:"rangeNumber"`
	Name          string `json:"name"`
	PoweredOn     bool   `json:"poweredOn"`
	CPU           int32  `json:"cpu"`
	RAM           int32  `json:"ram"`
	OsVersion     string `json:"osVersion,omitempty"`
	LicenseStatus string `json:"licenseStatus,omitempty"`
	LastUpdate    string `json:"lastUpdate,omitempty"`
}

type ListAllUsersResponseItem struct {
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	UserNumber      int       `json:"userNumber,omitempty"`
	DateCreated     time.Time `json:"dateCreated,omitempty"`
	DateLastActive  time.Time `json:"dateLastActive,omitempty"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername,omitempty"`
	QuotaRAM        int       `json:"quotaRAM,omitempty"`
	QuotaCPU        int       `json:"quotaCPU,omitempty"`
	QuotaVMs        int       `json:"quotaVMs,omitempty"`
	QuotaRanges     int       `json:"quotaRanges,omitempty"`
}

type ListGroupMembersResponseItem struct {
	Name   string `json:"name,omitempty"`
	UserID string `json:"userID,omitempty"`
	Role   string `json:"role,omitempty"`
}
type ListGroupRangesResponseItem struct {
	NumberOfVMs    int32                                `json:"numberOfVMs"`
	AllowedIPs     []string                             `json:"allowedIPs,omitempty"`
	AllowedDomains []string                             `json:"allowedDomains,omitempty"`
	RangeState     string                               `json:"rangeState,omitempty"`
	RangeNumber    int32                                `json:"rangeNumber"`
	Description    string                               `json:"description,omitempty"`
	Purpose        string                               `json:"purpose,omitempty"`
	ThumbnailUrl   string                               `json:"thumbnailUrl,omitempty"`
	LastDeployment time.Time                            `json:"lastDeployment"`
	TestingEnabled bool                                 `json:"testingEnabled"`
	VMs            []ListGroupRangesResponseItemVMsItem `json:"VMs"`
	RangeID        string                               `json:"rangeID,omitempty"`
	Name           string                               `json:"name,omitempty"`
}
type ListGroupRangesResponseItemVMsItem struct {
	RangeNumber int32  `json:"rangeNumber"`
	Name        string `json:"name"`
	PoweredOn   bool   `json:"poweredOn"`
	Ip          string `json:"ip,omitempty"`
	IsRouter    bool   `json:"isRouter,omitempty"`
	ID          int32  `json:"ID"`
	ProxmoxID   int32  `json:"proxmoxID"`
	CPU         int32  `json:"cpu"`
	RAM         int32  `json:"ram"`
}
type ListGroupsResponseItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	NumMembers  int    `json:"numMembers"`
	NumManagers int    `json:"numManagers"`
	NumRanges   int    `json:"numRanges"`
}

type GetUserMembershipsResponseItem struct {
	GroupName   string `json:"groupName"`
	Description string `json:"description,omitempty"`
	Role        string `json:"role"` // "member" or "manager"
}

type ListRangeResponse struct {
	RangeState     string                     `json:"rangeState,omitempty"`
	RangeNumber    int32                      `json:"rangeNumber"`
	Description    string                     `json:"description,omitempty"`
	Purpose        string                     `json:"purpose,omitempty"`
	ThumbnailUrl   string                     `json:"thumbnailUrl,omitempty"`
	LastDeployment time.Time                  `json:"lastDeployment"`
	TestingEnabled bool                       `json:"testingEnabled"`
	VMs            []ListRangeResponseVMsItem `json:"VMs"`
	RangeID        string                     `json:"rangeID,omitempty"`
	Name           string                     `json:"name,omitempty"`
	NumberOfVMs    int32                      `json:"numberOfVMs"`
	AllowedIPs     []string                   `json:"allowedIPs,omitempty"`
	AllowedDomains []string                   `json:"allowedDomains,omitempty"`
}
type ListRangeResponseVMsItem struct {
	ProxmoxID     int32  `json:"proxmoxID"`
	RangeNumber   int32  `json:"rangeNumber"`
	Name          string `json:"name"`
	PoweredOn     bool   `json:"poweredOn"`
	Ip            string `json:"ip,omitempty"`
	IsRouter      bool   `json:"isRouter,omitempty"`
	CPU           int32  `json:"cpu"`
	RAM           int32  `json:"ram"`
	ID            int32  `json:"ID"`
	OsVersion     string `json:"osVersion,omitempty"`
	LicenseStatus string `json:"licenseStatus,omitempty"`
	LastUpdate    string `json:"lastUpdate,omitempty"`
}
type ListRangeTagsResponse struct {
	Tags []string `json:"tags"`
}
type ListRangeUsersResponseItem struct {
	UserID     string `json:"userID,omitempty"`
	UserNumber int    `json:"userNumber,omitempty"`
	Name       string `json:"name,omitempty"`
	AccessType string `json:"accessType,omitempty"`
}
type ListUserAccessibleRangesResponse struct {
	Result []ListUserAccessibleRangesResponseItem `json:"result"`
}
type ListUserAccessibleRangesResponseItem struct {
	RangeNumber int    `json:"rangeNumber,omitempty"`
	RangeID     string `json:"rangeID,omitempty"`
	AccessType  string `json:"accessType,omitempty"`
}
type ListBlueprintsResponseItem struct {
	BlueprintID  string    `json:"blueprintID"`
	Name         string    `json:"name"`
	Description  string    `json:"description,omitempty"`
	ThumbnailURL string    `json:"thumbnailUrl,omitempty"`
	OwnerUserID  string    `json:"ownerUserID"`
	SharedUsers  []string  `json:"sharedUsers,omitempty"`
	SharedGroups []string  `json:"sharedGroups,omitempty"`
	AccessType   string    `json:"accessType,omitempty"`
	SourceID     string    `json:"sourceID,omitempty"`
	Tags         []string  `json:"tags,omitempty"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}
type ListBlueprintAccessUsersResponseItem struct {
	UserID string   `json:"userID"`
	Name   string   `json:"name,omitempty"`
	Access []string `json:"access,omitempty"`
	Groups []string `json:"groups,omitempty"`
}
type ListBlueprintAccessGroupsResponseItem struct {
	GroupName string   `json:"groupName"`
	Managers  []string `json:"managers,omitempty"`
	Members   []string `json:"members,omitempty"`
}

type ListSourceTemplatesResponseItem struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ListSourceRolesResponseItem struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Scope   string `json:"scope"`
}

type ListSourceCollectionsResponseItem struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type ListUserResponse struct {
	Value []ListUserResponseItem `json:"-"`
}
type ListUserResponseItem struct {
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	UserNumber      int       `json:"userNumber,omitempty"`
	DateCreated     time.Time `json:"dateCreated,omitempty"`
	DateLastActive  time.Time `json:"dateLastActive,omitempty"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername,omitempty"`
	QuotaRAM        int       `json:"quotaRAM,omitempty"`
	QuotaCPU        int       `json:"quotaCPU,omitempty"`
	QuotaVMs        int       `json:"quotaVMs,omitempty"`
	QuotaRanges     int       `json:"quotaRanges,omitempty"`
}
type ProvisionOAuth2UserResponse struct {
	RecordID string `json:"recordID"`
}
type PostCredentialsResponse struct {
	Result string `json:"result,omitempty"`
}
type PowerOffRangeResponse struct {
	Result string `json:"result,omitempty"`
}
type PowerOnRangeResponse struct {
	Result string `json:"result,omitempty"`
}
type SnapshotsRemoveResponse struct {
	Success []int64                             `json:"success,omitempty"`
	Errors  []SnapshotsRemoveResponseErrorsItem `json:"errors,omitempty"`
}
type SnapshotsRemoveResponseErrorsItem struct {
	Vmid   int32  `json:"vmid,omitempty"`
	Vmname string `json:"vmname,omitempty"`
	Error  string `json:"error,omitempty"`
}
type SnapshotsRollbackResponse struct {
	Success []int64                               `json:"success,omitempty"`
	Errors  []SnapshotsRollbackResponseErrorsItem `json:"errors,omitempty"`
}
type SnapshotsRollbackResponseErrorsItem struct {
	Vmname string `json:"vmname,omitempty"`
	Error  string `json:"error,omitempty"`
	Vmid   int32  `json:"vmid,omitempty"`
}
type SnapshotsTakeResponse struct {
	Success []int64                           `json:"success,omitempty"`
	Errors  []SnapshotsTakeResponseErrorsItem `json:"errors,omitempty"`
}
type SnapshotsTakeResponseErrorsItem struct {
	Error  string `json:"error,omitempty"`
	Vmid   int32  `json:"vmid,omitempty"`
	Vmname string `json:"vmname,omitempty"`
}
type UpdateResponse struct {
	Result string `json:"result,omitempty"`
}

type GetDiagnosticsResponse struct {
	CPU          GetDiagnosticsResponseCPU           `json:"cpu"`
	StoragePools []GetDiagnosticsResponseStoragePool `json:"storage_pools"`
	Pveperf      GetDiagnosticsResponsePveperf       `json:"pveperf"`
}

type GetDiagnosticsResponseCPU struct {
	Model string `json:"model"`
	Cores int    `json:"cores"`
}

type GetDiagnosticsResponseStoragePool struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	SizeGB         float64 `json:"size_gb"`
	UsedGB         float64 `json:"used_gb"`
	FreeGB         float64 `json:"free_gb"`
	FreePercentage float64 `json:"free_percentage"`
}

type GetDiagnosticsResponsePveperf struct {
	CPUBogomips     float64 `json:"cpu_bogomips"`
	RegexPerSecond  int64   `json:"regex_per_second"`
	HdSize          string  `json:"hd_size"`
	BufferedReads   string  `json:"buffered_reads"`
	AverageSeekTime string  `json:"average_seek_time"`
	FsyncsPerSecond float64 `json:"fsyncs_per_second"`
	DNSExt          string  `json:"dns_ext"`
}

type GetConsoleWebsocketTicketResponse struct {
	Ticket   string `json:"ticket"`
	Password string `json:"password"`
	Port     int    `json:"port"`
}

type CreateRangeResponseError struct {
	Errors []CreateRangeResponseErrorItem `json:"errors"`
}
type CreateRangeResponseErrorItem struct {
	UserID string `json:"userID"`
	Error  string `json:"error"`
}

type GetSubscriptionRolesResponseItem struct {
	Role             string `json:"role,omitempty"`
	FileCount        int    `json:"file_count,omitempty"`
	LastModified     string `json:"last_modified,omitempty"`
	LastModifiedUnix string `json:"last_modified_unix,omitempty"`
	Version          string `json:"version,omitempty"`
	Description      string `json:"description,omitempty"`
	PackageUUID      string `json:"package_uuid,omitempty"`
	Entitlements     string `json:"entitlements,omitempty"`
}
type InstallSubscriptionRolesResponse struct {
	Success []string                                     `json:"success"`
	Errors  []InstallSubscriptionRolesResponseErrorsItem `json:"errors"`
}
type InstallSubscriptionRolesResponseErrorsItem struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}
type GetRoleVarsResponse struct {
	Roles []GetRoleVarsResponseRole `json:"roles"`
}
type GetRoleVarsResponseRole struct {
	Name   string                 `json:"name"`
	Global bool                   `json:"global,omitempty"`
	Vars   map[string]interface{} `json:"vars"`
}
type MoveRoleScopeResponse struct {
	Success []string                          `json:"success"`
	Errors  []MoveRoleScopeResponseErrorsItem `json:"errors"`
}
type MoveRoleScopeResponseErrorsItem struct {
	Role  string `json:"role"`
	Error string `json:"error"`
}
type BulkGroupOperationResponse struct {
	Success []string                      `json:"success,omitempty"`
	Errors  []BulkGroupOperationErrorItem `json:"errors,omitempty"`
}
type BulkGroupOperationErrorItem struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}
type BulkBlueprintOperationResponse struct {
	Success []string                          `json:"success,omitempty"`
	Errors  []BulkBlueprintOperationErrorItem `json:"errors,omitempty"`
}
type BulkBlueprintOperationErrorItem struct {
	Item   string `json:"item"`
	Reason string `json:"reason"`
}

// SDNStatus represents the SDN migration status returned by the API
// and consumed by the ludus client.
type SDNStatus struct {
	SDNZoneExists      bool   `json:"sdn_zone_exists"`
	NATVNetExists      bool   `json:"nat_vnet_exists"`
	NeedsMigration     bool   `json:"needs_migration"`
	ClusterMode        bool   `json:"cluster_mode"`
	RequiresManualZone bool   `json:"requires_manual_zone"`
	CurrentSDNZone     string `json:"current_sdn_zone"`
	LudusNATInterface  string `json:"ludus_nat_interface"`
	Message            string `json:"message"`
}

type VersionResponse struct {
	Version string `json:"version"`
	Result  string `json:"result"`
}

type GetQuotaStatusResponse struct {
	LimitRAM    int `json:"limitRAM"`
	LimitCPU    int `json:"limitCPU"`
	LimitVMs    int `json:"limitVMs"`
	LimitRanges int `json:"limitRanges"`
	UsedRAM     int `json:"usedRAM"`
	UsedCPU     int `json:"usedCPU"`
	UsedVMs     int `json:"usedVMs"`
	UsedRanges  int `json:"usedRanges"`
}

type GetAllQuotaStatusResponseItem struct {
	UserID       string `json:"userID"`
	Name         string `json:"name"`
	LimitRAM     int    `json:"limitRAM"`
	LimitCPU     int    `json:"limitCPU"`
	LimitVMs     int    `json:"limitVMs"`
	LimitRanges  int    `json:"limitRanges"`
	UsedRAM      int    `json:"usedRAM"`
	UsedCPU      int    `json:"usedCPU"`
	UsedVMs      int    `json:"usedVMs"`
	UsedRanges   int    `json:"usedRanges"`
	SourceRAM    string `json:"sourceRAM"`
	SourceCPU    string `json:"sourceCPU"`
	SourceVMs    string `json:"sourceVMs"`
	SourceRanges string `json:"sourceRanges"`
}

type GetGroupQuotaResponseItem struct {
	Name               string `json:"name"`
	DefaultQuotaRAM    int    `json:"defaultQuotaRAM"`
	DefaultQuotaCPU    int    `json:"defaultQuotaCPU"`
	DefaultQuotaVMs    int    `json:"defaultQuotaVMs"`
	DefaultQuotaRanges int    `json:"defaultQuotaRanges"`
	MemberCount        int    `json:"memberCount"`
}

type LogHistoryEntry struct {
	Id       string    `json:"id"`
	Template string    `json:"template,omitempty"`
	Status   string    `json:"status"`
	Start    time.Time `json:"start,omitempty"`
	End      time.Time `json:"end,omitempty"`
	Created  time.Time `json:"created"`
}

type LogHistoryDetailResponse struct {
	Id      string    `json:"id"`
	Status  string    `json:"status"`
	Start   time.Time `json:"start,omitempty"`
	End     time.Time `json:"end,omitempty"`
	Created time.Time `json:"created"`
	Result  string    `json:"result"`
}

type AutoShutdownDetail struct {
	ServerDefault string `json:"serverDefault"`
	RangeOverride string `json:"rangeOverride"`
	Effective     string `json:"effective"`
}

type AutoShutdownResponse struct {
	AutoShutdownTimeout AutoShutdownDetail `json:"autoShutdownTimeout"`
}

type DeleteSourceResponse struct {
	Status string `json:"status"`
}

// BlueprintCreatedResponse is the shape returned by CreateBlueprint,
// CreateBlueprintFromRange, CopyBlueprint, and ImportBlueprint.
type BlueprintCreatedResponse struct {
	Result      string                               `json:"result,omitempty"`
	BlueprintID string                               `json:"blueprintID"`
	ID          string                               `json:"id,omitempty"` // record ID; emitted by ImportBlueprint
	RoleResults []BlueprintCreatedResponseRoleResult `json:"roleResults,omitempty"`
}

type BlueprintCreatedResponseRoleResult struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
}

type SourceResponse struct {
	ID             string   `json:"id"`
	SourceID       string   `json:"sourceID"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Authors        []string `json:"authors,omitempty"`
	Homepage       string   `json:"homepage,omitempty"`
	License        string   `json:"license,omitempty"`
	Kind           string   `json:"kind"`
	Type           string   `json:"type"`
	URL            string   `json:"url,omitempty"`
	Ref            string   `json:"ref,omitempty"`
	OwnerUserID    string   `json:"ownerUserID"`
	LastSyncedAt   string   `json:"lastSyncedAt,omitempty"`
	LastSyncStatus string   `json:"lastSyncStatus,omitempty"`
	LastSyncError  string   `json:"lastSyncError,omitempty"`
}

// RegisterSourceResponse is what POST /sources returns. Always a catalog —
// installs are driven via POST /sources/{id}/install (an absent selection
// installs everything the source ships).
type RegisterSourceResponse struct {
	SourceID string           `json:"sourceID"`
	Catalog  SourceCatalogDTO `json:"catalog"`
}

// SourceCatalogDTO mirrors the internal SourceCatalog for the wire.
type SourceCatalogDTO struct {
	SourceID         string               `json:"sourceID"`
	SourceName       string               `json:"sourceName"`
	Blueprints       CatalogBlueprintsDTO `json:"blueprints"`
	Templates        []CatalogItemDTO     `json:"templates"`
	LocalRoles       []CatalogItemDTO     `json:"localRoles"`
	LocalCollections []CatalogItemDTO     `json:"localCollections"`
}

// CatalogBlueprintsDTO groups the source's blueprints with the dependency
// closure they pull in: galaxy roles/collections and subscription roles
// unioned across every blueprint's requirements.yml, plus any config.yml role
// references that aren't declared there.
type CatalogBlueprintsDTO struct {
	Items                  []CatalogBlueprintDTO     `json:"items"`
	RequiredRoles          []CatalogItemDTO          `json:"requiredRoles"`
	RequiredCollections    []CatalogItemDTO          `json:"requiredCollections"`
	SubscriptionRoles      []CatalogItemDTO          `json:"subscriptionRoles"`
	UndeclaredDependencies []UndeclaredDependencyDTO `json:"undeclaredDependencies,omitempty"`
}

type CatalogBlueprintDTO struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Version             string   `json:"version"`
	State               string   `json:"state"`
	InstalledVersion    string   `json:"installedVersion,omitempty"`
	RequiredTemplates   []string `json:"requiredTemplates,omitempty"`
	RequiredLocalRoles  []string `json:"requiredLocalRoles,omitempty"`
	RequiredRoles       []string `json:"requiredRoles,omitempty"`
	RequiredCollections []string `json:"requiredCollections,omitempty"`
}

type CatalogItemDTO struct {
	Name               string            `json:"name"`
	Description        string            `json:"description,omitempty"`
	Version            string            `json:"version,omitempty"`
	State              string            `json:"state"`
	InstalledVersion   string            `json:"installedVersion,omitempty"`
	Global             bool              `json:"global,omitempty"`
	Scopes             []ScopeInstallDTO `json:"scopes,omitempty"`
	Type               string            `json:"type,omitempty"`
	Fqcn               string            `json:"fqcn,omitempty"`
	RequiredBy         []string          `json:"requiredBy,omitempty"`
	VersionByBlueprint map[string]string `json:"versionByBlueprint,omitempty"`
}

// ScopeInstallDTO is one installed copy of a role: the scope it lives in
// ("global"/"user"), the on-disk version there, and its state against the
// required pin ("installed" / "upgrade_available"). A role can have entries
// for both scopes, at different versions.
type ScopeInstallDTO struct {
	Scope   string `json:"scope"`
	Version string `json:"version,omitempty"`
	State   string `json:"state,omitempty"`
}

// UndeclaredDependencyDTO mirrors the internal UndeclaredDependency.
// Kind classifies the gap so renderers can dedupe + group items and emit
// one guidance message per kind, instead of one prose hint per item.
type UndeclaredDependencyDTO struct {
	BlueprintID      string `json:"blueprintID"`
	Role             string `json:"role"`
	Kind             string `json:"kind"`                       // "missing_role" | "missing_collection"
	ParentCollection string `json:"parentCollection,omitempty"` // populated when kind=missing_collection
}

type SourceBlueprintListItem struct {
	ID                string   `json:"id"`
	SourceID          string   `json:"sourceID"`
	SourceBlueprintID string   `json:"sourceBlueprintID"`
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version,omitempty"`
	Authors           []string `json:"authors,omitempty"`
	Homepage          string   `json:"homepage,omitempty"`
	License           string   `json:"license,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	MinLudusVersion   string   `json:"min_ludus_version,omitempty"`
}
