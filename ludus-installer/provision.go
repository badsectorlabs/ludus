package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

func runInstall(ctx context.Context, opts *Options, inv *Inventory, migration *Migration) (installResult, error) {
	if err := provisionInstall(ctx, opts, inv, migration); err != nil {
		return installResult{}, err
	}
	return summarizeInstall(ctx, opts, migration != nil)
}

func provisionInstall(ctx context.Context, opts *Options, inv *Inventory, migration *Migration) (resultErr error) {
	if err := ensureVMIDAvailable(ctx, opts.VMID); err != nil {
		return err
	}
	artifacts, err := prepareArtifacts(ctx, opts)
	if err != nil {
		return err
	}
	defer artifacts.close()
	// A download or interactive session can take a long time. Never change a
	// token or guest on the strength of the earlier inventory snapshot.
	if err := ensureVMIDAvailable(ctx, opts.VMID); err != nil {
		return err
	}
	committed := false
	if err := ensureHostDependencies(ctx, opts, migration != nil); err != nil {
		return err
	}
	if migration != nil {
		// The form may replace Options after planning. Prepare must use the
		// confirmed values, including the persisted forwarding opt-out.
		migration.Options = opts
		defer func() {
			if resultErr == nil || committed {
				return
			}
			rollbackCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if rollbackErr := migration.Rollback(rollbackCtx); rollbackErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("migration rollback: %w", rollbackErr))
			}
		}()
		if err := migration.Prepare(ctx); err != nil {
			return err
		}
	}
	if err := prepareAirgappedISOs(ctx, opts, artifacts); err != nil {
		return err
	}
	if err := ensureVMIDAvailable(ctx, opts.VMID); err != nil {
		return err
	}
	api, err := ensureProxmoxToken(ctx, opts)
	if err != nil {
		return err
	}
	defer api.client.CloseIdleConnections()
	if migration != nil {
		if err := migration.StageNetworks(ctx); err != nil {
			return err
		}
	} else if err := configureFreshSDN(ctx, opts, inv, api); err != nil {
		return err
	}

	vmid := strconv.Itoa(opts.VMID)
	logStep("Creating LXC %d (rootfs on %s, eth0 on %s)", opts.VMID, opts.Storage, opts.Bridge)
	if err := createGuest(ctx, opts, artifacts.installer, artifacts.template, migration); err != nil {
		return err
	}
	if migration != nil {
		if err := migration.OwnCandidate(opts.VMID); err != nil {
			return err
		}
	}
	if err := configureGuestTUN(opts.VMID); err != nil {
		return err
	}
	if err := commandRun(ctx, "pct", "start", vmid); err != nil {
		return fmt.Errorf("start LXC %d: %w", opts.VMID, err)
	}
	if err := waitProvision(ctx, time.Minute, time.Second, "LXC did not become ready for configuration", func(ctx context.Context) error {
		_, err := guestOutput(ctx, opts.VMID, "true")
		return err
	}); err != nil {
		return err
	}
	// PVE 9 requires a running container for pct push. Stop both services before
	// pushing any configuration or local inputs; bootstrap must see them together.
	if err := guestRun(ctx, opts.VMID, "systemctl", "mask", "--now", "systemd-networkd-wait-online.service"); err != nil {
		return err
	}
	if err := guestRun(ctx, opts.VMID, "systemctl", "stop", "ludus-admin", "ludus"); err != nil {
		return fmt.Errorf("stop Ludus before artifact staging: %w", err)
	}
	if err := stageGuestInputs(ctx, opts, artifacts, migration != nil); err != nil {
		return err
	}

	if migration != nil {
		if err := guestRun(ctx, opts.VMID, "systemctl", "restart", "ludus-admin", "ludus"); err != nil {
			return err
		}
		logStep("Staging the LXC runtime while the existing server remains online (up to 5m)")
		if err := waitGuestBootstrap(ctx, opts, true); err != nil {
			return err
		}
		if err := migration.StageCandidateState(ctx); err != nil {
			return err
		}
		if err := migration.Cutover(ctx); err != nil {
			return err
		}
	}
	if artifacts.state != "" {
		logStep("Importing database and WireGuard state")
		if err := importGuestState(ctx, opts.VMID, artifacts.state); err != nil {
			return err
		}
		if err := readImportedEndpoints(ctx, opts); err != nil {
			return err
		}
	}
	if err := guestRun(ctx, opts.VMID, "systemctl", "restart", "ludus-admin", "ludus"); err != nil {
		return err
	}
	logStep("Waiting for first-boot bootstrap (up to 5m)")
	if err := waitGuestBootstrap(ctx, opts, false); err != nil {
		return err
	}
	if migration != nil {
		if err := migration.MoveNICs(ctx); err != nil {
			return err
		}
	}
	if err := waitProvision(ctx, 2*time.Minute, 2*time.Second, "Ludus API services did not become healthy; check journalctl -u ludus -u ludus-admin inside the LXC", func(ctx context.Context) error {
		_, err := guestOutput(ctx, opts.VMID, guestInstaller, "--guest-health", "all", "--api-port", strconv.Itoa(opts.APIPort), "--admin-port", strconv.Itoa(opts.AdminPort))
		return err
	}); err != nil {
		// ludus-admin exits when it cannot create the initial admin.
		return errors.Join(err, initialAdminFailure(ctx, opts))
	}
	if opts.AdminUserID != "" {
		if err := verifyInitialAdmin(ctx, opts); err != nil {
			return err
		}
	}
	if migration != nil {
		if err := migration.StartForwarding(ctx); err != nil {
			return err
		}
		if _, err := statusRequest(ctx, opts.APIPort, "/api/health", ""); err != nil {
			return errors.New("original API endpoint is not reachable after forwarding")
		}
		if err := migration.DrainAPIBridge(ctx); err != nil {
			return err
		}
		if err := migration.Commit(ctx); err != nil {
			return err
		}
		committed = true
	}
	if err := writeInstallMetadata(opts.VMID, opts.Version); err != nil {
		return fmt.Errorf("Ludus is running, but host metadata could not be installed: %w", err)
	}
	if err := installHostStatus(artifacts.installer); err != nil {
		return fmt.Errorf("Ludus is running, but the host status helper could not be installed: %w", err)
	}
	if migration == nil {
		if err := installWireGuardForward(ctx, opts); err != nil {
			return fmt.Errorf("Ludus is running, but host WireGuard forwarding could not be installed: %w", err)
		}
	}
	return nil
}

