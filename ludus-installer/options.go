package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

func parseOptions(args []string, getenv func(string) string, output io.Writer) (*Options, error) {
	opts := &Options{Provided: make(map[string]bool)}
	flags := flag.NewFlagSet("ludus-installer", flag.ContinueOnError)
	flags.SetOutput(output)
	envErrors := make(map[string]error)
	text := func(target *string, name, environment, fallback, usage string) {
		value := getenv(environment)
		if value != "" {
			opts.Provided[name] = true
		} else {
			value = fallback
		}
		flags.StringVar(target, name, value, usage)
		// Help describes nominal defaults, never environment-supplied secrets.
		flags.Lookup(name).DefValue = fallback
	}
	number := func(target *int, name, environment string, fallback int, usage string) {
		value := fallback
		if raw := getenv(environment); raw != "" {
			var err error
			value, err = strconv.Atoi(raw)
			if err != nil {
				envErrors[name] = fmt.Errorf("%s must be an integer", environment)
			}
			opts.Provided[name] = true
		}
		flags.IntVar(target, name, value, usage)
		flags.Lookup(name).DefValue = strconv.Itoa(fallback)
	}
	boolean := func(target *bool, name, environment, usage string) {
		value := false
		if raw := getenv(environment); raw != "" {
			var err error
			value, err = strconv.ParseBool(raw)
			if err != nil {
				envErrors[name] = fmt.Errorf("%s must be a boolean or 0/1", environment)
			}
			opts.Provided[name] = true
		}
		flags.BoolVar(target, name, value, usage)
		flags.Lookup(name).DefValue = "false"
	}
	text(&opts.Version, "version", "LUDUS_VERSION", Version, "appliance release version (inferred from a local template when omitted)")
	text(&opts.Channel, "channel", "LUDUS_RELEASE_CHANNEL", "stable", "online release channel: stable or beta")
	text(&opts.ReleaseBase, "release-base", "R2_BUCKET_BASE_URL", "https://beta-files.ludus.cloud", "beta artifact base URL")
	text(&opts.ApplianceBase, "appliance-base", "LUDUS_R2_BASE", "https://lxc.ludus.cloud", "appliance artifact base URL")
	boolean(&opts.Offline, "offline", "LUDUS_OFFLINE", "use verified local media only; never download artifacts")
	boolean(&opts.NoPrompt, "no-prompt", "NO_PROMPT", "install without interactive prompts using supplied flags and defaults")
	boolean(&opts.MigrateHost, "migrate-host", "MIGRATE_HOST", "migrate an existing host installation, preserving state and endpoints")
	boolean(&opts.SkipVerification, "skip-verification", "SKIP_VERIFICATION", "explicit development-only bypass of local media signature verification")
	boolean(&opts.VerifyProxmoxTLS, "verify-proxmox-tls", "VERIFY_PROXMOX_TLS", "validate Proxmox endpoint certificates from the LXC")
	text(&opts.TemplateFile, "template-file", "TEMPLATE_FILE", "", "local LXC appliance template")
	text(&opts.ISODirectory, "iso-directory", "ISO_DIRECTORY", "", "offline directory containing all required template ISOs")
	text(&opts.BgInfoFile, "bginfo-file", "BGINFO_FILE", "", "offline copy of bginfo.exe from https://live.sysinternals.com/bginfo.exe")
	text(&opts.LicenseFile, "license-file", "LICENSE_FILE", "", "signed offline license (requires --enterprise-plugin and --license)")
	text(&opts.EnterprisePlugin, "enterprise-plugin", "ENTERPRISE_PLUGIN", "", "local Enterprise plugin package ZIP")
	text(&opts.CACertificate, "ca-certificate", "CA_CERTIFICATE", "", "PEM CA certificate trusted by the container and built templates")
	text(&opts.ChecksumFile, "checksum-file", "CHECKSUM_FILE", "", "SHA-256 manifest covering local installation artifacts")
	text(&opts.ChecksumSignature, "checksum-signature", "CHECKSUM_SIGNATURE", "", "detached signature of the checksum manifest")
	text(&opts.ChecksumPublicKey, "checksum-public-key", "CHECKSUM_PUBLIC_KEY", "", "trusted PEM public key for signature verification")
	text(&opts.ImportDB, "import-db", "IMPORT_DB", "", "complete state archive imported before first boot")
	text(&opts.TokenID, "token-id", "TOKEN_ID", "", "Proxmox API token ID (supply with --token-secret)")
	text(&opts.TokenSecret, "token-secret", "TOKEN_SECRET", "", "Proxmox API token secret (supply with --token-id)")
	text(&opts.Endpoints, "endpoints", "ENDPOINTS", "", "space-separated https://host:port Proxmox endpoints")
	number(&opts.VMID, "vmid", "VMID", 0, "LXC VMID (default: next free cluster VMID)")
	text(&opts.Hostname, "hostname", "LXC_HOSTNAME", "ludus", "LXC hostname")
	text(&opts.Storage, "storage", "STORAGE", "", "active rootdir storage (prefer local-lvm)")
	number(&opts.RootFSSize, "rootfs-size", "LXC_ROOTFS_SIZE", 250, "root filesystem size in GiB, minimum 20")
	text(&opts.Bridge, "bridge", "LXC_BRIDGE", "", "management bridge (prefer vmbr0)")
	text(&opts.VLANTag, "vlan-tag", "LXC_VLAN_TAG", "", "optional management VLAN tag, 1-4094")
	text(&opts.IP, "ip", "ETH0_IP", "", "required static management address with CIDR prefix")
	text(&opts.Gateway, "gw", "ETH0_GW", "", "required gateway for static management addressing")
	text(&opts.Nameserver, "nameserver", "LXC_NAMESERVER", "", "DNS resolver IP; empty inherits Proxmox DNS")
	text(&opts.WireGuardEndpoint, "wg-endpoint", "WG_EP", "", "WireGuard address used by clients (default: local Proxmox node IP; host forwarding enabled)")
	number(&opts.WireGuardPort, "wg-port", "WG_PORT", 51820, "WireGuard UDP port forwarded persistently by the host unless --no-wg-forward is set")
	boolean(&opts.NoWireGuardForward, "no-wg-forward", "NO_WG_FORWARD", "do not create the persistent host WireGuard UDP port forward")
	text(&opts.VMStorage, "vm-storage", "VM_STORAGE", "", "active VM-image storage (prefer local)")
	text(&opts.VMStorageFormat, "vm-storage-format", "VM_STORAGE_FORMAT", "", "VM disk format: qcow2 or raw, selected from the pool backend")
	text(&opts.ISOStorage, "iso-storage", "ISO_STORAGE", "", "active ISO storage (must be shared for offline installation)")
	text(&opts.License, "license", "LICENSE", "community", "Ludus license key, or community")
	text(&opts.NATIP, "nat-ip", "LUDUS_NAT_IP", "192.0.2.253", "Ludus service address on the NAT network")
	text(&opts.NATGateway, "nat-gateway", "LUDUS_NAT_GATEWAY", "192.0.2.254", "Proxmox-side NAT gateway")
	number(&opts.APIPort, "api-port", "LUDUS_API_PORT", 8080, "public Ludus API port")
	number(&opts.AdminPort, "admin-port", "LUDUS_ADMIN_PORT", 8081, "container-local admin API port")
	opts.SDNZone = "ludus"
	flags.BoolVar(&opts.MigrationInfo, "migration-info", false, "print read-only legacy migration metadata")
	flags.StringVar(&opts.ExportState, "export-state", "", "export stopped legacy services' durable state to a new tar.gz archive")
	flags.StringVar(&opts.ExportLive, "export-state-live", "", "export a consistent snapshot while legacy services remain online")
	flags.StringVar(&opts.ImportState, "import-state", "", "import a state archive into a stopped, unbootstrapped appliance")
	flags.Usage = func() {
		fmt.Fprintf(output, "Ludus Installer (build %s)\n\nUsage: ludus-installer [options]\n\n", Version)
		flags.PrintDefaults()
		fmt.Fprintln(output, "\nStatus: ludus-installer --status [--credentials]")
		fmt.Fprintln(output, "Build version: ludus-installer --build-version")
		fmt.Fprintln(output, "Environment values override the listed defaults; command-line flags override the environment.")
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected positional arguments")
	}
	flags.Visit(func(f *flag.Flag) {
		opts.Provided[f.Name] = true
		delete(envErrors, f.Name)
	})
	var envErr error
	flags.VisitAll(func(f *flag.Flag) {
		if envErr == nil {
			envErr = envErrors[f.Name]
		}
	})
	if envErr != nil {
		return nil, envErr
	}
	if opts.TemplateFile != "" && !opts.Provided["version"] {
		if inferred := inferTemplateVersion(opts.TemplateFile); inferred != "" {
			opts.Version = inferred
		}
	}
	return opts, nil
}

func inferTemplateVersion(path string) string {
	name := filepath.Base(path)
	const prefix, suffix = "ludus-", "-debian13-amd64.tar.zst"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
}
