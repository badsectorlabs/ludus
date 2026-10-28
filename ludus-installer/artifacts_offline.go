package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func provisionBool(value json.RawMessage) bool {
	return string(value) == "true" || string(value) == "1" || string(value) == `"1"`
}

func provisionContent(value json.RawMessage, content string) bool {
	var items []string
	var joined string
	if json.Unmarshal(value, &joined) == nil {
		items = strings.Split(joined, ",")
	} else if json.Unmarshal(value, &items) != nil {
		return false
	}
	for _, item := range items {
		if item == content {
			return true
		}
	}
	return false
}

func validateSharedISOStorage(config, status map[string]json.RawMessage) error {
	if !provisionBool(config["shared"]) || !provisionContent(config["content"], "iso") {
		return errors.New("offline ISO storage must be shared and support iso content")
	}
	if !provisionBool(status["active"]) || (status["enabled"] != nil && !provisionBool(status["enabled"])) {
		return errors.New("offline ISO storage is not active and enabled on this node")
	}
	return nil
}

func provisionPVEGet(ctx context.Context, endpoint string, target any, args ...string) error {
	arguments := []string{"get", endpoint, "--output-format", "json"}
	arguments = append(arguments, args...)
	output, err := commandOutput(ctx, "pvesh", arguments...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("invalid Proxmox response for %s: %w", endpoint, err)
	}
	return nil
}

func prepareAirgappedISOs(ctx context.Context, opts *Options, artifacts *installArtifacts) error {
	if !opts.Offline {
		return nil
	}
	if opts.ISOStorage == "" {
		return errors.New("offline installation requires a shared --iso-storage pool")
	}
	var config, status map[string]json.RawMessage
	if err := provisionPVEGet(ctx, "/storage/"+opts.ISOStorage, &config); err != nil {
		return err
	}
	endpoint := "/nodes/" + opts.Node + "/storage/" + opts.ISOStorage
	if err := provisionPVEGet(ctx, endpoint+"/status", &status); err != nil {
		return err
	}
	if err := validateSharedISOStorage(config, status); err != nil {
		return fmt.Errorf("ISO storage %s: %w", opts.ISOStorage, err)
	}
	var contents []struct {
		VolID string `json:"volid"`
	}
	if err := provisionPVEGet(ctx, endpoint+"/content", &contents, "--content", "iso"); err != nil {
		return err
	}
	existing := make(map[string]bool, len(contents))
	for _, item := range contents {
		existing[item.VolID] = true
	}
	for _, name := range airgappedISOFilenames {
		expected, ok := artifacts.isoDigests[name]
		if !ok {
			return fmt.Errorf("ISO %s was not verified during artifact preparation", name)
		}
		volume := opts.ISOStorage + ":iso/" + name
		if existing[volume] {
			output, err := commandOutput(ctx, "pvesm", "path", volume)
			if err != nil {
				return err
			}
			filename := strings.TrimSpace(string(output))
			if !filepath.IsAbs(filename) || strings.ContainsAny(filename, "\r\n\x00") {
				return errors.New("Proxmox returned an invalid ISO path")
			}
			actual, err := hashArtifact(filename)
			if err != nil {
				return err
			}
			if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				return fmt.Errorf("existing ISO %s does not match the verified source", volume)
			}
			logStep("ISO %s already matches the verified source", volume)
			continue
		}
		logStep("Uploading ISO %s to %s", name, opts.ISOStorage)
		if err := uploadAirgappedISO(ctx, endpoint, filepath.Join(opts.ISODirectory, name), name, expected); err != nil {
			return err
		}
	}
	return nil
}

func uploadAirgappedISO(ctx context.Context, endpoint, source, name string, expected [32]byte) error {
	// PVE's upload API requires this exact temporary filename shape. Reserve it
	// exclusively before copying; never overwrite a symlink or another upload.
	var filename string
	for range 10 {
		var random [3]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		filename = "/var/tmp/pveupload-" + hex.EncodeToString(random[:])
		file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if os.IsExist(err) {
			filename = ""
			continue
		}
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(filename)
			return err
		}
		break
	}
	if filename == "" {
		return errors.New("unable to reserve an ISO upload file")
	}
	defer os.Remove(filename)
	if err := copyFile(source, filename, 0600); err != nil {
		return err
	}
	actual, err := hashArtifact(filename)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
		return fmt.Errorf("ISO %s changed after verification", name)
	}
	return commandRun(ctx, "pvesh", "create", endpoint+"/upload", "--content", "iso", "--filename", name,
		"--checksum", hex.EncodeToString(expected[:]), "--checksum-algorithm", "sha256", "--tmpfilename", filename)
}
