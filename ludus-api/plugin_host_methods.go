package ludusapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"ludusapi/models"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
	yaml "sigs.k8s.io/yaml"
)

var rangeIDPlaceholder = regexp.MustCompile(`{{\s*range_id\s*}}`)

const (
	// defaultHostReadBytes applies when a plugin passes no size for range
	// files and log tails.
	defaultHostReadBytes = 1 << 20
	// defaultHostLogsLimit applies when Logs is called without a limit.
	defaultHostLogsLimit = 100
	// hostProxmoxTimeout bounds Proxmox work done for a host-service call.
	hostProxmoxTimeout = 5 * time.Minute
)

func hostErrorf(code protocol.ErrorCode, format string, args ...any) *protocol.HostError {
	return protocol.HostErrorf(code, format, args...)
}

func (h *HostService) installPath() string {
	if h.server.LudusInstallPath != "" {
		return h.server.LudusInstallPath
	}
	return ludusInstallPath
}

// Ranges and VMs.

func (h *HostService) AccessibleRanges(args protocol.CallArgs, reply *protocol.RangesReply) error {
	reply.Err = h.run(args.Call, accessCaller, "AccessibleRanges", func(c *hostScope) *protocol.HostError {
		if c.admin {
			ranges, err := allPluginRanges()
			reply.Ranges = ranges
			return err
		}
		user, herr := c.requireUser()
		if herr != nil {
			return herr
		}
		ids := slices.Clone(user.GetStringSlice("ranges"))
		groups, err := app.FindRecordsByFilter("groups", "members.id ?= {:user} || managers.id ?= {:user}", "", 0, 0, dbx.Params{"user": user.Id})
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "list range access groups: %v", err)
		}
		for _, group := range groups {
			ids = append(ids, group.GetStringSlice("ranges")...)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		records, err := app.FindRecordsByIds("ranges", ids)
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "load accessible ranges: %v", err)
		}
		reply.Ranges = pluginRanges(records)
		return nil
	})
	return nil
}

func (h *HostService) CanAccessRange(args protocol.RangeArgs, reply *protocol.BoolReply) error {
	reply.Err = h.run(args.Call, accessCaller, "CanAccessRange", func(c *hostScope) *protocol.HostError {
		reply.Value = c.canAccessRange(args.Number)
		return nil
	})
	return nil
}

func (h *HostService) RangeVMs(args protocol.RangeArgs, reply *protocol.VMsReply) error {
	reply.Err = h.run(args.Call, accessCallerOrSystem, "RangeVMs", func(c *hostScope) *protocol.HostError {
		usersRange, herr := c.accessibleRange(args.Number)
		if herr != nil {
			return herr
		}
		vms, herr := rangeVMs(usersRange, c.h.installPath())
		reply.VMs = vms
		return herr
	})
	return nil
}

func (h *HostService) FindVM(args protocol.FindVMArgs, reply *protocol.FindVMReply) error {
	reply.Err = h.run(args.Call, accessCaller, "FindVM", func(c *hostScope) *protocol.HostError {
		record, err := app.FindFirstRecordByData("vms", "proxmoxID", args.ProxmoxID)
		if err != nil {
			return recordLookupError(err, "VM %d", args.ProxmoxID)
		}
		rangeRecord, err := app.FindRecordById("ranges", record.GetString("range"))
		if err != nil {
			return recordLookupError(err, "range of VM %d", args.ProxmoxID)
		}
		usersRange := &models.Range{}
		usersRange.SetProxyRecord(rangeRecord)
		if !c.canAccessRange(usersRange.RangeNumber()) {
			return hostErrorf(protocol.CodeForbidden, "VM %d is not part of an accessible range", args.ProxmoxID)
		}
		reply.VM = pluginVM(record, usersRange, loadRangeVMDetails(usersRange, c.h.installPath()))
		reply.Range = pluginRange(usersRange)
		return nil
	})
	return nil
}

func (h *HostService) ReadRangeFile(args protocol.ReadRangeFileArgs, reply *protocol.BytesReply) error {
	reply.Err = h.run(args.Call, accessCaller, "ReadRangeFile", func(c *hostScope) *protocol.HostError {
		var name string
		switch args.File {
		case protocol.RangeFileConfig:
			name = "range-config.yml"
		case protocol.RangeFileAnsibleLog:
			name = "ansible.log"
		default:
			return hostErrorf(protocol.CodeInvalid, "unknown range file %q", args.File)
		}
		usersRange, herr := c.accessibleRange(args.Number)
		if herr != nil {
			return herr
		}
		rangeDir, herr := rangeDirectoryName(usersRange)
		if herr != nil {
			return herr
		}
		base := filepath.Join(h.installPath(), "ranges")
		resolved, herr := resolveUnder(base, filepath.Join(rangeDir, name))
		if herr != nil {
			return herr
		}
		data, herr := readLocalFile(resolved, hostReadLimit(args.MaxBytes), args.File == protocol.RangeFileAnsibleLog)
		reply.Value = data
		return herr
	})
	return nil
}

