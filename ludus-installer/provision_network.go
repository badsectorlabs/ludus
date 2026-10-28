package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type provisionAPI struct {
	client        *http.Client
	authorization string
}

func newProvisionAPI(opts *Options) (*provisionAPI, error) {
	if opts.TokenID == "" || opts.TokenSecret == "" || strings.ContainsAny(opts.TokenID+opts.TokenSecret, "\r\n\x00") {
		return nil, errors.New("Proxmox token ID and secret must be non-empty and contain no control characters")
	}
	return &provisionAPI{
		client: &http.Client{
			Timeout: 90 * time.Second,
			// Only the node-local Proxmox API is accessed here. User-selected TLS
			// verification applies to the endpoints written into guest config.
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}},
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		authorization: "PVEAPIToken=" + opts.TokenID + "=" + opts.TokenSecret,
	}, nil
}

func (api *provisionAPI) request(ctx context.Context, method, endpoint string, form url.Values, result any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://127.0.0.1:8006/api2/json"+endpoint, body)
	if err != nil {
		return errors.New("unable to construct local Proxmox request")
	}
	req.Header.Set("Authorization", api.authorization)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := api.client.Do(req)
	if err != nil {
		return fmt.Errorf("local Proxmox API request failed for %s", endpoint)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Proxmox API %s returned HTTP %d", endpoint, response.StatusCode)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return errors.New("invalid local Proxmox API response")
	}
	if len(envelope.Errors) != 0 && !bytes.Equal(envelope.Errors, []byte("null")) {
		return fmt.Errorf("Proxmox rejected request for %s", endpoint)
	}
	if result != nil {
		if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
			return fmt.Errorf("Proxmox returned no data for %s", endpoint)
		}
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			return fmt.Errorf("invalid Proxmox data for %s", endpoint)
		}
	}
	return nil
}

func ensureVMIDAvailable(ctx context.Context, vmid int) error {
	if vmid <= 0 {
		return errors.New("LXC VMID must be a positive integer")
	}
	var resources []struct {
		VMID int `json:"vmid"`
	}
	if err := provisionPVEGet(ctx, "/cluster/resources", &resources, "--type", "vm"); err != nil {
		return err
	}
	for _, item := range resources {
		if item.VMID == vmid {
			return fmt.Errorf("VMID %d is occupied; refusing to modify credentials or guest state", vmid)
		}
	}
	return nil
}

