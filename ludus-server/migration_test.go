package main

import (
	"archive/tar"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMigrationInventorySurvivesLegacyCacheRefresh(t *testing.T) {
	responses := map[string]string{
		"/pools/LAB":                  `{"members":[{"vmid":201,"name":"LAB-client","node":"node","type":"qemu"},{"vmid":200,"name":"custom-router","node":"node","type":"qemu"}]}`,
		"/nodes/node/qemu/200/config": `{"net0":"virtio=00:11:22:33:44:55,bridge=vmbr1000,tag=1","net1":"virtio=00:11:22:33:44:56,bridge=vmbr1002,trunks=10;99"}`,
		"/nodes/node/qemu/201/config": `{"net0":"virtio=00:11:22:33:44:57,bridge=vmbr1002,tag=10"}`,
	}
	get := func(path string, result interface{}) error {
		data, ok := responses[path]
		if !ok {
			return fmt.Errorf("unexpected Proxmox path %s", path)
		}
		return json.Unmarshal([]byte(data), result)
	}
	ranges := []migrationRange{{ID: "LAB", Number: 2}}
	inventory, err := migrationProxmoxInventory(ranges, get)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 || inventory[0].VMID != 200 || !inventory[0].IsRouter || inventory[1].IsRouter {
		t.Fatalf("incorrect authoritative inventory: %+v", inventory)
	}
	// Simulate the legacy DELETE followed by a partial repopulation. Neither
	// state may drop the live router from the imported configuration.
	for _, cache := range [][]migrationVM{nil, {{VMID: 201, Name: "LAB-client", RangeNumber: 2}}} {
		stage := t.TempDir()
		config := filepath.Join(stage, "opt/ludus/ranges/LAB/range-config.yml")
		if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte("ludus: []\n"), 0600); err != nil {
			t.Fatal(err)
		}
		m := migrationManifest{Ranges: ranges, VMs: cache, Inventory: inventory, RouterTemplate: "debian-12-x64-server-template"}
		if err := migrationAnnotateRouters(stage, m); err != nil {
			t.Fatal(err)
		}
		values, err := migrationReadYAML(config)
		if err != nil {
			t.Fatal(err)
		}
		if values["router"].(map[interface{}]interface{})["vm_name"] != "custom-router" {
			t.Fatal("partial legacy VM cache lost the actual router identity")
		}
	}
	// Pool response ordering is immaterial, but a real NIC change must cause
	// the cutover inventory comparison to fail.
	responses["/pools/LAB"] = `{"members":[{"vmid":200,"name":"custom-router","node":"node","type":"qemu"},{"vmid":201,"name":"LAB-client","node":"node","type":"qemu"}]}`
	after, err := migrationProxmoxInventory(ranges, get)
	if err != nil || !reflect.DeepEqual(inventory, after) {
		t.Fatal("pool ordering changed inventory")
	}
	responses["/nodes/node/qemu/201/config"] = `{"net0":"virtio=00:11:22:33:44:57,bridge=vmbr1002,tag=99"}`
	after, err = migrationProxmoxInventory(ranges, get)
	if err != nil || reflect.DeepEqual(inventory, after) {
		t.Fatal("real network change went undetected")
	}
	delete(responses, "/nodes/node/qemu/200/config")
	if _, err := migrationProxmoxInventory(ranges, get); err == nil {
		t.Fatal("missing VM configuration was accepted")
	}
}

func TestMigrationSnapshotDatabaseWhileSourceIsOpen(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	destination := filepath.Join(dir, "snapshot.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("PRAGMA journal_mode=WAL; CREATE TABLE values_table (value TEXT); INSERT INTO values_table VALUES ('before')"); err != nil {
		t.Fatal(err)
	}
	if err := migrationSnapshotDatabase(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO values_table VALUES ('after')"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sql.Open("sqlite", "file:"+destination+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var count int
	if err := snapshot.QueryRow("SELECT count(*) FROM values_table").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("snapshot changed with live database: got %d rows", count)
	}
}

