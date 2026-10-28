package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func migrationExitCode(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return -1
}
func migrationLoad(directory string) (*Migration, error) {
	clean := filepath.Clean(directory)
	if !filepath.IsAbs(directory) || directory != clean || filepath.Dir(directory) != "/var/lib/ludus-migration" || !strings.HasPrefix(filepath.Base(directory), "upgrade.") {
		return nil, errors.New("invalid migration rollback directory")
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil || resolved != directory {
		return nil, errors.New("migration directory must not contain symlinks")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("migration directory must be private to root")
	}
	if err = migrationCheckRootOwned(directory); err != nil {
		return nil, err
	}
	if err = migrationCheckRootOwned(filepath.Join(directory, "migration.json")); err != nil {
		return nil, err
	}
	saved, err := migrationReadFile(filepath.Join(directory, "migration.json"))
	if err != nil {
		return nil, err
	}
	if saved.Mode&0077 != 0 {
		return nil, errors.New("migration state must be private to root")
	}
	var m Migration
	if err = json.Unmarshal(saved.Data, &m); err != nil {
		return nil, err
	}
	if m.Directory != directory || !m.Prepared {
		return nil, errors.New("migration state directory does not match its ownership record")
	}
	prefix, err := netip.ParsePrefix(m.Network.IP)
	if err != nil || !prefix.Addr().Is4() {
		return nil, errors.New("invalid saved migration management address")
	}
	if err = migrationValidateInterface(m.Network.Bridge); err != nil {
		return nil, err
	}
	if err = migrationValidateInterface(m.Network.Uplink); err != nil {
		return nil, err
	}
	for _, port := range []int{m.Network.Port, m.Network.AdminPort, m.Network.WireGuardPort} {
		if port < 1 || port > 65535 {
			return nil, errors.New("invalid saved migration endpoint")
		}
	}
	return &m, nil
}

// Internal command modes are intentionally dispatched before installation flags.
// They are native runtime operations, never a compatibility shell/Python engine.
func handleMigrationRuntime(ctx context.Context, args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "--migration-probe", "--migration-announce", "--migration-final-import", "--migration-forward-start", "--migration-forward-stop", "--migration-rollback", "--migration-api-bridge":
	default:
		return false, nil
	}
	if os.Geteuid() != 0 {
		return true, errors.New("migration runtime commands require root")
	}
	if args[0] == "--migration-probe" || args[0] == "--migration-announce" {
		if len(args) != 3 {
			return true, fmt.Errorf("%s requires INTERFACE IPv4_ADDRESS", args[0])
		}
		if err := migrationValidateInterface(args[1]); err != nil {
			return true, err
		}
		if args[0] == "--migration-probe" {
			return true, migrationProbeAddress(ctx, args[1], args[2])
		}
		return true, migrationAnnounceAddress(ctx, args[1], args[2])
	}
	if len(args) != 2 {
		return true, fmt.Errorf("%s requires one path", args[0])
	}
	if args[0] == "--migration-final-import" {
		return true, migrationImportFinalState(args[1])
	}
	m, err := migrationLoad(args[1])
	if err != nil {
		return true, err
	}
	switch args[0] {
	case "--migration-api-bridge":
		return true, runMigrationAPIBridge(ctx, m)
	case "--migration-forward-start":
		return true, m.forward(ctx, "start")
	case "--migration-forward-stop":
		return true, m.removeOwnedRules(ctx)
	case "--migration-rollback":
		lock, err := migrationAcquireLock()
		if err != nil {
			return true, err
		}
		m.lock = lock
		// Explicit operator rollback is supported after commit, unlike deferred rollback.
		m.Committed = false
		return true, m.Rollback(ctx)
	}
	return true, errors.New("unsupported migration runtime mode")
}
