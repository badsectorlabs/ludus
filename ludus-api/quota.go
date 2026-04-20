package ludusapi

import (
	"fmt"
	"ludusapi/models"
	"os"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	yaml "sigs.k8s.io/yaml"
)

// QuotaResources represents resource counts for quota tracking.
type QuotaResources struct {
	TotalRAM    int
	TotalCPU    int
	TotalVMs    int
	TotalRanges int
}

// QuotaStatus represents the resolved quota limits and current usage for a user.
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

// QuotaViolation describes a single quota limit that would be exceeded.
type QuotaViolation struct {
	Resource  string
	Limit     int
	Current   int
	Requested int
}

// ValidateQuotaValues checks that all provided quota values are non-negative.
// 0 is allowed (used internally by the reset command to clear a quota).
func ValidateQuotaValues(values ...*int) error {
	for _, v := range values {
		if v != nil && *v < 0 {
			return fmt.Errorf("quota values must be >= 0")
		}
	}
	return nil
}

// resolveQuota returns the effective quota limit using the priority chain:
// user (if > 0) > group default (if > 0) > system default.
// A return value of 0 means unlimited.
func resolveQuota(userQuota, groupQuota, systemQuota int) int {
	if userQuota > 0 {
		return userQuota
	}
	if groupQuota > 0 {
		return groupQuota
	}
	return systemQuota
}

// isQuotaExceeded returns true if current+requested exceeds the limit.
// A limit of 0 means unlimited and is never exceeded.
func isQuotaExceeded(current, requested, limit int) bool {
	if limit == 0 {
		return false
	}
	return current+requested > limit
}

// calculateConfigResources parses range-config YAML and sums the CPU, RAM,
// and VM count from all VMs defined in the config.
func calculateConfigResources(yamlData []byte) (QuotaResources, error) {
	var config LudusConfig
	err := yaml.Unmarshal(yamlData, &config)
	if err != nil {
		return QuotaResources{}, fmt.Errorf("failed to parse range config YAML: %w", err)
	}

	var resources QuotaResources
	for _, vm := range config.Ludus {
		resources.TotalCPU += vm.CPUs
		resources.TotalRAM += vm.RamGB
		resources.TotalVMs++
	}
	return resources, nil
}

// GetGroupDefaultQuota returns the MAX (most permissive) group default quota
// across all groups the user belongs to for the given quota type.
// Returns 0 if the user has no groups or no group sets a default for that type.
func GetGroupDefaultQuota(user *models.User, quotaType string) int {
	groups := user.Groups()
	maxQuota := 0
	for _, group := range groups {
		var val int
		switch quotaType {
		case "ram":
			val = group.DefaultQuotaRam()
		case "cpu":
			val = group.DefaultQuotaCpu()
		case "vms":
			val = group.DefaultQuotaVms()
		case "ranges":
			val = group.DefaultQuotaRanges()
		}
		if val > maxQuota {
			maxQuota = val
		}
	}
	return maxQuota
}

// ResolveQuotaSource returns a label indicating where the effective quota comes from:
// "U" for user, "G" for group default, "S" for system default, "" for unlimited.
func ResolveQuotaSource(userQuota, groupQuota, systemQuota int) string {
	if userQuota > 0 {
		return "U"
	}
	if groupQuota > 0 {
		return "G"
	}
	if systemQuota > 0 {
		return "S"
	}
	return ""
}

// ResolveUserEffectiveQuotas resolves all 4 quota types for a user using
// the priority chain: user quota > group default > system default.
func ResolveUserEffectiveQuotas(user *models.User) QuotaStatus {
	return QuotaStatus{
		LimitRAM: resolveQuota(
			user.QuotaRam(),
			GetGroupDefaultQuota(user, "ram"),
			ServerConfiguration.DefaultQuotaRAM,
		),
		LimitCPU: resolveQuota(
			user.QuotaCpu(),
			GetGroupDefaultQuota(user, "cpu"),
			ServerConfiguration.DefaultQuotaCPU,
		),
		LimitVMs: resolveQuota(
			user.QuotaVms(),
			GetGroupDefaultQuota(user, "vms"),
			ServerConfiguration.DefaultQuotaVMs,
		),
		LimitRanges: resolveQuota(
			user.QuotaRanges(),
			GetGroupDefaultQuota(user, "ranges"),
			ServerConfiguration.DefaultQuotaRanges,
		),
	}
}

