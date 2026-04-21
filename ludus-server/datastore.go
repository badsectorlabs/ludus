package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Datastore is a Proxmox storage pool as seen at install time.
// Populated by merging `pvesm status` (runtime state) with `pvesh get /storage` (config).
type Datastore struct {
	Name    string
	Type    string   // dir, zfspool, lvmthin, nfs, btrfs, lvm, rbd, cephfs, cifs, iscsi, ...
	Content []string // subset of {"images","iso","vztmpl","backup","rootdir","snippets"}
	Active  bool
	AvailKB int64
}

// --- Pure functions (unit-testable) ---

func parsePvesmStatus(out string) ([]Datastore, error) {
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	result := make([]Datastore, 0, len(lines))
	headerSkipped := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		// Header detection: first non-empty line starts with "Name"
		if !headerSkipped {
			headerSkipped = true
			if len(fields) >= 1 && fields[0] == "Name" {
				continue
			}
		}
		// Expected columns: Name Type Status Total Used Available Percent
		if len(fields) < 7 {
			return nil, fmt.Errorf("pvesm status: malformed row (want >=7 fields, got %d): %q", len(fields), line)
		}
		name, typ, status := fields[0], fields[1], fields[2]
		avail, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("pvesm status: bad Available value %q: %w", fields[5], err)
		}
		active := status == "active"
		if !active {
			continue
		}
		result = append(result, Datastore{
			Name:    name,
			Type:    typ,
			Active:  active,
			AvailKB: avail,
		})
	}
	return result, nil
}

func parsePveshStorage(jsonBytes []byte) ([]Datastore, error) {
	var raw []struct {
		Storage string `json:"storage"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(jsonBytes, &raw); err != nil {
		return nil, fmt.Errorf("pvesh storage: unmarshal: %w", err)
	}
	result := make([]Datastore, 0, len(raw))
	for _, r := range raw {
		content := []string{}
		if r.Content != "" {
			for _, c := range strings.Split(r.Content, ",") {
				c = strings.TrimSpace(c)
				if c != "" {
					content = append(content, c)
				}
			}
		}
		result = append(result, Datastore{
			Name:    r.Storage,
			Type:    r.Type,
			Content: content,
		})
	}
	return result, nil
}

// joinStores merges runtime state from pvesm status with config from pvesh,
// keyed by storage name. Policy: storage is authoritative for Type and Content;
// status is authoritative for Active and AvailKB. Stores present only in
// pvesh (inactive) are dropped. Stores present only in pvesm (rare; config
// reload in flight) are kept with empty Content — note this means they will
// be filtered out of later filterByContent calls, since no content type
// matches an empty set. That's intentional: we can't safely place images on
// a store whose allowed content types we don't know.
func joinStores(status, storage []Datastore) []Datastore {
	byName := make(map[string]Datastore, len(storage))
	for _, s := range storage {
		byName[s.Name] = s
	}
	out := make([]Datastore, 0, len(status))
	for _, st := range status {
		merged := Datastore{
			Name:    st.Name,
			Type:    st.Type,
			Content: []string{},
			Active:  st.Active,
			AvailKB: st.AvailKB,
		}
		if cfg, ok := byName[st.Name]; ok {
			if cfg.Type != "" {
				merged.Type = cfg.Type
			}
			merged.Content = cfg.Content
		}
		out = append(out, merged)
	}
	return out
}

func filterByContent(stores []Datastore, content string) []Datastore {
	out := []Datastore{}
	for _, s := range stores {
		for _, c := range s.Content {
			if c == content {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// formatForPoolType maps a Proxmox storage backend type to the recommended
// disk image format. Returns "" for unknown types (caller keeps existing default).
func formatForPoolType(t string) string {
	switch t {
	case "dir", "nfs", "cifs", "cephfs", "glusterfs":
		return "qcow2"
	case "zfspool", "zfs", "lvm", "lvmthin", "rbd", "iscsi", "iscsidirect", "btrfs":
		return "raw"
	default:
		return ""
	}
}

// labelFor renders a one-line option label like "local-zfs (zfspool) — 1.8T free".
func labelFor(d Datastore) string {
	return fmt.Sprintf("%s (%s) — %s free", d.Name, d.Type, humanizeKB(d.AvailKB))
}

// humanizeKB converts free-space kilobytes into a short human-readable string
// (e.g. 412 GB, 1.5 GB, 1.8 TB). Uses one decimal place below 100 of each
// unit and drops it above, matching `du -h` behavior.
func humanizeKB(kb int64) string {
	const (
		mb = int64(1024)
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case kb < gb:
		return fmt.Sprintf("%d MB", kb/mb)
	case kb < tb:
		gbVal := float64(kb) / float64(gb)
		if gbVal >= 100 {
			return fmt.Sprintf("%d GB", int64(gbVal))
		}
		return fmt.Sprintf("%.1f GB", gbVal)
	default:
		tbVal := float64(kb) / float64(tb)
		if tbVal >= 100 {
			return fmt.Sprintf("%d TB", int64(tbVal))
		}
		return fmt.Sprintf("%.1f TB", tbVal)
	}
}

// --- Impure wrapper (integration-tested manually) ---

// enumerateDatastores runs `pvesm` and `pvesh` to list Proxmox storages.
// Contract:
//   - (nil, nil)  when pvesm is not on PATH (fresh Debian 12 / non-Proxmox host).
//   - (nil, err)  when a CLI call fails or output is unparseable.
//   - (slice, nil) on success; inactive rows already filtered out.
func enumerateDatastores() ([]Datastore, error) {
	// Fresh-install path: pvesm isn't present yet.
	if _, err := exec.LookPath("pvesm"); err != nil {
		return nil, nil
	}

	statusOut, err := runCmdTimeout("pvesm", "status")
	if err != nil {
		return nil, fmt.Errorf("pvesm status: %w", err)
	}
	storageOut, err := runCmdTimeout("pvesh", "get", "/storage", "--output-format=json")
	if err != nil {
		return nil, fmt.Errorf("pvesh get /storage: %w", err)
	}

	status, err := parsePvesmStatus(statusOut)
	if err != nil {
		return nil, err
	}
	storage, err := parsePveshStorage([]byte(storageOut))
	if err != nil {
		return nil, err
	}
	return joinStores(status, storage), nil
}

// runCmd runs an external command with the given context and returns stdout.
// stderr is captured and included in error messages on non-zero exit.
// Deliberately distinct from process.go's Run/RunWithOutput, which shell out
// via /bin/bash -c and merge stdout+stderr — we need separate streams (for
// parsing stdout cleanly) and an explicit deadline.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s: %w (stderr: %s)", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runCmdTimeout is runCmd with a 3-second deadline, the default for install-time
// enumeration calls. Each call gets its own budget so one slow command can't
// starve the next.
func runCmdTimeout(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return runCmd(ctx, name, args...)
}
