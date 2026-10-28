//go:build !linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
)

func migrationAcquireLock() (*os.File, error) {
	return nil, errors.New("host migration requires Linux")
}
func migrationDetachProcess(cmd *exec.Cmd) error {
	return errors.New("migration helper processes require Linux")
}
func migrationCheckRootOwned(path string) error {
	return errors.New("migration runtime requires Linux")
}
func migrationProbeAddress(ctx context.Context, interfaceName, address string) error {
	return errors.New("raw ARP probing requires Linux")
}
func migrationAnnounceAddress(ctx context.Context, interfaceName, address string) error {
	return errors.New("raw ARP announcements require Linux")
}
