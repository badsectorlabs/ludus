package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"
)

func configurationInventory() *Inventory {
	return &Inventory{
		Node: "pve-local", LocalIP: "203.0.113.11", NextVMID: 101,
		Endpoints: []string{"https://203.0.113.10:8006", "https://203.0.113.11:8006"},
		VMIDs:     map[int]bool{100: true}, Bridges: []string{"vmbr0"},
		Storages: []StorageInfo{
			{Name: "local", Type: "dir", Content: "iso,vztmpl", Active: true},
			{Name: "local-lvm", Type: "lvmthin", Content: "rootdir,images", Active: true},
			{Name: "fast-images", Type: "zfspool", Content: "images", Active: true},
			{Name: "shared-isos", Type: "nfs", Content: "iso", Active: true, Shared: true},
			{Name: "inactive", Type: "dir", Content: "rootdir,images,iso", Active: false},
		},
	}
}

func configurationOptions(t *testing.T, args ...string) *Options {
	t.Helper()
	opts, err := parseOptions(args, func(string) string { return "" }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return opts
}

func TestConfigurationCLIOverridesEnvironmentIncludingInvalidDefaults(t *testing.T) {
	environment := map[string]string{"VMID": "invalid", "NO_PROMPT": "invalid", "WG_EP": "old.example", "LICENSE": "supplied-license"}
	opts, err := parseOptions([]string{"--vmid", "303", "--no-prompt", "--wg-endpoint", "new.example"}, func(name string) string { return environment[name] }, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if opts.VMID != 303 || !opts.NoPrompt || opts.WireGuardEndpoint != "new.example" || opts.License != "supplied-license" {
		t.Fatal("CLI precedence discarded a supplied setting")
	}
	if _, err := parseOptions(nil, func(name string) string { return environment[name] }, io.Discard); err == nil {
		t.Fatal("invalid unoverridden environment settings were accepted")
	}
}

func TestConfigurationHelpDoesNotDiscloseEnvironmentCredentials(t *testing.T) {
	environment := map[string]string{"TOKEN_SECRET": "private-token-secret", "LICENSE": "private-license-key"}
	var output bytes.Buffer
	_, err := parseOptions([]string{"--help"}, func(name string) string { return environment[name] }, &output)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
	for _, secret := range environment {
		if strings.Contains(output.String(), secret) {
			t.Fatal("help disclosed an environment credential")
		}
	}
	opts, err := parseOptions(nil, func(name string) string { return environment[name] }, io.Discard)
	if err != nil || opts.TokenSecret != environment["TOKEN_SECRET"] || opts.License != environment["LICENSE"] {
		t.Fatal("redacting help must not discard installation credentials")
	}
}

func TestLocalTemplateVersionHonorsExplicitSelection(t *testing.T) {
	path := "/media/ludus-v2.4.0-beta.4-debian13-amd64.tar.zst"
	inferred := configurationOptions(t, "--template-file", path)
	explicit := configurationOptions(t, "--template-file", path, "--version", "installer-candidate")
	if inferred.Version != "v2.4.0-beta.4" || explicit.Version != "installer-candidate" {
		t.Fatal("local artifact release selection did not respect explicit precedence")
	}
}

func TestConfigurationDefaultsUseLocalNodeAndStorageCapabilities(t *testing.T) {
	inv := configurationInventory()
	opts := configurationOptions(t, "--endpoints", "https://custom.example:8006", "--ip", "192.0.2.7/24", "--gw", "192.0.2.1")
	if err := applyDefaults(opts, inv); err != nil {
		t.Fatal(err)
	}
	if opts.WireGuardEndpoint != inv.LocalIP || opts.Endpoints != "https://custom.example:8006" || opts.IP != "192.0.2.7/24" {
		t.Fatal("WireGuard default must use the local node, not an API endpoint or guest IP")
	}
	if opts.Storage != "local-lvm" || opts.VMStorage != "fast-images" || opts.VMStorageFormat != "raw" || opts.ISOStorage != "local" {
		t.Fatal("defaults selected storage without the required content/backend capability")
	}
	if err := validateOptions(opts, inv); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitInvalidVMIDIsNotReplacedByNextID(t *testing.T) {
	for _, value := range []string{"0", "100", "1000000000"} {
		t.Run(value, func(t *testing.T) {
			opts := configurationOptions(t, "--vmid", value, "--ip", "192.0.2.7/24", "--gw", "192.0.2.1")
			inv := configurationInventory()
			if err := applyDefaults(opts, inv); err != nil {
				t.Fatal(err)
			}
			if err := validateOptions(opts, inv); err == nil {
				t.Fatal("invalid or occupied explicit VMID was accepted")
			}
		})
	}
}

func TestConfigurationRequiresStaticManagementAndMatchingGateway(t *testing.T) {
	inv := configurationInventory()
	opts := configurationOptions(t, "--gw", "192.0.2.1")
	if err := applyDefaults(opts, inv); err != nil {
		t.Fatal(err)
	}
	if opts.IP != "" || opts.Gateway != "192.0.2.1" {
		t.Fatal("defaults must not invent an address or discard a supplied gateway")
	}
	for _, unattended := range []bool{false, true} {
		opts.NoPrompt = unattended
		for _, address := range []string{"", "dhcp", "192.0.2.7", "0.0.0.0/24", "127.0.0.1/8", "ff02::1/64"} {
			opts.IP = address
			if err := validateOptions(opts, inv); err == nil {
				t.Fatalf("accepted unusable static management address %q (unattended=%v)", address, unattended)
			}
		}
	}
	opts.IP, opts.Gateway = "::ffff:192.0.2.7/120", "::ffff:192.0.2.1"
	if err := validateOptions(opts, inv); err == nil {
		t.Fatal("accepted an IPv4-mapped IPv6 management address unsupported by forwarding")
	}
	for _, addresses := range [][2]string{{"192.0.2.7/24", "192.0.2.1"}, {"2001:db8::2/64", "2001:db8::1"}} {
		opts.IP, opts.Gateway = addresses[0], addresses[1]
		if err := validateOptions(opts, inv); err != nil {
			t.Fatal(err)
		}
		opts.Gateway = ""
		if err := validateOptions(opts, inv); err == nil {
			t.Fatal("static addressing accepted a missing gateway")
		}
	}
	opts.Gateway = "192.0.2.1"
	if err := validateOptions(opts, inv); err == nil {
		t.Fatal("static IPv6 accepted an IPv4 gateway")
	}
}

func TestWireGuardForwardOptOutPrecedence(t *testing.T) {
	for _, test := range []struct {
		name        string
		environment string
		args        []string
		disabled    bool
	}{
		{"enabled unless declined", "", nil, false},
		{"CLI opt out", "", []string{"--no-wg-forward"}, true},
		{"environment opt out", "1", nil, true},
		{"CLI reenable", "true", []string{"--no-wg-forward=false"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts, err := parseOptions(test.args, func(name string) string {
				if name == "NO_WG_FORWARD" {
					return test.environment
				}
				return ""
			}, io.Discard)
			if err != nil || opts.NoWireGuardForward != test.disabled {
				t.Fatalf("forwarding opt-out precedence: options=%+v error=%v", opts, err)
			}
		})
	}
}

func TestConfigurationRejectsUnsafeEndpointsAndStorage(t *testing.T) {
	inv := configurationInventory()
	opts := configurationOptions(t)
	if err := applyDefaults(opts, inv); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://node:8006", "https://user:secret@node:8006", "https://node:0", "https://node:65536", "https://node/api", "https://node?secret=token"} {
		if err := validateField("ENDPOINTS", endpoint, opts, inv); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
	if err := validateField("ENDPOINTS", "https://[2001:db8::1]:8006 https://node.example:8006", opts, inv); err != nil {
		t.Fatal(err)
	}
	if err := validateField("STORAGE", "local", opts, inv); err == nil {
		t.Fatal("ISO-only pool accepted for a container root filesystem")
	}
	if err := validateField("VM_STORAGE_FORMAT", "qcow2", opts, inv); err == nil {
		t.Fatal("block storage accepted a qcow2 disk format")
	}
	opts.Offline = true
	if err := validateField("ISO_STORAGE", "local", opts, inv); err == nil {
		t.Fatal("offline installation accepted node-local ISO storage")
	}
	if err := validateField("ISO_STORAGE", "shared-isos", opts, inv); err != nil {
		t.Fatal(err)
	}
}

func TestPlannedMigrationBridgeRequiresMigrationContext(t *testing.T) {
	inv := configurationInventory()
	opts := configurationOptions(t)
	opts.Bridge = "ludusmg"
	if err := validateField("LXC_BRIDGE", opts.Bridge, opts, inv); err == nil {
		t.Fatal("fresh install accepted an unavailable management bridge")
	}
	opts.MigrateHost = true
	if err := validateField("LXC_BRIDGE", opts.Bridge, opts, inv); err != nil {
		t.Fatal(err)
	}
	if err := validateField("LXC_BRIDGE", "unplanned", opts, inv); err == nil {
		t.Fatal("migration accepted an unrelated unavailable bridge")
	}
}
