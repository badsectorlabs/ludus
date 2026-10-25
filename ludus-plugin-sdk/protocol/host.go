package protocol

import (
	"errors"
	"fmt"
	"net/rpc"
	"time"
)

// HostServiceName is the net/rpc service name the host registers on the
// broker stream it passes in InitializeRequest.HostService.
const HostServiceName = "Host"

// ErrorCode classifies a host-service failure. The net/rpc error return is
// reserved for transport failures; host decisions travel as *HostError.
type ErrorCode string

const (
	CodeNotFound    ErrorCode = "not_found"
	CodeForbidden   ErrorCode = "forbidden"
	CodeInvalid     ErrorCode = "invalid"
	CodeExpired     ErrorCode = "expired"
	CodeUnavailable ErrorCode = "unavailable"
)

type HostError struct {
	Code    ErrorCode
	Message string
}

func (e *HostError) Error() string {
	if e.Message == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Message
}

// Is matches another *HostError with the same code, so callers can test
// errors.Is(err, &HostError{Code: CodeForbidden}).
func (e *HostError) Is(target error) bool {
	var other *HostError
	return errors.As(target, &other) && other.Code == e.Code
}

// HostErrorf builds a *HostError.
func HostErrorf(code ErrorCode, format string, args ...any) *HostError {
	return &HostError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ReplyStatus is embedded in every host-service reply.
type ReplyStatus struct {
	Err *HostError
}

func (s *ReplyStatus) status() *HostError { return s.Err }

type hostReply interface{ status() *HostError }

// Arguments. Every argument struct carries the CallID of the host → plugin
// call it is made on behalf of.

type CallArgs struct {
	Call CallID
}

type RangeArgs struct {
	Call   CallID
	Number int
}

type FindVMArgs struct {
	Call      CallID
	ProxmoxID int
}

type ReadRangeFileArgs struct {
	Call     CallID
	Number   int
	File     RangeFile
	MaxBytes int64 // <= 0 means the host default (1 MiB); logs return their tail
}

type LogsArgs struct {
	Call        CallID
	RangeNumber int   // 0 means every range
	Limit       int   // <= 0 means 100
	TailEntries int   // how many of the newest archived entries get a Tail
	TailBytes   int64 // size of each Tail
}

type SetInactivityTimeoutArgs struct {
	Call    CallID
	Number  int
	Timeout string // "" server default, "0" disabled, otherwise a Go duration
}

type QuotaStatusArgs struct {
	Call   CallID
	UserID string // "" means the effective user of the request
}

type SetUserQuotaArgs struct {
	Call    CallID
	UserIDs []string
	Patch   QuotaPatch
}

type SetGroupQuotaArgs struct {
	Call       CallID
	GroupNames []string
	Patch      QuotaPatch
}

type PowerOffArgs struct {
	Call   CallID
	Number int
	VMIDs  []int
}

type SetLicenseStateArgs struct {
	Call    CallID
	License License
}

type AddOnArgs struct {
	Call CallID
	Name string
}

// OpenAppArgs starts an app session for the caller, or, with Session set,
// issues a new launch URL for one of the caller's live sessions.
type OpenAppArgs struct {
	Call    CallID
	Path    string // where the browser lands, relative to the app root; "/" or "/#/fragment"
	Session string
}

// App session limits. A plugin's limits apply to its own sessions only.
const (
	DefaultAppSessionsPerUser = 16
	DefaultAppSessions        = 512
	MaxAppSessions            = 65536
)

// AppSessionLimitsArgs sets how many live app sessions the plugin allows per
// authenticated user and in total: 1 <= PerUser <= Total <= MaxAppSessions.
// Existing sessions are kept when limits are lowered; new ones are refused
// with CodeUnavailable while a limit is reached.
type AppSessionLimitsArgs struct {
	Call    CallID
	PerUser int
	Total   int
}

// Replies.

type EmptyReply struct{ ReplyStatus }

type BoolReply struct {
	ReplyStatus
	Value bool
}

type StringReply struct {
	ReplyStatus
	Value string
}

type BytesReply struct {
	ReplyStatus
	Value []byte
}

type RangesReply struct {
	ReplyStatus
	Ranges []Range
}

type VMsReply struct {
	ReplyStatus
	VMs []VM
}

type FindVMReply struct {
	ReplyStatus
	VM    VM
	Range Range
}

type LogsReply struct {
	ReplyStatus
	Logs []LogEntry
}

type PlaybookReply struct {
	ReplyStatus
	Result PlaybookResult
}

type ProxmoxCredentialsReply struct {
	ReplyStatus
	Credentials ProxmoxCredentials
}

type LicenseReply struct {
	ReplyStatus
	License License
}

type QuotaStatusReply struct {
	ReplyStatus
	Status UserQuotaStatus
}

type UserQuotaStatusesReply struct {
	ReplyStatus
	Statuses []UserQuotaStatus
}

type GroupQuotasReply struct {
	ReplyStatus
	Groups []GroupQuota
}

// AppLaunch is a one-time browser URL for an app session.
type AppLaunch struct {
	URL      string // same-origin path; the first browser visit redeems it
	Session  string
	Call     CallID // the session's CallID, valid until the session ends
	BasePath string
	Expires  time.Time
}

type OpenAppReply struct {
	ReplyStatus
	Launch AppLaunch
}

// BatchReply reports per-item failures of a batch operation that otherwise
// succeeded.
type BatchReply struct {
	ReplyStatus
	Errors []string
}

// HostAPI is the host service. The host's implementation must satisfy it; each
// method is served under HostServiceName via net/rpc.
//
// Principals (see PLUGIN_REDESIGN.md, "Host service"):
//   - caller: any plugin while handling a request, evaluated as the caller.
//     The CallID of an app session (AppContext.Call, AppLaunch.Call) is a
//     caller call that stays valid until the session ends.
//   - caller; system level: system-level plugins only, evaluated as the caller
//   - system level: the plugin principal of a system-level plugin, or a Handle
//     call to a system-level plugin whose caller is an admin
type HostAPI interface {
	OpenApp(OpenAppArgs, *OpenAppReply) error                              // caller; manifest app
	EndAppSession(CallArgs, *EmptyReply) error                             // app session call
	SetAppSessionLimits(AppSessionLimitsArgs, *EmptyReply) error           // any; manifest app
	TouchRange(RangeArgs, *EmptyReply) error                               // caller
	AccessibleRanges(CallArgs, *RangesReply) error                         // caller
	CanAccessRange(RangeArgs, *BoolReply) error                            // caller
	RangeVMs(RangeArgs, *VMsReply) error                                   // caller (any level), or system level for any range
	FindVM(FindVMArgs, *FindVMReply) error                                 // caller
	ReadRangeFile(ReadRangeFileArgs, *BytesReply) error                    // caller
	Logs(LogsArgs, *LogsReply) error                                       // caller
	RunPlaybook(PlaybookRequest, *PlaybookReply) error                     // caller
	ProxmoxCredentials(CallArgs, *ProxmoxCredentialsReply) error           // caller
	SetRangeInactivityTimeout(SetInactivityTimeoutArgs, *EmptyReply) error // caller
	License(CallArgs, *LicenseReply) error                                 // any
	QuotaStatus(QuotaStatusArgs, *QuotaStatusReply) error                  // caller; system level
	ListQuotaStatus(CallArgs, *UserQuotaStatusesReply) error               // caller; system level; admin
	ListGroupQuotas(CallArgs, *GroupQuotasReply) error                     // caller; system level; admin
	SetUserQuota(SetUserQuotaArgs, *BatchReply) error                      // caller; system level; admin
	SetGroupDefaultQuota(SetGroupQuotaArgs, *BatchReply) error             // caller; system level; admin
	MachineCredentialsDir(RangeArgs, *StringReply) error                   // caller; system level
	ListRanges(CallArgs, *RangesReply) error                               // system level
	PowerOffVMs(PowerOffArgs, *BatchReply) error                           // system level
	RootProxmoxCredentials(CallArgs, *ProxmoxCredentialsReply) error       // system level
	SetLicenseState(SetLicenseStateArgs, *EmptyReply) error                // licensed ludus-enterprise
	RequestAddOn(AddOnArgs, *EmptyReply) error                             // licensed ludus-enterprise
}

// HostClient is the plugin-side client of the host service. Methods return a
// *HostError for host decisions and a plain error for transport failures.
type HostClient struct {
	c *rpc.Client
}

func NewHostClient(c *rpc.Client) *HostClient { return &HostClient{c: c} }

func (h *HostClient) Close() error { return h.c.Close() }

func (h *HostClient) invoke(method string, args any, reply hostReply) error {
	if err := h.c.Call(HostServiceName+"."+method, args, reply); err != nil {
		return fmt.Errorf("host %s: %w", method, err)
	}
	if hostErr := reply.status(); hostErr != nil {
		return hostErr
	}
	return nil
}

func (h *HostClient) AccessibleRanges(call CallID) ([]Range, error) {
	var reply RangesReply
	err := h.invoke("AccessibleRanges", CallArgs{Call: call}, &reply)
	return reply.Ranges, err
}

func (h *HostClient) CanAccessRange(call CallID, number int) (bool, error) {
	var reply BoolReply
	err := h.invoke("CanAccessRange", RangeArgs{Call: call, Number: number}, &reply)
	return reply.Value, err
}

func (h *HostClient) RangeVMs(call CallID, number int) ([]VM, error) {
	var reply VMsReply
	err := h.invoke("RangeVMs", RangeArgs{Call: call, Number: number}, &reply)
	return reply.VMs, err
}

func (h *HostClient) FindVM(call CallID, proxmoxID int) (VM, Range, error) {
	var reply FindVMReply
	err := h.invoke("FindVM", FindVMArgs{Call: call, ProxmoxID: proxmoxID}, &reply)
	return reply.VM, reply.Range, err
}

func (h *HostClient) ReadRangeFile(call CallID, number int, file RangeFile, maxBytes int64) ([]byte, error) {
	var reply BytesReply
	err := h.invoke("ReadRangeFile", ReadRangeFileArgs{Call: call, Number: number, File: file, MaxBytes: maxBytes}, &reply)
	return reply.Value, err
}

func (h *HostClient) Logs(args LogsArgs) ([]LogEntry, error) {
	var reply LogsReply
	err := h.invoke("Logs", args, &reply)
	return reply.Logs, err
}

func (h *HostClient) RunPlaybook(request PlaybookRequest) (PlaybookResult, error) {
	var reply PlaybookReply
	err := h.invoke("RunPlaybook", request, &reply)
	return reply.Result, err
}

func (h *HostClient) ProxmoxCredentials(call CallID) (ProxmoxCredentials, error) {
	var reply ProxmoxCredentialsReply
	err := h.invoke("ProxmoxCredentials", CallArgs{Call: call}, &reply)
	return reply.Credentials, err
}

func (h *HostClient) SetRangeInactivityTimeout(call CallID, number int, timeout string) error {
	var reply EmptyReply
	return h.invoke("SetRangeInactivityTimeout", SetInactivityTimeoutArgs{Call: call, Number: number, Timeout: timeout}, &reply)
}

func (h *HostClient) License(call CallID) (License, error) {
	var reply LicenseReply
	err := h.invoke("License", CallArgs{Call: call}, &reply)
	return reply.License, err
}

func (h *HostClient) QuotaStatus(call CallID, userID string) (UserQuotaStatus, error) {
	var reply QuotaStatusReply
	err := h.invoke("QuotaStatus", QuotaStatusArgs{Call: call, UserID: userID}, &reply)
	return reply.Status, err
}

func (h *HostClient) ListQuotaStatus(call CallID) ([]UserQuotaStatus, error) {
	var reply UserQuotaStatusesReply
	err := h.invoke("ListQuotaStatus", CallArgs{Call: call}, &reply)
	return reply.Statuses, err
}

func (h *HostClient) ListGroupQuotas(call CallID) ([]GroupQuota, error) {
	var reply GroupQuotasReply
	err := h.invoke("ListGroupQuotas", CallArgs{Call: call}, &reply)
	return reply.Groups, err
}

func (h *HostClient) SetUserQuota(call CallID, userIDs []string, patch QuotaPatch) ([]string, error) {
	var reply BatchReply
	err := h.invoke("SetUserQuota", SetUserQuotaArgs{Call: call, UserIDs: userIDs, Patch: patch}, &reply)
	return reply.Errors, err
}

func (h *HostClient) SetGroupDefaultQuota(call CallID, groupNames []string, patch QuotaPatch) ([]string, error) {
	var reply BatchReply
	err := h.invoke("SetGroupDefaultQuota", SetGroupQuotaArgs{Call: call, GroupNames: groupNames, Patch: patch}, &reply)
	return reply.Errors, err
}

func (h *HostClient) MachineCredentialsDir(call CallID, number int) (string, error) {
	var reply StringReply
	err := h.invoke("MachineCredentialsDir", RangeArgs{Call: call, Number: number}, &reply)
	return reply.Value, err
}

func (h *HostClient) ListRanges(call CallID) ([]Range, error) {
	var reply RangesReply
	err := h.invoke("ListRanges", CallArgs{Call: call}, &reply)
	return reply.Ranges, err
}

func (h *HostClient) PowerOffVMs(call CallID, number int, vmIDs []int) ([]string, error) {
	var reply BatchReply
	err := h.invoke("PowerOffVMs", PowerOffArgs{Call: call, Number: number, VMIDs: vmIDs}, &reply)
	return reply.Errors, err
}

func (h *HostClient) RootProxmoxCredentials(call CallID) (ProxmoxCredentials, error) {
	var reply ProxmoxCredentialsReply
	err := h.invoke("RootProxmoxCredentials", CallArgs{Call: call}, &reply)
	return reply.Credentials, err
}

func (h *HostClient) SetLicenseState(call CallID, license License) error {
	var reply EmptyReply
	return h.invoke("SetLicenseState", SetLicenseStateArgs{Call: call, License: license}, &reply)
}

func (h *HostClient) RequestAddOn(call CallID, name string) error {
	var reply EmptyReply
	return h.invoke("RequestAddOn", AddOnArgs{Call: call, Name: name}, &reply)
}

func (h *HostClient) TouchRange(call CallID, number int) error {
	var reply EmptyReply
	return h.invoke("TouchRange", RangeArgs{Call: call, Number: number}, &reply)
}

func (h *HostClient) OpenApp(call CallID, path, session string) (AppLaunch, error) {
	var reply OpenAppReply
	err := h.invoke("OpenApp", OpenAppArgs{Call: call, Path: path, Session: session}, &reply)
	return reply.Launch, err
}

func (h *HostClient) EndAppSession(call CallID) error {
	var reply EmptyReply
	return h.invoke("EndAppSession", CallArgs{Call: call}, &reply)
}

func (h *HostClient) SetAppSessionLimits(call CallID, perUser, total int) error {
	var reply EmptyReply
	return h.invoke("SetAppSessionLimits", AppSessionLimitsArgs{Call: call, PerUser: perUser, Total: total}, &reply)
}
