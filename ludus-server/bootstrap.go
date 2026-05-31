package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"

	_ "modernc.org/sqlite"

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

// Privilege sets mirror what the legacy proxmox-install ansible (stage-3) granted.
var (
	privsPacker = []string{"VM.Config.Disk", "VM.Config.CPU", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.Config.CDROM", "VM.Config.Cloudinit", "VM.Config.HWType", "VM.PowerMgmt", "VM.Audit", "VM.Allocate", "VM.Monitor", "VM.Console", "Datastore.AllocateSpace", "Datastore.AllocateTemplate", "Datastore.Audit", "Sys.Audit", "Sys.Modify", "SDN.Use", "Sys.AccessNetwork"}
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
	// Capture before bootstrapLocalState: EnsureWireguard will create the key
	// if absent, so checking afterward can't tell imported-vs-generated apart.
	wgKeyExisted := fileExists("/etc/wireguard/server-private-key")
	if err := bootstrapLocalState(cfg); err != nil {
		return err
	}
	dbPath := filepath.Join(cfg.DataDirectory, "data.db")
	ranges := loadImportedRanges(dbPath)
	users := loadImportedUsers(dbPath)
	if len(ranges) > 0 || len(users) > 0 {
		logf("bootstrap: reconciling imported DB (%d ranges, %d users)", len(ranges), len(users))
		warns, err := reconcileImportedState(ctx, pc, cfg, ranges, users, "/etc/network/if-up.d/ludus-routes")
		if err != nil {
			return fmt.Errorf("reconcile: %w", err)
		}
		for _, w := range warns {
			logf("WARN: %s", w)
		}
	}
	if !wgKeyExisted && len(users) > 0 {
		logf("WARN: WireGuard server key was regenerated; existing client configs will break. Copy old /etc/wireguard/ or run `ludus user wg regen --all`")
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
		peers, err = pc.ClusterNodeIPs(ctx)
		if err != nil {
			return fmt.Errorf("preflight cluster node IPs: %w", err)
		}
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
	// SDN — must exist before ACLs reference /sdn/zones/<zone>
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
	// ACLs
	acls := []struct {
		path, role string
		groups     []string
	}{
		{"/pool/SHARED", "LudusUser", []string{"ludus_users", "ludus_admins"}},
		{"/pool/ADMIN", "LudusAdmin", []string{"ludus_admins"}},
		{"/sdn/zones/" + cfg.SDNZone, "LudusUser", []string{"ludus_users", "ludus_admins"}},
		{"/nodes", "LudusPacker", []string{"ludus_users", "ludus_admins"}},
	}
	for _, a := range acls {
		if err := pc.EnsureACL(ctx, a.path, a.role, a.groups, nil); err != nil {
			return fmt.Errorf("acl %s: %w", a.path, err)
		}
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
	// chown -R ludus:ludus /opt/ludus so the non-root ludus.service can read/write.
	if err := exec.Command("chown", "-R", "ludus:ludus", "/opt/ludus").Run(); err != nil {
		logf("WARN: chown /opt/ludus: %v (continuing)", err)
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

// queryPocketBase opens the PocketBase sqlite file read-only and runs q.
// Caller must Close() both returned values (db first via rows.Close(), then db.Close()).
func queryPocketBase(dbPath, q string) (*sql.DB, *sql.Rows, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, nil, err
	}
	rows, err := db.Query(q)
	if err != nil {
		db.Close()
		return nil, nil, err
	}
	return db, rows, nil
}

// loadImportedRanges reads range numbers from a pre-existing PocketBase DB.
// Returns nil on any error (missing file, schema mismatch) — bootstrap treats
// "no imported state" as the safe default.
func loadImportedRanges(dbPath string) []importedRange {
	db, rows, err := queryPocketBase(dbPath, "SELECT rangeNumber FROM ranges")
	if err != nil {
		return nil
	}
	defer db.Close()
	defer rows.Close()
	var out []importedRange
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err == nil {
			out = append(out, importedRange{Number: n})
		}
	}
	return out
}

// loadImportedUsers reads Proxmox userids (username@realm) from a pre-existing
// PocketBase DB, excluding the synthetic ROOT user.
func loadImportedUsers(dbPath string) []importedUser {
	db, rows, err := queryPocketBase(dbPath, "SELECT proxmoxUsername, proxmoxRealm FROM users WHERE userID != 'ROOT'")
	if err != nil {
		return nil
	}
	defer db.Close()
	defer rows.Close()
	var out []importedUser
	for rows.Next() {
		var name, realm string
		if err := rows.Scan(&name, &realm); err == nil {
			out = append(out, importedUser{ProxmoxUsername: name + "@" + realm})
		}
	}
	return out
}

// Compile-time assertion that *pveclient.Client satisfies PVEClient.
var _ PVEClient = (*pveclient.Client)(nil)
