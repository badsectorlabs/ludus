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
	"time"

	"ludusapi/dto"

	goproxmox "github.com/luthermonson/go-proxmox"
	"github.com/pocketbase/pocketbase/core"
)

// GetDiagnostics returns system diagnostics including CPU info and Proxmox storage information
func GetDiagnostics(e *core.RequestEvent) error {

	if !e.Auth.GetBool("isAdmin") {
		return JSONError(e, http.StatusForbidden, "You are not authorized to access this endpoint")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get CPU information
	cpuModel, cpuCores, err := getCPUInfo()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error getting CPU info: %v", err))
	}

	// Get Proxmox client (using root client for diagnostics)
	proxmoxClient, err := getRootGoProxmoxClient()
	if err != nil {
		return JSONError(e, http.StatusInternalServerError, fmt.Sprintf("Error creating Proxmox client: %v", err))
	}

	// Get storage pools
	storagePools, err := getStoragePools(ctx, proxmoxClient)
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

// getStoragePools retrieves storage pool information using pvesm status
func getStoragePools(ctx context.Context, client *goproxmox.Client) ([]StoragePoolInfo, error) {
	// Get all storage pools from pvesm status
	pools, err := getAllStoragePoolsFromPvesm()
	if err != nil {
		return nil, fmt.Errorf("failed to get storage pools from pvesm: %w", err)
	}

	return pools, nil
}

// getAllStoragePoolsFromPvesm gets all storage pools using pvesm status command
// Output format:
// Name         Type     Status     Total (KiB)      Used (KiB) Available (KiB)        %
// local         dir     active      1920514320      1428785804       394098004   74.40%
func getAllStoragePoolsFromPvesm() ([]StoragePoolInfo, error) {
	cmd := exec.Command("pvesm", "status")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute pvesm status: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("unexpected pvesm status output: less than 2 lines")
	}

	var storagePools []StoragePoolInfo

	// Skip the header line (first line) and process data lines
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}

		// Parse the line - the format uses fixed-width columns, but we'll use Fields
		// which handles variable spacing. The columns are:
		// Name, Type, Status, Total (KiB), Used (KiB), Available (KiB), %
		fields := strings.Fields(line)
		if len(fields) < 7 {
			// Skip lines that don't have enough fields
			continue
		}

		poolInfo := StoragePoolInfo{
			Name: fields[0],
			Type: fields[1],
		}

		// Parse Total (KiB) - field[3] and convert to GB
		totalKiB, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			continue // Skip this line if we can't parse
		}
		// Convert KiB to GB: KiB / (1024 * 1024) = KiB / 1,048,576
		poolInfo.SizeGB = math.Round(float64(totalKiB)/1048576.0*100) / 100

		// Parse Used (KiB) - field[4] and convert to GB
		usedKiB, err := strconv.ParseInt(fields[4], 10, 64)
		if err != nil {
			continue // Skip this line if we can't parse
		}
		// Convert KiB to GB: KiB / (1024 * 1024) = KiB / 1,048,576
		poolInfo.UsedGB = math.Round(float64(usedKiB)/1048576.0*100) / 100

		// Parse Available (KiB) - field[5] and convert to GB
		availKiB, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			continue // Skip this line if we can't parse
		}
		// Convert KiB to GB: KiB / (1024 * 1024) = KiB / 1,048,576
		poolInfo.FreeGB = math.Round(float64(availKiB)/1048576.0*100) / 100

		// Parse percentage - field[6] (remove % sign)
		percentageStr := strings.TrimSuffix(fields[6], "%")
		percentage, err := strconv.ParseFloat(percentageStr, 64)
		if err != nil {
			continue // Skip this line if we can't parse
		}
		// Calculate free percentage (100 - used percentage) and round to 2 decimal places
		poolInfo.FreePercentage = math.Round((100.0-percentage)*100) / 100

		storagePools = append(storagePools, poolInfo)
	}

	return storagePools, nil
}

// PveperfInfo represents performance information from pveperf command
type PveperfInfo struct {
	CPUBogomips     float64 `json:"cpu_bogomips"`
	RegexPerSecond  int64   `json:"regex_per_second"`
	HdSize          string  `json:"hd_size"`           // e.g., "1831.55 GB (/dev/md0)"
	BufferedReads   string  `json:"buffered_reads"`    // e.g., "5228.59 MB/sec"
	AverageSeekTime string  `json:"average_seek_time"` // e.g., "0.11 ms"
	FsyncsPerSecond float64 `json:"fsyncs_per_second"`
	DNSExt          string  `json:"dns_ext"` // e.g., "17.29 ms"
}

// getPveperf runs pveperf command and parses the output
// Output format:
// CPU BOGOMIPS:      121375.20
// REGEX/SECOND:      7276490
// HD SIZE:           1831.55 GB (/dev/md0)
// BUFFERED READS:    5228.59 MB/sec
// AVERAGE SEEK TIME: 0.11 ms
// FSYNCS/SECOND:     1322.63
// DNS EXT:           17.29 ms
func getPveperf() (*PveperfInfo, error) {
	cmd := exec.Command("pveperf")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to execute pveperf: %w", err)
	}

	lines := strings.Split(string(output), "\n")
	perf := &PveperfInfo{}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse each line by looking for the colon separator
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "CPU BOGOMIPS":
			val, err := strconv.ParseFloat(value, 64)
			if err == nil {
				perf.CPUBogomips = val
			}
		case "REGEX/SECOND":
			val, err := strconv.ParseInt(value, 10, 64)
			if err == nil {
				perf.RegexPerSecond = val
			}
		case "HD SIZE":
			perf.HdSize = value
		case "BUFFERED READS":
			perf.BufferedReads = value
		case "AVERAGE SEEK TIME":
			perf.AverageSeekTime = value
		case "FSYNCS/SECOND":
			val, err := strconv.ParseFloat(value, 64)
			if err == nil {
				perf.FsyncsPerSecond = val
			}
		case "DNS EXT":
			perf.DNSExt = value
		}
	}

	return perf, nil
}