func TestMigrationRejectsUnsafeArchiveEntries(t *testing.T) {
	for _, header := range []tar.Header{
		{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "opt/ludus/db/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow", Mode: 0600},
		{Name: "opt/ludus/ludus-server", Typeflag: tar.TypeReg, Mode: 0711},
		{Name: "etc/systemd/system/ludus.service", Typeflag: tar.TypeReg, Mode: 0644},
		{Name: "home/ludus/.profile", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "home/root/.ssh/id_ed25519", Typeflag: tar.TypeReg, Mode: 0600},
		{Name: "home/ludus/.gitconfig/extra", Typeflag: tar.TypeReg, Mode: 0600},
	} {
		t.Run(header.Name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "state.tar.gz")
			out, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			gz := gzip.NewWriter(out)
			tw := tar.NewWriter(gz)
			if err := tw.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
			if err := migrationExtract(archive, t.TempDir()); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestMigrationConfigPreservesSecretsAndUnknownSettings(t *testing.T) {
	old := map[string]interface{}{
		"database_encryption_key": "old-key", "license_key": "old-license", "port": 9080,
		"proxmox_public_ip": "old.example", "custom_plugin_setting": map[interface{}]interface{}{"enabled": false},
		"data_directory": "/srv/pocketbase", "proxmox_token_secret": "old-token",
		"expose_admin_port": true,
	}
	generated := map[string]interface{}{
		"proxmox_endpoints": []interface{}{"https://pve.example:8006"}, "proxmox_node": "node",
		"proxmox_token_id": "ludus@pve!api", "proxmox_token_secret": "new-token",
		"tls_cert_file": "/opt/ludus/tls/server.crt", "tls_key_file": "/opt/ludus/tls/server.key",
		"database_encryption_key": "new-key", "license_key": "community", "port": 8080,
		"wireguard_endpoint": "new.example", "ludus_nat_ip": "192.0.2.254",
		"expose_admin_port": false,
	}
	merged, err := migrationMergeConfig(old, generated)
	if err != nil {
		t.Fatal(err)
	}
	if merged["expose_admin_port"] != true {
		t.Fatal("legacy admin exposure setting was replaced")
	}
	for key, want := range map[string]interface{}{"database_encryption_key": "old-key", "license_key": "old-license", "port": 9080, "wireguard_endpoint": "old.example", "proxmox_token_secret": "new-token", "ludus_nat_ip": "192.0.2.254", "data_directory": "/opt/ludus/db"} {
		if !reflect.DeepEqual(merged[key], want) {
			t.Errorf("%s was not preserved/overridden correctly", key)
		}
	}
	if !reflect.DeepEqual(merged["custom_plugin_setting"], old["custom_plugin_setting"]) {
		t.Fatal("unknown plugin settings lost")
	}
	if old["data_directory"] != "/srv/pocketbase" {
		t.Fatal("merge modified original configuration")
	}
	delete(old, "database_encryption_key")
	merged, err = migrationMergeConfig(old, generated)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := merged["database_encryption_key"]; ok {
		t.Fatal("generated key replaced the legacy implicit encryption default")
	}
}

func TestMigrationPinsExistingRouterIdentity(t *testing.T) {
	stage := t.TempDir()
	config := filepath.Join(stage, "opt/ludus/ranges/LAB/range-config.yml")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("ludus: []\nrouter:\n  memory: 2048\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := migrationManifest{RouterTemplate: "debian-12-x64-server-template", Ranges: []migrationRange{{ID: "LAB", Number: 2}}, VMs: []migrationVM{{VMID: 200, RangeNumber: 2, IsRouter: true, Name: "LAB-router-debian12-x64"}}}
	if err := migrationAnnotateRouters(stage, m); err != nil {
		t.Fatal(err)
	}
	values, err := migrationReadYAML(config)
	if err != nil {
		t.Fatal(err)
	}
	router := values["router"].(map[interface{}]interface{})
	if router["vm_name"] != "LAB-router-debian12-x64" || router["template"] != "debian-12-x64-server-template" || router["memory"] != 2048 {
		t.Fatal("existing router identity or custom settings lost")
	}
}

func TestMigrationRejectsWireguardHostHooksAndKeyMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "server-private-key"), []byte("original-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, config := range []string{
		"[Interface]\nPrivateKey = original-key\nListenPort = 51820\nPostUp = /opt/ludus/old-host-script\n",
		"[Interface]\nPrivateKey = different-key\nListenPort = 51820\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, "wg0.conf"), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		if err := migrationValidateWireguard(dir, 51820); err == nil {
			t.Fatal("unsafe WireGuard configuration accepted")
		}
	}
}

