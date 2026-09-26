package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"ludus-server/localgen"
	"ludusapi"
	"ludusapi/pluginrpc"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func migrationTestWrite(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func migrationTestArchive(t *testing.T, stage string) string {
	t.Helper()
	archive := filepath.Join(t.TempDir(), "state.tar.gz")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(stage, func(filename string, st os.FileInfo, walkErr error) error {
		if walkErr != nil || filename == stage {
			return walkErr
		}
		header, err := tar.FileInfoHeader(st, "")
		if err != nil {
			return err
		}
		header.Name, err = filepath.Rel(stage, filename)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(header); err != nil || st.IsDir() {
			return err
		}
		in, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, closer := range []io.Closer{tw, gz, out} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return archive
}

func migrationTestState(t *testing.T) (string, migrationManifest) {
	t.Helper()
	stage := t.TempDir()
	migrationTestWrite(t, stage, "opt/ludus/config.yml", "data_directory: /srv/private-pocketbase\nwireguard_endpoint: original.example\n")
	migrationTestWrite(t, stage, "opt/ludus/install/root-api-key", "root-secret\n")
	migrationTestWrite(t, stage, "etc/wireguard/server-private-key", "wg-secret\n")
	migrationTestWrite(t, stage, "etc/wireguard/wg0.conf", "[Interface]\nPrivateKey = wg-secret\nListenPort = 51820\n")
	migrationTestWrite(t, stage, "opt/ludus/ansible/server-config.yml", "# Original administrator defaults\ndefaults:\n  snapshot: false\n  linux:\n    username: retained-user\ndefault_router_template_name: old-router-template\n")
	migrationTestWrite(t, stage, "opt/ludus/db/storage/private/blob", "retained attachment\n")
	db, err := sql.Open("sqlite", filepath.Join(stage, "opt/ludus/db/data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE _collections (name TEXT);
INSERT INTO _collections VALUES ('users'), ('ranges'), ('vms');
CREATE TABLE users (userID TEXT, hashedAPIKey TEXT, proxmoxTokenSecret TEXT, proxmoxPassword TEXT, proxmoxUsername TEXT);
CREATE TABLE ranges (id TEXT, rangeID TEXT, rangeNumber INTEGER);
CREATE TABLE vms (proxmoxID INTEGER, range TEXT, isRouter INTEGER, name TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("root-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users VALUES ('ROOT', ?, '', '', '')", string(hash)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	values, err := migrationReadYAML(filepath.Join(stage, "opt/ludus/config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := migrationDatabase(stage, values)
	if err != nil {
		t.Fatal(err)
	}
	m.RouterTemplate = "old-router-template"
	m.CustomTLS = true
	if err := localgen.EnsureTLSCert(filepath.Join(stage, "migration-tls/server.crt"), filepath.Join(stage, "migration-tls/server.key"), "original.example", nil); err != nil {
		t.Fatal(err)
	}
	migrationTestManifest(t, stage, m)
	return stage, m
}

func migrationTestManifest(t *testing.T, stage string, m migrationManifest) {
	t.Helper()
	var err error
	m.Files, err = migrationInventory(stage)
	if err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	migrationTestWrite(t, stage, "migration.json", string(content))
}

func TestMigrationCustomDataDirectoryAndDefaultsRoundTrip(t *testing.T) {
	stage, m := migrationTestState(t)
	if m.DataDirectory != "/srv/private-pocketbase" {
		t.Fatalf("metadata lost original database directory: %q", m.DataDirectory)
	}
	restored := t.TempDir()
	if err := migrationExtract(migrationTestArchive(t, stage), restored); err != nil {
		t.Fatal(err)
	}
	got, old, err := migrationValidateStage(restored)
	if err != nil {
		t.Fatal(err)
	}
	if got.DataDirectory != m.DataDirectory || old["data_directory"] != m.DataDirectory {
		t.Fatal("archive metadata no longer identifies the source database directory")
	}
	for _, name := range []string{"opt/ludus/ansible/server-config.yml", "opt/ludus/db/storage/private/blob"} {
		want, err := os.ReadFile(filepath.Join(stage, name))
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(restored, name))
		if err != nil || string(got) != string(want) {
			t.Fatalf("archived state changed: %s", name)
		}
	}
	for _, directory := range []string{"", "/opt/ludus/db", "/srv/another-db"} {
		m.DataDirectory = directory
		migrationTestManifest(t, restored, m)
		if _, _, err := migrationValidateStage(restored); err == nil {
			t.Fatalf("accepted mismatched database metadata %q", directory)
		}
	}
}

func TestMigrationRejectsInvalidGlobalDefaults(t *testing.T) {
	stage, m := migrationTestState(t)
	for _, content := range []string{"- not-a-mapping\n", "default_router_template_name: 42\n", "default_router_template_name: different-template\n"} {
		migrationTestWrite(t, stage, "opt/ludus/ansible/server-config.yml", content)
		migrationTestManifest(t, stage, m)
		if _, _, err := migrationValidateStage(stage); err == nil {
			t.Fatalf("accepted incompatible defaults %q", content)
		}
	}
	if err := os.Remove(filepath.Join(stage, "opt/ludus/ansible/server-config.yml")); err != nil {
		t.Fatal(err)
	}
	migrationTestManifest(t, stage, m)
	if _, _, err := migrationValidateStage(stage); err == nil {
		t.Fatal("accepted archive without supported global configuration")
	}
}

func TestMigrationPreservesLegacyGlobalRouterDefault(t *testing.T) {
	stage := t.TempDir()
	name := "opt/ludus/ansible/server-config.yml"
	migrationTestWrite(t, stage, name, "defaults:\n  snapshot: false\ndefault_router_vm_name: '{{ range_id }}-original-router'\n")
	if err := migrationPreserveRouterDefault(stage, "original-router-template"); err != nil {
		t.Fatal(err)
	}
	values, err := migrationReadYAML(filepath.Join(stage, name))
	if err != nil {
		t.Fatal(err)
	}
	if values["default_router_template_name"] != "original-router-template" || values["default_router_vm_name"] != "{{ range_id }}-original-router" || values["defaults"].(map[interface{}]interface{})["snapshot"] != false {
		t.Fatal("global router fallback or administrator defaults changed")
	}
	before, err := os.ReadFile(filepath.Join(stage, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationPreserveRouterDefault(stage, "replacement-template"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(stage, name))
	if err != nil || string(before) != string(after) {
		t.Fatal("explicit global router template was overwritten")
	}
}

// The helper is a real RPC subprocess, exercising the same handshake and metadata
// validation used by production, without compiling a fixture during tests.
type migrationProcessPlugin struct{ pluginrpc.Plugin }

func (p *migrationProcessPlugin) Metadata() (pluginrpc.Metadata, error) {
	return pluginrpc.Metadata{Name: os.Getenv("LUDUS_MIGRATION_PLUGIN_NAME"), Version: os.Getenv("LUDUS_MIGRATION_PLUGIN_VERSION")}, nil
}
func (p *migrationProcessPlugin) Shutdown() error { return nil }

func TestMigrationPluginProcessHelper(t *testing.T) {
	if os.Getenv("LUDUS_MIGRATION_PLUGIN_HELPER") != "1" {
		return
	}
	pluginrpc.Serve(&migrationProcessPlugin{})
	os.Exit(0)
}

func migrationTestPlugin(t *testing.T, filename, name, version string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	content := "#!/bin/sh\nexport LUDUS_MIGRATION_PLUGIN_HELPER=1\nexport LUDUS_MIGRATION_PLUGIN_NAME=" + quote(name) + "\nexport LUDUS_MIGRATION_PLUGIN_VERSION=" + quote(version) + "\nexec " + quote(executable) + " -test.run='^TestMigrationPluginProcessHelper$'\n"
	migrationTestWrite(t, filepath.Dir(filename), filepath.Base(filename), content)
	if err := os.Chmod(filename, 0755); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationRequiresCompatibleEnterpriseRuntimeForBothServices(t *testing.T) {
	directory := t.TempDir()
	public := filepath.Join(directory, ludusapi.EnterprisePluginFilename)
	admin := filepath.Join(directory, "admin", ludusapi.EnterprisePluginFilename)
	migrationTestWrite(t, directory, "legacy.so", "not an RPC runtime")
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("legacy shared object satisfied RPC runtime requirement")
	}
	migrationTestPlugin(t, public, ludusapi.EnterprisePluginName, "1.2.3")
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted missing admin runtime")
	}
	migrationTestPlugin(t, admin, ludusapi.EnterprisePluginName, "1.2.3")
	if err := migrationValidatePlugins(directory); err != nil {
		t.Fatal(err)
	}
	for _, identity := range [][2]string{{"Unrelated Plugin", "1.2.3"}, {ludusapi.EnterprisePluginName, "1.2.4"}, {ludusapi.EnterprisePluginName, ""}} {
		migrationTestPlugin(t, admin, identity[0], identity[1])
		if err := migrationValidatePlugins(directory); err == nil {
			t.Fatalf("accepted inconsistent enterprise runtime: %v", identity)
		}
	}
	migrationTestPlugin(t, admin, ludusapi.EnterprisePluginName, "1.2.3")
	if err := os.Chmod(admin, 0644); err != nil {
		t.Fatal(err)
	}
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted non-executable runtime")
	}
	if err := os.Remove(admin); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(public, admin); err != nil {
		t.Fatal(err)
	}
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted linked runtime")
	}
	if err := os.Remove(admin); err != nil {
		t.Fatal(err)
	}
	migrationTestWrite(t, filepath.Dir(admin), filepath.Base(admin), "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(admin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted executable without compatible RPC handshake")
	}
}

func TestMigrationDefaultsSurviveAnsiblePayloadUpdate(t *testing.T) {
	ludusInstallPath := t.TempDir()
	defaults := "# Administrator values\ndefaults:\n  snapshot: false\n  linux:\n    username: retained-user\n"
	migrationTestWrite(t, ludusInstallPath, "ansible/server-config.yml", defaults)
	migrationTestWrite(t, ludusInstallPath, "ansible/obsolete-playbook.yml", "legacy code")
	if err := replaceAnsibleFiles(ludusInstallPath, "migration-regression"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(ludusInstallPath, "ansible/server-config.yml"))
	if err != nil || string(content) != defaults {
		t.Fatal("payload update overwrote supported global configuration")
	}
	st, err := os.Stat(filepath.Join(ludusInstallPath, "ansible/server-config.yml"))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("payload update changed global configuration permissions")
	}
	if _, err := os.Stat(filepath.Join(ludusInstallPath, "ansible/obsolete-playbook.yml")); !os.IsNotExist(err) {
		t.Fatal("legacy executable payload survived replacement")
	}
}

func TestMigrationRejectsUnsafeSourceDataDirectory(t *testing.T) {
	for _, directory := range []interface{}{"", "/", "/opt", "/opt/ludus", "relative/db", "/srv/../db", 42} {
		if _, err := migrationDatabase(t.TempDir(), map[string]interface{}{"data_directory": directory}); err == nil {
			t.Fatalf("accepted unsafe source data directory %v", directory)
		} else if !strings.Contains(err.Error(), "data_directory") {
			t.Fatalf("source directory was not rejected before reading state: %v", err)
		}
	}
}

func TestMigrationDefaultsUpdateRejectsSymlinkBeforeReplacingPayload(t *testing.T) {
	root := t.TempDir()
	migrationTestWrite(t, root, "private-defaults.yml", "defaults:\n  snapshot: false\n")
	migrationTestWrite(t, root, "ansible/old-playbook.yml", "original payload")
	if err := os.Symlink(filepath.Join(root, "private-defaults.yml"), filepath.Join(root, "ansible/server-config.yml")); err != nil {
		t.Fatal(err)
	}
	if err := replaceAnsibleFiles(root, "rejected-update"); err == nil {
		t.Fatal("update accepted linked global configuration")
	}
	content, err := os.ReadFile(filepath.Join(root, "ansible/old-playbook.yml"))
	if err != nil || string(content) != "original payload" {
		t.Fatal("failed configuration preflight replaced existing playbooks")
	}
	content, err = os.ReadFile(filepath.Join(root, "private-defaults.yml"))
	if err != nil || string(content) != "defaults:\n  snapshot: false\n" {
		t.Fatal("failed update modified linked administrator configuration")
	}
}