// CalculateUserUsage sums CPU, RAM, and VM count across all of a user's ranges
// by querying the VMs collection in the database.
func CalculateUserUsage(txApp core.App, user *models.User) (QuotaResources, error) {
	var usage QuotaResources

	ranges := user.Ranges()
	usage.TotalRanges = len(ranges)

	for _, r := range ranges {
		vmRecords, err := txApp.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": r.Id}))
		if err != nil {
			return QuotaResources{}, fmt.Errorf("error finding VMs for range %s: %w", r.RangeId(), err)
		}
		for _, vmRecord := range vmRecords {
			vmObj := &models.VMs{}
			vmObj.SetProxyRecord(vmRecord)
			usage.TotalCPU += vmObj.Cpu()
			usage.TotalRAM += vmObj.Ram()
			usage.TotalVMs++
		}
	}

	return usage, nil
}

// CheckDeployQuota checks whether deploying a range would exceed the user's quotas.
// It subtracts the current range's existing VMs from usage (since deploy replaces them),
// then adds the new config's requirements. Returns any quota violations found.
func CheckDeployQuota(txApp core.App, user *models.User, rangeID string) ([]QuotaViolation, error) {
	status := ResolveUserEffectiveQuotas(user)

	// Calculate current total usage across all user ranges
	totalUsage, err := CalculateUserUsage(txApp, user)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate user usage: %w", err)
	}

	// Find the range being deployed and subtract its current VMs
	// (deploy replaces all VMs in the range)
	currentRangeUsage := QuotaResources{}
	rangeRecord, err := txApp.FindFirstRecordByData("ranges", "rangeID", rangeID)
	if err != nil {
		return nil, fmt.Errorf("error finding range %s: %w", rangeID, err)
	}
	vmRecords, err := txApp.FindAllRecords("vms", dbx.NewExp("range = {:rangeID}", dbx.Params{"rangeID": rangeRecord.Id}))
	if err != nil {
		return nil, fmt.Errorf("error finding VMs for range %s: %w", rangeID, err)
	}
	for _, vmRecord := range vmRecords {
		vmObj := &models.VMs{}
		vmObj.SetProxyRecord(vmRecord)
		currentRangeUsage.TotalCPU += vmObj.Cpu()
		currentRangeUsage.TotalRAM += vmObj.Ram()
		currentRangeUsage.TotalVMs++
	}

	// Read the new config from disk
	configPath := fmt.Sprintf("%s/ranges/%s/range-config.yml", ludusInstallPath, rangeID)
	yamlData, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read range config %s: %w", configPath, err)
	}

	newResources, err := calculateConfigResources(yamlData)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate config resources: %w", err)
	}

	// Effective usage = total usage - current range + new config
	effectiveCPU := totalUsage.TotalCPU - currentRangeUsage.TotalCPU
	effectiveRAM := totalUsage.TotalRAM - currentRangeUsage.TotalRAM
	effectiveVMs := totalUsage.TotalVMs - currentRangeUsage.TotalVMs

	var violations []QuotaViolation

	if isQuotaExceeded(effectiveCPU, newResources.TotalCPU, status.LimitCPU) {
		violations = append(violations, QuotaViolation{
			Resource:  "CPU",
			Limit:     status.LimitCPU,
			Current:   effectiveCPU,
			Requested: newResources.TotalCPU,
		})
	}

	if isQuotaExceeded(effectiveRAM, newResources.TotalRAM, status.LimitRAM) {
		violations = append(violations, QuotaViolation{
			Resource:  "RAM",
			Limit:     status.LimitRAM,
			Current:   effectiveRAM,
			Requested: newResources.TotalRAM,
		})
	}

	if isQuotaExceeded(effectiveVMs, newResources.TotalVMs, status.LimitVMs) {
		violations = append(violations, QuotaViolation{
			Resource:  "VMs",
			Limit:     status.LimitVMs,
			Current:   effectiveVMs,
			Requested: newResources.TotalVMs,
		})
	}

	return violations, nil
}

// CheckRangeQuota checks if creating a new range would exceed the user's range quota.
func CheckRangeQuota(txApp core.App, user *models.User) ([]QuotaViolation, error) {
	status := ResolveUserEffectiveQuotas(user)

	usage, err := CalculateUserUsage(txApp, user)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate user usage: %w", err)
	}

	var violations []QuotaViolation

	if isQuotaExceeded(usage.TotalRanges, 1, status.LimitRanges) {
		violations = append(violations, QuotaViolation{
			Resource:  "Ranges",
			Limit:     status.LimitRanges,
			Current:   usage.TotalRanges,
			Requested: 1,
		})
	}

	return violations, nil
}

// FormatQuotaViolations formats quota violations into a human-readable error message.
func FormatQuotaViolations(violations []QuotaViolation) string {
	if len(violations) == 0 {
		return ""
	}

	var parts []string
	for _, v := range violations {
		parts = append(parts, fmt.Sprintf(
			"%s: limit %d, current %d, requested %d (would be %d)",
			v.Resource, v.Limit, v.Current, v.Requested, v.Current+v.Requested,
		))
	}

	return fmt.Sprintf("Quota exceeded: %s", strings.Join(parts, "; "))
}
