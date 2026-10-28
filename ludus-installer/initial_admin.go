package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v2"
)

// ludus-admin reads this file during first boot (ludus-api InitDb), creates the
// user, records its ID in initialAdminMarker, and deletes the file. A failure
// leaves the file and writes initialAdminError.
const (
	initialAdminFile   = "/opt/ludus/install/initial-admin.yml"
	initialAdminMarker = "/opt/ludus/install/initial-admin-userid"
	initialAdminError  = "/opt/ludus/install/initial-admin-error"
)

func stageInitialAdmin(ctx context.Context, opts *Options, directory string) error {
	data, err := yaml.Marshal(struct {
		Name     string `yaml:"name"`
		Email    string `yaml:"email"`
		UserID   string `yaml:"userID"`
		Password string `yaml:"password"`
	}{opts.AdminName, opts.AdminEmail, opts.AdminUserID, opts.AdminPassword})
	if err != nil {
		return err
	}
	local := filepath.Join(directory, "initial-admin.yml")
	if err := atomicWriteFile(local, data, 0600); err != nil {
		return err
	}
	defer os.Remove(local)
	return pushGuestFile(ctx, opts.VMID, local, initialAdminFile, "0600", "0", "0")
}

// initialAdminFailure returns the guest's recorded creation error, if any.
func initialAdminFailure(ctx context.Context, opts *Options) error {
	if opts.AdminUserID == "" {
		return nil
	}
	message, err := guestOutput(ctx, opts.VMID, "cat", initialAdminError)
	if err != nil {
		return nil
	}
	return fmt.Errorf("initial admin %s was not created: %s (fix the cause, or remove %s inside the LXC, then restart ludus-admin)", opts.AdminUserID, strings.TrimSpace(string(message)), initialAdminFile)
}

func verifyInitialAdmin(ctx context.Context, opts *Options) error {
	created, err := guestOutput(ctx, opts.VMID, "cat", initialAdminMarker)
	if err == nil && strings.TrimSpace(string(created)) == opts.AdminUserID {
		return nil
	}
	if failure := initialAdminFailure(ctx, opts); failure != nil {
		return failure
	}
	return errors.New("Ludus is running, but the initial admin user was not created; check journalctl -u ludus-admin inside the LXC")
}

// issueInitialAdminAPIKey runs inside the LXC. Ludus discards the key it
// generates while creating the initial admin, so issue the admin's first key
// with the root key; nothing else can hold an older one yet.
func issueInitialAdminAPIKey(ctx context.Context, adminPort int) (string, error) {
	marker, err := os.ReadFile(initialAdminMarker)
	userID := strings.TrimSpace(string(marker))
	if err != nil || userID == "" {
		return "", errors.New("no initial admin has been created")
	}
	rootKey, err := os.ReadFile(statusInstallDir + "/root-api-key")
	if err != nil || strings.TrimSpace(string(rootKey)) == "" {
		return "", errors.New("local root API key is unavailable")
	}
	data, err := statusRequest(ctx, adminPort, "/api/v2/user/apikey?"+url.Values{"userID": []string{userID}}.Encode(), strings.TrimSpace(string(rootKey)))
	if err != nil {
		return "", err
	}
	var response struct {
		Result struct {
			APIKey string `json:"apiKey"`
			UserID string `json:"userID"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil || response.Result.APIKey == "" || response.Result.UserID != userID {
		return "", errors.New("Ludus returned an invalid API key response")
	}
	return response.Result.APIKey, nil
}

func initialAdminAPIKey(ctx context.Context, opts *Options) (string, error) {
	output, err := guestOutput(ctx, opts.VMID, guestInstaller, "--guest-admin-apikey", "--admin-port", strconv.Itoa(opts.AdminPort))
	if err != nil {
		return "", fmt.Errorf("Ludus is running, but the initial admin API key could not be issued; sign in to the web UI to create one: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}
