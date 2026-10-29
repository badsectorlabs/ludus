package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func readUpdateMetadata(filename string) (map[string]json.RawMessage, int, os.FileMode, error) {
	data, err := readArtifactLimited(filename, 1<<20)
	if err != nil {
		return nil, 0, 0, err
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, 0, 0, errors.New("invalid LXC host metadata")
	}
	var vmid int
	if err := json.Unmarshal(metadata["vmid"], &vmid); err != nil || vmid <= 0 {
		return nil, 0, 0, errors.New("invalid VMID in LXC host metadata")
	}
	info, err := os.Stat(filename)
	if err != nil {
		return nil, 0, 0, err
	}
	return metadata, vmid, info.Mode().Perm(), nil
}

func updateMetadataVersion(filename, version string, expectedVMID int) error {
	// Read again after updating the guest: do not overwrite unrelated metadata
	// changes or associate this version with a different container.
	metadata, vmid, mode, err := readUpdateMetadata(filename)
	if err != nil {
		return err
	}
	if vmid != expectedVMID {
		return errors.New("LXC metadata changed during the update; refusing to overwrite it")
	}
	metadata["version"], err = json.Marshal(version)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(filename, append(data, '\n'), mode)
}

func safeGuestTemporary(filename, prefix string) bool {
	if filepath.Dir(filename) != "/tmp" || !strings.HasPrefix(filepath.Base(filename), prefix) {
		return false
	}
	suffix := strings.TrimPrefix(filepath.Base(filename), prefix)
	if suffix == "" {
		return false
	}
	for _, character := range suffix {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') && !(character >= '0' && character <= '9') && character != '.' && character != '-' && character != '_' {
			return false
		}
	}
	return true
}

func updateExisting(ctx context.Context, opts *Options) (resultErr error) {
	if opts.Offline {
		return errors.New("offline installation does not update an existing LXC")
	}
	if opts.Channel != "beta" {
		return errors.New("an existing LXC is only updated by the beta channel")
	}
	if opts.TemplateFile != "" {
		return errors.New("an existing beta LXC must be updated online without --template-file")
	}
	_, vmid, _, err := readUpdateMetadata("/etc/ludus-lxc.json")
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "ludus-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	server := filepath.Join(directory, "ludus-server")
	if err := downloadArtifact(ctx, opts, opts.ReleaseBase, "ludus-server", server); err != nil {
		return err
	}
	// Runtime --update only replaces embedded payloads. It is deliberately run
	// inside the existing guest and never against the Proxmox host's state.
	manifest, err := fetchReleaseManifest(ctx, opts, opts.ReleaseBase, "ludus_"+opts.Version+"_checksums.txt", directory)
	if err != nil {
		return err
	}
	if err := manifest.verify(server, "ludus-server"); err != nil {
		return err
	}
	output, err := guestOutput(ctx, vmid, "mktemp", "/tmp/ludus-update.XXXXXXXX")
	if err != nil {
		return err
	}
	guestFile := strings.TrimSpace(string(output))
	if !safeGuestTemporary(guestFile, "ludus-update.") {
		return errors.New("guest returned an unsafe update temporary path")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := guestOutput(cleanup, vmid, "rm", "-f", "--", guestFile); err != nil {
			resultErr = errors.Join(resultErr, errors.New("unable to remove temporary guest update executable"))
		}
	}()
	if err := pushGuestFile(ctx, vmid, server, guestFile, "0700", "0", "0"); err != nil {
		return err
	}
	logStep("Updating Ludus %s inside LXC %d", opts.Version, vmid)
	if err := guestRun(ctx, vmid, "env", "HOME=/root", "TMPDIR=/tmp",
		"PATH=/opt/ludus/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG=C.UTF-8", "LC_ALL=C.UTF-8", guestFile, "--update"); err != nil {
		return fmt.Errorf("LXC %s runtime update failed: %w", strconv.Itoa(vmid), err)
	}
	if err := updateMetadataVersion("/etc/ludus-lxc.json", opts.Version, vmid); err != nil {
		return err
	}
	logStep("Ludus server LXC update complete")
	return nil
}