func (h *HostService) ListRanges(args protocol.CallArgs, reply *protocol.RangesReply) error {
	reply.Err = h.run(args.Call, accessSystem, "ListRanges", func(c *hostScope) *protocol.HostError {
		ranges, herr := allPluginRanges()
		reply.Ranges = ranges
		return herr
	})
	return nil
}

func (h *HostService) SetRangeInactivityTimeout(args protocol.SetInactivityTimeoutArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessCaller, "SetRangeInactivityTimeout", func(c *hostScope) *protocol.HostError {
		// "" uses the server default; any non-negative Go duration ("0",
		// "0s", "30m", ...) is stored as given.
		if args.Timeout != "" {
			timeout, err := time.ParseDuration(args.Timeout)
			if err != nil {
				return hostErrorf(protocol.CodeInvalid, "invalid timeout duration %q: %v", args.Timeout, err)
			}
			if timeout < 0 {
				return hostErrorf(protocol.CodeInvalid, "timeout duration cannot be negative")
			}
		}
		usersRange, herr := c.accessibleRange(args.Number)
		if herr != nil {
			return herr
		}
		// A narrow UPDATE cannot clobber fields concurrently written by other
		// handlers (for example the deploy pipeline).
		if _, err := app.DB().NewQuery("UPDATE ranges SET inactivityShutdownTimeout = {:timeout} WHERE id = {:id}").
			Bind(dbx.Params{"timeout": args.Timeout, "id": usersRange.Id}).
			Execute(); err != nil {
			return hostErrorf(protocol.CodeUnavailable, "save range settings: %v", err)
		}
		if current, ok := c.event.Get("range").(*models.Range); ok && current != nil && current.Record != nil && current.Id == usersRange.Id {
			current.SetInactivityShutdownTimeout(args.Timeout)
		}
		return nil
	})
	return nil
}

func (h *HostService) PowerOffVMs(args protocol.PowerOffArgs, reply *protocol.BatchReply) error {
	reply.Err = h.run(args.Call, accessSystem, "PowerOffVMs", func(c *hostScope) *protocol.HostError {
		usersRange, herr := rangeByNumber(args.Number)
		if herr != nil {
			return herr
		}
		vms, herr := rangeVMs(usersRange, c.h.installPath())
		if herr != nil {
			return herr
		}
		inRange := make(map[int]bool, len(vms))
		for _, vm := range vms {
			inRange[vm.ProxmoxID] = true
		}
		for _, vmID := range args.VMIDs {
			if !inRange[vmID] {
				return hostErrorf(protocol.CodeInvalid, "VM %d does not belong to range %s", vmID, usersRange.RangeId())
			}
		}
		if len(args.VMIDs) == 0 {
			return nil
		}
		client, err := GetRootGoProxmoxClient()
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "Proxmox client: %v", err)
		}
		ctx, cancel := context.WithTimeout(c.ctx, hostProxmoxTimeout)
		defer cancel()
		for _, err := range PowerOffVMs(ctx, client, args.VMIDs) {
			if err != nil {
				reply.Errors = append(reply.Errors, err.Error())
			}
		}
		return nil
	})
	return nil
}

func (h *HostService) MachineCredentialsDir(args protocol.RangeArgs, reply *protocol.StringReply) error {
	reply.Err = h.run(args.Call, accessCallerSystem, "MachineCredentialsDir", func(c *hostScope) *protocol.HostError {
		usersRange, herr := c.accessibleRange(args.Number)
		if herr != nil {
			return herr
		}
		dir, err := MachineCredentialsDirForRange(usersRange.RangeId())
		if err != nil {
			if os.IsNotExist(err) {
				return hostErrorf(protocol.CodeNotFound, "machine credentials have not been provisioned for this range")
			}
			return hostErrorf(protocol.CodeUnavailable, "machine credentials are unavailable or invalid")
		}
		reply.Value = dir
		return nil
	})
	return nil
}

// Logs and playbooks.

