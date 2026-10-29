package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Rules are restored at boot by netfilter-persistent from these files.
const wireGuardRulesV4 = "/etc/iptables/rules.v4"
const wireGuardRulesV6 = "/etc/iptables/rules.v6"
const wireGuardSysctlFile = "/etc/sysctl.d/99-ludus-wireguard-forward.conf"
const wireGuardComment = "ludus-wireguard"

type wireGuardForwardConfig struct {
	Address netip.Addr
	Port    int
}

func wireGuardForwardPlan(opts *Options) (wireGuardForwardConfig, error) {
	prefix, err := netip.ParsePrefix(opts.IP)
	if err != nil || prefix.Addr().IsUnspecified() || prefix.Addr().IsLoopback() || prefix.Addr().IsMulticast() || prefix.Addr().Is4In6() || prefix.Addr().IsLinkLocalUnicast() {
		return wireGuardForwardConfig{}, errors.New("WireGuard forwarding requires a static, routable management IP/CIDR")
	}
	if opts.WireGuardPort < 1 || opts.WireGuardPort > 65535 {
		return wireGuardForwardConfig{}, errors.New("invalid WireGuard forwarding port")
	}
	return wireGuardForwardConfig{Address: prefix.Addr(), Port: opts.WireGuardPort}, nil
}

// firewall returns the address family's firewall binary, persistent rules
// file, and forwarding sysctl.
func (c wireGuardForwardConfig) firewall() (binary, rulesFile, sysctl string) {
	if c.Address.Is6() {
		return "ip6tables", wireGuardRulesV6, "net.ipv6.conf.all.forwarding"
	}
	return "iptables", wireGuardRulesV4, "net.ipv4.ip_forward"
}

// Never flush a chain or replace the host's firewall. DNAT is restricted to
// locally addressed UDP, not routed traffic.
func (c wireGuardForwardConfig) rules() []migrationFirewallRule {
	address := c.Address.String()
	port := strconv.Itoa(c.Port)
	rules := []migrationFirewallRule{}
	for _, chain := range []string{"PREROUTING", "OUTPUT"} {
		rules = append(rules, migrationFirewallRule{"nat", chain, []string{"-m", "addrtype", "--dst-type", "LOCAL", "-p", "udp", "--dport", port, "-j", "DNAT", "--to-destination", net.JoinHostPort(address, port)}})
	}
	// SNAT forces replies through the host even when a client and the guest
	// share a subnet. Direct guest traffic is not NATed or granted access.
	rules = append(rules,
		migrationFirewallRule{"nat", "POSTROUTING", []string{"-d", address, "-p", "udp", "--dport", port, "-m", "conntrack", "--ctstate", "DNAT", "--ctorigdstport", port, "-j", "MASQUERADE"}},
		migrationFirewallRule{"filter", "FORWARD", []string{"-d", address, "-p", "udp", "--dport", port, "-m", "conntrack", "--ctstate", "DNAT", "--ctdir", "ORIGINAL", "--ctorigdstport", port, "-j", "ACCEPT"}},
		migrationFirewallRule{"filter", "FORWARD", []string{"-s", address, "-p", "udp", "--sport", port, "-m", "conntrack", "--ctstate", "DNAT", "--ctdir", "REPLY", "--ctorigdstport", port, "-j", "ACCEPT"}},
	)
	for i := range rules {
		rules[i].Args = append([]string{"-m", "comment", "--comment", wireGuardComment}, rules[i].Args...)
	}
	return rules
}

// mergeWireGuardRules replaces every Ludus WireGuard rule in an iptables-save
// file with rules, placed first in their chains like the live -I insertion.
// All other tables, chains, and rules are preserved.
func mergeWireGuardRules(existing []byte, rules []migrationFirewallRule) []byte {
	pending := map[string][]string{}
	var tables []string
	for _, rule := range rules {
		if _, ok := pending[rule.Table]; !ok {
			tables = append(tables, rule.Table)
		}
		pending[rule.Table] = append(pending[rule.Table], "-A "+rule.Chain+" "+strings.Join(rule.Args, " "))
	}
	var lines, out []string
	if text := strings.TrimRight(string(existing), "\n"); text != "" {
		lines = strings.Split(text, "\n")
	}
	table := ""
	for _, line := range lines {
		if strings.Contains(line+" ", " --comment "+wireGuardComment+" ") {
			continue
		}
		if strings.HasPrefix(line, "*") {
			table = line[1:]
		} else if table != "" && (strings.HasPrefix(line, "-") || line == "COMMIT") {
			out = append(out, pending[table]...)
			delete(pending, table)
		}
		out = append(out, line)
	}
	for _, table := range tables {
		if added, ok := pending[table]; ok {
			out = append(append(append(out, "*"+table), added...), "COMMIT")
		}
	}
	return []byte(strings.Join(out, "\n") + "\n")
}

// installWireGuardForward applies the forward and persists it with
// iptables-persistent. Rerunning replaces previously persisted Ludus rules.
func installWireGuardForward(ctx context.Context, opts *Options) error {
	if opts.NoWireGuardForward {
		return nil
	}
	c, err := wireGuardForwardPlan(opts)
	if err != nil {
		return err
	}
	binary, rulesFile, sysctl := c.firewall()
	if err := commandRun(ctx, "sysctl", "-q", "-w", sysctl+"=1"); err != nil {
		return err
	}
	if err := atomicWriteFile(wireGuardSysctlFile, []byte(sysctl+"=1\n"), 0644); err != nil {
		return err
	}
	rules := c.rules()
	for _, rule := range rules {
		if err := migrationChangeRule(ctx, binary, rule, true); err != nil {
			return err
		}
	}
	existing, err := os.ReadFile(rulesFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	merged := mergeWireGuardRules(existing, rules)
	// netfilter-persistent loads nothing at boot from a file that fails this test.
	if err := commandInput(ctx, merged, binary+"-restore", "--test"); err != nil {
		return fmt.Errorf("validate merged %s: %w", rulesFile, err)
	}
	return atomicWriteFile(rulesFile, merged, 0640)
}
