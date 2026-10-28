package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMigrationClusterRequiresOnlineNodesAndQuorum(t *testing.T) {
	nodes := []migrationClusterStatus{{Type: "node", Name: "a", IP: "192.0.2.10", Online: 1}, {Type: "node", Name: "b", IP: "192.0.2.11", Online: 1}, {Type: "cluster"}}
	if _, err := validateMigrationCluster(nodes); err == nil || !strings.Contains(err.Error(), "quorum") {
		t.Fatalf("missing quorum accepted: %v", err)
	}
	nodes[2].Quorate = 1
	nodes[1].Online = 0
	if _, err := validateMigrationCluster(nodes); err == nil || !strings.Contains(err.Error(), "online") {
		t.Fatalf("offline peer accepted: %v", err)
	}
	nodes[1].Online = 1
	if result, err := validateMigrationCluster(nodes); err != nil || len(result) != 2 {
		t.Fatalf("healthy cluster: %v %v", result, err)
	}
}
func TestMigrationManagementNetworkAvoidsRoutesAndAddresses(t *testing.T) {
	local := migrationNodeSnapshot{Routes: []migrationRoute{{Destination: "172.31.255.0/24"}}, Addresses: []migrationLink{{Name: "vmbr0", Addresses: []migrationAddress{{Local: "172.31.254.1", PrefixLen: 30}}}}}
	bridge, ip, gateway, automatic, err := migrationManagementNetwork("", "", "", "vmbr1000", local)
	if err != nil || bridge != "ludusmg" || ip != "172.31.253.2/30" || gateway != "172.31.253.1" || !automatic {
		t.Fatalf("unsafe allocation: %s %s %s %t %v", bridge, ip, gateway, automatic, err)
	}
	local.Routes = []migrationRoute{{Destination: "172.31.0.0/16"}}
	if _, _, _, _, err = migrationManagementNetwork("", "", "", "vmbr1000", local); err == nil {
		t.Fatal("allocated overlapping subnet")
	}
	for _, input := range [][3]string{{"vmbr0", "dhcp", ""}, {"vmbr0", "192.0.2.2/24", "192.0.2.1"}, {"vmbr0", "198.51.100.2/24", "198.51.100.1"}, {"vmbr1000", "203.0.113.2/24", "203.0.113.1"}, {"vmbr0", "203.0.113.2/24", "203.0.114.1"}} {
		if _, _, _, _, err = migrationManagementNetwork(input[0], input[1], input[2], "vmbr1000", migrationNodeSnapshot{}); err == nil {
			t.Errorf("accepted unsafe management input %v", input)
		}
	}
}
func TestMigrationRemovesOnlyLegacyGatewayAndSelectedRange(t *testing.T) {
	text := "auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.5/24\niface ludusnat\n\taddress 192.0.2.254\n\tnetmask 255.255.255.0\n\tpost-up ip route add 10.7.0.0/16 via 192.0.2.107 dev ludusnat\n\tpost-up ip route add 10.70.0.0/16 via 192.0.2.170 dev ludusnat\n"
	result := migrationRemoveLegacyHooks(text, "ludusnat", []migrationRange{{Number: 7}})
	for _, retired := range []string{"address 192.0.2.254", "10.7.0.0/16", "netmask 255.255.255.0"} {
		if strings.Contains(result, retired) {
			t.Errorf("retired hook remains: %s", retired)
		}
	}
	for _, preserved := range []string{"address 203.0.113.5/24", "10.70.0.0/16 via 192.0.2.170"} {
		if !strings.Contains(result, preserved) {
			t.Errorf("unrelated configuration lost: %s", preserved)
		}
	}
}
func TestMigrationRetiresManagedRouteBlocksOnly(t *testing.T) {
	manual := "#!/bin/sh\nip route replace 10.7.9.0/24 via 203.0.113.9\n"
	block := func(number int) string {
		return fmt.Sprintf("# LUDUS MANAGED BLOCK FOR RANGE %d BEGIN\nif [ \"$IFACE\" = \"r%d\" ]; then\n\tip route replace 10.%d.0.0/16 via 192.0.2.%d dev ludusnat\nfi\n# LUDUS MANAGED BLOCK FOR RANGE %d END\n", number, number, number, 100+number, number)
	}
	original := manual + block(7) + "# administrator notes\n" + block(70)
	if got := migrationRemoveRouteBlocks(original, []migrationRange{{Number: 7}}); got != manual+"# administrator notes\n"+block(70) {
		t.Fatalf("unrelated route hook changed: %q", got)
	}
}
func TestMigrationPreservesNICFieldsAndNativeVLAN(t *testing.T) {
	original := "virtio=00:11:22:33:44:55,bridge=vmbr1000,firewall=1,tag=1,rate=100,queues=4,mtu=1450,link_down=1"
	migrated, relevant, err := migrationRewriteNIC(original, "vmbr1000", []migrationRange{{Number: 7}})
	expected := "virtio=00:11:22:33:44:55,bridge=ludusnat,firewall=1,rate=100,queues=4,mtu=1450,link_down=1"
	if err != nil || !relevant || migrated != expected {
		t.Fatalf("guest NIC changed incorrectly: %s %t %v", migrated, relevant, err)
	}
	for _, tag := range []string{"tag=2", "trunks=2;3"} {
		if _, _, err = migrationRewriteNIC("virtio=00:11:22:33:44:55,bridge=ludusnat,"+tag, "ludusnat", nil); err == nil {
			t.Fatalf("unsafe NAT VLAN accepted: %s", tag)
		}
	}
	internal := "virtio=00:11:22:33:44:55,bridge=vmbr1007,tag=7,trunks=8;9,firewall=1"
	migrated, _, err = migrationRewriteNIC(internal, "vmbr1000", []migrationRange{{Number: 7}})
	if err != nil || migrated != strings.Replace(internal, "vmbr1007", "r7", 1) {
		t.Fatalf("internal VLAN fields lost: %s %v", migrated, err)
	}
}
func TestMigrationTapFollowsFirewallUplink(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"tap901i0", "fwbr901i0", "fwpr901p0", "r7", "r8"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	tap := filepath.Join(root, "tap901i0/master")
	uplink := filepath.Join(root, "fwpr901p0/master")
	if err := os.Symlink(filepath.Join(root, "fwbr901i0"), tap); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "r8"), uplink); err != nil {
		t.Fatal(err)
	}
	value := "virtio=00:11:22:33:44:55,bridge=r7,firewall=1"
	if err := migrationCheckTapAttachment(root, 901, "net0", value, filepath.EvalSymlinks); err == nil || !strings.Contains(err.Error(), "expected r7") {
		t.Fatalf("wrong firewall uplink accepted: %v", err)
	}
	if err := os.Remove(uplink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "r7"), uplink); err != nil {
		t.Fatal(err)
	}
	if err := migrationCheckTapAttachment(root, 901, "net0", value, filepath.EvalSymlinks); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(tap); err != nil {
		t.Fatal(err)
	}
	if err := migrationCheckTapAttachment(root, 901, "net0", value, filepath.EvalSymlinks); err == nil || !strings.Contains(err.Error(), "detached") {
		t.Fatalf("detached tap accepted: %v", err)
	}
}
func TestMigrationARPClaimsRequireLiveMatchingFrame(t *testing.T) {
	mac, _ := net.ParseMAC("00:11:22:33:44:55")
	own, _ := net.ParseMAC("00:11:22:33:44:66")
	target := netip.MustParseAddr("192.0.2.49")
	for _, operation := range []uint16{1, 2} {
		frame, err := migrationARPFrame(mac, target, target, operation)
		if err != nil {
			t.Fatal(err)
		}
		if !migrationARPClaims(frame, target, own) {
			t.Fatal("active address owner was ignored")
		}
		if migrationARPClaims(frame, target, mac) {
			t.Fatal("own ARP transmission was treated as a conflict")
		}
		if migrationARPClaims(frame, netip.MustParseAddr("192.0.2.48"), own) {
			t.Fatal("other address treated as a conflict")
		}
		if migrationARPClaims(frame[:20], target, own) {
			t.Fatal("truncated ARP treated as a conflict")
		}
	}
	probe, err := migrationARPFrame(own, netip.IPv4Unspecified(), target, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(probe[28:32], []byte{0, 0, 0, 0}) {
		t.Fatal("probe requires an already assigned NAT address")
	}
}
func TestMigrationFinalArchiveUsesCustomDatabase(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(root, "custom database")
	wireguard := filepath.Join(root, "wireguard")
	for _, dir := range []string{database, wireguard} {
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(database, "ludus.db"), []byte("latest durable state"), 0640); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(wireguard, "wg0.conf"), []byte("private-key-preserved"), 0600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "final.tar.gz")
	if err = migrationWriteFinalState(archive, database, database, wireguard, filepath.Join(root, "absent-leases")); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "extracted")
	if err = os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	if err = migrationExtractFinalState(archive, destination); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"opt/ludus/db/ludus.db": "latest durable state", "etc/wireguard/wg0.conf": "private-key-preserved"} {
		got, err := os.ReadFile(filepath.Join(destination, path))
		if err != nil || string(got) != want {
			t.Fatalf("durable state lost: %s %q %v", path, got, err)
		}
	}
	changed := filepath.Join(root, "changed.tar.gz")
	if err = migrationWriteFinalState(changed, database, filepath.Join(root, "other"), wireguard, ""); err == nil {
		t.Fatal("changed source database accepted")
	}
	if _, err = os.Stat(changed); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid source created archive")
	}
}
func TestMigrationFinalArchiveRejectsEscapesAndLinks(t *testing.T) {
	for _, header := range []*tar.Header{{Name: "../../escape", Typeflag: tar.TypeReg}, {Name: "/etc/shadow", Typeflag: tar.TypeReg}, {Name: "opt/ludus/db/link", Typeflag: tar.TypeSymlink, Linkname: "/etc"}, {Name: "opt/ludus/db/link", Typeflag: tar.TypeLink, Linkname: "etc/wireguard/wg0.conf"}} {
		t.Run(header.Name+fmt.Sprint(header.Typeflag), func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "input.tar.gz")
			file, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			compressed := gzip.NewWriter(file)
			writer := tar.NewWriter(compressed)
			if err = writer.WriteHeader(header); err != nil {
				t.Fatal(err)
			}
			if err = errors.Join(writer.Close(), compressed.Close(), file.Close()); err != nil {
				t.Fatal(err)
			}
			if err = migrationExtractFinalState(archive, filepath.Join(root, "out")); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}
func TestMigrationFingerprintDetectsSameLengthChange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yml")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := migrationFingerprintPaths([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("change"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := migrationFingerprintPaths([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("same-size static state change was not detected")
	}
}
func TestMigrationFingerprintFollowsExportedSymlinks(t *testing.T) {
	for _, kind := range []string{"directory root", "file root", "nested file", "nested directory"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			source := filepath.Join(base, "source")
			if err := os.MkdirAll(filepath.Join(source, "data"), 0700); err != nil {
				t.Fatal(err)
			}
			referent := filepath.Join(source, "data", "state")
			if err := os.WriteFile(referent, []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			root := source
			switch kind {
			case "directory root":
				root = filepath.Join(base, "resources")
				if err := os.Symlink(source, root); err != nil {
					t.Fatal(err)
				}
			case "file root":
				root = filepath.Join(base, "config.yml")
				if err := os.Symlink(referent, root); err != nil {
					t.Fatal(err)
				}
			case "nested file", "nested directory":
				target := referent
				if kind == "nested directory" {
					target = filepath.Dir(referent)
				}
				if err := os.Symlink(target, filepath.Join(source, "alias")); err != nil {
					t.Fatal(err)
				}
			}
			before, err := migrationFingerprintPaths([]string{root})
			if err != nil {
				t.Fatal(err)
			}
			staged := filepath.Join(base, "staged")
			if err := migrationCopyTree(root, staged); err != nil {
				t.Fatal(err)
			}
			unchanged, err := migrationFingerprintPaths([]string{root})
			if err != nil || unchanged != before {
				t.Fatalf("export changed source fingerprint: %v", err)
			}
			if err := os.WriteFile(referent, []byte("change"), 0600); err != nil {
				t.Fatal(err)
			}
			after, err := migrationFingerprintPaths([]string{root})
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Fatal("post-export referent change was not detected")
			}
			stagedFile := filepath.Join(staged, "data", "state")
			if kind == "file root" {
				stagedFile = staged
			} else if kind == "nested file" {
				stagedFile = filepath.Join(staged, "alias")
			} else if kind == "nested directory" {
				stagedFile = filepath.Join(staged, "alias", "state")
			}
			data, err := os.ReadFile(stagedFile)
			if err != nil || string(data) != "before" {
				t.Fatalf("export did not retain original referent data: %q %v", data, err)
			}
		})
	}
}