func (h *HostService) Logs(args protocol.LogsArgs, reply *protocol.LogsReply) error {
	reply.Err = h.run(args.Call, accessCaller, "Logs", func(c *hostScope) *protocol.HostError {
		user, herr := c.requireUser()
		if herr != nil {
			return herr
		}
		filter := "user = {:user}"
		params := dbx.Params{"user": user.Id}
		if args.RangeNumber != 0 {
			usersRange, herr := rangeByNumber(args.RangeNumber)
			if herr != nil {
				return herr
			}
			filter += " && range = {:range}"
			params["range"] = usersRange.Id
		}
		limit := args.Limit
		if limit <= 0 {
			limit = defaultHostLogsLimit
		}
		records, err := app.FindRecordsByFilter("logs", filter, "-created", limit, 0, params)
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "query log history: %v", err)
		}
		if errs := app.ExpandRecords(records, []string{"range", "template"}, nil); len(errs) > 0 {
			logger.Debug(fmt.Sprintf("Expanding plugin log history relations: %v", errs))
		}
		tailBytes := hostReadLimit(args.TailBytes)
		tails := 0
		entries := make([]protocol.LogEntry, 0, len(records))
		for _, record := range records {
			entry := protocol.LogEntry{
				ID:       record.Id,
				Created:  record.GetDateTime("created").Time(),
				Status:   record.GetString("status"),
				Archived: record.GetString("log") != "",
			}
			if rangeRecord := record.ExpandedOne("range"); rangeRecord != nil {
				entry.RangeID = rangeRecord.GetString("rangeID")
			}
			if template := record.ExpandedOne("template"); template != nil {
				entry.Template = template.GetString("name")
			}
			if entry.Archived && tails < args.TailEntries {
				tails++
				tail, err := archivedLogTail(record, tailBytes)
				if err != nil {
					logger.Debug(fmt.Sprintf("Reading archived log %s for plugin: %v", record.Id, err))
				}
				entry.Tail = tail
			}
			entries = append(entries, entry)
		}
		reply.Logs = entries
		return nil
	})
	return nil
}

func (h *HostService) RunPlaybook(request protocol.PlaybookRequest, reply *protocol.PlaybookReply) error {
	reply.Err = h.run(request.Call, accessCaller, "RunPlaybook", func(c *hostScope) *protocol.HostError {
		result, herr := c.runPlaybook(request)
		reply.Result = result
		return herr
	})
	return nil
}

func (c *hostScope) runPlaybook(request protocol.PlaybookRequest) (protocol.PlaybookResult, *protocol.HostError) {
	user, herr := c.requireUser()
	if herr != nil {
		return protocol.PlaybookResult{}, herr
	}
	usersRange, ok := c.event.Get("range").(*models.Range)
	if !ok || usersRange == nil || usersRange.Record == nil || usersRange.Id == "" {
		return protocol.PlaybookResult{}, hostErrorf(protocol.CodeInvalid, "RunPlaybook requires a range")
	}
	rangeDir, herr := rangeDirectoryName(usersRange)
	if herr != nil {
		return protocol.PlaybookResult{}, herr
	}

	var root string
	switch request.Root {
	case protocol.PlaybookRootStateDir:
		stateDir, err := pluginStateDir(c.h.plugin)
		if err != nil {
			return protocol.PlaybookResult{}, hostErrorf(protocol.CodeUnavailable, "plugin state directory: %v", err)
		}
		root = stateDir
	case protocol.PlaybookRootRangeManagement:
		if !c.system {
			return protocol.PlaybookResult{}, hostErrorf(protocol.CodeForbidden, "range-management playbooks require a system-level plugin")
		}
		root = filepath.Join(c.h.installPath(), "ansible", "range-management")
	default:
		return protocol.PlaybookResult{}, hostErrorf(protocol.CodeInvalid, "unknown playbook root %q", request.Root)
	}

	playbook, herr := resolveUnder(root, request.Playbook)
	if herr != nil {
		return protocol.PlaybookResult{}, herr
	}
	extraVarsFiles := make([]string, 0, len(request.ExtraVarsFiles))
	for _, name := range request.ExtraVarsFiles {
		resolved, herr := resolveUnder(root, name)
		if herr != nil {
			return protocol.PlaybookResult{}, herr
		}
		extraVarsFiles = append(extraVarsFiles, "@"+resolved)
	}
	var extraVars map[string]any
	if len(request.ExtraVarsJSON) > 0 {
		if err := json.Unmarshal(request.ExtraVarsJSON, &extraVars); err != nil {
			return protocol.PlaybookResult{}, hostErrorf(protocol.CodeInvalid, "ExtraVarsJSON must be a JSON object: %v", err)
		}
	}

	// The range log is shared by every run on the range: archive each plugin
	// run separately under the host's normal retention policy.
	logPath := filepath.Join(c.h.installPath(), "ranges", rangeDir, "ansible.log")
	logID := createRunningLogHistory(app, user.Id, usersRange.Id, "", logPath, time.Now())
	if logID == "" {
		return protocol.PlaybookResult{}, hostErrorf(protocol.CodeUnavailable, "failed to create playbook log history")
	}

	output, runErr := c.h.runPlaybook(c.event, []string{playbook}, extraVarsFiles, extraVars, request.Tags, request.Verbose, request.Limit)
	status := "success"
	if runErr != nil {
		status = "failure"
	}
	// The playbook can outlive the caller's HTTP connection; finalization uses
	// no request context, so the run is archived before a later operation
	// overwrites the shared range log.
	finishPluginPlaybookLog(logID, status, logPath)

	result := protocol.PlaybookResult{Output: output, Failed: runErr != nil, LogID: logID}
	if runErr != nil {
		result.Errors = ansibleLogErrors(logPath)
		if len(result.Errors) == 0 {
			result.Errors = []string{runErr.Error()}
		}
	}
	return result, nil
}

