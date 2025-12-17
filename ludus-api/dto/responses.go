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
	LicensedTo string    `json:"licensed_to,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitempty"`
	Active     bool      `json:"active,omitempty"`
	Message    string    `json:"message,omitempty"`
	Edition    string    `json:"edition,omitempty"`
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
	Name  string `json:"name,omitempty"`
	Built bool   `json:"built,omitempty"`
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
	LastDeployment time.Time                         `json:"lastDeployment"`
	TestingEnabled bool                              `json:"testingEnabled"`
}
type ListAllRangeResponseItemVMsItem struct {
	Ip          string `json:"ip,omitempty"`
	IsRouter    bool   `json:"isRouter,omitempty"`
	ID          int32  `json:"ID"`
	ProxmoxID   int32  `json:"proxmoxID"`
	RangeNumber int32  `json:"rangeNumber"`
	Name        string `json:"name"`
	PoweredOn   bool   `json:"poweredOn"`
}

type ListAllUsersResponseItem struct {
	Name            string    `json:"name"`
	UserID          string    `json:"userID"`
	UserNumber      int       `json:"userNumber,omitempty"`
	DateCreated     time.Time `json:"dateCreated,omitempty"`
	DateLastActive  time.Time `json:"dateLastActive,omitempty"`
	IsAdmin         bool      `json:"isAdmin"`
	ProxmoxUsername string    `json:"proxmoxUsername,omitempty"`
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
	ProxmoxID   int32  `json:"proxmoxID"`
	RangeNumber int32  `json:"rangeNumber"`
	Name        string `json:"name"`
	PoweredOn   bool   `json:"poweredOn"`
	Ip          string `json:"ip,omitempty"`
	IsRouter    bool   `json:"isRouter,omitempty"`
	ID          int32  `json:"ID"`
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