// ensureHostDependencies installs the host packages WireGuard forwarding needs:
// conntrack for the migration handoff, iptables-persistent for fresh installs.
func ensureHostDependencies(ctx context.Context, opts *Options, migrating bool) error {
	if opts.NoWireGuardForward {
		return nil
	}
	if migrating {
		_, err := exec.LookPath("conntrack")
		return ensureHostPackage(ctx, opts, "conntrack", err == nil, "")
	}
	// Never let the package snapshot the live ruleset (SDN, pve-firewall);
	// only the Ludus rules are merged into the persisted files.
	return ensureHostPackage(ctx, opts, "iptables-persistent", fileExists("/usr/share/netfilter-persistent/plugins.d/15-ip4tables"),
		"iptables-persistent iptables-persistent/autosave_v4 boolean false\niptables-persistent iptables-persistent/autosave_v6 boolean false\n")
}

func ensureHostPackage(ctx context.Context, opts *Options, pkg string, present bool, debconf string) error {
	if present {
		return nil
	}
	if opts.Offline {
		return fmt.Errorf("offline installation requires the %s host package to be installed first; no packages will be downloaded", pkg)
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		return fmt.Errorf("automatic %s installation requires apt-get", pkg)
	}
	logStep("Installing the host %s package required for WireGuard forwarding", pkg)
	if debconf != "" {
		if err := commandInput(ctx, []byte(debconf), "debconf-set-selections"); err != nil {
			return err
		}
	}
	// Unsubscribed hosts often keep the enterprise repositories enabled, so
	// update fails on their 401 although the Debian indexes refresh. Let the
	// install decide; report the update errors only if it fails too.
	updateErr := commandRun(ctx, "apt-get", "update", "-qq")
	if err := commandRun(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "apt-get", "install", "-y", pkg); err != nil {
		if updateErr != nil {
			return errors.Join(err, fmt.Errorf("apt-get update also failed: %w", updateErr))
		}
		return err
	}
	return nil
}