// finishPluginPlaybookLog archives a plugin run. An abort may already have
// archived it; a completed archive is never replaced with the range's
// mutable ansible.log.
func finishPluginPlaybookLog(logID, status, logPath string) {
	record, err := app.FindRecordById("logs", logID)
	if err != nil {
		logger.Error(fmt.Sprintf("Plugin playbook log %s disappeared: %v", logID, err))
		return
	}
	if record.GetString("status") != "running" && record.GetString("log") != "" {
		runningLogFilePathByLogID.Delete(logID)
		return
	}
	if err := finalizeRunningLogHistoryByID(app, logID, status, logPath, time.Now()); err != nil {
		logger.Error(fmt.Sprintf("Failed to archive plugin playbook log %s: %v", logID, err))
	}
}

// Credentials.

func (h *HostService) ProxmoxCredentials(args protocol.CallArgs, reply *protocol.ProxmoxCredentialsReply) error {
	reply.Err = h.run(args.Call, accessCaller, "ProxmoxCredentials", func(c *hostScope) *protocol.HostError {
		user, herr := c.requireUser()
		if herr != nil {
			return herr
		}
		if user.UserId() == "ROOT" {
			return hostErrorf(protocol.CodeForbidden, "the ROOT user has no Proxmox user token")
		}
		if user.ProxmoxTokenId() == "" || user.ProxmoxTokenSecret() == "" {
			return hostErrorf(protocol.CodeUnavailable, "user %s has no Proxmox API token", user.UserId())
		}
		secret, err := DecryptStringFromDatabase(user.ProxmoxTokenSecret())
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "could not decrypt the Proxmox token secret")
		}
		ConfigMu.RLock()
		reply.Credentials = protocol.ProxmoxCredentials{
			Endpoints:   slices.Clone(ServerConfiguration.ProxmoxEndpoints),
			TokenID:     user.ProxmoxTokenId(),
			TokenSecret: secret,
			InsecureTLS: ServerConfiguration.ProxmoxInvalidCert,
		}
		ConfigMu.RUnlock()
		return nil
	})
	return nil
}

func (h *HostService) RootProxmoxCredentials(args protocol.CallArgs, reply *protocol.ProxmoxCredentialsReply) error {
	reply.Err = h.run(args.Call, accessSystem, "RootProxmoxCredentials", func(c *hostScope) *protocol.HostError {
		ConfigMu.RLock()
		reply.Credentials = protocol.ProxmoxCredentials{
			Endpoints:   slices.Clone(ServerConfiguration.ProxmoxEndpoints),
			TokenID:     ServerConfiguration.ProxmoxTokenID,
			TokenSecret: ServerConfiguration.ProxmoxTokenSecret,
			InsecureTLS: ServerConfiguration.ProxmoxInvalidCert,
		}
		ConfigMu.RUnlock()
		return nil
	})
	return nil
}

// Licensing.

func (h *HostService) License(args protocol.CallArgs, reply *protocol.LicenseReply) error {
	reply.Err = h.run(args.Call, accessAny, "License", func(c *hostScope) *protocol.HostError {
		reply.License = h.server.licenseSnapshot()
		return nil
	})
	return nil
}

func (h *HostService) SetLicenseState(args protocol.SetLicenseStateArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessEnterprise, "SetLicenseState", func(c *hostScope) *protocol.HostError {
		h.server.setLicenseFromPlugin(args.License)
		return nil
	})
	return nil
}

func (h *HostService) RequestAddOn(args protocol.AddOnArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessEnterprise, "RequestAddOn", func(c *hostScope) *protocol.HostError {
		if err := h.server.requestAddOn(args.Name); err != nil {
			var hostErr *protocol.HostError
			if errors.As(err, &hostErr) {
				return hostErr
			}
			return hostErrorf(protocol.CodeInvalid, "%v", err)
		}
		return nil
	})
	return nil
}

// Quotas.

