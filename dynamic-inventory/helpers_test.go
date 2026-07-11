package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/luthermonson/go-proxmox"
)

func TestEnvTrue(t *testing.T) {
	cases := []struct {
		val  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"1", true},
		{"yes", true},
		{"YES", true},
		{"", false},
		{"0", false},
		{"no", false},
		{"false", false},
		{"truthy", false},
	}
	for _, c := range cases {
		t.Setenv("DI_TEST_FLAG", c.val)
		if got := envTrue("DI_TEST_FLAG"); got != c.want {
			t.Errorf("envTrue(%q) = %v, want %v", c.val, got, c.want)
		}
	}
}

func TestParseMetadata(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]interface{}
	}{
		{
			name: "empty input returns empty map",
			in:   "",
			want: map[string]interface{}{},
		},
		{
			name: "valid JSON",
			in:   `{"groups": ["webservers", "prod"], "owner": "ops"}`,
			want: map[string]interface{}{
				"groups": []interface{}{"webservers", "prod"},
				"owner":  "ops",
			},
		},
		{
			name: "single-quoted python-style JSON falls back",
			in:   `{'role': 'db', 'tier': 'edge'}`,
			want: map[string]interface{}{"role": "db", "tier": "edge"},
		},
		{
			name: "non-JSON description preserved as notes",
			in:   "Just some free-form text",
			want: map[string]interface{}{"notes": "Just some free-form text"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseMetadata(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("parseMetadata(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestExtractIPs(t *testing.T) {
	ifaces := []*proxmox.AgentNetworkIface{
		{
			Name: "lo",
			IPAddresses: []*proxmox.AgentNetworkIPAddress{
				{IPAddress: "127.0.0.1"},
			},
		},
		{
			Name: "eth0",
			IPAddresses: []*proxmox.AgentNetworkIPAddress{
				{IPAddress: "10.5.10.20"},
				{IPAddress: "fe80::1"},
				{IPAddress: "not-an-ip"}, // dropped
			},
		},
	}
	got := extractIPs(ifaces)
	want := []string{"127.0.0.1", "10.5.10.20"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractIPs got %v, want %v", got, want)
	}

	if got := extractIPs(nil); len(got) != 0 {
		t.Errorf("extractIPs(nil) = %v, want empty", got)
	}
}

func TestCheckIPAddresses(t *testing.T) {
	envEmpty := ludusEnv{}

	t.Run("returns first non-loopback non-172 non-link-local", func(t *testing.T) {
		ips := []string{"127.0.0.1", "172.20.0.5", "fe80::1", "10.5.10.20", "8.8.8.8"}
		if got := checkIPAddresses(envEmpty, "anyvm", ips); got != "10.5.10.20" {
			t.Errorf("got %q, want 10.5.10.20", got)
		}
	})

	t.Run("prefers 192.0.2.0/24 over other valid IPs", func(t *testing.T) {
		ips := []string{"10.5.10.20", "192.0.2.7"}
		if got := checkIPAddresses(envEmpty, "anyvm", ips); got != "192.0.2.7" {
			t.Errorf("got %q, want 192.0.2.7", got)
		}
	})

	t.Run("config IP exact match wins immediately", func(t *testing.T) {
		env := ludusEnv{
			RangeID:     "TEST7",
			RangeNumber: "7",
			VMs: []LudusVM{
				{VMName: "{{ range_id }}-web", VLAN: 10, IPLastOctet: 5},
			},
		}
		ips := []string{"172.20.0.5", "192.0.2.7", "10.7.10.5"}
		if got := checkIPAddresses(env, "TEST7-web", ips); got != "10.7.10.5" {
			t.Errorf("got %q, want 10.7.10.5", got)
		}
	})

	t.Run("forceIP returns config IP when nothing else is valid", func(t *testing.T) {
		env := ludusEnv{
			RangeID:     "TEST7",
			RangeNumber: "7",
			VMs: []LudusVM{
				{VMName: "TEST7-db", VLAN: 20, IPLastOctet: 9, ForceIP: true},
			},
		}
		ips := []string{"127.0.0.1", "172.20.0.5", "fe80::abcd"}
		if got := checkIPAddresses(env, "TEST7-db", ips); got != "10.7.20.9" {
			t.Errorf("got %q, want 10.7.20.9 (forced)", got)
		}
	})

	t.Run("no forceIP and no valid IPs returns empty", func(t *testing.T) {
		env := ludusEnv{
			RangeID:     "TEST7",
			RangeNumber: "7",
			VMs: []LudusVM{
				{VMName: "TEST7-db", VLAN: 20, IPLastOctet: 9, ForceIP: false},
			},
		}
		ips := []string{"127.0.0.1", "172.20.0.5"}
		if got := checkIPAddresses(env, "TEST7-db", ips); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("range_id template casing is matched case-insensitively", func(t *testing.T) {
		env := ludusEnv{
			RangeID:     "TEST7",
			RangeNumber: "7",
			VMs: []LudusVM{
				{VMName: "{{RANGE_ID}}-web", VLAN: 10, IPLastOctet: 5, ForceIP: true},
			},
		}
		if got := checkIPAddresses(env, "TEST7-web", nil); got != "10.7.10.5" {
			t.Errorf("got %q, want 10.7.10.5", got)
		}
	})

	t.Run("invalid IP strings ignored", func(t *testing.T) {
		ips := []string{"not-an-ip", "still-not-an-ip", "10.0.0.1"}
		if got := checkIPAddresses(envEmpty, "anyvm", ips); got != "10.0.0.1" {
			t.Errorf("got %q, want 10.0.0.1", got)
		}
	})
}

func TestGetOSInfoFromConfig(t *testing.T) {
	env := ludusEnv{
		RangeID: "R1",
		VMs: []LudusVM{
			{VMName: "{{ range_id }}-win", Windows: true},
			{VMName: "{{ range_id }}-lin", Linux: true},
			{VMName: "{{ range_id }}-mac", MacOS: true},
			{VMName: "{{ range_id }}-none"},
		},
	}
	cases := []struct {
		vm   string
		want string
	}{
		{"R1-win", "windows"},
		{"R1-lin", "linux"},
		{"R1-mac", "macos"},
		{"R1-none", ""},
		{"R1-missing", ""},
	}
	for _, c := range cases {
		if got := getOSInfoFromConfig(env, c.vm); got != c.want {
			t.Errorf("getOSInfoFromConfig(%q) = %q, want %q", c.vm, got, c.want)
		}
	}

	if got := getOSInfoFromConfig(ludusEnv{}, "anything"); got != "" {
		t.Errorf("empty env should always return empty, got %q", got)
	}
}

func TestProcessMembers(t *testing.T) {
	t.Run("collects qemu/lxc names but skips templates and storage", func(t *testing.T) {
		groupMap := map[string][]string{}
		validVMIDs := map[string]bool{}
		members := []proxmox.ClusterResource{
			{Type: "qemu", Name: "vm-a", VMID: 100, Template: 0},
			{Type: "qemu", Name: "vm-tmpl", VMID: 101, Template: 1},
			{Type: "lxc", Name: "ct-a", VMID: 200},
			{Type: "storage", Name: "local"},
		}
		processMembers(members, "POOL1", ludusEnv{}, true, groupMap, validVMIDs)

		got := append([]string(nil), groupMap["POOL1"]...)
		sort.Strings(got)
		want := []string{"ct-a", "vm-a"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("groupMap[POOL1] = %v, want %v", got, want)
		}
		if len(validVMIDs) != 0 {
			t.Errorf("validVMIDs should be empty without rangeID filter, got %v", validVMIDs)
		}
	})

	t.Run("populates validVMIDs when poolID matches rangeID", func(t *testing.T) {
		groupMap := map[string][]string{}
		validVMIDs := map[string]bool{}
		members := []proxmox.ClusterResource{
			{Type: "qemu", Name: "vm-a", VMID: 100},
			{Type: "lxc", Name: "ct-a", VMID: 200},
		}
		env := ludusEnv{RangeID: "RANGE7"}
		processMembers(members, "RANGE7", env, true, groupMap, validVMIDs)
		if !validVMIDs["100"] || !validVMIDs["200"] {
			t.Errorf("expected vmids 100 and 200 marked valid, got %v", validVMIDs)
		}
	})

	t.Run("admin user collects ADMIN pool members under range filter", func(t *testing.T) {
		groupMap := map[string][]string{}
		validVMIDs := map[string]bool{}
		members := []proxmox.ClusterResource{
			{Type: "qemu", Name: "shared-router", VMID: 999},
		}
		env := ludusEnv{RangeID: "RANGE7", UserIsAdmin: true}
		processMembers(members, "ADMIN", env, true, groupMap, validVMIDs)
		if !validVMIDs["999"] {
			t.Errorf("admin user should see ADMIN-pool members, got %v", validVMIDs)
		}
	})

	t.Run("non-admin under range filter does not see other pools", func(t *testing.T) {
		groupMap := map[string][]string{}
		validVMIDs := map[string]bool{}
		members := []proxmox.ClusterResource{
			{Type: "qemu", Name: "other-vm", VMID: 500},
		}
		env := ludusEnv{RangeID: "RANGE7"}
		processMembers(members, "OTHER", env, false, groupMap, validVMIDs)
		if validVMIDs["500"] {
			t.Errorf("non-admin should not see other pools, got %v", validVMIDs)
		}
		if len(groupMap["OTHER"]) != 0 {
			t.Errorf("non-visible pool should not collect group members, got %v", groupMap["OTHER"])
		}
	})

	t.Run("returnAllRanges disables vmid filtering bookkeeping", func(t *testing.T) {
		groupMap := map[string][]string{}
		validVMIDs := map[string]bool{}
		members := []proxmox.ClusterResource{
			{Type: "qemu", Name: "vm-a", VMID: 100},
		}
		env := ludusEnv{RangeID: "RANGE7", ReturnAllRanges: true}
		processMembers(members, "RANGE7", env, true, groupMap, validVMIDs)
		if len(validVMIDs) != 0 {
			t.Errorf("returnAllRanges=true should leave validVMIDs empty, got %v", validVMIDs)
		}
	})
}

func TestLoadLudusEnv(t *testing.T) {
	t.Run("env-only fields populated when no config file", func(t *testing.T) {
		t.Setenv("LUDUS_RANGE_ID", "R1")
		t.Setenv("LUDUS_RANGE_NUMBER", "11")
		t.Setenv("LUDUS_USER_IS_ADMIN", "true")
		t.Setenv("LUDUS_RETURN_ALL_RANGES", "")
		t.Setenv("LUDUS_RANGE_CONFIG", "")

		e := loadLudusEnv()
		if e.RangeID != "R1" || e.RangeNumber != "11" || !e.UserIsAdmin || e.ReturnAllRanges {
			t.Errorf("unexpected env: %+v", e)
		}
		if e.VMs != nil {
			t.Errorf("VMs should be nil when no config file, got %v", e.VMs)
		}
	})

	t.Run("config file is loaded and parsed", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "range.yml")
		yml := `ludus:
  - vm_name: "{{ range_id }}-web"
    vlan: 10
    ip_last_octet: 5
    linux:
      packages:
        - curl
  - vm_name: "{{ range_id }}-dc"
    vlan: 10
    ip_last_octet: 6
    windows:
      sysprep: false
    force_ip: true
  - vm_name: "{{ range_id }}-mac"
    vlan: 20
    ip_last_octet: 7
    macOS: true
`
		if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("LUDUS_RANGE_ID", "R2")
		t.Setenv("LUDUS_RANGE_NUMBER", "22")
		t.Setenv("LUDUS_RANGE_CONFIG", path)
		t.Setenv("LUDUS_USER_IS_ADMIN", "")
		t.Setenv("LUDUS_RETURN_ALL_RANGES", "")

		e := loadLudusEnv()
		if len(e.VMs) != 3 {
			t.Fatalf("expected 3 VMs loaded, got %d", len(e.VMs))
		}
		if !bool(e.VMs[0].Linux) || e.VMs[0].VLAN != 10 || e.VMs[0].IPLastOctet != 5 {
			t.Errorf("VM0 not parsed correctly: %+v", e.VMs[0])
		}
		if !bool(e.VMs[1].Windows) || !e.VMs[1].ForceIP {
			t.Errorf("VM1 not parsed correctly: %+v", e.VMs[1])
		}
		if !e.VMs[2].MacOS {
			t.Errorf("VM2 not parsed correctly: %+v", e.VMs[2])
		}

		vm := e.findLudusVM("R2-dc")
		if vm == nil || !bool(vm.Windows) {
			t.Errorf("findLudusVM should resolve range_id template, got %+v", vm)
		}
		if e.findLudusVM("does-not-exist") != nil {
			t.Errorf("findLudusVM should return nil for unknown VM")
		}
	})

	t.Run("missing range_id disables config load", func(t *testing.T) {
		t.Setenv("LUDUS_RANGE_ID", "")
		t.Setenv("LUDUS_RANGE_NUMBER", "1")
		t.Setenv("LUDUS_RANGE_CONFIG", "/tmp/does-not-matter.yml")
		t.Setenv("LUDUS_USER_IS_ADMIN", "")
		t.Setenv("LUDUS_RETURN_ALL_RANGES", "")
		e := loadLudusEnv()
		if e.VMs != nil {
			t.Errorf("VMs should not be loaded without RangeID, got %v", e.VMs)
		}
	})
}

func TestLXCIPRegex(t *testing.T) {
	cases := []struct {
		net0 string
		want string
	}{
		{"name=eth0,bridge=vmbr0,ip=10.5.99.10/24,gw=10.5.99.1", "10.5.99.10"},
		{"name=eth0,bridge=vmbr0,ip=dhcp", ""},
		{"", ""},
	}
	for _, c := range cases {
		m := lxcIPRegex.FindStringSubmatch(c.net0)
		got := ""
		if len(m) > 1 {
			got = m[1]
		}
		if got != c.want {
			t.Errorf("lxcIPRegex(%q) = %q, want %q", c.net0, got, c.want)
		}
	}
}
