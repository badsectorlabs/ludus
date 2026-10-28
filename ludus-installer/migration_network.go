package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

type migrationFirewallRule struct {
	Table string   `json:"table"`
	Chain string   `json:"chain"`
	Args  []string `json:"args"`
}

func migrationForwardRules(c migrationNetworkPlan, mode, owner string) []migrationFirewallRule {
	ip := strings.Split(c.IP, "/")[0]
	rules := []migrationFirewallRule{
		{"raw", "PREROUTING", []string{"-i", c.Bridge, "-s", "127.0.0.0/8", "-j", "DROP"}},
		{"nat", "POSTROUTING", []string{"-s", ip + "/32", "-o", c.Uplink, "-j", "MASQUERADE"}},
		{"filter", "FORWARD", []string{"-s", ip, "-j", "ACCEPT"}},
		{"filter", "FORWARD", []string{"-d", ip, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}},
		{"filter", "INPUT", []string{"-s", ip, "-p", "tcp", "--dport", "8006", "-j", "ACCEPT"}},
	}
	if mode != "stage" {
		rules = append(rules,
			migrationFirewallRule{"nat", "POSTROUTING", []string{"-s", "192.0.2.0/24", "-o", c.Uplink, "-j", "MASQUERADE"}},
			migrationFirewallRule{"filter", "FORWARD", []string{"-i", "ludusnat", "-o", c.Uplink, "-j", "ACCEPT"}},
			migrationFirewallRule{"filter", "FORWARD", []string{"-i", c.Uplink, "-o", "ludusnat", "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"}})
		endpoints := []struct {
			protocol string
			port     int
		}{}
		if !c.NoWireGuardForward {
			endpoints = append(endpoints, struct {
				protocol string
				port     int
			}{"udp", c.WireGuardPort})
		}
		if mode != "wireguard" {
			endpoints = append(endpoints, struct {
				protocol string
				port     int
			}{"tcp", c.Port})
			if c.ExposeAdminPort {
				endpoints = append(endpoints, struct {
					protocol string
					port     int
				}{"tcp", c.AdminPort})
			}
		}
		for _, endpoint := range endpoints {
			port := strconv.Itoa(endpoint.port)
			for _, chain := range []string{"PREROUTING", "OUTPUT"} {
				rules = append(rules, migrationFirewallRule{"nat", chain, []string{"-m", "addrtype", "--dst-type", "LOCAL", "-p", endpoint.protocol, "--dport", port, "-j", "DNAT", "--to-destination", ip + ":" + port}})
			}
			rules = append(rules, migrationFirewallRule{"nat", "POSTROUTING", []string{"-d", ip, "-p", endpoint.protocol, "--dport", port, "-j", "MASQUERADE"}}, migrationFirewallRule{"filter", "FORWARD", []string{"-d", ip, "-p", endpoint.protocol, "--dport", port, "-j", "ACCEPT"}})
		}
	}
	for i := range rules {
		rules[i].Args = append([]string{"-m", "comment", "--comment", "ludus-migration:" + owner}, rules[i].Args...)
	}
	return rules
}
func migrationChangeRule(ctx context.Context, binary string, rule migrationFirewallRule, add bool) error {
	_, err := commandOutput(ctx, binary, append([]string{"-w", "-t", rule.Table, "-C", rule.Chain}, rule.Args...)...)
	if err != nil && migrationExitCode(err) != 1 {
		return err
	}
	if exists := err == nil; exists == add {
		return nil
	}
	args := []string{"-w", "-t", rule.Table}
	if add {
		args = append(args, "-I", rule.Chain, "1")
	} else {
		args = append(args, "-D", rule.Chain)
	}
	return commandRun(ctx, binary, append(args, rule.Args...)...)
}
func (m *Migration) forward(ctx context.Context, mode string) error {
	rules := migrationForwardRules(m.Network, mode, filepath.Base(m.Directory))
	for _, rule := range rules {
		owned := false
		for _, existing := range m.OwnedRules {
			if reflect.DeepEqual(existing, rule) {
				owned = true
				break
			}
		}
		if !owned {
			m.OwnedRules = append(m.OwnedRules, rule)
			if err := m.save(); err != nil {
				return err
			}
		}
		if err := migrationChangeRule(ctx, "iptables", rule, true); err != nil {
			return err
		}
	}
	if err := commandRun(ctx, "sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	if mode != "stage" {
		return commandRun(ctx, "sysctl", "-w", "net.ipv4.conf."+m.Network.Bridge+".route_localnet=1")
	}
	return nil
}
func (m *Migration) removeOwnedRules(ctx context.Context) error {
	var failures []error
	for i := len(m.OwnedRules) - 1; i >= 0; i-- {
		if err := migrationChangeRule(ctx, "iptables", m.OwnedRules[i], false); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func migrationARPFrame(mac net.HardwareAddr, sender, target netip.Addr, operation uint16) ([]byte, error) {
	if len(mac) != 6 || !sender.Is4() || !target.Is4() || (operation != 1 && operation != 2) {
		return nil, errors.New("invalid ARP frame parameters")
	}
	frame := make([]byte, 42)
	for i := range 6 {
		frame[i] = 255
	}
	copy(frame[6:12], mac)
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)
	binary.BigEndian.PutUint16(frame[14:16], 1)
	binary.BigEndian.PutUint16(frame[16:18], 0x0800)
	frame[18] = 6
	frame[19] = 4
	binary.BigEndian.PutUint16(frame[20:22], operation)
	copy(frame[22:28], mac)
	source := sender.As4()
	destination := target.As4()
	copy(frame[28:32], source[:])
	if operation == 2 {
		for i := 32; i < 38; i++ {
			frame[i] = 255
		}
	}
	copy(frame[38:42], destination[:])
	return frame, nil
}
func migrationARPClaims(frame []byte, target netip.Addr, own net.HardwareAddr) bool {
	if len(frame) < 42 || !target.Is4() || binary.BigEndian.Uint16(frame[12:14]) != 0x0806 || binary.BigEndian.Uint16(frame[14:16]) != 1 || binary.BigEndian.Uint16(frame[16:18]) != 0x0800 || frame[18] != 6 || frame[19] != 4 {
		return false
	}
	operation := binary.BigEndian.Uint16(frame[20:22])
	if operation != 1 && operation != 2 {
		return false
	}
	a := target.As4()
	for i := range 4 {
		if frame[28+i] != a[i] {
			return false
		}
	}
	// Ignore only our own raw socket transmission, never stale neighbor-cache state.
	if len(own) != 6 {
		return true
	}
	for i := range 6 {
		if frame[22+i] != own[i] {
			return true
		}
	}
	return false
}
func migrationValidateInterface(name string) error {
	if name == "" || len(name) > 15 || strings.ContainsAny(name, "/\\\x00 \t\r\n") {
		return fmt.Errorf("invalid network interface %q", name)
	}
	return nil
}