func (h *HostService) QuotaStatus(args protocol.QuotaStatusArgs, reply *protocol.QuotaStatusReply) error {
	reply.Err = h.run(args.Call, accessCallerSystem, "QuotaStatus", func(c *hostScope) *protocol.HostError {
		userID := args.UserID
		if userID == "" {
			user, herr := c.requireUser()
			if herr != nil {
				return herr
			}
			userID = user.UserId()
		} else if !c.admin && (c.user == nil || c.user.UserId() != userID) {
			return hostErrorf(protocol.CodeForbidden, "only admins can view another user's quota")
		}
		record, err := app.FindFirstRecordByData("users", "userID", userID)
		if err != nil {
			return recordLookupError(err, "user %s", userID)
		}
		user := quotaUser(record)
		status, err := userQuotaStatus(user)
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "calculate usage: %v", err)
		}
		reply.Status = status
		return nil
	})
	return nil
}

func (h *HostService) ListQuotaStatus(args protocol.CallArgs, reply *protocol.UserQuotaStatusesReply) error {
	reply.Err = h.run(args.Call, accessCallerSystemAdmin, "ListQuotaStatus", func(c *hostScope) *protocol.HostError {
		records, err := app.FindAllRecords("users")
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "list users: %v", err)
		}
		statuses := make([]protocol.UserQuotaStatus, 0, len(records))
		for _, record := range records {
			// ROOT is the internal service user and has no quota semantics.
			if record.GetString("userID") == "ROOT" {
				continue
			}
			status, err := userQuotaStatus(quotaUser(record))
			if err != nil {
				// One user's usage must not fail the whole listing.
				logger.Error(fmt.Sprintf("Error calculating usage for user %s: %v", record.GetString("userID"), err))
			}
			statuses = append(statuses, status)
		}
		reply.Statuses = statuses
		return nil
	})
	return nil
}

func (h *HostService) ListGroupQuotas(args protocol.CallArgs, reply *protocol.GroupQuotasReply) error {
	reply.Err = h.run(args.Call, accessCallerSystemAdmin, "ListGroupQuotas", func(c *hostScope) *protocol.HostError {
		records, err := app.FindAllRecords("groups")
		if err != nil {
			return hostErrorf(protocol.CodeUnavailable, "list groups: %v", err)
		}
		groups := make([]protocol.GroupQuota, 0, len(records))
		for _, record := range records {
			group := &models.Groups{}
			group.SetProxyRecord(record)
			groups = append(groups, protocol.GroupQuota{
				Name:               group.Name(),
				DefaultQuotaRAM:    group.DefaultQuotaRam(),
				DefaultQuotaCPU:    group.DefaultQuotaCpu(),
				DefaultQuotaVMs:    group.DefaultQuotaVms(),
				DefaultQuotaRanges: group.DefaultQuotaRanges(),
				MemberCount:        len(record.GetStringSlice("members")) + len(record.GetStringSlice("managers")),
			})
		}
		reply.Groups = groups
		return nil
	})
	return nil
}

func (h *HostService) SetUserQuota(args protocol.SetUserQuotaArgs, reply *protocol.BatchReply) error {
	reply.Err = h.run(args.Call, accessCallerSystemAdmin, "SetUserQuota", func(c *hostScope) *protocol.HostError {
		if len(args.UserIDs) == 0 {
			return hostErrorf(protocol.CodeInvalid, "userIDs array is required")
		}
		if herr := validateQuotaPatch(args.Patch); herr != nil {
			return herr
		}
		for _, userID := range args.UserIDs {
			record, err := app.FindFirstRecordByData("users", "userID", userID)
			if err != nil {
				reply.Errors = append(reply.Errors, fmt.Sprintf("User %s not found", userID))
				continue
			}
			applyQuotaPatch(record, args.Patch, "quotaRAM", "quotaCPU", "quotaVMs", "quotaRanges")
			if err := app.Save(record); err != nil {
				reply.Errors = append(reply.Errors, fmt.Sprintf("User %s: error saving: %v", userID, err))
			}
		}
		return nil
	})
	return nil
}

func (h *HostService) SetGroupDefaultQuota(args protocol.SetGroupQuotaArgs, reply *protocol.BatchReply) error {
	reply.Err = h.run(args.Call, accessCallerSystemAdmin, "SetGroupDefaultQuota", func(c *hostScope) *protocol.HostError {
		if len(args.GroupNames) == 0 {
			return hostErrorf(protocol.CodeInvalid, "groupNames array is required")
		}
		if herr := validateQuotaPatch(args.Patch); herr != nil {
			return herr
		}
		for _, groupName := range args.GroupNames {
			record, err := app.FindFirstRecordByData("groups", "name", groupName)
			if err != nil {
				reply.Errors = append(reply.Errors, fmt.Sprintf("Group %s not found", groupName))
				continue
			}
			applyQuotaPatch(record, args.Patch, "defaultQuotaRAM", "defaultQuotaCPU", "defaultQuotaVMs", "defaultQuotaRanges")
			if err := app.Save(record); err != nil {
				reply.Errors = append(reply.Errors, fmt.Sprintf("Group %s: error saving: %v", groupName, err))
			}
		}
		return nil
	})
	return nil
}

