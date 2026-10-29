package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type migrationAPIMapping struct {
	Source   int `json:"source"`
	Listener int `json:"listener"`
}

func migrationAPIRules(mappings []migrationAPIMapping, owner string) []migrationFirewallRule {
	var rules []migrationFirewallRule
	for _, mapping := range mappings {
		source, listener := strconv.Itoa(mapping.Source), strconv.Itoa(mapping.Listener)
		for _, chain := range []string{"PREROUTING", "OUTPUT"} {
			rules = append(rules, migrationFirewallRule{"nat", chain, []string{"-m", "addrtype", "--dst-type", "LOCAL", "-p", "tcp", "--dport", source, "-j", "REDIRECT", "--to-ports", listener}})
		}
		rules = append(rules, migrationFirewallRule{"filter", "INPUT", []string{"-p", "tcp", "--dport", listener, "-j", "ACCEPT"}})
	}
	for i := range rules {
		rules[i].Args = append([]string{"-m", "comment", "--comment", "ludus-migration-bridge:" + owner}, rules[i].Args...)
	}
	return rules
}
func (m *Migration) startAPIBridge(ctx context.Context) error {
	if m.BridgePID != 0 {
		return errors.New("API bridge already started")
	}
	log, err := os.OpenFile(filepath.Join(m.Directory, "api-bridge.log"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd := exec.Command(filepath.Join(m.Directory, "ludus-installer"), "--migration-api-bridge", m.Directory)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = migrationDetachProcess(cmd); err != nil {
		return err
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	m.bridgeProcess = cmd.Process
	m.BridgePID = cmd.Process.Pid
	if err = m.save(); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return err
	}
	ended := make(chan error, 1)
	go func() { ended <- cmd.Wait() }()
	readyPath := filepath.Join(m.Directory, "api-bridge.json")
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, readErr := os.ReadFile(readyPath)
		if readErr == nil {
			var mappings []migrationAPIMapping
			if err = json.Unmarshal(data, &mappings); err != nil {
				return err
			}
			expected := 1
			if m.Network.ExposeAdminPort {
				expected++
			}
			if len(mappings) != expected {
				return errors.New("API bridge has an unexpected listener count")
			}
			m.BridgeRules = migrationAPIRules(mappings, filepath.Base(m.Directory))
			if err = m.save(); err != nil {
				return err
			}
			for _, rule := range m.BridgeRules {
				if err = migrationChangeRule(ctx, "iptables", rule, true); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		select {
		case err := <-ended:
			return fmt.Errorf("API bridge failed to start: %w", err)
		case <-deadline.C:
			return errors.New("API bridge did not become ready")
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func migrationRelay(ctx context.Context, downstream net.Conn, target string) {
	defer downstream.Close()
	dialer := net.Dialer{Timeout: time.Second}
	var upstream net.Conn
	for {
		connection, err := dialer.DialContext(ctx, "tcp", target)
		if err == nil {
			upstream = connection
			break
		}
		if migrationPause(ctx, 100*time.Millisecond) != nil {
			return
		}
	}
	defer upstream.Close()
	finished := make(chan struct{}, 2)
	pipe := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		if tcp, ok := destination.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		finished <- struct{}{}
	}
	go pipe(upstream, downstream)
	go pipe(downstream, upstream)
	select {
	case <-ctx.Done():
		return
	case <-finished:
	}
	select {
	case <-ctx.Done():
		return
	case <-finished:
	}
}
func runMigrationAPIBridge(ctx context.Context, m *Migration) error {
	ip := strings.Split(m.Network.IP, "/")[0]
	ports := []int{m.Network.Port}
	if m.Network.ExposeAdminPort {
		ports = append(ports, m.Network.AdminPort)
	}
	var listeners []net.Listener
	defer func() {
		for _, listener := range listeners {
			listener.Close()
		}
	}()
	var mappings []migrationAPIMapping
	for _, port := range ports {
		listener, err := net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
		mappings = append(mappings, migrationAPIMapping{Source: port, Listener: listener.Addr().(*net.TCPAddr).Port})
	}
	data, err := json.Marshal(mappings)
	if err != nil {
		return err
	}
	if err = atomicWriteFile(filepath.Join(m.Directory, "api-bridge.json"), data, 0600); err != nil {
		return err
	}
	// Shutdown stops accepting new clients, but existing TLS streams remain end-to-end
	// and drain without a deadline. Rollback can kill this isolated helper explicitly.
	var accepts, connections sync.WaitGroup
	for i, listener := range listeners {
		accepts.Add(1)
		go func(listener net.Listener, port int) {
			defer accepts.Done()
			for {
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				connections.Add(1)
				go func() {
					defer connections.Done()
					migrationRelay(context.Background(), connection, net.JoinHostPort(ip, strconv.Itoa(port)))
				}()
			}
		}(listener, ports[i])
	}
	<-ctx.Done()
	for _, listener := range listeners {
		listener.Close()
	}
	accepts.Wait()
	connections.Wait()
	return nil
}
func (m *Migration) stopAPIBridge(ctx context.Context, drain bool) error {
	var failures []error
	for _, rule := range m.BridgeRules {
		if err := migrationChangeRule(ctx, "iptables", rule, false); err != nil {
			failures = append(failures, err)
		}
	}
	if m.BridgePID == 0 {
		return errors.Join(failures...)
	}
	process := m.bridgeProcess
	if process == nil {
		// A persisted PID alone cannot establish ownership after process reuse.
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", m.BridgePID))
		if errors.Is(err, os.ErrNotExist) {
			return errors.Join(failures...)
		}
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		words := strings.Split(string(data), "\x00")
		if len(words) < 3 || words[1] != "--migration-api-bridge" || words[2] != m.Directory {
			return errors.Join(append(failures, errors.New("saved API bridge PID is not migration-owned"))...)
		}
		process, err = os.FindProcess(m.BridgePID)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
	}
	if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		failures = append(failures, err)
	}
	if !drain {
		// Force-close any waiting/long-lived clients before reactivating the old API.
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			failures = append(failures, err)
		}
		m.BridgePID = 0
		m.bridgeProcess = nil
	}
	return errors.Join(failures...)
}
func (m *Migration) DrainAPIBridge(ctx context.Context) error { return m.stopAPIBridge(ctx, true) }
