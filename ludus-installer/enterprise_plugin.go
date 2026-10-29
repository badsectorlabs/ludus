package main

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"

	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

const enterprisePluginID = "ludus-enterprise"
const enterprisePluginDir = "/opt/ludus/plugins/" + enterprisePluginID

// The Enterprise package ZIP holds exactly these entries; nothing else is
// copied into the root-owned plugin store.
var enterprisePackageModes = map[string]os.FileMode{
	protocol.ManifestFile:   0o644,
	protocol.ExecutableFile: 0o755,
	"ui/index.html":         0o644,
}

// readEnterprisePackage validates the package ZIP and returns its entries in
// archive order. Executable payloads are never run here.
func readEnterprisePackage(archive string) (*zip.ReadCloser, error) {
	reader, err := zip.OpenReader(archive)
	if err != nil {
		return nil, fmt.Errorf("open Enterprise plugin package: %w", err)
	}
	fail := func(err error) (*zip.ReadCloser, error) {
		reader.Close()
		return nil, err
	}
	var names []string
	var manifest []byte
	for _, file := range reader.File {
		if _, ok := enterprisePackageModes[file.Name]; !ok || slices.Contains(names, file.Name) || !file.Mode().IsRegular() {
			return fail(fmt.Errorf("invalid Enterprise plugin package entry: %q", file.Name))
		}
		names = append(names, file.Name)
		if file.Name == protocol.ManifestFile {
			body, err := file.Open()
			if err != nil {
				return fail(err)
			}
			manifest, err = io.ReadAll(io.LimitReader(body, 1<<20))
			body.Close()
			if err != nil {
				return fail(err)
			}
		}
	}
	if manifest == nil || !slices.Contains(names, protocol.ExecutableFile) {
		return fail(errors.New("the Enterprise plugin package requires plugin.json and plugin"))
	}
	parsed, err := protocol.ParseManifest(manifest)
	if err != nil {
		return fail(fmt.Errorf("validate Enterprise plugin package: %w", err))
	}
	if parsed.ID != enterprisePluginID || parsed.Executable == "" {
		return fail(fmt.Errorf("unexpected Enterprise plugin package identity %q", parsed.ID))
	}
	return reader, nil
}

func validateEnterprisePackage(archive string) error {
	reader, err := readEnterprisePackage(archive)
	if err != nil {
		return err
	}
	return reader.Close()
}

// installGuestEnterprisePackage extracts the package on the host and replaces
// the guest's root-owned package directory with it. Both services are stopped.
func installGuestEnterprisePackage(ctx context.Context, vmid int, archive string) error {
	reader, err := readEnterprisePackage(archive)
	if err != nil {
		return err
	}
	defer reader.Close()
	local, err := os.MkdirTemp(filepath.Dir(archive), "enterprise-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(local)
	stage := path.Join(path.Dir(enterprisePluginDir), "."+enterprisePluginID+".install")
	if err := guestRun(ctx, vmid, "install", "-d", "-m", "0755", "-o", "0", "-g", "0", path.Dir(enterprisePluginDir)); err != nil {
		return err
	}
	if err := guestRun(ctx, vmid, "rm", "-rf", stage); err != nil {
		return err
	}
	for index, file := range reader.File {
		source := filepath.Join(local, strconv.Itoa(index))
		if err := extractZipEntry(file, source); err != nil {
			return err
		}
		destination := path.Join(stage, file.Name)
		if err := guestRun(ctx, vmid, "install", "-d", "-m", "0755", "-o", "0", "-g", "0", path.Dir(destination)); err != nil {
			return err
		}
		mode := "0" + strconv.FormatUint(uint64(enterprisePackageModes[file.Name]), 8)
		if err := pushGuestFile(ctx, vmid, source, destination, mode, "0", "0"); err != nil {
			return err
		}
	}
	if err := guestRun(ctx, vmid, "rm", "-rf", enterprisePluginDir); err != nil {
		return err
	}
	return guestRun(ctx, vmid, "mv", stage, enterprisePluginDir)
}

func extractZipEntry(file *zip.File, destination string) error {
	body, err := file.Open()
	if err != nil {
		return err
	}
	defer body.Close()
	return writeAtomic(destination, body, 0o600)
}