// quotaPatchValues lists the patch fields in RAM, CPU, VMs, Ranges order.
// Only fields whose Set flag is true change; 0 means unlimited.
func quotaPatchValues(patch protocol.QuotaPatch) [4]*int {
	var values [4]*int
	for i, field := range []struct {
		set   bool
		value int
	}{{patch.SetRAM, patch.RAM}, {patch.SetCPU, patch.CPU}, {patch.SetVMs, patch.VMs}, {patch.SetRanges, patch.Ranges}} {
		if field.set {
			values[i] = &field.value
		}
	}
	return values
}

func validateQuotaPatch(patch protocol.QuotaPatch) *protocol.HostError {
	values := quotaPatchValues(patch)
	if err := ValidateQuotaValues(values[:]...); err != nil {
		return hostErrorf(protocol.CodeInvalid, "%v", err)
	}
	return nil
}

func applyQuotaPatch(record *core.Record, patch protocol.QuotaPatch, ramField, cpuField, vmsField, rangesField string) {
	for i, value := range quotaPatchValues(patch) {
		if value != nil {
			record.Set([]string{ramField, cpuField, vmsField, rangesField}[i], *value)
		}
	}
}

func quotaUser(record *core.Record) *models.User {
	app.ExpandRecord(record, []string{"ranges", "groups"}, nil)
	user := &models.User{}
	user.SetProxyRecord(record)
	return user
}

// userQuotaStatus resolves limits (user > group default > system default) and
// usage over the user's ranges. On a usage error the limits are still filled.
func userQuotaStatus(user *models.User) (protocol.UserQuotaStatus, error) {
	ConfigMu.RLock()
	limits := ResolveUserEffectiveQuotas(user)
	status := protocol.UserQuotaStatus{
		UserID:       user.UserId(),
		Name:         user.Name(),
		SourceRAM:    ResolveQuotaSource(user.QuotaRam(), GetGroupDefaultQuota(user, "ram"), ServerConfiguration.DefaultQuotaRAM),
		SourceCPU:    ResolveQuotaSource(user.QuotaCpu(), GetGroupDefaultQuota(user, "cpu"), ServerConfiguration.DefaultQuotaCPU),
		SourceVMs:    ResolveQuotaSource(user.QuotaVms(), GetGroupDefaultQuota(user, "vms"), ServerConfiguration.DefaultQuotaVMs),
		SourceRanges: ResolveQuotaSource(user.QuotaRanges(), GetGroupDefaultQuota(user, "ranges"), ServerConfiguration.DefaultQuotaRanges),
	}
	ConfigMu.RUnlock()
	usage, err := CalculateUserUsage(app, user)
	status.QuotaStatus = protocol.QuotaStatus{
		LimitRAM:    limits.LimitRAM,
		LimitCPU:    limits.LimitCPU,
		LimitVMs:    limits.LimitVMs,
		LimitRanges: limits.LimitRanges,
		UsedRAM:     usage.TotalRAM,
		UsedCPU:     usage.TotalCPU,
		UsedVMs:     usage.TotalVMs,
		UsedRanges:  usage.TotalRanges,
	}
	return status, err
}

// Shared helpers.

// accessibleRange loads a range the principal may address.
func (c *hostScope) accessibleRange(rangeNumber int) (*models.Range, *protocol.HostError) {
	if !c.canAccessRange(rangeNumber) {
		return nil, hostErrorf(protocol.CodeForbidden, "no access to range number %d", rangeNumber)
	}
	return rangeByNumber(rangeNumber)
}

func rangeByNumber(rangeNumber int) (*models.Range, *protocol.HostError) {
	usersRange, err := GetRangeObjectByNumber(rangeNumber)
	if err != nil {
		return nil, recordLookupError(err, "range number %d", rangeNumber)
	}
	return usersRange, nil
}

func recordLookupError(err error, format string, args ...any) *protocol.HostError {
	what := fmt.Sprintf(format, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return hostErrorf(protocol.CodeNotFound, "%s not found", what)
	}
	return hostErrorf(protocol.CodeUnavailable, "load %s: %v", what, err)
}

func allPluginRanges() ([]protocol.Range, *protocol.HostError) {
	records, err := app.FindAllRecords("ranges")
	if err != nil {
		return nil, hostErrorf(protocol.CodeUnavailable, "list ranges: %v", err)
	}
	return pluginRanges(records), nil
}

// pluginRanges converts range records sorted by number, then range ID.
func pluginRanges(records []*core.Record) []protocol.Range {
	ranges := make([]protocol.Range, 0, len(records))
	for _, record := range records {
		usersRange := &models.Range{}
		usersRange.SetProxyRecord(record)
		ranges = append(ranges, pluginRange(usersRange))
	}
	slices.SortFunc(ranges, func(a, b protocol.Range) int {
		if a.Number != b.Number {
			return a.Number - b.Number
		}
		return strings.Compare(a.RangeID, b.RangeID)
	})
	return ranges
}

