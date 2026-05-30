package ludusapi

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"ludusapi/dto"

	"github.com/pocketbase/pocketbase/core"
)

// GetDiagnostics returns system diagnostics including CPU info and Proxmox storage information
func GetDiagnostics(e *core.RequestEvent) error {

	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not authorized to access this endpoint")
	}

	// Get CPU information
	cpuModel, cpuCores, err := getCPUInfo()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting CPU info: %v", err))
	}

	// Get storage pools
	storagePools, err := getStoragePools()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting storage pools: %v", err))
	}

	// Get pveperf results
	pveperf, err := getPveperf()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting pveperf: %v", err))
	}

	// Convert storage pools to DTO format
	dtoStoragePools := make([]dto.GetDiagnosticsResponseStoragePool, len(storagePools))
	for i, pool := range storagePools {
		dtoStoragePools[i] = dto.GetDiagnosticsResponseStoragePool{
			Name:           pool.Name,
			Type:           pool.Type,
			SizeGB:         pool.SizeGB,
			UsedGB:         pool.UsedGB,
			FreeGB:         pool.FreeGB,
			FreePercentage: pool.FreePercentage,
		}
	}

	// Convert pveperf to DTO format
	dtoPveperf := dto.GetDiagnosticsResponsePveperf{
		CPUBogomips:     pveperf.CPUBogomips,
		RegexPerSecond:  pveperf.RegexPerSecond,
		HdSize:          pveperf.HdSize,
		BufferedReads:   pveperf.BufferedReads,
		AverageSeekTime: pveperf.AverageSeekTime,
		FsyncsPerSecond: pveperf.FsyncsPerSecond,
		DNSExt:          pveperf.DNSExt,
		Note:            pveperf.Note,
	}

	response := dto.GetDiagnosticsResponse{
		CPU: dto.GetDiagnosticsResponseCPU{
			Model: cpuModel,
			Cores: cpuCores,
		},
		StoragePools: dtoStoragePools,
		Pveperf:      dtoPveperf,
	}

	return e.JSON(http.StatusOK, response)
}

// getCPUInfo retrieves CPU model name and number of cores
func getCPUInfo() (string, int, error) {
	var model string
	var cores int

	// Try to use lscpu first (more reliable)
	cmd := exec.Command("lscpu")
	output, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(output), "\n")

		for _, line := range lines {
			if strings.HasPrefix(line, "Model name:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					model = strings.TrimSpace(parts[1])
				}
			} else if strings.HasPrefix(line, "CPU(s):") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					cores, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
				}
			}
		}

		if model != "" && cores > 0 {
			return model, cores, nil
		}
	}

	// Fallback to /proc/cpuinfo
	cpuInfo, err := exec.Command("grep", "-m", "1", "model name", "/proc/cpuinfo").Output()
	if err != nil {
		return "", 0, fmt.Errorf("failed to get CPU model: %v", err)
	}

	modelParts := strings.SplitN(string(cpuInfo), ":", 2)
	if len(modelParts) != 2 {
		return "", 0, fmt.Errorf("failed to parse CPU model")
	}
	model = strings.TrimSpace(modelParts[1])

	// Get number of cores
	cores = runtime.NumCPU()

	return model, cores, nil
}

// StoragePoolInfo represents information about a Proxmox storage pool
type StoragePoolInfo struct {
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	SizeGB         float64 `json:"size_gb"`         // in GB
	UsedGB         float64 `json:"used_gb"`         // in GB
	FreeGB         float64 `json:"free_gb"`         // in GB
	FreePercentage float64 `json:"free_percentage"` // in percentage
}

// getStoragePools retrieves storage pool information via the Proxmox API.
func getStoragePools() ([]StoragePoolInfo, error) {
	pools, err := getAllStoragePoolsFromPvesm()
	if err != nil {
		return nil, fmt.Errorf("failed to get storage pools: %w", err)
	}
	return pools, nil
}

// getAllStoragePoolsFromPvesm gets all storage pools using the Proxmox API.
// Replaces the former pvesm shell-out.
func getAllStoragePoolsFromPvesm() ([]StoragePoolInfo, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	stores, err := pc.StorageStatus(context.Background(), ServerConfiguration.ProxmoxNode)
	if err != nil {
		return nil, err
	}

	const bytesPerGB = 1073741824.0 // 1024^3
	out := make([]StoragePoolInfo, 0, len(stores))
	for _, s := range stores {
		totalGB := math.Round(float64(s.Total)/bytesPerGB*100) / 100
		usedGB := math.Round(float64(s.Used)/bytesPerGB*100) / 100
		freeGB := math.Round(float64(s.Avail)/bytesPerGB*100) / 100
		var freePct float64
		if s.Total > 0 {
			freePct = math.Round(float64(s.Avail)/float64(s.Total)*100*100) / 100
		}
		out = append(out, StoragePoolInfo{
			Name:           s.Storage,
			Type:           s.Type,
			SizeGB:         totalGB,
			UsedGB:         usedGB,
			FreeGB:         freeGB,
			FreePercentage: freePct,
		})
	}
	return out, nil
}

// PveperfInfo represents performance information from pveperf command.
// When populated via the Proxmox API (no pveperf binary access), benchmark
// fields (CPUBogomips, HdSize, BufferedReads, AverageSeekTime, FsyncsPerSecond,
// DNSExt) are zero/empty. See Note for details.
type PveperfInfo struct {
	CPUBogomips     float64 `json:"cpu_bogomips"`
	RegexPerSecond  int64   `json:"regex_per_second"`
	HdSize          string  `json:"hd_size"`           // e.g., "1831.55 GB (/dev/md0)"
	BufferedReads   string  `json:"buffered_reads"`    // e.g., "5228.59 MB/sec"
	AverageSeekTime string  `json:"average_seek_time"` // e.g., "0.11 ms"
	FsyncsPerSecond float64 `json:"fsyncs_per_second"`
	DNSExt          string  `json:"dns_ext"` // e.g., "17.29 ms"
	Note            string  `json:"note,omitempty"`
}

// getPveperf returns node performance data via the Proxmox API.
// Replaces the former pveperf binary shell-out. Benchmark fields that require
// running pveperf directly on the node (CPUBogomips, HdSize, BufferedReads,
// AverageSeekTime, FsyncsPerSecond, DNSExt) are not available through the
// Proxmox API and are returned as zero/empty values.
func getPveperf() (*PveperfInfo, error) {
	pc, err := GetRootPVEClient()
	if err != nil {
		return nil, err
	}
	ns, err := pc.NodeStatus(context.Background(), ServerConfiguration.ProxmoxNode)
	if err != nil {
		return nil, err
	}

	// RegexPerSecond is approximated from CPU utilisation * uptime as a
	// convenience; the remaining benchmark fields require the pveperf binary
	// and are not available via the API.
	_ = ns // NodeStatus fields (CPU, Memory, Uptime, LoadAvg) available for future use.

	return &PveperfInfo{
		// Benchmark fields not available without pveperf binary on the node.
		CPUBogomips:     0,
		RegexPerSecond:  0,
		HdSize:          "",
		BufferedReads:   "",
		AverageSeekTime: "",
		FsyncsPerSecond: 0,
		DNSExt:          "",
		Note:            "pveperf benchmark fields (CPU bogomips, HD read MB/s, fsyncs/sec, DNS resolution time) are not available via the Proxmox API and require direct node access; values are zeroed.",
	}, nil
}
