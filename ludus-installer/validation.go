package main

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/go-ozzo/ozzo-validation/v4/is"
)

var (
	decimalInteger = regexp.MustCompile(`^[0-9]+$`)
	dnsLabel       = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
	bridgeName     = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)
	// These mirror ludus-api's user rules (UserIDRegex and reserved IDs).
	adminUserID      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,20}$`)
	proxmoxUserName  = regexp.MustCompile(`^[^\s:/@]{1,60}$`)
	reservedAdminIDs = []string{"ADMIN", "ROOT", "CICD", "SHARED", "0"}
)

// adminProxmoxUsername is the Proxmox user name Ludus derives from a display
// name (ludus-api createInitialAdminFromFile).
func adminProxmoxUsername(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

func validDNSName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func validHost(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil || validDNSName(value)
}

func validateField(key, value string, opts *Options, inv *Inventory) error {
	if key == "VMID" || key == "LXC_ROOTFS_SIZE" || key == "WG_PORT" || key == "LXC_VLAN_TAG" {
		if key == "LXC_VLAN_TAG" && value == "" {
			return nil
		}
		low, high := 1, 0
		switch key {
		case "VMID":
			low, high = 100, 999999999
		case "LXC_ROOTFS_SIZE":
			low = 20
		case "WG_PORT":
			high = 65535
		case "LXC_VLAN_TAG":
			high = 4094
		}
		number, err := strconv.Atoi(value)
		if err != nil || !decimalInteger.MatchString(value) || number < low || (high != 0 && number > high) {
			if high == 0 {
				return fmt.Errorf("enter an integer of at least %d", low)
			}
			return fmt.Errorf("enter an integer from %d to %d", low, high)
		}
		if key == "VMID" && inv.VMIDs[number] {
			return fmt.Errorf("VMID is occupied; choose an unused ID")
		}
		return nil
	}
	switch key {
	case "ENDPOINTS":
		if len(strings.Fields(value)) == 0 {
			return fmt.Errorf("at least one HTTPS endpoint is required")
		}
		for _, endpoint := range strings.Fields(value) {
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Scheme != "https" || !validHost(parsed.Hostname()) || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
				return fmt.Errorf("use space-separated https://host:port endpoints without credentials, paths or queries")
			}
			if port := parsed.Port(); port != "" {
				number, err := strconv.Atoi(port)
				if err != nil || number < 1 || number > 65535 {
					return fmt.Errorf("endpoint port must be between 1 and 65535")
				}
			}
		}
	case "LXC_HOSTNAME":
		if !validDNSName(value) || strings.HasSuffix(value, ".") {
			return fmt.Errorf("enter a valid hostname")
		}
	case "LXC_BRIDGE":
		if !bridgeName.MatchString(value) {
			return fmt.Errorf("enter a valid management bridge")
		}
		// An inspected migration plan may create a private bridge only after
		// confirmation. Its locked value is not a currently active bridge.
		if !(opts.MigrateHost && value == opts.Bridge) && !slices.Contains(inv.Bridges, value) {
			return fmt.Errorf("select an available management bridge")
		}
	case "ETH0_IP":
		address, err := netip.ParsePrefix(value)
		if err != nil || !address.Addr().IsGlobalUnicast() || address.Addr().Is4In6() {
			return fmt.Errorf("enter a static management IP with CIDR prefix; DHCP is not supported")
		}
	case "ETH0_GW":
		gateway, err := netip.ParseAddr(value)
		if err != nil {
			return fmt.Errorf("a gateway IP is required for static addressing")
		}
		management, err := netip.ParsePrefix(opts.IP)
		if err != nil || gateway.Is4() != management.Addr().Is4() {
			return fmt.Errorf("gateway and management IP must use the same address family")
		}
	case "LXC_NAMESERVER":
		if value != "" {
			if _, err := netip.ParseAddr(value); err != nil {
				return fmt.Errorf("enter a DNS server IP or leave empty")
			}
		}
	case "WG_EP":
		if !validHost(value) {
			return fmt.Errorf("enter an IP address or DNS hostname")
		}
	case "WG_FORWARD":
		if value != "yes" && value != "no" {
			return fmt.Errorf("choose whether the installer should forward WireGuard UDP traffic")
		}
	case "STORAGE", "VM_STORAGE", "ISO_STORAGE":
		content := "rootdir"
		if key == "VM_STORAGE" {
			content = "images"
		} else if key == "ISO_STORAGE" {
			content = "iso"
		}
		shared := key == "ISO_STORAGE" && opts.Offline
		for index := range inv.Storages {
			store := &inv.Storages[index]
			if store.Name == value && store.Active && (!shared || store.Shared) && hasStorageContent(store.Content, content) {
				return nil
			}
		}
		if shared {
			return fmt.Errorf("select an active shared datastore supporting %s", content)
		}
		return fmt.Errorf("select an active datastore supporting %s", content)
	case "VM_STORAGE_FORMAT":
		for index := range inv.Storages {
			store := &inv.Storages[index]
			if store.Name == opts.VMStorage && store.Active && hasStorageContent(store.Content, "images") && (value == "raw" || (value == "qcow2" && storageFormat(store.Type) == "qcow2")) {
				return nil
			}
		}
		return fmt.Errorf("select a disk format supported by the VM storage backend")
	case "LICENSE":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("enter a license key or community")
		}
	case "CA_CERTIFICATE":
		if value != "" {
			return validateCertificate(value)
		}
	case "ADMIN_NAME":
		if value == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("enter a display name without leading or trailing spaces")
		}
		username := adminProxmoxUsername(value)
		if !proxmoxUserName.MatchString(username) {
			return fmt.Errorf("use a name without ':', '/' or '@' (at most 60 characters)")
		}
		if inv.ProxmoxUsers[username+"@pve"] {
			return fmt.Errorf("Proxmox user %s@pve already exists", username)
		}
	case "ADMIN_EMAIL":
		// PocketBase stores the user with this exact rule; a looser check lets
		// first boot fail after the container is already created.
		if value == "" || is.EmailFormat.Validate(value) != nil {
			return fmt.Errorf("enter a valid email address, for example jane@example.com")
		}
	case "ADMIN_USERID":
		if !adminUserID.MatchString(value) {
			return fmt.Errorf("start with a letter; use 1-21 letters and numbers")
		}
		if slices.Contains(reservedAdminIDs, strings.ToUpper(value)) {
			return fmt.Errorf("%s is a reserved user ID", value)
		}
		if inv.Pools[value] {
			return fmt.Errorf("a Proxmox pool named %s already exists", value)
		}
	case "ADMIN_PASSWORD":
		if len(value) < 8 {
			return fmt.Errorf("use at least 8 characters")
		}
	case "ADMIN_PASSWORD_CONFIRM":
		if value != opts.AdminPassword {
			return fmt.Errorf("passwords do not match")
		}
	default:
		return fmt.Errorf("unknown installer field %s", key)
	}
	return nil
}

func validateCertificate(path string) error {
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return fmt.Errorf("enter a path to a readable PEM certificate")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("enter a path to a readable PEM certificate")
	}
	for len(data) > 0 {
		block, rest := pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			if _, err := x509.ParseCertificate(block.Bytes); err == nil {
				return nil
			}
			break
		}
		data = rest
	}
	return fmt.Errorf("enter a valid PEM X.509 certificate")
}

func validateOptions(opts *Options, inv *Inventory) error {
	fields := []struct{ key, value, label string }{
		{"ENDPOINTS", opts.Endpoints, "Proxmox endpoints"},
		{"VMID", strconv.Itoa(opts.VMID), "LXC VMID"},
		{"LXC_HOSTNAME", opts.Hostname, "LXC hostname"},
		{"LXC_ROOTFS_SIZE", strconv.Itoa(opts.RootFSSize), "Rootfs size"},
		{"LXC_BRIDGE", opts.Bridge, "Management bridge"},
		{"LXC_VLAN_TAG", opts.VLANTag, "VLAN tag"},
		{"ETH0_IP", opts.IP, "Management IP"},
		{"ETH0_GW", opts.Gateway, "Gateway"},
		{"LXC_NAMESERVER", opts.Nameserver, "DNS resolver"},
		{"WG_EP", opts.WireGuardEndpoint, "WireGuard endpoint"},
		{"WG_PORT", strconv.Itoa(opts.WireGuardPort), "WireGuard UDP port"},
		{"STORAGE", opts.Storage, "LXC rootfs storage"},
		{"VM_STORAGE", opts.VMStorage, "VM storage"},
		{"VM_STORAGE_FORMAT", opts.VMStorageFormat, "VM disk format"},
		{"ISO_STORAGE", opts.ISOStorage, "ISO storage"},
		{"LICENSE", opts.License, "License key"},
		{"CA_CERTIFICATE", opts.CACertificate, "CA certificate"},
	}
	for _, field := range fields {
		if err := validateField(field.key, field.value, opts, inv); err != nil {
			return fmt.Errorf("%s: %w", field.label, err)
		}
	}
	if opts.AdminUserID != "" || opts.AdminName != "" || opts.AdminEmail != "" || opts.AdminPassword != "" {
		if opts.MigrateHost || opts.ImportDB != "" {
			return fmt.Errorf("an initial admin is created only for new installations")
		}
		for _, field := range []struct{ key, value, label string }{
			{"ADMIN_NAME", opts.AdminName, "Admin display name"},
			{"ADMIN_EMAIL", opts.AdminEmail, "Admin email"},
			{"ADMIN_USERID", opts.AdminUserID, "Admin user ID"},
			{"ADMIN_PASSWORD", opts.AdminPassword, "Admin password"},
		} {
			if err := validateField(field.key, field.value, opts, inv); err != nil {
				return fmt.Errorf("%s: %w", field.label, err)
			}
		}
	}
	if (opts.TokenID == "") != (opts.TokenSecret == "") {
		return fmt.Errorf("--token-id and --token-secret must be supplied together")
	}
	for _, port := range []int{opts.APIPort, opts.AdminPort} {
		if port < 1 || port > 65535 {
			return fmt.Errorf("API ports must be between 1 and 65535")
		}
	}
	return nil
}

func validateMode(opts *Options) error {
	if opts.Channel != "stable" && opts.Channel != "beta" {
		return fmt.Errorf("release channel must be stable or beta")
	}
	if opts.Version == "" || opts.Version == "dev" {
		return fmt.Errorf("supply --version or a versioned local appliance template")
	}
	if opts.Channel == "beta" && !opts.Offline && !strings.Contains(opts.Version, "-beta") {
		return fmt.Errorf("the beta channel requires a version containing -beta")
	}
	if opts.MigrateHost && opts.ImportDB != "" {
		return fmt.Errorf("do not combine --migrate-host and --import-db")
	}
	if !opts.Offline && (opts.ISODirectory != "" || opts.LicenseFile != "" || opts.BgInfoFile != "") {
		return fmt.Errorf("--iso-directory, --license-file and --bginfo-file require the offline installer")
	}
	if opts.Offline {
		// The appliance does not ship BgInfo; Windows range configuration
		// otherwise downloads it from live.sysinternals.com.
		if opts.TemplateFile == "" || opts.ISODirectory == "" || opts.BgInfoFile == "" {
			return fmt.Errorf("offline installation requires --template-file, --iso-directory and --bginfo-file")
		}
		if opts.NoPrompt && opts.ISOStorage == "" {
			return fmt.Errorf("unattended offline installation requires an explicit shared --iso-storage")
		}
		if !opts.SkipVerification && (opts.ChecksumFile == "" || opts.ChecksumSignature == "" || opts.ChecksumPublicKey == "") {
			return fmt.Errorf("offline installation requires a signed manifest, detached signature, and trusted public key")
		}
		if opts.LicenseFile != "" && (opts.EnterprisePlugin == "" || !opts.Provided["license"] || opts.License == "") {
			return fmt.Errorf("--license-file requires --enterprise-plugin and --license")
		}
	}
	return nil
}