func rangeVMs(usersRange *models.Range, installPath string) ([]protocol.VM, *protocol.HostError) {
	records, err := app.FindAllRecords("vms", dbx.HashExp{"range": usersRange.Id})
	if err != nil {
		return nil, hostErrorf(protocol.CodeUnavailable, "list VMs of range %s: %v", usersRange.RangeId(), err)
	}
	details := loadRangeVMDetails(usersRange, installPath)
	vms := make([]protocol.VM, 0, len(records))
	for _, record := range records {
		vms = append(vms, pluginVM(record, usersRange, details))
	}
	slices.SortFunc(vms, func(a, b protocol.VM) int { return a.ProxmoxID - b.ProxmoxID })
	return vms, nil
}

// rangeVMDetails is what the range configuration and template records say
// about a range's VMs. configs is shared with rangeConfigs; treat it as
// read-only. Template OS lookups are memoized per host-service call.
type rangeVMDetails struct {
	configs map[string]VM // by VM name with {{ range_id }} substituted
	os      map[string]string
}

// rangeConfigCache holds parsed range configurations by resolved path. An
// entry is reused while the file's size and modification time are unchanged,
// so plugins polling RangeVMs do not re-read and re-parse YAML.
type rangeConfigCache struct {
	mu      sync.Mutex
	entries map[string]rangeConfigEntry
}

type rangeConfigEntry struct {
	modTime time.Time
	size    int64
	rangeID string
	configs map[string]VM
}

var rangeConfigs = &rangeConfigCache{entries: map[string]rangeConfigEntry{}}