func TestMigrationFingerprintRejectsUnsafeExportLinks(t *testing.T) {
	for _, kind := range []string{"escape", "ancestor cycle", "link cycle", "dangling", "dangling root"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "resources")
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, "alias")
			target := filepath.Join(base, "outside")
			switch kind {
			case "escape":
				if err := os.WriteFile(target, []byte("outside"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ancestor cycle":
				target = root
			case "link cycle":
				target = link
			case "dangling root":
				root = filepath.Join(base, "missing-root")
				link = root
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if err := migrationCopyTree(root, filepath.Join(base, "staged")); err == nil {
				t.Fatal("export accepted unsafe link")
			}
			if _, err := migrationFingerprintPaths([]string{root}); err == nil {
				t.Fatal("fingerprint accepted a link rejected by export")
			}
		})
	}
}

func TestMigrationRollbackCandidateDescription(t *testing.T) {
	for _, suffix := range []string{"", "\n", " edited\n", "\nextra\n", "\n\n", " \n", "\r\n"} {
		t.Run(fmt.Sprintf("suffix_%q", suffix), func(t *testing.T) {
			base := t.TempDir()
			m := &Migration{Directory: filepath.Join(base, "migration"), CandidateVMID: 2010}
			config, err := json.Marshal(map[string]string{"description": m.CandidateDescription() + suffix})
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("MIGRATION_TEST_CONFIG", string(config))
			calls := filepath.Join(base, "calls")
			stopped := filepath.Join(base, "stopped")
			t.Setenv("MIGRATION_TEST_CALLS", calls)
			t.Setenv("MIGRATION_TEST_STOPPED", stopped)
			commands := map[string]string{
				"pvesh": "#!/bin/sh\nprintf '%s\\n' \"$MIGRATION_TEST_CONFIG\"\n",
				"pct": `#!/bin/sh
printf '%s\n' "$*" >> "$MIGRATION_TEST_CALLS"
case "$1" in
  status)
    if [ -f "$MIGRATION_TEST_STOPPED" ]; then
      printf 'status: stopped\n'
    else
      printf 'status: running\n'
    fi ;;
  stop) : > "$MIGRATION_TEST_STOPPED" ;;
  set) [ "$3" = "--onboot" ] && [ "$4" = "0" ] ;;
  *) exit 1 ;;
esac
`,
			}
			for name, script := range commands {
				if err := os.WriteFile(filepath.Join(base, name), []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", base)
			err = m.Rollback(context.Background())
			if suffix != "" && suffix != "\n" {
				if err == nil || !strings.Contains(err.Error(), "ownership marker changed") {
					t.Fatalf("modified ownership marker accepted: %v", err)
				}
				if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("rollback touched an unowned candidate: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("owned candidate rollback failed: %v", err)
			}
			data, err := os.ReadFile(calls)
			if err != nil || string(data) != "status 2010\nstop 2010\nset 2010 --onboot 0\nstatus 2010\n" {
				t.Fatalf("owned candidate was not stopped and disabled: %q %v", data, err)
			}
		})
	}
}

func TestMigrationRollbackProtectsUnownedNetworkEdits(t *testing.T) {
	old := migrationSavedFile{Data: []byte("original"), Mode: 0640}
	versions := []migrationSavedFile{{Data: []byte("stage"), Mode: 0640}, {Data: []byte("cutover"), Mode: 0640}}
	for _, value := range []string{"stage", "cutover"} {
		restore, err := migrationRestoreFileDecision(&old, versions, []byte(value), true)
		if err != nil || !restore {
			t.Fatalf("owned partial write not restored: %s %v", value, err)
		}
	}
	if restore, err := migrationRestoreFileDecision(&old, versions, old.Data, true); err != nil || restore {
		t.Fatal("untouched network file modified")
	}
	if restore, err := migrationRestoreFileDecision(&old, versions, []byte("administrator change"), true); err == nil || restore {
		t.Fatal("independent network edit overwritten")
	}
	if restore, err := migrationRestoreFileDecision(nil, versions, []byte("stage"), true); err != nil || !restore {
		t.Fatal("owned created file not removed")
	}
}
func TestMigrationRollbackCandidateStopAndDisableAreBarriers(t *testing.T) {
	for _, fail := range []string{"stop", "set"} {
		t.Run(fail, func(t *testing.T) {
			var calls []string
			run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
				calls = append(calls, args[0])
				if args[0] == fail {
					return nil, errors.New("candidate operation failed")
				}
				if args[0] == "status" {
					return []byte("status: running\n"), nil
				}
				return nil, nil
			}
			err := migrationStopCandidate(context.Background(), 2010, run)
			if err == nil {
				t.Fatal("unsafe candidate accepted")
			}
			if fail == "stop" && !reflect.DeepEqual(calls, []string{"status", "stop"}) {
				t.Fatalf("advanced past failed stop: %v", calls)
			}
			if fail == "set" && !reflect.DeepEqual(calls, []string{"status", "stop", "set"}) {
				t.Fatalf("advanced past failed disable: %v", calls)
			}
		})
	}
	calls := 0
	if err := migrationStopCandidate(context.Background(), 0, func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }); err != nil || calls != 0 {
		t.Fatal("rollback touched an unowned guest")
	}
}
func TestMigrationIPAMRollbackPreservesOtherSubnets(t *testing.T) {
	current := map[string]any{"zones": map[string]any{"ludus": map[string]any{"subnets": map[string]any{"192.0.2.0/24": "candidate", "10.7.0.0/16": "administrator"}}, "other": map[string]any{"identity": "preserved"}}}
	saved := map[string]any{"zones": map[string]any{"ludus": map[string]any{"subnets": map[string]any{"192.0.2.0/24": "original"}}}}
	got := migrationRestoreIPAM(current, saved, "ludus")
	subnets := migrationMap(migrationMap(migrationMap(got, "zones"), "ludus"), "subnets")
	if subnets["192.0.2.0/24"] != "original" || subnets["10.7.0.0/16"] != "administrator" || migrationMap(migrationMap(got, "zones"), "other")["identity"] != "preserved" {
		t.Fatalf("unrelated IPAM lost: %v", got)
	}
}
func TestMigrationForwardingSeparatesStagingFromEndpoints(t *testing.T) {
	c := migrationNetworkPlan{Bridge: "ludusmg", IP: "172.31.255.2/30", Uplink: "vmbr0", Port: 8080, AdminPort: 8081, WireGuardPort: 51820, ExposeAdminPort: true}
	for _, mode := range []string{"stage", "wireguard", "start"} {
		rules := migrationForwardRules(c, mode, "owned")
		tcp, udp := false, false
		for _, rule := range rules {
			args := strings.Join(rule.Args, " ")
			if !strings.Contains(args, "--comment ludus-migration:owned") {
				t.Fatal("rule has no rollback ownership")
			}
			if strings.Contains(args, "-j DNAT") {
				tcp = tcp || strings.Contains(args, "-p tcp")
				udp = udp || strings.Contains(args, "-p udp")
			}
		}
		if tcp != (mode == "start") || udp != (mode != "stage") {
			t.Fatalf("premature endpoint redirection in %s: TCP %t UDP %t", mode, tcp, udp)
		}
	}
}
func TestMigrationRelayPreservesBytesAndHalfClose(t *testing.T) {
	upstream, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	bridge, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		connection, err := bridge.Accept()
		if err != nil {
			done <- err
			return
		}
		migrationRelay(ctx, connection, upstream.Addr().String())
		done <- nil
	}()
	go func() {
		connection, err := upstream.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		data, err := io.ReadAll(connection)
		if err == nil {
			_, _ = connection.Write(append([]byte("response:"), data...))
		}
	}()
	client, err := net.Dial("tcp4", bridge.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := []byte{0, 1, 2, 255, 10, 13, 0, 42}
	if _, err = client.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = client.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	result, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(result, append([]byte("response:"), payload...)) {
		t.Fatalf("relay altered stream: %v", result)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("relay did not drain")
	}
}

func TestMigrationServiceRestoreReportsFailuresAndContinues(t *testing.T) {
	services := map[string]migrationServiceState{"ludus": {Active: true, Enablement: "enabled"}, "wg-quick@wg0": {Active: true, Enablement: "disabled"}}
	var calls []string
	err := migrationRestoreServices(context.Background(), services, func(ctx context.Context, name string, args ...string) error {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if call == "start ludus" {
			return errors.New("service startup failed")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "ludus") {
		t.Fatalf("lost service restoration error: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"enable ludus", "start ludus", "disable wg-quick@wg0", "start wg-quick@wg0"}) {
		t.Fatalf("other services were not restored after failure: %v", calls)
	}
}