func TestMigrationReadsLegacyRouterTemplateFromPlaybook(t *testing.T) {
	dir := t.TempDir()
	defaults := filepath.Join(dir, "server-config.yml")
	playbook := filepath.Join(dir, "ludus.yml")
	if err := os.WriteFile(defaults, []byte("default_router_vm_name: '{{ range_id }}-router-debian11-x64'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(playbook, []byte("- name: Deploy the router VM\n  vars:\n    template_vm_name: \"{{ router.template | default('debian-11-x64-server-template') }}\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := migrationRouterTemplate(defaults, playbook)
	if err != nil || got != "debian-11-x64-server-template" {
		t.Fatalf("legacy router template: %q, %v", got, err)
	}
	if err := os.WriteFile(defaults, []byte("default_router_template_name: custom-router-template\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = migrationRouterTemplate(defaults, playbook)
	if err != nil || got != "custom-router-template" {
		t.Fatalf("explicit default must override legacy playbook: %q, %v", got, err)
	}
}

func TestMigrationEnvironmentFilePreservesSecretValues(t *testing.T) {
	input := "# comment\n" +
		` LUDUS_DB_ENCRYPTION_PASSWORD = "literal\n\t\s\q\\\"\$\` + "`" + `" ` + "\n" +
		"LUDUS_SECRET_SINGLE='  literal\\n $HOME %i  '\n" +
		"LUDUS_SECRET_UNQUOTED=  words with\\ spaces\\n and\"quotes\"\\ \t\n" +
		"LUDUS_SECRET_UNICODE=\u00a0secret\u00a0\n" +
		"LUDUS_SECRET_EMPTY=  \t\n"
	want := map[string]string{
		"LUDUS_DB_ENCRYPTION_PASSWORD": "literal\\n\\t\\s\\q\\\"$`",
		"LUDUS_SECRET_SINGLE":          "  literal\\n $HOME %i  ",
		"LUDUS_SECRET_UNQUOTED":        "words with spacesn and\"quotes\" ",
		"LUDUS_SECRET_UNICODE":         "\u00a0secret\u00a0",
		"LUDUS_SECRET_EMPTY":           "",
	}
	got, err := migrationEnvironmentFile(input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("EnvironmentFile changed secret values")
	}
	// The same backslash-n means a newline in systemctl-show's C escaping,
	// but remains two bytes inside an EnvironmentFile's double quotes.
	words, err := migrationWords(`"LUDUS_SECRET_SHOW=line\nbreak"`)
	if err != nil || !reflect.DeepEqual(words, []string{"LUDUS_SECRET_SHOW=line\nbreak"}) {
		t.Fatal("systemctl-show escaping was changed")
	}
	stage := t.TempDir()
	b, err := json.Marshal(map[string]map[string]string{"ludus": got, "ludus-admin": got})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "migration-environments.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrationStageEnvironments(stage); err != nil {
		t.Fatal(err)
	}
	for _, service := range migrationServices {
		b, err := os.ReadFile(filepath.Join(stage, "etc/systemd/system", service+".service.d/90-ludus-migration.conf"))
		if err != nil {
			t.Fatal(err)
		}
		restored := map[string]string{}
		for _, line := range strings.Split(string(b), "\n") {
			assignment, ok := strings.CutPrefix(line, "Environment=")
			if !ok {
				continue
			}
			words, err := migrationWords(assignment)
			if err != nil || len(words) != 1 {
				t.Fatal("invalid migrated environment assignment")
			}
			name, value, _ := strings.Cut(words[0], "=")
			restored[name] = strings.ReplaceAll(value, "%%", "%")
		}
		if !reflect.DeepEqual(restored, want) {
			t.Fatal("staged service environment changed secret values")
		}
	}
}

func TestMigrationEnvironmentFileRejectsUnsupportedForms(t *testing.T) {
	for _, input := range []string{
		"LUDUS_SECRET_TEST=one\\\ntwo\n",
		"LUDUS_SECRET_TEST='one\ntwo'\n",
		"LUDUS_SECRET_TEST=\"one\"suffix\n",
		"OTHER='first\nLUDUS_SECRET_TEST=not-an-assignment\nlast'\n",
		"LUDUS_SECRET_TEST=\x00\n",
		"LUDUS_SECRET_TEST=\xff\n",
	} {
		if _, err := migrationEnvironmentFile(input); err == nil {
			t.Fatal("unsupported EnvironmentFile was accepted")
		}
	}
}

func TestMigrationTLSUsesConfiguredNodeAndCompletePreferredPair(t *testing.T) {
	root := t.TempDir()
	values := map[string]interface{}{"proxmox_node": "configured"}
	files := map[string]string{
		"etc/pve/local/pveproxy-ssl.pem":            "wrong-node-cert",
		"etc/pve/local/pveproxy-ssl.key":            "wrong-node-key",
		"etc/pve/nodes/configured/pveproxy-ssl.pem": "uploaded-cert",
		"etc/pve/nodes/configured/pveproxy-ssl.key": "uploaded-key",
		"etc/pve/nodes/configured/pve-ssl.pem":      "fallback-cert",
		"etc/pve/nodes/configured/pve-ssl.key":      "fallback-key",
	}
	for name, content := range files {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(wantCert, wantKey string) {
		t.Helper()
		cert, key, err := migrationTLS(values, root)
		if err != nil {
			t.Fatal(err)
		}
		for filename, want := range map[string]string{cert: wantCert, key: wantKey} {
			b, err := os.ReadFile(filename)
			if err != nil || string(b) != want {
				t.Fatal("selected TLS identity was not preserved")
			}
		}
	}
	check("uploaded-cert", "uploaded-key")
	if err := os.Remove(filepath.Join(root, "etc/pve/nodes/configured/pveproxy-ssl.key")); err != nil {
		t.Fatal(err)
	}
	check("fallback-cert", "fallback-key")
	values["tls_cert_file"] = filepath.Join(root, "etc/pve/local/pveproxy-ssl.pem")
	values["tls_key_file"] = filepath.Join(root, "etc/pve/local/pveproxy-ssl.key")
	check("wrong-node-cert", "wrong-node-key")
}

func TestMigrationPrivateSourceCredentialsRoundTrip(t *testing.T) {
	oldHome, stage, restored := t.TempDir(), t.TempDir(), t.TempDir()
	credentials := map[string]string{
		".ssh/id_ed25519":  "synthetic-private-key\n",
		".ssh/known_hosts": "git.example ssh-ed25519 synthetic-host-key\n",
		".ssh/config":      "Host git.example\n  IdentityFile ~/.ssh/id_ed25519\n",
		".gitconfig":       "[credential]\n\thelper = store\n",
		".git-credentials": "https://synthetic:password@git.example\n",
	}
	for name, content := range credentials {
		filename := filepath.Join(oldHome, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if name == ".gitconfig" {
			if err := os.Chmod(filename, 0660); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := migrationCollectCredentials(stage, oldHome); err != nil {
		t.Fatal(err)
	}
	inventory, err := migrationInventory(stage)
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "state.tar.gz")
	out, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, file := range inventory {
		header := tar.Header{Name: file.Path, Mode: int64(file.Mode), Size: file.Size, Typeflag: tar.TypeReg}
		if file.Directory {
			header.Typeflag = tar.TypeDir
			header.Size = 0
		}
		if err := tw.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if !file.Directory {
			in, err := os.Open(filepath.Join(stage, file.Path))
			if err != nil {
				t.Fatal(err)
			}
			_, copyErr := io.Copy(tw, in)
			closeErr := in.Close()
			if copyErr != nil || closeErr != nil {
				t.Fatal("archive credential copy failed")
			}
		}
	}
	for _, closer := range []io.Closer{tw, gz, out} {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrationExtract(archive, restored); err != nil {
		t.Fatal(err)
	}
	newHome := t.TempDir()
	for _, name := range migrationCredentials {
		if err := migrationCopyTree(filepath.Join(restored, "home/ludus", name), filepath.Join(newHome, name)); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range credentials {
		filename := filepath.Join(newHome, name)
		b, err := os.ReadFile(filename)
		if err != nil || string(b) != want {
			t.Fatal("private-source credential was lost or changed")
		}
		mode := os.FileMode(0600)
		if name == ".gitconfig" {
			mode = 0660
		}
		st, err := os.Stat(filename)
		if err != nil || st.Mode().Perm() != mode {
			t.Fatal("private-source credential permissions changed")
		}
	}
	if err := os.WriteFile(filepath.Join(oldHome, ".ssh/config"), []byte("IdentityFile "+oldHome+"/.ssh/id_ed25519\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := migrationCollectCredentials("", oldHome); err == nil {
		t.Fatal("unsupported absolute old-home credential reference was accepted")
	}
}

func TestMigrationRejectsInvalidAdminExposure(t *testing.T) {
	for _, value := range []interface{}{"true", 1, nil} {
		_, err := migrationBool(map[string]interface{}{"expose_admin_port": value}, "expose_admin_port")
		if err == nil {
			t.Fatal("non-boolean admin exposure was accepted")
		}
	}
}