func ensureProxmoxToken(ctx context.Context, opts *Options) (*provisionAPI, error) {
	if (opts.TokenID == "") != (opts.TokenSecret == "") {
		return nil, errors.New("--token-id and --token-secret must be supplied together")
	}
	if opts.TokenID == "" {
		name := "ludus-lxc-" + strconv.Itoa(opts.VMID)
		logStep("Generating installation-specific API token root@pam!%s", name)
		output, err := commandOutput(ctx, "pveum", "user", "token", "add", "root@pam", name, "--privsep", "0", "--output-format", "json")
		if err != nil {
			return nil, errors.New("unable to create installation-specific API token; an existing token is never rotated or revoked: supply its ID and secret, or choose a new VMID")
		}
		var token struct {
			ID     string `json:"full-tokenid"`
			Secret string `json:"value"`
		}
		if err := json.Unmarshal(output, &token); err != nil || token.ID != "root@pam!"+name || token.Secret == "" {
			return nil, errors.New("Proxmox returned an invalid generated token; existing credentials were not changed")
		}
		opts.TokenID, opts.TokenSecret = token.ID, token.Secret
	}
	api, err := newProvisionAPI(opts)
	if err != nil {
		return nil, err
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := api.request(ctx, http.MethodGet, "/version", nil, &version); err != nil || version.Version == "" {
		api.client.CloseIdleConnections()
		return nil, errors.New("Proxmox API token validation failed")
	}
	logStep("Proxmox API token validated")
	return api, nil
}

func configureFreshSDN(ctx context.Context, opts *Options, inv *Inventory, api *provisionAPI) error {
	zone := opts.SDNZone
	if zone == "" {
		zone = "ludus"
	}
	zoneType := "simple"
	var peers []string
	for _, node := range inv.Nodes {
		if node.IP != "" {
			peers = append(peers, node.IP)
		}
	}
	if len(inv.Nodes) > 1 {
		zoneType = "vxlan"
		if len(peers) != len(inv.Nodes) {
			return errors.New("every Proxmox cluster node must have an IP for the VXLAN zone")
		}
	}
	var zones []struct {
		Zone string `json:"zone"`
	}
	if err := api.request(ctx, http.MethodGet, "/cluster/sdn/zones", nil, &zones); err != nil {
		return err
	}
	zoneExists := false
	for _, item := range zones {
		if item.Zone == zone {
			zoneExists = true
		}
	}
	if !zoneExists {
		values := url.Values{"zone": {zone}, "type": {zoneType}, "ipam": {"pve"}}
		if zoneType == "vxlan" {
			values.Set("peers", strings.Join(peers, ","))
		}
		if err := api.request(ctx, http.MethodPost, "/cluster/sdn/zones", values, nil); err != nil {
			return err
		}
	}
	var vnets []struct {
		VNet string `json:"vnet"`
		Zone string `json:"zone"`
	}
	if err := api.request(ctx, http.MethodGet, "/cluster/sdn/vnets", nil, &vnets); err != nil {
		return err
	}
	vnetExists := false
	for _, item := range vnets {
		if item.VNet == "ludusnat" {
			if item.Zone != zone {
				return errors.New("existing ludusnat VNet belongs to a different SDN zone")
			}
			vnetExists = true
		}
	}
	values := url.Values{}
	if zoneType == "vxlan" {
		values.Set("tag", "100000")
	}
	if vnetExists {
		values.Set("vlanaware", "0")
		if err := api.request(ctx, http.MethodPut, "/cluster/sdn/vnets/ludusnat", values, nil); err != nil {
			return err
		}
	} else {
		values.Set("vnet", "ludusnat")
		values.Set("zone", zone)
		if err := api.request(ctx, http.MethodPost, "/cluster/sdn/vnets", values, nil); err != nil {
			return err
		}
	}
	var subnets []struct {
		Subnet string `json:"subnet"`
	}
	if err := api.request(ctx, http.MethodGet, "/cluster/sdn/vnets/ludusnat/subnets", nil, &subnets); err != nil {
		return err
	}
	subnet := ""
	for _, item := range subnets {
		if strings.HasSuffix(item.Subnet, "-192.0.2.0-24") {
			subnet = item.Subnet
		}
	}
	values = url.Values{"gateway": {opts.NATGateway}, "snat": {"1"}}
	if subnet != "" {
		if err := api.request(ctx, http.MethodPut, "/cluster/sdn/vnets/ludusnat/subnets/"+url.PathEscape(subnet), values, nil); err != nil {
			return err
		}
	} else {
		values.Set("subnet", "192.0.2.0/24")
		values.Set("type", "subnet")
		if err := api.request(ctx, http.MethodPost, "/cluster/sdn/vnets/ludusnat/subnets", values, nil); err != nil {
			return err
		}
	}
	if err := commandRun(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	if err := os.MkdirAll("/etc/sysctl.d", 0755); err != nil {
		return err
	}
	if err := atomicWriteFile("/etc/sysctl.d/99-ludus-ip-forward.conf", []byte("net.ipv4.ip_forward=1\n"), 0644); err != nil {
		return err
	}
	if err := ensureSDNInterfacesSource("/etc/network/interfaces"); err != nil {
		return err
	}
	if err := api.request(ctx, http.MethodPut, "/cluster/sdn", nil, nil); err != nil {
		return err
	}
	return waitProvision(ctx, time.Minute, time.Second, "SDN apply did not activate ludusnat within 60s; check pvesh get /cluster/sdn", func(ctx context.Context) error {
		output, err := commandOutput(ctx, "ip", "-j", "-4", "address", "show", "dev", "ludusnat")
		if err != nil {
			return err
		}
		var addresses []struct {
			Info []struct {
				Local string `json:"local"`
			} `json:"addr_info"`
		}
		if err := json.Unmarshal(output, &addresses); err != nil {
			return err
		}
		for _, item := range addresses {
			for _, address := range item.Info {
				if address.Local == opts.NATGateway {
					return nil
				}
			}
		}
		return errors.New("NAT gateway is not present")
	})
}

var sdnSourcePattern = regexp.MustCompile(`(?m)^\s*(source\s+/etc/network/interfaces\.d/(\*|sdn)|source-directory\s+/etc/network/interfaces\.d/?)(\s|$)`)

func ensureSDNInterfacesSource(filename string) error {
	data, err := os.ReadFile(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if sdnSourcePattern.Match(data) {
		return nil
	}
	info, err := os.Stat(filename)
	if err != nil {
		return err
	}
	return atomicWriteFile(filename, append(data, []byte("\nsource /etc/network/interfaces.d/sdn\n")...), info.Mode().Perm())
}

func waitProvision(ctx context.Context, timeout, interval time.Duration, failure string, condition func(context.Context) error) error {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for {
		if err := bounded.Err(); err != nil {
			return fmt.Errorf("%s: %w", failure, err)
		}
		if err := condition(bounded); err == nil {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-bounded.Done():
			timer.Stop()
			return fmt.Errorf("%s: %w", failure, bounded.Err())
		case <-timer.C:
		}
	}
}