func guestOutput(ctx context.Context, vmid int, args ...string) ([]byte, error) {
	return commandOutput(ctx, "pct", append([]string{"exec", strconv.Itoa(vmid), "--"}, args...)...)
}

func guestRun(ctx context.Context, vmid int, args ...string) error {
	return commandRun(ctx, "pct", append([]string{"exec", strconv.Itoa(vmid), "--"}, args...)...)
}

func pushGuestFile(ctx context.Context, vmid int, source, destination, mode, user, group string) error {
	return commandRun(ctx, "pct", "push", strconv.Itoa(vmid), source, destination, "--perms", mode, "--user", user, "--group", group)
}

func guestCreateArgs(opts *Options, template string, migrating bool) []string {
	addressKey, gatewayKey := "ip", "gw"
	if strings.Contains(opts.IP, ":") {
		addressKey, gatewayKey = "ip6", "gw6"
	}
	net0 := "name=eth0,bridge=" + opts.Bridge + "," + addressKey + "=" + opts.IP
	if opts.Gateway != "" {
		net0 += "," + gatewayKey + "=" + opts.Gateway
	}
	if opts.VLANTag != "" {
		net0 += ",tag=" + opts.VLANTag
	}
	net0 += ",firewall=0"
	net1 := "name=eth1,bridge=ludusnat,ip=" + opts.NATIP + "/24"
	onboot := "1"
	if migrating {
		net1 += ",link_down=1"
		onboot = "0"
	}
	args := []string{"create", strconv.Itoa(opts.VMID), template,
		"--hostname", opts.Hostname, "--unprivileged", "1", "--features", "nesting=1,keyctl=1",
		"--cores", "4", "--memory", "4096", "--swap", "512",
		"--rootfs", opts.Storage + ":" + strconv.Itoa(opts.RootFSSize), "--net0", net0, "--net1", net1,
		"--onboot", onboot, "--startup", "order=99"}
	if opts.Nameserver != "" {
		args = append(args, "--nameserver", opts.Nameserver)
	}
	return args
}

func createGuest(ctx context.Context, opts *Options, installer, template string, migration *Migration) error {
	if opts.RootFSSize < 20 {
		return errors.New("LXC rootfs must be at least 20 GiB")
	}
	if err := ensureVMIDAvailable(ctx, opts.VMID); err != nil {
		return err
	}
	// Re-exec the verified native installer solely to set the child's umask.
	// The helper replaces itself with pct, so context cancellation cannot leave
	// a detached transient unit creating a container after rollback.
	args := append([]string{"--internal-pct-create"}, guestCreateArgs(opts, template, migration != nil)...)
	if migration != nil {
		args = append(args, "--description", migration.CandidateDescription())
	}
	if err := commandRun(ctx, installer, args...); err != nil {
		return fmt.Errorf("pct create failed; check selected storage and /etc/vzdump.conf tmpdir: %w", err)
	}
	return nil
}

func provisionCreateRuntime(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("container creation requires root")
	}
	if len(args) < 3 || args[0] != "create" {
		return errors.New("invalid internal container creation arguments")
	}
	executable, err := exec.LookPath("pct")
	if err != nil {
		return err
	}
	syscall.Umask(0022)
	return syscall.Exec(executable, append([]string{"pct"}, args...), os.Environ())
}

func readImportedEndpoints(ctx context.Context, opts *Options) error {
	data, err := guestOutput(ctx, opts.VMID, "cat", "/opt/ludus/config.yml")
	if err != nil {
		return errors.New("unable to read imported guest endpoint configuration")
	}
	return applyImportedEndpoints(data, opts)
}

