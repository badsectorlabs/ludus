package ludus_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	ludus "gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk"
	"gitlab.com/badsectorlabs/ludus/ludus-plugin-sdk/protocol"
)

var installFS = fstest.MapFS{
	"ansible/range-management/plugin.yml":      {Data: []byte("- hosts: all\n")},
	"ansible/roles/plugin-role/tasks/main.yml": {Data: []byte("---\n")},
}

func TestInstallFilesRequiresSystemLevel(t *testing.T) {
	installPath := t.TempDir()
	err := ludus.InstallFiles(protocol.Environment{Level: protocol.LevelStandard, InstallPath: installPath}, installFS)
	if err == nil {
		t.Fatal("InstallFiles() succeeded for a standard-level plugin")
	}
	if entries, _ := os.ReadDir(installPath); len(entries) != 0 {
		t.Fatalf("standard-level InstallFiles wrote %v", entries)
	}
}

func TestInstallFilesWritesTree(t *testing.T) {
	installPath := t.TempDir()
	extra := fstest.MapFS{"templates/extra.txt": {Data: []byte("extra")}}
	if err := ludus.InstallFiles(protocol.Environment{Level: protocol.LevelSystem, InstallPath: installPath}, installFS, nil, extra); err != nil {
		t.Fatalf("InstallFiles() error = %v", err)
	}
	for name, want := range map[string]string{
		"ansible/range-management/plugin.yml":      "- hosts: all\n",
		"ansible/roles/plugin-role/tasks/main.yml": "---\n",
		"templates/extra.txt":                      "extra",
	} {
		path := filepath.Join(installPath, filepath.FromSlash(name))
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, err, want)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o644 {
			t.Errorf("%s mode = %v, %v; want 0644", name, info.Mode().Perm(), err)
		}
	}
	if info, err := os.Stat(filepath.Join(installPath, "ansible", "roles")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("directory mode = %v, %v; want 0755", info.Mode().Perm(), err)
	}
}

func TestInstallFilesDoesNotFollowSymlinksOutOfTheTree(t *testing.T) {
	installPath := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(installPath, "ansible")); err != nil {
		t.Fatal(err)
	}
	err := ludus.InstallFiles(protocol.Environment{Level: protocol.LevelSystem, InstallPath: installPath}, installFS)
	if err == nil {
		t.Fatal("InstallFiles() followed a symlink out of the install tree")
	}
	_ = fs.WalkDir(os.DirFS(outside), ".", func(path string, entry fs.DirEntry, err error) error {
		if path != "." {
			t.Errorf("InstallFiles() wrote %s outside the install tree", path)
		}
		return nil
	})
}
