package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	"ludusapi"
	"ludusapi/pveclient"

	"ludus-server/localgen"
)

// PVEClient is the subset of pveclient.Client used by bootstrap. Allows mocking.
type PVEClient interface {
	Version(context.Context) (pveclient.Version, error)
	VerifyTokenOnAll(context.Context) error
	ClusterNodeCount(context.Context) (int, error)
	ClusterNodeIPs(context.Context) ([]string, error)
	EnsureRole(context.Context, string, []string) error
	EnsureGroup(context.Context, string) error
	EnsurePool(context.Context, string) error
	EnsureACL(ctx context.Context, path, role string, groups, users []string) error
	EnsureSDNZone(ctx context.Context, name, kind string, peers []string) error
	EnsureVNet(ctx context.Context, zone, name string, tag int, vlanaware bool) error
	EnsureSubnet(ctx context.Context, vnet, cidr, gw string, snat bool) error
	ApplySDN(context.Context) error
}

const bootstrapMarker = "/opt/ludus/install/.bootstrap-complete"

// Privilege sets mirror current ansible/proxmox-install/stage-3.yml.
var (
	privsPacker = []string{"VM.Config.Disk", "VM.Config.CPU", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.Config.CDROM", "VM.Config.Cloudinit", "VM.Config.HWType", "VM.PowerMgmt", "VM.Audit", "VM.Allocate", "VM.Monitor", "VM.Console", "Datastore.AllocateSpace", "Datastore.AllocateTemplate", "Datastore.Audit", "Sys.Audit", "Sys.Modify", "SDN.Use"}
	privsUser   = append([]string{"Pool.Audit", "VM.Clone", "VM.Snapshot", "VM.Snapshot.Rollback"}, privsPacker...)
	privsAdmin  = append([]string{"Pool.Allocate", "User.Modify", "Realm.AllocateUser", "Permissions.Modify", "SDN.Allocate"}, privsUser...)
)

// bootstrap is the entry point called from main() when the marker is absent.
func bootstrap(ctx context.Context, cfg ludusapi.Configuration) error {
	logf("bootstrap: starting")
	pc, err := pveclient.New(cfg.PVEClientConfig())
	if err != nil {
		return fmt.Errorf("pveclient: %w", err)
	}
	defer pc.Close()
	if err := bootstrapProxmoxObjects(ctx, pc, cfg, "/opt/ludus"); err != nil {
		return err
	}
	if err := bootstrapLocalState(cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(bootstrapMarker), 0755); err != nil {
		return err
	}
	return os.WriteFile(bootstrapMarker, []byte("ok\n"), 0644)
}

func bootstrapProxmoxObjects(ctx context.Context, pc PVEClient, cfg ludusapi.Configuration, installDir string) error {
	// Preflight
	v, err := pc.Version(ctx)
	if err != nil {
		return fmt.Errorf("preflight version: %w", err)
	}
	if !v.AtLeast(8, 0) {
		return fmt.Errorf("Proxmox %s is too old; Ludus requires 8.0+", v.Version)
	}
	if err := pc.VerifyTokenOnAll(ctx); err != nil {
		return fmt.Errorf("preflight token: %w", err)
	}
	nodes, err := pc.ClusterNodeCount(ctx)
	if err != nil {
		return fmt.Errorf("preflight node count: %w", err)
	}
	zoneType := "simple"
	var peers []string
	if nodes > 1 {
		zoneType = "vxlan"
		peers, _ = pc.ClusterNodeIPs(ctx)
	}
	logf("bootstrap: PVE %s, %d node(s), SDN zone type=%s", v.Version, nodes, zoneType)

	// Roles, groups, pools
	for name, privs := range map[string][]string{"LudusPacker": privsPacker, "LudusUser": privsUser, "LudusAdmin": privsAdmin} {
		if err := pc.EnsureRole(ctx, name, privs); err != nil {
			return fmt.Errorf("role %s: %w", name, err)
		}
	}
	for _, g := range []string{"ludus_users", "ludus_admins"} {
		if err := pc.EnsureGroup(ctx, g); err != nil {
			return fmt.Errorf("group %s: %w", g, err)
		}
	}
	for _, p := range []string{"SHARED", "ADMIN"} {
		if err := pc.EnsurePool(ctx, p); err != nil {
			return fmt.Errorf("pool %s: %w", p, err)
		}
	}
	// ACLs
	acls := []struct {
		path, role string
		groups     []string
	}{
		{"/pool/SHARED", "LudusUser", []string{"ludus_users", "ludus_admins"}},
		{"/pool/ADMIN", "LudusAdmin", []string{"ludus_admins"}},
		{"/sdn/zones/" + cfg.SDNZone, "LudusUser", []string{"ludus_users", "ludus_admins"}},
	}
	for _, a := range acls {
		if err := pc.EnsureACL(ctx, a.path, a.role, a.groups, nil); err != nil {
			return fmt.Errorf("acl %s: %w", a.path, err)
		}
	}
	// SDN
	if err := pc.EnsureSDNZone(ctx, cfg.SDNZone, zoneType, peers); err != nil {
		return fmt.Errorf("sdn zone: %w", err)
	}
	if err := pc.EnsureVNet(ctx, cfg.SDNZone, cfg.LudusNATInterface, 0, true); err != nil {
		return fmt.Errorf("vnet %s: %w", cfg.LudusNATInterface, err)
	}
	if err := pc.EnsureSubnet(ctx, cfg.LudusNATInterface, "192.0.2.0/24", cfg.LudusNATGateway, true); err != nil {
		return fmt.Errorf("subnet: %w", err)
	}
	if err := pc.ApplySDN(ctx); err != nil {
		return fmt.Errorf("apply sdn: %w", err)
	}
	_ = installDir // reserved for future per-install state
	return nil
}

func bootstrapLocalState(cfg ludusapi.Configuration) error {
	// TLS
	hostname, _ := os.Hostname()
	ips := localIPs()
	if err := localgen.EnsureTLSCert(cfg.TLSCertFile, cfg.TLSKeyFile, hostname, ips); err != nil {
		return fmt.Errorf("tls: %w", err)
	}
	// WireGuard
	if err := localgen.EnsureWireguard(localgen.WGConfig{
		Dir: "/etc/wireguard", ListenPort: cfg.WireguardPort, ServerIP: "198.51.100.1/24",
	}); err != nil {
		return fmt.Errorf("wireguard: %w", err)
	}
	// dnsmasq
	if err := localgen.WriteDnsmasq("/etc/dnsmasq.d/ludus.conf", localgen.DnsmasqConfig{
		BindIP: cfg.LudusNATIP, Gateway: cfg.LudusNATGateway,
		PoolLow: "192.0.2.50", PoolHigh: "192.0.2.100", IfName: "eth1",
	}); err != nil {
		return fmt.Errorf("dnsmasq: %w", err)
	}
	// Dirs
	for _, d := range []string{"/opt/ludus/users", "/opt/ludus/ci", "/opt/ludus/previous-versions", "/opt/ludus/tls"} {
		if err := os.MkdirAll(d, 0750); err != nil {
			return err
		}
	}
	// Enable services
	for _, svc := range []string{"wg-quick@wg0", "dnsmasq"} {
		if err := exec.Command("systemctl", "enable", "--now", svc).Run(); err != nil {
			logf("WARN: systemctl enable %s: %v (continuing)", svc, err)
		}
	}
	return nil
}

func localIPs() []net.IP {
	var out []net.IP
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() && ipn.IP.To4() != nil {
			out = append(out, ipn.IP)
		}
	}
	return out
}

func logf(format string, args ...any) {
	f, err := os.OpenFile("/opt/ludus/install/install.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(f, format+"\n", args...)
		f.Close()
	}
	fmt.Printf(format+"\n", args...)
}

// Compile-time assertion that *pveclient.Client satisfies PVEClient.
var _ PVEClient = (*pveclient.Client)(nil)
