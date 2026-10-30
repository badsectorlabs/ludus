// Package ludustest runs Ludus plugins in-process for unit tests.
package ludustest

import (
	"errors"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// Host is a fake host service. Each method calls the matching func field.
// An unset field answers CodeUnavailable. A returned *protocol.HostError is
// sent as the host decision; any other error becomes CodeInvalid.
type Host struct {
	OpenAppFunc             func(protocol.OpenAppArgs) (protocol.AppLaunch, error)
	EndAppSessionFunc       func(protocol.CallArgs) error
	SetAppSessionLimitsFunc func(protocol.AppSessionLimitsArgs) error
	TouchRangeFunc          func(protocol.RangeArgs) error

	AccessibleRangesFunc          func(protocol.CallArgs) ([]protocol.Range, error)
	CanAccessRangeFunc            func(protocol.RangeArgs) (bool, error)
	RangeVMsFunc                  func(protocol.RangeArgs) ([]protocol.VM, error)
	FindVMFunc                    func(protocol.FindVMArgs) (protocol.VM, protocol.Range, error)
	ReadRangeFileFunc             func(protocol.ReadRangeFileArgs) ([]byte, error)
	LogsFunc                      func(protocol.LogsArgs) ([]protocol.LogEntry, error)
	RunPlaybookFunc               func(protocol.PlaybookRequest) (protocol.PlaybookResult, error)
	ProxmoxCredentialsFunc        func(protocol.CallArgs) (protocol.ProxmoxCredentials, error)
	SetRangeInactivityTimeoutFunc func(protocol.SetInactivityTimeoutArgs) error
	LicenseFunc                   func(protocol.CallArgs) (protocol.License, error)
	QuotaStatusFunc               func(protocol.QuotaStatusArgs) (protocol.UserQuotaStatus, error)
	ListQuotaStatusFunc           func(protocol.CallArgs) ([]protocol.UserQuotaStatus, error)
	ListGroupQuotasFunc           func(protocol.CallArgs) ([]protocol.GroupQuota, error)
	SetUserQuotaFunc              func(protocol.SetUserQuotaArgs) ([]string, error)
	SetGroupDefaultQuotaFunc      func(protocol.SetGroupQuotaArgs) ([]string, error)
	MachineCredentialsDirFunc     func(protocol.RangeArgs) (string, error)
	ListRangesFunc                func(protocol.CallArgs) ([]protocol.Range, error)
	PowerOffVMsFunc               func(protocol.PowerOffArgs) ([]string, error)
	RootProxmoxCredentialsFunc    func(protocol.CallArgs) (protocol.ProxmoxCredentials, error)
	SetLicenseStateFunc           func(protocol.SetLicenseStateArgs) error
}

var _ protocol.HostAPI = (*Host)(nil)

func unset(method string) *protocol.HostError {
	return protocol.HostErrorf(protocol.CodeUnavailable, "ludustest.Host.%sFunc is not set", method)
}

func hostError(err error) *protocol.HostError {
	if err == nil {
		return nil
	}
	var hostErr *protocol.HostError
	if errors.As(err, &hostErr) {
		return hostErr
	}
	return &protocol.HostError{Code: protocol.CodeInvalid, Message: err.Error()}
}

func (h *Host) AccessibleRanges(args protocol.CallArgs, reply *protocol.RangesReply) error {
	if h.AccessibleRangesFunc == nil {
		reply.Err = unset("AccessibleRanges")
		return nil
	}
	ranges, err := h.AccessibleRangesFunc(args)
	reply.Ranges, reply.Err = ranges, hostError(err)
	return nil
}

func (h *Host) CanAccessRange(args protocol.RangeArgs, reply *protocol.BoolReply) error {
	if h.CanAccessRangeFunc == nil {
		reply.Err = unset("CanAccessRange")
		return nil
	}
	value, err := h.CanAccessRangeFunc(args)
	reply.Value, reply.Err = value, hostError(err)
	return nil
}

func (h *Host) RangeVMs(args protocol.RangeArgs, reply *protocol.VMsReply) error {
	if h.RangeVMsFunc == nil {
		reply.Err = unset("RangeVMs")
		return nil
	}
	vms, err := h.RangeVMsFunc(args)
	reply.VMs, reply.Err = vms, hostError(err)
	return nil
}

func (h *Host) FindVM(args protocol.FindVMArgs, reply *protocol.FindVMReply) error {
	if h.FindVMFunc == nil {
		reply.Err = unset("FindVM")
		return nil
	}
	vm, rng, err := h.FindVMFunc(args)
	reply.VM, reply.Range, reply.Err = vm, rng, hostError(err)
	return nil
}

func (h *Host) ReadRangeFile(args protocol.ReadRangeFileArgs, reply *protocol.BytesReply) error {
	if h.ReadRangeFileFunc == nil {
		reply.Err = unset("ReadRangeFile")
		return nil
	}
	value, err := h.ReadRangeFileFunc(args)
	reply.Value, reply.Err = value, hostError(err)
	return nil
}

func (h *Host) Logs(args protocol.LogsArgs, reply *protocol.LogsReply) error {
	if h.LogsFunc == nil {
		reply.Err = unset("Logs")
		return nil
	}
	logs, err := h.LogsFunc(args)
	reply.Logs, reply.Err = logs, hostError(err)
	return nil
}

func (h *Host) RunPlaybook(args protocol.PlaybookRequest, reply *protocol.PlaybookReply) error {
	if h.RunPlaybookFunc == nil {
		reply.Err = unset("RunPlaybook")
		return nil
	}
	result, err := h.RunPlaybookFunc(args)
	reply.Result, reply.Err = result, hostError(err)
	return nil
}

func (h *Host) ProxmoxCredentials(args protocol.CallArgs, reply *protocol.ProxmoxCredentialsReply) error {
	if h.ProxmoxCredentialsFunc == nil {
		reply.Err = unset("ProxmoxCredentials")
		return nil
	}
	credentials, err := h.ProxmoxCredentialsFunc(args)
	reply.Credentials, reply.Err = credentials, hostError(err)
	return nil
}

func (h *Host) SetRangeInactivityTimeout(args protocol.SetInactivityTimeoutArgs, reply *protocol.EmptyReply) error {
	if h.SetRangeInactivityTimeoutFunc == nil {
		reply.Err = unset("SetRangeInactivityTimeout")
		return nil
	}
	reply.Err = hostError(h.SetRangeInactivityTimeoutFunc(args))
	return nil
}

func (h *Host) License(args protocol.CallArgs, reply *protocol.LicenseReply) error {
	if h.LicenseFunc == nil {
		reply.Err = unset("License")
		return nil
	}
	license, err := h.LicenseFunc(args)
	reply.License, reply.Err = license, hostError(err)
	return nil
}

func (h *Host) QuotaStatus(args protocol.QuotaStatusArgs, reply *protocol.QuotaStatusReply) error {
	if h.QuotaStatusFunc == nil {
		reply.Err = unset("QuotaStatus")
		return nil
	}
	status, err := h.QuotaStatusFunc(args)
	reply.Status, reply.Err = status, hostError(err)
	return nil
}

func (h *Host) ListQuotaStatus(args protocol.CallArgs, reply *protocol.UserQuotaStatusesReply) error {
	if h.ListQuotaStatusFunc == nil {
		reply.Err = unset("ListQuotaStatus")
		return nil
	}
	statuses, err := h.ListQuotaStatusFunc(args)
	reply.Statuses, reply.Err = statuses, hostError(err)
	return nil
}

func (h *Host) ListGroupQuotas(args protocol.CallArgs, reply *protocol.GroupQuotasReply) error {
	if h.ListGroupQuotasFunc == nil {
		reply.Err = unset("ListGroupQuotas")
		return nil
	}
	groups, err := h.ListGroupQuotasFunc(args)
	reply.Groups, reply.Err = groups, hostError(err)
	return nil
}

func (h *Host) SetUserQuota(args protocol.SetUserQuotaArgs, reply *protocol.BatchReply) error {
	if h.SetUserQuotaFunc == nil {
		reply.Err = unset("SetUserQuota")
		return nil
	}
	failures, err := h.SetUserQuotaFunc(args)
	reply.Errors, reply.Err = failures, hostError(err)
	return nil
}

func (h *Host) SetGroupDefaultQuota(args protocol.SetGroupQuotaArgs, reply *protocol.BatchReply) error {
	if h.SetGroupDefaultQuotaFunc == nil {
		reply.Err = unset("SetGroupDefaultQuota")
		return nil
	}
	failures, err := h.SetGroupDefaultQuotaFunc(args)
	reply.Errors, reply.Err = failures, hostError(err)
	return nil
}

func (h *Host) MachineCredentialsDir(args protocol.RangeArgs, reply *protocol.StringReply) error {
	if h.MachineCredentialsDirFunc == nil {
		reply.Err = unset("MachineCredentialsDir")
		return nil
	}
	dir, err := h.MachineCredentialsDirFunc(args)
	reply.Value, reply.Err = dir, hostError(err)
	return nil
}

func (h *Host) ListRanges(args protocol.CallArgs, reply *protocol.RangesReply) error {
	if h.ListRangesFunc == nil {
		reply.Err = unset("ListRanges")
		return nil
	}
	ranges, err := h.ListRangesFunc(args)
	reply.Ranges, reply.Err = ranges, hostError(err)
	return nil
}

func (h *Host) PowerOffVMs(args protocol.PowerOffArgs, reply *protocol.BatchReply) error {
	if h.PowerOffVMsFunc == nil {
		reply.Err = unset("PowerOffVMs")
		return nil
	}
	failures, err := h.PowerOffVMsFunc(args)
	reply.Errors, reply.Err = failures, hostError(err)
	return nil
}

func (h *Host) RootProxmoxCredentials(args protocol.CallArgs, reply *protocol.ProxmoxCredentialsReply) error {
	if h.RootProxmoxCredentialsFunc == nil {
		reply.Err = unset("RootProxmoxCredentials")
		return nil
	}
	credentials, err := h.RootProxmoxCredentialsFunc(args)
	reply.Credentials, reply.Err = credentials, hostError(err)
	return nil
}

func (h *Host) SetLicenseState(args protocol.SetLicenseStateArgs, reply *protocol.EmptyReply) error {
	if h.SetLicenseStateFunc == nil {
		reply.Err = unset("SetLicenseState")
		return nil
	}
	reply.Err = hostError(h.SetLicenseStateFunc(args))
	return nil
}

func (h *Host) TouchRange(args protocol.RangeArgs, reply *protocol.EmptyReply) error {
	if h.TouchRangeFunc == nil {
		reply.Err = unset("TouchRange")
		return nil
	}
	reply.Err = hostError(h.TouchRangeFunc(args))
	return nil
}

func (h *Host) OpenApp(args protocol.OpenAppArgs, reply *protocol.OpenAppReply) error {
	if h.OpenAppFunc == nil {
		reply.Err = unset("OpenApp")
		return nil
	}
	launch, err := h.OpenAppFunc(args)
	reply.Launch, reply.Err = launch, hostError(err)
	return nil
}

func (h *Host) EndAppSession(args protocol.CallArgs, reply *protocol.EmptyReply) error {
	if h.EndAppSessionFunc == nil {
		reply.Err = unset("EndAppSession")
		return nil
	}
	reply.Err = hostError(h.EndAppSessionFunc(args))
	return nil
}

func (h *Host) SetAppSessionLimits(args protocol.AppSessionLimitsArgs, reply *protocol.EmptyReply) error {
	if h.SetAppSessionLimitsFunc == nil {
		reply.Err = unset("SetAppSessionLimits")
		return nil
	}
	reply.Err = hostError(h.SetAppSessionLimitsFunc(args))
	return nil
}
