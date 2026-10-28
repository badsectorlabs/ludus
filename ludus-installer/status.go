package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
)

const statusInstallDir = "/opt/ludus/install"

var localStatusClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
		// Requests are always to numeric loopback. Appliance TLS is self-signed.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func statusRequest(ctx context.Context, port int, path, key string) ([]byte, error) {
	if port < 1 || port > 65535 || !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("invalid local API request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid local API request")
	}
	if key != "" {
		request.Header.Set("X-API-KEY", key)
	}
	response, err := localStatusClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("local API on port %d is unavailable", port)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("local API on port %d returned HTTP %d", port, response.StatusCode)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("cannot read local API response on port %d", port)
	}
	return data, nil
}

func statusVMID(data []byte) (int, error) {
	var metadata struct {
		VMID int `json:"vmid"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil || metadata.VMID <= 0 {
		return 0, fmt.Errorf("invalid LXC VMID in /etc/ludus-lxc.json")
	}
	return metadata.VMID, nil
}

func statusPorts(data []byte) (int, int, error) {
	var config map[string]any
	if err := yaml.Unmarshal(data, &config); err != nil {
		return 0, 0, fmt.Errorf("invalid API port configuration")
	}
	ports := [2]int{8080, 8081}
	for index, name := range []string{"port", "admin_port"} {
		if value, present := config[name]; present {
			port, ok := value.(int)
			if !ok || port < 1 || port > 65535 {
				return 0, 0, fmt.Errorf("invalid API port configuration")
			}
			ports[index] = port
		}
	}
	return ports[0], ports[1], nil
}

func requireActiveServices(ctx context.Context) error {
	// systemctl with several units succeeds when ANY unit is active.
	for _, service := range []string{"ludus", "ludus-admin"} {
		if _, err := commandOutput(ctx, "systemctl", "is-active", "--quiet", service); err != nil {
			return fmt.Errorf("%s is not active; inspect journalctl inside the LXC", service)
		}
	}
	return nil
}

func runGuestHealth(ctx context.Context, apiPort, adminPort int, includeAdmin bool) error {
	if includeAdmin {
		if err := requireActiveServices(ctx); err != nil {
			return err
		}
	}
	if _, err := statusRequest(ctx, apiPort, "/api/health", ""); err != nil {
		return err
	}
	if !includeAdmin {
		return nil
	}
	key, err := os.ReadFile(statusInstallDir + "/root-api-key")
	if err != nil || strings.TrimSpace(string(key)) == "" {
		return fmt.Errorf("local root API key is unavailable")
	}
	_, err = statusRequest(ctx, adminPort, "/api/v2/user/all", strings.TrimSpace(string(key)))
	return err
}

func runInstallStatus(ctx context.Context, credentials bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run this command as root")
	}
	metadata, err := os.ReadFile("/etc/ludus-lxc.json")
	if err == nil {
		vmid, err := statusVMID(metadata)
		if err != nil {
			return err
		}
		args := []string{"exec", strconv.Itoa(vmid), "--", "/opt/ludus/install/ludus-installer", "--status"}
		if credentials {
			args = append(args, "--credentials")
		}
		return commandAttached(ctx, "pct", args...)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot read LXC metadata")
	}
	if _, err := os.Stat(statusInstallDir + "/.bootstrap-complete"); err != nil {
		return fmt.Errorf("bootstrap is not complete; inspect /opt/ludus/install/install.log inside the Ludus LXC")
	}
	if err := requireActiveServices(ctx); err != nil {
		return err
	}
	config, err := os.ReadFile("/opt/ludus/config.yml")
	if err != nil {
		return fmt.Errorf("cannot read local API configuration")
	}
	publicPort, adminPort, err := statusPorts(config)
	if err != nil {
		return err
	}
	if _, err := statusRequest(ctx, publicPort, "/api/health", ""); err != nil {
		return err
	}
	if _, err := statusRequest(ctx, adminPort, "/api/health", ""); err != nil {
		return err
	}
	fmt.Println("Ludus installation is healthy (services and APIs inside the LXC).")
	marker, err := os.ReadFile(statusInstallDir + "/initial-admin-userid")
	if errors.Is(err, os.ErrNotExist) {
		fmt.Println("No initial-admin marker exists. Migrated installations retain their existing users. This command does not create users. Use an existing administrator and ludus user add against the public API; for a new installation configure /opt/ludus/install/initial-admin.yml inside the LXC and restart ludus-admin.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read initial admin marker")
	}
	userID := strings.TrimSpace(string(marker))
	if userID == "" {
		return fmt.Errorf("initial admin marker is empty")
	}
	fmt.Printf("Initial admin: %s\n", userID)
	if !credentials {
		fmt.Println("Use --credentials to display initial admin credentials. Existing API keys are unchanged.")
		return nil
	}
	key, err := os.ReadFile(statusInstallDir + "/root-api-key")
	if err != nil || strings.TrimSpace(string(key)) == "" {
		return fmt.Errorf("local root API key is unavailable")
	}
	query := url.Values{"userID": []string{userID}}
	data, err := statusRequest(ctx, adminPort, "/api/v2/user/credentials?"+query.Encode(), strings.TrimSpace(string(key)))
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
