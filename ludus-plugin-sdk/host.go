package ludus

import (
	"encoding/json"
	"fmt"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

// Host decisions arrive as *protocol.HostError; test them with errors.Is.
var (
	ErrNotFound    error = &protocol.HostError{Code: protocol.CodeNotFound}
	ErrForbidden   error = &protocol.HostError{Code: protocol.CodeForbidden}
	ErrInvalid     error = &protocol.HostError{Code: protocol.CodeInvalid}
	ErrExpired     error = &protocol.HostError{Code: protocol.CodeExpired}
	ErrUnavailable error = &protocol.HostError{Code: protocol.CodeUnavailable}
)

// Host is the host service bound to one call. Each method is evaluated as the
// principal of that call (see PLUGIN_REDESIGN.md, "Host service").
type Host struct {
	client *protocol.HostClient
	call   protocol.CallID
}

// LogsQuery selects range log history entries.
type LogsQuery struct {
	RangeNumber int   // 0 means every range
	Limit       int   // <= 0 means 100
	TailEntries int   // how many of the newest archived entries get a Tail
	TailBytes   int64 // size of each Tail
}

// Playbook is an Ansible run the host performs for the plugin.
type Playbook struct {
	Root           protocol.PlaybookRoot
	Playbook       string   // filepath.IsLocal, relative to Root
	ExtraVarsFiles []string // same rule as Playbook
	ExtraVars      map[string]any
	Tags, Limit    string
	Verbose        bool
}

func (h *Host) hostClient() (*protocol.HostClient, error) {
	if h == nil || h.client == nil {
		return nil, protocol.HostErrorf(protocol.CodeUnavailable, "no host service")
	}
	return h.client, nil
}

func (h *Host) AccessibleRanges() ([]protocol.Range, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.AccessibleRanges(h.call)
}

func (h *Host) CanAccessRange(number int) (bool, error) {
	c, err := h.hostClient()
	if err != nil {
		return false, err
	}
	return c.CanAccessRange(h.call, number)
}

// RangeVMs returns the range's VMs as Ludus last recorded them. Power state
// and IP addresses are refreshed by range status requests, not by this call,
// so they can lag behind Proxmox right after a power change.
func (h *Host) RangeVMs(number int) ([]protocol.VM, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.RangeVMs(h.call, number)
}

func (h *Host) FindVM(proxmoxID int) (protocol.VM, protocol.Range, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.VM{}, protocol.Range{}, err
	}
	return c.FindVM(h.call, proxmoxID)
}

// ReadRangeFile reads a range file. maxBytes <= 0 means the host default (1 MiB);
// logs return their tail.
func (h *Host) ReadRangeFile(number int, file protocol.RangeFile, maxBytes int64) ([]byte, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.ReadRangeFile(h.call, number, file, maxBytes)
}

func (h *Host) Logs(query LogsQuery) ([]protocol.LogEntry, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.Logs(protocol.LogsArgs{
		Call:        h.call,
		RangeNumber: query.RangeNumber,
		Limit:       query.Limit,
		TailEntries: query.TailEntries,
		TailBytes:   query.TailBytes,
	})
}

func (h *Host) RunPlaybook(playbook Playbook) (protocol.PlaybookResult, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.PlaybookResult{}, err
	}
	var extraVars []byte
	if playbook.ExtraVars != nil {
		if extraVars, err = json.Marshal(playbook.ExtraVars); err != nil {
			return protocol.PlaybookResult{}, fmt.Errorf("encode playbook extra vars: %w", err)
		}
	}
	return c.RunPlaybook(protocol.PlaybookRequest{
		Call:           h.call,
		Root:           playbook.Root,
		Playbook:       playbook.Playbook,
		ExtraVarsFiles: playbook.ExtraVarsFiles,
		ExtraVarsJSON:  extraVars,
		Tags:           playbook.Tags,
		Limit:          playbook.Limit,
		Verbose:        playbook.Verbose,
	})
}

func (h *Host) ProxmoxCredentials() (protocol.ProxmoxCredentials, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.ProxmoxCredentials{}, err
	}
	return c.ProxmoxCredentials(h.call)
}

// SetRangeInactivityTimeout sets the range override: "" server default, "0"
// disabled, otherwise a Go duration.
func (h *Host) SetRangeInactivityTimeout(number int, timeout string) error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.SetRangeInactivityTimeout(h.call, number, timeout)
}

func (h *Host) License() (protocol.License, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.License{}, err
	}
	return c.License(h.call)
}

// QuotaStatus returns a user's quota; "" means the effective user of the request.
func (h *Host) QuotaStatus(userID string) (protocol.UserQuotaStatus, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.UserQuotaStatus{}, err
	}
	return c.QuotaStatus(h.call, userID)
}

func (h *Host) ListQuotaStatus() ([]protocol.UserQuotaStatus, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.ListQuotaStatus(h.call)
}

func (h *Host) ListGroupQuotas() ([]protocol.GroupQuota, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.ListGroupQuotas(h.call)
}

// SetUserQuota applies patch to each user and returns per-user failures.
func (h *Host) SetUserQuota(userIDs []string, patch protocol.QuotaPatch) ([]string, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.SetUserQuota(h.call, userIDs, patch)
}

// SetGroupDefaultQuota applies patch to each group and returns per-group failures.
func (h *Host) SetGroupDefaultQuota(groupNames []string, patch protocol.QuotaPatch) ([]string, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.SetGroupDefaultQuota(h.call, groupNames, patch)
}

func (h *Host) MachineCredentialsDir(number int) (string, error) {
	c, err := h.hostClient()
	if err != nil {
		return "", err
	}
	return c.MachineCredentialsDir(h.call, number)
}

func (h *Host) ListRanges() ([]protocol.Range, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.ListRanges(h.call)
}

// PowerOffVMs powers off VMs of a range and returns per-VM failures.
func (h *Host) PowerOffVMs(number int, vmIDs []int) ([]string, error) {
	c, err := h.hostClient()
	if err != nil {
		return nil, err
	}
	return c.PowerOffVMs(h.call, number, vmIDs)
}

func (h *Host) RootProxmoxCredentials() (protocol.ProxmoxCredentials, error) {
	c, err := h.hostClient()
	if err != nil {
		return protocol.ProxmoxCredentials{}, err
	}
	return c.RootProxmoxCredentials(h.call)
}

func (h *Host) SetLicenseState(license protocol.License) error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.SetLicenseState(h.call, license)
}

func (h *Host) RequestAddOn(name string) error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.RequestAddOn(h.call, name)
}

// TouchRange records activity on an accessible range using the host's clock.
func (h *Host) TouchRange(number int) error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.TouchRange(h.call, number)
}

// EndAppSession ends the app session this Host is bound to (Call.AppSession).
// The host closes the session's connections and calls AppSessionClosed.
func (h *Host) EndAppSession() error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.EndAppSession(h.call)
}

// SetAppSessionLimits sets how many live app sessions of this plugin the host
// allows per authenticated user and in total (protocol.DefaultAppSessionsPerUser
// and protocol.DefaultAppSessions until set). Callable from Init, jobs, and
// request handlers; it takes effect for the next OpenApp.
func (h *Host) SetAppSessionLimits(perUser, total int) error {
	c, err := h.hostClient()
	if err != nil {
		return err
	}
	return c.SetAppSessionLimits(h.call, perUser, total)
}
