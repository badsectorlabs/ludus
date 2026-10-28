package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

var errCancelled = errors.New("installation cancelled; no server installation was started")

type interruptedBy struct{ signal os.Signal }

func (err interruptedBy) Error() string { return "interrupted by " + err.signal.String() }

func main() {
	os.Exit(installerMain(os.Args[1:]))
}

func installerMain(args []string) int {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(interrupts)
	go func() {
		select {
		case sig := <-interrupts:
			cancel(interruptedBy{sig})
		case <-ctx.Done():
		}
	}()
	err := runInstaller(ctx, args)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ludus-installer: %v\n", err)
		if cause, ok := context.Cause(ctx).(interruptedBy); ok {
			if number, ok := cause.signal.(syscall.Signal); ok {
				return 128 + int(number)
			}
			return 130
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

func runInstaller(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "--build-version" {
		if len(args) != 1 {
			return fmt.Errorf("--build-version does not accept other arguments")
		}
		fmt.Println(Version)
		return nil
	}
	if len(args) > 0 && args[0] == "--internal-pct-create" {
		return provisionCreateRuntime(args[1:])
	}
	if handled, err := handleMigrationRuntime(ctx, args); handled {
		return err
	}
	if handled, err := handleServiceCommand(ctx, args); handled {
		return err
	}
	opts, err := parseOptions(args, os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	if handled, err := runStateUtility(opts); handled {
		return err
	}
	if err := validateMode(opts); err != nil {
		return err
	}
	if err := requireProxmox(ctx); err != nil {
		return err
	}
	if existingLXC() {
		if opts.MigrateHost {
			return fmt.Errorf("host has already been migrated to an LXC")
		}
		if opts.Offline {
			return fmt.Errorf("offline installation does not update an existing LXC")
		}
		if opts.Channel == "stable" {
			logStep("Ludus is already installed in an LXC on this host")
			return nil
		}
		return updateExisting(ctx, opts)
	}
	if legacyHostInstall() {
		opts.MigrateHost = true
		logStep("Preserving the existing host installation's state and endpoints")
	}
	if err := validateMode(opts); err != nil {
		return err
	}
	inv, err := discoverInventory(ctx)
	if err != nil {
		return err
	}
	opts.Node = inv.Node
	var migration *Migration
	if opts.MigrateHost {
		migration, err = planMigration(ctx, opts)
		if err != nil {
			return err
		}
	}
	if err := applyDefaults(opts, inv); err != nil {
		return err
	}
	if opts.NoPrompt {
		if err := validateOptions(opts, inv); err != nil {
			return err
		}
		result, err := runInstall(ctx, opts, inv, migration)
		if err == nil {
			fmt.Print(result.plain())
		}
		return err
	}
	accepted, err := runLicenseDialog(ctx)
	if err != nil {
		return err
	}
	if !accepted {
		return errCancelled
	}
	confirmed, dark, err := runInstallerForm(ctx, opts, inv)
	if err != nil {
		return err
	}
	if !confirmed {
		return errCancelled
	}
	if err := validateOptions(opts, inv); err != nil {
		return err
	}
	return runInstallWithProgress(ctx, opts, inv, migration, dark)
}

func requireProxmox(ctx context.Context) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("server installation requires an amd64 Proxmox VE host")
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("server installation requires root; use sudo or the bootstrap installer")
	}
	data, err := commandOutput(ctx, "pveversion")
	if err != nil {
		return fmt.Errorf("server installation requires Proxmox VE: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(string(data)), "/")
	if len(parts) < 2 {
		return fmt.Errorf("cannot determine Proxmox VE version")
	}
	major, err := strconv.Atoi(strings.SplitN(parts[1], ".", 2)[0])
	if err != nil || major < 8 {
		return fmt.Errorf("Proxmox VE 8.0 or newer is required")
	}
	for _, name := range []string{"pvesh", "pct", "pveum", "ip"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("required Proxmox command %s is unavailable", name)
		}
	}
	return nil
}

func existingLXC() bool {
	return fileExists("/etc/ludus-lxc.json") || fileExists("/etc/systemd/system/ludus-lxc-forwarding.service")
}

func legacyHostInstall() bool {
	return fileExists("/opt/ludus/config.yml") && !fileExists(statusInstallDir+"/.bootstrap-complete") && !existingLXC()
}

func runStateUtility(opts *Options) (bool, error) {
	count := 0
	for _, enabled := range []bool{opts.MigrationInfo, opts.ExportState != "", opts.ExportLive != "", opts.ImportState != ""} {
		if enabled {
			count++
		}
	}
	if count == 0 {
		return false, nil
	}
	if count != 1 {
		return true, fmt.Errorf("choose only one of --migration-info, --export-state, --export-state-live or --import-state")
	}
	if os.Geteuid() != 0 {
		return true, fmt.Errorf("state migration utilities require root")
	}
	if opts.ImportState != "" {
		return true, importMigrationState(opts.ImportState)
	}
	destination := opts.ExportState
	if opts.ExportLive != "" {
		destination = opts.ExportLive
	}
	return true, exportMigrationState(destination, opts.MigrationInfo, opts.ExportLive != "")
}

func handleServiceCommand(ctx context.Context, args []string) (bool, error) {
	status := filepath.Base(os.Args[0]) == "ludus-install-status"
	if !status && len(args) > 0 && args[0] == "--status" {
		status, args = true, args[1:]
	}
	if status {
		flags := flag.NewFlagSet("ludus-install-status", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		credentials := flags.Bool("credentials", false, "display initial admin credentials without rotating API keys")
		if err := flags.Parse(args); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, fmt.Errorf("unexpected status arguments")
		}
		return true, runInstallStatus(ctx, *credentials)
	}
	if len(args) > 0 && args[0] == "--guest-admin-apikey" {
		if os.Geteuid() != 0 {
			return true, fmt.Errorf("issuing the initial admin API key requires root")
		}
		flags := flag.NewFlagSet("guest admin API key", flag.ContinueOnError)
		adminPort := flags.Int("admin-port", 8081, "local admin API port")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, fmt.Errorf("unexpected guest admin API key arguments")
		}
		key, err := issueInitialAdminAPIKey(ctx, *adminPort)
		if err == nil {
			fmt.Println(key)
		}
		return true, err
	}
	if len(args) == 0 || args[0] != "--guest-health" {
		return false, nil
	}
	if len(args) < 2 || (args[1] != "public" && args[1] != "all") {
		return true, fmt.Errorf("--guest-health requires public or all")
	}
	if os.Geteuid() != 0 {
		return true, fmt.Errorf("guest health requires root")
	}
	flags := flag.NewFlagSet("guest health", flag.ContinueOnError)
	apiPort := flags.Int("api-port", 8080, "public API port")
	adminPort := flags.Int("admin-port", 8081, "local admin API port")
	if err := flags.Parse(args[2:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, fmt.Errorf("unexpected guest health arguments")
	}
	return true, runGuestHealth(ctx, *apiPort, *adminPort, args[1] == "all")
}