func (c *rangeConfigCache) load(path, rangeID string) map[string]VM {
	info, err := os.Stat(path)
	if err != nil || info.Size() > defaultHostReadBytes {
		c.mu.Lock()
		delete(c.entries, path)
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	entry, ok := c.entries[path]
	c.mu.Unlock()
	if ok && entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) && entry.rangeID == rangeID {
		return entry.configs
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > defaultHostReadBytes {
		return nil
	}
	configs := map[string]VM{}
	var config LudusConfig
	if yaml.Unmarshal(data, &config) == nil {
		for _, vm := range config.Ludus {
			configs[rangeIDPlaceholder.ReplaceAllString(vm.VMName, rangeID)] = vm
		}
	}
	// An invalid file is cached as empty until it changes.
	c.mu.Lock()
	c.entries[path] = rangeConfigEntry{modTime: info.ModTime(), size: info.Size(), rangeID: rangeID, configs: configs}
	c.mu.Unlock()
	return configs
}

func loadRangeVMDetails(usersRange *models.Range, installPath string) *rangeVMDetails {
	details := &rangeVMDetails{configs: map[string]VM{}, os: map[string]string{}}
	name, herr := rangeDirectoryName(usersRange)
	if herr != nil {
		return details
	}
	path, herr := resolveUnder(filepath.Join(installPath, "ranges", name), "range-config.yml")
	if herr != nil {
		return details
	}
	if configs := rangeConfigs.load(path, usersRange.RangeId()); configs != nil {
		details.configs = configs
	}
	return details
}

// templateOS returns the os of the named template record, or "".
func (d *rangeVMDetails) templateOS(template string) string {
	if template == "" {
		return ""
	}
	if value, ok := d.os[template]; ok {
		return value
	}
	value := ""
	if record, err := app.FindFirstRecordByData("templates", "name", template); err == nil {
		value = record.GetString("os")
	}
	d.os[template] = value
	return value
}

func pluginVM(record *core.Record, usersRange *models.Range, details *rangeVMDetails) protocol.VM {
	vm := &models.VM{}
	vm.SetProxyRecord(record)
	result := protocol.VM{
		RecordID:    vm.Id,
		RangeID:     usersRange.RangeId(),
		RangeNumber: usersRange.RangeNumber(),
		Name:        vm.Name(),
		IP:          vm.Ip(),
		ProxmoxID:   vm.ProxmoxId(),
		PoweredOn:   vm.PoweredOn(),
		IsRouter:    vm.IsRouter(),
		CPU:         vm.Cpu(),
		RAM:         vm.Ram(),
	}
	if config, ok := details.configs[result.Name]; ok {
		result.Template = config.Template
		result.OS = details.templateOS(config.Template)
		if config.VLAN > 0 && config.VLAN < 255 && config.IPLastOctet > 0 && config.IPLastOctet < 255 {
			result.ConfiguredIP = fmt.Sprintf("10.%d.%d.%d", usersRange.RangeNumber(), config.VLAN, config.IPLastOctet)
		}
	}
	return result
}

// rangeDirectoryName returns the range's directory name under <install>/ranges.
func rangeDirectoryName(usersRange *models.Range) (string, *protocol.HostError) {
	name := usersRange.RangeId()
	if !filepath.IsLocal(name) || filepath.Clean(name) != name {
		return "", hostErrorf(protocol.CodeInvalid, "invalid range directory %q", name)
	}
	return name, nil
}

// resolveUnder resolves a local relative path under root, following symlinks,
// and rejects anything that leaves root or is not a regular file.
func resolveUnder(root, relative string) (string, *protocol.HostError) {
	if relative == "" || !filepath.IsLocal(relative) {
		return "", hostErrorf(protocol.CodeInvalid, "path %q must be local and relative", relative)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", pathError(err, root)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(resolvedRoot, relative))
	if err != nil {
		return "", pathError(err, relative)
	}
	inside, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || !filepath.IsLocal(inside) {
		return "", hostErrorf(protocol.CodeInvalid, "path %q resolves outside its root", relative)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", pathError(err, relative)
	}
	if !info.Mode().IsRegular() {
		return "", hostErrorf(protocol.CodeInvalid, "path %q is not a regular file", relative)
	}
	return resolved, nil
}

func pathError(err error, name string) *protocol.HostError {
	if errors.Is(err, os.ErrNotExist) {
		return hostErrorf(protocol.CodeNotFound, "%s does not exist", name)
	}
	return hostErrorf(protocol.CodeUnavailable, "resolve %s: %v", name, err)
}

func hostReadLimit(requested int64) int64 {
	if requested <= 0 {
		return defaultHostReadBytes
	}
	return min(requested, protocol.MaxBodyBytes)
}

// readLocalFile reads at most limit bytes: the tail when tail is set,
// otherwise the whole file, which must fit.
func readLocalFile(name string, limit int64, tail bool) ([]byte, *protocol.HostError) {
	file, err := os.Open(name)
	if err != nil {
		return nil, pathError(err, filepath.Base(name))
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, pathError(err, filepath.Base(name))
	}
	if size := info.Size(); size > limit {
		if !tail {
			return nil, hostErrorf(protocol.CodeInvalid, "%s is larger than %d bytes", filepath.Base(name), limit)
		}
		if _, err := file.Seek(size-limit, io.SeekStart); err != nil {
			return nil, pathError(err, filepath.Base(name))
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, pathError(err, filepath.Base(name))
	}
	return data, nil
}

// archivedLogTail reads the end of a log record's archived file from
// PocketBase storage.
func archivedLogTail(record *core.Record, limit int64) ([]byte, error) {
	fsClient, err := app.NewFilesystem()
	if err != nil {
		return nil, err
	}
	defer fsClient.Close()
	reader, err := fsClient.GetReader(path.Join(record.BaseFilesPath(), record.GetString("log")))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if size := reader.Size(); size > limit {
		if _, err := reader.Seek(size-limit, io.SeekStart); err != nil {
			return nil, err
		}
	}
	return io.ReadAll(io.LimitReader(reader, limit))
}

// rangeActivityInterval matches the request middleware: lastActive is written
// at most this often per range, since the inactivity shutdown only needs
// minute resolution.
const rangeActivityInterval = 30 * time.Second

// rangeTouches remembers when TouchRange last wrote each range's lastActive.
var rangeTouches = struct {
	mu   sync.Mutex
	last map[string]time.Time
}{last: map[string]time.Time{}}

// TouchRange keeps active plugin sessions from being shut down for inactivity.
// The caller can only touch an accessible range, never supply its timestamp.
// Touches within rangeActivityInterval of the last write are not written.
func (h *HostService) TouchRange(args protocol.RangeArgs, reply *protocol.EmptyReply) error {
	reply.Err = h.run(args.Call, accessCaller, "TouchRange", func(c *hostScope) *protocol.HostError {
		usersRange, herr := c.accessibleRange(args.Number)
		if herr != nil {
			return herr
		}
		now := time.Now()
		rangeTouches.mu.Lock()
		recent := now.Sub(rangeTouches.last[usersRange.Id]) < rangeActivityInterval
		if !recent {
			// Claim the write so concurrent touches of this range skip it.
			rangeTouches.last[usersRange.Id] = now
		}
		rangeTouches.mu.Unlock()
		if recent {
			return nil
		}
		if err := recordRangeActivity(app, usersRange.Id); err != nil {
			rangeTouches.mu.Lock()
			delete(rangeTouches.last, usersRange.Id)
			rangeTouches.mu.Unlock()
			return hostErrorf(protocol.CodeUnavailable, "record range activity: %v", err)
		}
		return nil
	})
	return nil
}