func applyImportedEndpoints(data []byte, opts *Options) error {
	var endpoints struct {
		Port              int    `yaml:"port"`
		AdminPort         int    `yaml:"admin_port"`
		WireGuardEndpoint string `yaml:"wireguard_endpoint"`
		WireGuardPort     int    `yaml:"wireguard_port"`
	}
	// Legacy archives may omit values that the runtime supplies by default.
	endpoints.Port, endpoints.AdminPort, endpoints.WireGuardPort = 8080, 8081, 51820
	if err := yaml.Unmarshal(data, &endpoints); err != nil {
		return errors.New("invalid imported guest endpoint configuration")
	}
	for _, port := range []int{endpoints.Port, endpoints.AdminPort, endpoints.WireGuardPort} {
		if port < 1 || port > 65535 {
			return errors.New("imported configuration contains an invalid API or WireGuard port")
		}
	}
	opts.APIPort, opts.AdminPort = endpoints.Port, endpoints.AdminPort
	opts.WireGuardEndpoint, opts.WireGuardPort = endpoints.WireGuardEndpoint, endpoints.WireGuardPort
	return nil
}

func configureGuestTUN(vmid int) error {
	filename := filepath.Join("/etc/pve/lxc", strconv.Itoa(vmid)+".conf")
	// pmxcfs does not support arbitrary POSIX rename/chmod semantics. Append to
	// the newly created, exclusively owned guest config rather than replacing it.
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("\nlxc.cgroup2.devices.allow: c 10:200 rwm\nlxc.mount.entry: /dev/net/tun dev/net/tun none bind,create=file\n")
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func guestConfiguration(opts *Options, migrating bool, encryptionKey string) ([]byte, error) {
	if encryptionKey == "" {
		return nil, errors.New("database encryption key must not be empty")
	}
	zone := opts.SDNZone
	if zone == "" {
		zone = "ludus"
	}
	configuration := map[string]any{
		"proxmox_endpoints":         strings.Fields(opts.Endpoints),
		"proxmox_token_id":          opts.TokenID,
		"proxmox_token_secret":      opts.TokenSecret,
		"proxmox_node":              opts.Node,
		"proxmox_user_realm":        "pve",
		"proxmox_vm_storage_pool":   opts.VMStorage,
		"proxmox_vm_storage_format": opts.VMStorageFormat,
		"proxmox_iso_storage_pool":  opts.ISOStorage,
		"proxmox_invalid_cert":      !opts.VerifyProxmoxTLS,
		"ludus_nat_interface":       "ludusnat",
		"ludus_nat_ip":              opts.NATIP,
		"ludus_nat_gateway":         opts.NATGateway,
		"wireguard_endpoint":        opts.WireGuardEndpoint,
		"wireguard_port":            opts.WireGuardPort,
		"sdn_zone":                  zone,
		"host_managed_network":      migrating || opts.HostManagedNetwork,
		"license_key":               opts.License,
		"expose_admin_port":         false,
		"port":                      opts.APIPort,
		"admin_port":                opts.AdminPort,
		"data_directory":            "/opt/ludus/db",
		"tls_cert_file":             "/opt/ludus/tls/server.crt",
		"tls_key_file":              "/opt/ludus/tls/server.key",
		"database_encryption_key":   encryptionKey,
	}
	if opts.Offline {
		configuration["airgapped_install"] = true
	}
	return yaml.Marshal(configuration)
}

func stageGuestInputs(ctx context.Context, opts *Options, artifacts *installArtifacts, migrating bool) error {
	var key [24]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	configuration, err := guestConfiguration(opts, migrating, base64.RawURLEncoding.EncodeToString(key[:]))
	if err != nil {
		return err
	}
	filename := filepath.Join(artifacts.directory, "config.yml")
	if err := atomicWriteFile(filename, configuration, 0600); err != nil {
		return err
	}
	if err := guestRun(ctx, opts.VMID, "install", "-d", "-m", "0755", "/opt/ludus/install", "/usr/local/bin"); err != nil {
		return err
	}
	if err := pushGuestFile(ctx, opts.VMID, artifacts.installer, guestInstaller, "0755", "0", "0"); err != nil {
		return err
	}
	if err := guestRun(ctx, opts.VMID, "ln", "-sfn", guestInstaller, "/usr/local/bin/ludus-install-status"); err != nil {
		return err
	}
	if err := pushGuestFile(ctx, opts.VMID, filename, "/opt/ludus/config.yml", "0600", "1001", "1001"); err != nil {
		return err
	}
	if opts.AdminUserID != "" && !migrating {
		if err := stageInitialAdmin(ctx, opts, artifacts.directory); err != nil {
			return err
		}
	}
	if artifacts.license != "" {
		if err := pushGuestFile(ctx, opts.VMID, artifacts.license, "/opt/ludus/license.lic", "0640", "1001", "1001"); err != nil {
			return err
		}
	}
	if artifacts.bginfo != "" {
		// Windows range configuration uses this copy instead of downloading it.
		if err := pushGuestFile(ctx, opts.VMID, artifacts.bginfo, "/opt/ludus/resources/windows/bginfo.exe", "0644", "1001", "1001"); err != nil {
			return err
		}
	}
	if artifacts.plugin != "" {
		if err := installGuestEnterprisePackage(ctx, opts.VMID, artifacts.plugin); err != nil {
			return fmt.Errorf("install Enterprise plugin package: %w", err)
		}
	}
	if artifacts.ca != "" {
		paths := []string{"/opt/ludus/install/injected-ca-certificate.crt", "/usr/local/share/ca-certificates/ludus-injected-ca.crt"}
		for _, template := range []string{"debian11", "debian12", "debian13", "kali"} {
			directory := "/opt/ludus/packer/" + template + "/http"
			if err := guestRun(ctx, opts.VMID, "mkdir", "-p", directory); err != nil {
				return err
			}
			paths = append(paths, directory+"/ludus-injected-ca.crt")
		}
		for _, destination := range paths {
			if err := pushGuestFile(ctx, opts.VMID, artifacts.ca, destination, "0644", "0", "0"); err != nil {
				return err
			}
		}
		if err := guestRun(ctx, opts.VMID, "update-ca-certificates"); err != nil {
			return err
		}
	}
	return nil
}

func importGuestState(ctx context.Context, vmid int, source string) (resultErr error) {
	if err := guestRun(ctx, vmid, "systemctl", "stop", "ludus-admin", "ludus"); err != nil {
		return err
	}
	output, err := guestOutput(ctx, vmid, "mktemp", "/tmp/ludus-import.XXXXXXXX.tar.gz")
	if err != nil {
		return err
	}
	filename := strings.TrimSpace(string(output))
	if !safeGuestTemporary(filename, "ludus-import.") {
		return errors.New("guest returned an unsafe state-import temporary path")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := guestOutput(cleanup, vmid, "rm", "-f", "--", filename); err != nil {
			resultErr = errors.Join(resultErr, errors.New("unable to remove the temporary guest state archive"))
		}
	}()
	if err := pushGuestFile(ctx, vmid, source, filename, "0600", "0", "0"); err != nil {
		return err
	}
	return guestRun(ctx, vmid, guestInstaller, "--import-state", filename)
}

func waitGuestBootstrap(ctx context.Context, opts *Options, healthy bool) error {
	return waitProvision(ctx, 5*time.Minute, 5*time.Second, "bootstrap did not complete; see /opt/ludus/install/install.log inside the container", func(ctx context.Context) error {
		if _, err := guestOutput(ctx, opts.VMID, "test", "-f", "/opt/ludus/install/.bootstrap-complete"); err != nil {
			return err
		}
		if healthy {
			for _, service := range []string{"ludus", "ludus-admin"} {
				if _, err := guestOutput(ctx, opts.VMID, "systemctl", "is-active", "--quiet", service); err != nil {
					return err
				}
			}
			_, err := guestOutput(ctx, opts.VMID, guestInstaller, "--guest-health", "public", "--api-port", strconv.Itoa(opts.APIPort), "--admin-port", strconv.Itoa(opts.AdminPort))
			return err
		}
		return nil
	})
}

func writeInstallMetadata(vmid int, version string) error {
	data, err := json.MarshalIndent(map[string]any{"vmid": vmid, "version": version}, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile("/etc/ludus-lxc.json", append(data, '\n'), 0600)
}

func installHostStatus(installer string) error {
	const helper = "/usr/local/bin/ludus-install-status"
	const binary = "/usr/local/libexec/ludus-installer"
	for _, directory := range []string{"/usr/local/bin", "/usr/local/libexec"} {
		if err := os.MkdirAll(directory, 0755); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(helper); err == nil {
		if _, err := os.Lstat(helper + ".pre-lxc"); os.IsNotExist(err) {
			info, err := os.Stat(helper)
			if err != nil {
				return err
			}
			if err := copyFile(helper, helper+".pre-lxc", info.Mode().Perm()); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := copyFile(installer, binary, 0755); err != nil {
		return err
	}
	if err := os.Chown(binary, 0, 0); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(helper), ".ludus-status-")
	if err != nil {
		return err
	}
	name := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	defer os.Remove(name)
	if err := os.Remove(name); err != nil {
		return err
	}
	if err := os.Symlink(binary, name); err != nil {
		return err
	}
	if err := os.Lchown(name, 0, 0); err != nil {
		return err
	}
	return os.Rename(name, helper)
}

// installResult is the closing summary: a plain text form for unattended runs
// and a boxed form for the interactive progress view. Commands are printed
// below the box, unindented, so they can be copied and pasted as-is.
type installResult struct {
	Title    string
	Rows     [][2]string
	Notes    []string
	Commands []string
}

func (r installResult) plain() string {
	var text strings.Builder
	fmt.Fprintf(&text, "\n%s\n", r.Title)
	for _, row := range r.Rows {
		fmt.Fprintf(&text, "  %-11s %s\n", row[0]+":", row[1])
	}
	text.WriteString("\n")
	for _, note := range r.Notes {
		text.WriteString(note + "\n")
	}
	if len(r.Commands) > 0 {
		text.WriteString("\n" + strings.Join(r.Commands, "\n") + "\n")
	}
	return text.String()
}

// shellWord quotes a value only when the shell would otherwise alter it.
func shellWord(value string) string {
	if value != "" && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._:/+=@%-") == "" {
		return value
	}
	return shellQuote(value)
}

func summarizeInstall(ctx context.Context, opts *Options, migrated bool) (installResult, error) {
	result := installResult{Title: fmt.Sprintf("Ludus is running in LXC %d", opts.VMID)}
	apiURL := ""
	if migrated {
		result.Rows = append(result.Rows, [2]string{"API", "existing endpoint preserved"})
	} else {
		output, err := guestOutput(ctx, opts.VMID, "hostname", "-I")
		if err != nil {
			return result, fmt.Errorf("Ludus is running, but its IP could not be determined: %w", err)
		}
		address := ""
		for _, candidate := range strings.Fields(string(output)) {
			if net.ParseIP(candidate) != nil {
				address = candidate
				break
			}
		}
		if address == "" {
			return result, errors.New("Ludus is running, but the container has no IP address")
		}
		apiURL = "https://" + net.JoinHostPort(address, strconv.Itoa(opts.APIPort))
		result.Rows = append(result.Rows, [2]string{"API", apiURL})
		if opts.AdminUserID != "" {
			result.Rows = append(result.Rows, [2]string{"Web UI", apiURL + "/ui"})
		}
	}
	result.Rows = append(result.Rows, [2]string{"Status", fmt.Sprintf("ludus-install-status (automatically enters LXC %d)", opts.VMID)})
	switch {
	case migrated:
		result.Notes = []string{"Existing clients, API keys, and WireGuard configurations remain valid."}
	case opts.AdminUserID != "":
		result.Notes = []string{fmt.Sprintf("Initial admin %s created. Sign in to the web UI with %s and the password you chose.", opts.AdminUserID, opts.AdminEmail)}
		// The install has succeeded; a key that cannot be issued only changes
		// the advice, not the outcome.
		if key, err := initialAdminAPIKey(ctx, opts); err != nil {
			result.Notes = append(result.Notes, err.Error())
		} else {
			result.Notes = append(result.Notes, "Store the API key below securely: it is not shown again, but you can create a new one in the web UI or via the CLI. With ludus-client installed, run:")
			result.Commands = []string{
				"export LUDUS_URL=" + shellWord(apiURL),
				"export LUDUS_API_KEY=" + shellWord(key),
				"ludus range list all",
			}
		}
	default:
		result.Notes = []string{"Next: install ludus-client and run 'ludus user add <name>'."}
	}
	return result, nil
}
