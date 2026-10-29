//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func migrationAcquireLock() (*os.File, error) {
	file, err := os.OpenFile("/run/lock/ludus-lxc-migration.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("another host migration is running: %w", err)
	}
	return file, nil
}
func migrationDetachProcess(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}
func migrationCheckRootOwned(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("migration path must be owned by root and not a symlink: %s", path)
	}
	return nil
}
func migrationOpenARP(interfaceName string) (int, *net.Interface, error) {
	if err := migrationValidateInterface(interfaceName); err != nil {
		return -1, nil, err
	}
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return -1, nil, err
	}
	protocol := binary.NativeEndian.Uint16([]byte{0x08, 0x06})
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(protocol))
	if err != nil {
		return -1, nil, err
	}
	if err = syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: protocol, Ifindex: iface.Index}); err != nil {
		syscall.Close(fd)
		return -1, nil, err
	}
	if err = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &syscall.Timeval{Usec: 100000}); err != nil {
		syscall.Close(fd)
		return -1, nil, err
	}
	return fd, iface, nil
}
func migrationSendARP(fd int, iface *net.Interface, frame []byte) error {
	destination := &syscall.SockaddrLinklayer{Ifindex: iface.Index, Halen: 6, Protocol: binary.NativeEndian.Uint16([]byte{0x08, 0x06})}
	for i := range 6 {
		destination.Addr[i] = 255
	}
	return syscall.Sendto(fd, frame, 0, destination)
}
func migrationProbeAddress(ctx context.Context, interfaceName, address string) error {
	target, err := netip.ParseAddr(address)
	if err != nil || !target.Is4() {
		return errors.New("ARP probe requires an IPv4 address")
	}
	fd, iface, err := migrationOpenARP(interfaceName)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	frame, err := migrationARPFrame(iface.HardwareAddr, netip.IPv4Unspecified(), target, 1)
	if err != nil {
		return err
	}
	buffer := make([]byte, 2048)
	for range 3 {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = migrationSendARP(fd, iface, frame); err != nil {
			return err
		}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if err = ctx.Err(); err != nil {
				return err
			}
			count, _, err := syscall.Recvfrom(fd, buffer, 0)
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EINTR) {
				continue
			}
			if err != nil {
				return err
			}
			if migrationARPClaims(buffer[:count], target, iface.HardwareAddr) {
				return errors.New("reserved NAT gateway is already in use")
			}
		}
	}
	return nil
}
func migrationAnnounceAddress(ctx context.Context, interfaceName, address string) error {
	target, err := netip.ParseAddr(address)
	if err != nil || !target.Is4() {
		return errors.New("ARP announcement requires an IPv4 address")
	}
	fd, iface, err := migrationOpenARP(interfaceName)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	for _, operation := range []uint16{1, 2} {
		if err = ctx.Err(); err != nil {
			return err
		}
		frame, err := migrationARPFrame(iface.HardwareAddr, target, target, operation)
		if err != nil {
			return err
		}
		if err = migrationSendARP(fd, iface, frame); err != nil {
			return err
		}
	}
	return nil
}
