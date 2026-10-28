package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func migrationTestTLS(t *testing.T, stage string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "original.example"},
		DNSNames:     []string{"original.example"},
		NotBefore:    time.Unix(0, 0),
		NotAfter:     time.Unix(4102444800, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	migrationTestWrite(t, stage, "migration-tls/server.crt", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate})))
	migrationTestWrite(t, stage, "migration-tls/server.key", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})))
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
	migrationTestTLS(t, stage)
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

func TestMigrationAddsReleaseVariablesWithoutReplacingAdministratorValues(t *testing.T) {
	stage := t.TempDir()
	name := "opt/ludus/ansible/server-config.yml"
	// A 2.3.x file predates the machine credential paths used by cert auth.
	migrationTestWrite(t, stage, name, "defaults:\n  snapshot_with_RAM: false\nludus_ranges_dir: /srv/ludus-ranges\n")
	// The appliance ships these release defaults at /opt/ludus/ansible.
	bundled := filepath.Join("..", "ludus-server", "ansible", "server-config.yml")
	if err := migrationAddReleaseVariables(stage, bundled); err != nil {
		t.Fatal(err)
	}
	values, err := migrationReadYAML(filepath.Join(stage, name))
	if err != nil {
		t.Fatal(err)
	}
	if values["ludus_ranges_dir"] != "/srv/ludus-ranges" {
		t.Fatalf("administrator value replaced: %v", values["ludus_ranges_dir"])
	}
	defaults := values["defaults"].(map[interface{}]interface{})
	if len(defaults) != 1 || defaults["snapshot_with_RAM"] != false {
		t.Fatalf("administrator range defaults changed: %v", defaults)
	}
	for _, key := range []string{"ludus_machine_credentials_dir", "ludus_ssh_private_key_path", "ludus_ssh_public_key_path"} {
		if _, ok := values[key]; !ok {
			t.Fatalf("release variable %s was not added", key)
		}
	}
	before, err := os.ReadFile(filepath.Join(stage, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrationAddReleaseVariables(stage, bundled); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(stage, name))
	if err != nil || string(before) != string(after) {
		t.Fatal("complete global defaults were rewritten")
	}
}

func TestMigrationRequiresEnterprisePackage(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "plugin")
	manifest := func(id string) string {
		return `{"schemaVersion":1,"protocolVersion":1,"id":"` + id + `","name":"Ludus Enterprise","version":"1.2.3","level":"system","runAs":"root","executable":"plugin","vmHooks":{}}`
	}
	migrationTestWrite(t, directory, "ludus-enterprise.so", "not a package")
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("legacy shared object satisfied the package requirement")
	}
	migrationTestWrite(t, directory, "plugin.json", manifest("ludus-enterprise"))
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted a package without its executable")
	}
	migrationTestWrite(t, directory, "plugin", "#!/bin/sh\nexit 0\n")
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted a non-executable plugin")
	}
	if err := os.Chmod(executable, 0755); err != nil {
		t.Fatal(err)
	}
	if err := migrationValidatePlugins(directory); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{manifest("ludus-other"), `{"id":"ludus-enterprise"}`, manifest("ludus-enterprise") + "{}"} {
		migrationTestWrite(t, directory, "plugin.json", invalid)
		if err := migrationValidatePlugins(directory); err == nil {
			t.Fatalf("accepted invalid manifest %s", invalid)
		}
	}
	migrationTestWrite(t, directory, "plugin.json", manifest("ludus-enterprise"))
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugin")
	migrationTestWrite(t, filepath.Dir(target), "plugin", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, executable); err != nil {
		t.Fatal(err)
	}
	if err := migrationValidatePlugins(directory); err == nil {
		t.Fatal("accepted a linked executable")
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
