package ludusapi

import (
	"bytes"
	"os"
	"os/user"
	"path/filepath"
	"testing"
)

func TestAuthMaterialPathsForRange(t *testing.T) {
	paths, err := authMaterialPathsForRange("TEST2")
	if err != nil {
		t.Fatalf("authMaterialPathsForRange() error = %v", err)
	}

	expectedBase := filepath.Join(ludusInstallPath, "ranges", "TEST2", "machine-credentials")
	if paths.MachineCredDir != expectedBase {
		t.Fatalf("MachineCredDir = %q, want %q", paths.MachineCredDir, expectedBase)
	}

	nestedPaths, err := authMaterialPathsForRange("TEAM/TEST2")
	if err != nil {
		t.Fatalf("authMaterialPathsForRange() slash-delimited range ID error = %v", err)
	}
	expectedNestedBase := filepath.Join(ludusInstallPath, "ranges", "TEAM", "TEST2", "machine-credentials")
	if nestedPaths.MachineCredDir != expectedNestedBase {
		t.Fatalf("nested MachineCredDir = %q, want %q", nestedPaths.MachineCredDir, expectedNestedBase)
	}

	for _, rangeID := range []string{"", ".", "../TEST2", "TEST2/other/extra/depth", "TEST2/../other"} {
		if _, err := authMaterialPathsForRange(rangeID); err == nil {
			t.Fatalf("authMaterialPathsForRange(%q) succeeded, want error", rangeID)
		}
	}
}

func TestSSHAuthMaterialInitializationAndReuse(t *testing.T) {
	if os.Geteuid() == 0 {
		if _, err := user.Lookup("ludus"); err != nil {
			t.Skip("credential ownership requires the ludus system account when running as root")
		}
	}
	rangesDir := t.TempDir()
	rangeDir := filepath.Join(rangesDir, "TEST2")
	if err := os.Mkdir(rangeDir, 0700); err != nil {
		t.Fatal(err)
	}
	paths := authMaterialPaths(rangesDir, filepath.Join(rangeDir, "machine-credentials"))
	if err := ensureAuthMaterial(paths); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(rangeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name() != ".machine-credentials-initialized" || entries[1].Name() != "machine-credentials" {
		t.Fatalf("initialization left unexpected range entries: %v", entries)
	}
	entries, err = os.ReadDir(paths.MachineCredDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "ssh" || !entries[0].IsDir() {
		t.Fatalf("initialization created non-SSH material: %v", entries)
	}
	for _, path := range []string{paths.MachineCredDir, paths.SSHKeyDir, paths.SSHKeyPath, authMaterialMarkerPath(paths)} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("%s is accessible by group or other users", path)
		}
	}
	original := map[string][]byte{}
	for _, path := range []string{paths.SSHKeyPath, paths.SSHPubKeyPath} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		original[path] = contents
	}
	unrelatedPath := filepath.Join(paths.MachineCredDir, "unrelated-material")
	unrelated := []byte("unrelated material must not be validated, replaced, or deleted")
	if err := os.WriteFile(unrelatedPath, unrelated, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAuthMaterial(paths); err != nil {
		t.Fatalf("existing SSH-only credentials were not reusable: %v", err)
	}
	original[unrelatedPath] = unrelated
	for path, want := range original {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("reuse changed existing material %s: %v", path, err)
		}
	}
}

func TestExistingAuthMaterialFailsClosedWithoutRotation(t *testing.T) {
	rangesDir := t.TempDir()
	paths := authMaterialPaths(rangesDir, filepath.Join(rangesDir, "TEST2", "machine-credentials"))
	if err := validateAuthMaterial(paths); !os.IsNotExist(err) {
		t.Fatalf("missing credentials should be reported without creation, got %v", err)
	}
	if _, err := os.Stat(paths.MachineCredDir); !os.IsNotExist(err) {
		t.Fatal("read-only credential lookup created material")
	}
	if err := os.MkdirAll(paths.MachineCredDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := generateAuthMaterial(paths); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths); err != nil {
		t.Fatalf("generated material was rejected: %v", err)
	}
	if err := markAuthMaterialInitialized(paths); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	for _, path := range []string{paths.SSHKeyPath, paths.SSHPubKeyPath} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		original[path] = contents
	}
	for _, test := range []struct {
		name   string
		path   string
		remove bool
	}{
		{"missing public key", paths.SSHPubKeyPath, true},
		{"corrupt SSH private key", paths.SSHKeyPath, false},
		{"missing private key", paths.SSHKeyPath, true},
		{"corrupt SSH public key", paths.SSHPubKeyPath, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.remove {
				if err := os.Remove(test.path); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(test.path, []byte("corrupt"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ensureAuthMaterial(paths); err == nil {
				t.Fatal("invalid existing credentials were silently repaired")
			}
			for path, want := range original {
				if path == test.path {
					continue
				}
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("failure changed previously deployed material %s", path)
				}
			}
			if test.remove {
				if _, err := os.Lstat(test.path); !os.IsNotExist(err) {
					t.Fatal("missing material was regenerated")
				}
			} else {
				got, err := os.ReadFile(test.path)
				if err != nil || string(got) != "corrupt" {
					t.Fatal("corrupt material was overwritten")
				}
			}
			if err := os.WriteFile(test.path, original[test.path], 0600); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := os.Chmod(paths.SSHKeyPath, 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths); err == nil {
		t.Fatal("group-readable private key was accepted")
	}
	if err := os.Chmod(paths.SSHKeyPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.SSHKeyPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private-key")
	if err := os.WriteFile(outside, original[paths.SSHKeyPath], 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, paths.SSHKeyPath); err != nil {
		t.Fatal(err)
	}
	if err := validateAuthMaterial(paths); err == nil {
		t.Fatal("credential symlink outside the range was accepted")
	}
	if err := os.RemoveAll(paths.MachineCredDir); err != nil {
		t.Fatal(err)
	}
	if err := ensureAuthMaterial(paths); err == nil {
		t.Fatal("complete loss of previously provisioned credentials triggered generation")
	}
	if _, err := os.Lstat(paths.MachineCredDir); !os.IsNotExist(err) {
		t.Fatal("lost material was regenerated")
	}
}

func TestCertificateAuthenticationRequiresEntitlement(t *testing.T) {
	for _, test := range []struct {
		name         string
		defaults     map[string]interface{}
		entitlements []string
		wantEnabled  bool
		wantError    bool
	}{
		{"ordinary range", map[string]interface{}{}, nil, false, false},
		{"disabled", map[string]interface{}{"use_cert_auth": false}, nil, false, false},
		{"unlicensed", map[string]interface{}{"use_cert_auth": true}, nil, false, true},
		{"licensed", map[string]interface{}{"use_cert_auth": true}, []string{"ENTERPRISE_PLUGIN"}, true, false},
		{"nonboolean", map[string]interface{}{"use_cert_auth": "true"}, []string{"ENTERPRISE_PLUGIN"}, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			enabled, err := certAuthEnabled(test.defaults, test.entitlements)
			if enabled != test.wantEnabled || (err != nil) != test.wantError {
				t.Fatalf("certAuthEnabled() = %v, %v", enabled, err)
			}
		})
	}
}
