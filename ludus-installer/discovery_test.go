package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLocalKernelBridgesIncludesSourcedBridgesOnly(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"ci24mgmt", "vmbr0", "ludusnat", "r1", "fwbr100i0"} {
		if err := os.MkdirAll(filepath.Join(root, name, "bridge"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Interfaces without a bridge directory (NICs, veths, taps) are not bridges.
	for _, name := range []string{"ens18", "fwln100i0", "tap100i0"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := localKernelBridges(root, map[string]bool{"ludusnat": true, "r1": true})
	if want := []string{"ci24mgmt", "vmbr0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bridges = %v, want %v", got, want)
	}
	if got := localKernelBridges(filepath.Join(root, "missing"), nil); got != nil {
		t.Fatalf("missing sysfs root = %v, want none", got)
	}
}
