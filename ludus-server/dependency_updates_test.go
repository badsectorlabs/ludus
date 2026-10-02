package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDependencyFixture(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestInstallAnsiblePayloadReplacesShippedContentAndRemovesUserShadows(t *testing.T) {
	install, staging := t.TempDir(), t.TempDir()
	const role = "global-roles/vendor.shipped/tasks/main.yml"
	const collection = "global-collections/ansible_collections/vendor/shipped/plugins/modules/module.py"
	for _, name := range []string{role, collection} {
		writeDependencyFixture(t, staging, name, "new release")
		writeDependencyFixture(t, install, "resources/"+name, "old release")
		writeDependencyFixture(t, install, "resources/"+filepath.Dir(name)+"/removed", "obsolete release file")
	}
	for _, user := range []string{"root", "alice", "bob"} {
		writeDependencyFixture(t, install, "users/"+user+"/.ansible/roles/vendor.shipped/tasks/main.yml", "user shadow")
		writeDependencyFixture(t, install, "users/"+user+"/.ansible/collections/ansible_collections/vendor/shipped/plugins/modules/module.py", "user shadow")
		writeDependencyFixture(t, install, "users/"+user+"/.ansible/roles/custom.role/tasks/main.yml", "custom role")
		writeDependencyFixture(t, install, "users/"+user+"/.ansible/collections/ansible_collections/vendor/custom/MANIFEST.json", "custom collection")
	}
	writeDependencyFixture(t, install, "resources/global-roles/custom.role/tasks/main.yml", "custom global role")
	writeDependencyFixture(t, install, "resources/global-collections/ansible_collections/vendor/custom/MANIFEST.json", "custom global collection")
	if err := installAnsiblePayload(install, staging); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{role, collection} {
		data, err := os.ReadFile(filepath.Join(install, "resources", name))
		if err != nil || string(data) != "new release" {
			t.Fatalf("installed %s = %q, %v", name, data, err)
		}
		if _, err := os.Stat(filepath.Join(install, "resources", filepath.Dir(name), "removed")); !os.IsNotExist(err) {
			t.Fatalf("obsolete file survived for %s: %v", name, err)
		}
	}
	for _, user := range []string{"root", "alice", "bob"} {
		for _, name := range []string{"roles/vendor.shipped", "collections/ansible_collections/vendor/shipped"} {
			if _, err := os.Stat(filepath.Join(install, "users", user, ".ansible", name)); !os.IsNotExist(err) {
				t.Fatalf("shipped shadow survived for %s/%s: %v", user, name, err)
			}
		}
		for name, want := range map[string]string{
			"roles/custom.role/tasks/main.yml":                            "custom role",
			"collections/ansible_collections/vendor/custom/MANIFEST.json": "custom collection",
		} {
			data, err := os.ReadFile(filepath.Join(install, "users", user, ".ansible", name))
			if err != nil || string(data) != want {
				t.Fatalf("unrelated user dependency changed: %s/%s: %q, %v", user, name, data, err)
			}
		}
	}
	for name, want := range map[string]string{
		"global-roles/custom.role/tasks/main.yml":                            "custom global role",
		"global-collections/ansible_collections/vendor/custom/MANIFEST.json": "custom global collection",
	} {
		data, err := os.ReadFile(filepath.Join(install, "resources", name))
		if err != nil || string(data) != want {
			t.Fatalf("unrelated global dependency changed: %s: %q, %v", name, data, err)
		}
	}
}

func TestRemoveShippedAnsiblePathRefusesSymlinkedParents(t *testing.T) {
	install, outside := t.TempDir(), t.TempDir()
	writeDependencyFixture(t, outside, "roles/vendor.shipped/tasks/main.yml", "must survive")
	if err := os.MkdirAll(filepath.Join(install, "users/alice"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(install, "users/alice/.ansible")); err != nil {
		t.Fatal(err)
	}
	if err := removeShippedAnsiblePath(install, "users/alice/.ansible/roles/vendor.shipped"); err == nil {
		t.Fatal("accepted a symlinked Ansible home")
	}
	data, err := os.ReadFile(filepath.Join(outside, "roles/vendor.shipped/tasks/main.yml"))
	if err != nil || string(data) != "must survive" {
		t.Fatalf("deleted outside the user installation: %q, %v", data, err)
	}
}

func TestValidateAnsiblePayloadRejectsWrongOrMissingPins(t *testing.T) {
	resources := t.TempDir()
	requirements := []byte("collections:\n  - name: vendor.shipped\n    version: 2.0.0\nroles:\n  - name: vendor.role\n    src: vendor.role\n    version: v3.0.0\n")
	writeDependencyFixture(t, resources, "global-collections/ansible_collections/vendor/shipped/MANIFEST.json", `{"collection_info":{"version":"1.0.0"}}`)
	writeDependencyFixture(t, resources, "global-roles/vendor.role/meta/.galaxy_install_info", "version: v3.0.0\n")
	if err := validateAnsiblePayload(requirements, resources); err == nil || !strings.Contains(err.Error(), "want 2.0.0") {
		t.Fatalf("wrong collection version accepted: %v", err)
	}
	writeDependencyFixture(t, resources, "global-collections/ansible_collections/vendor/shipped/MANIFEST.json", `{"collection_info":{"version":"2.0.0"}}`)
	writeDependencyFixture(t, resources, "global-roles/vendor.role/meta/.galaxy_install_info", "version: v2.0.0\n")
	if err := validateAnsiblePayload(requirements, resources); err == nil || !strings.Contains(err.Error(), "want v3.0.0") {
		t.Fatalf("wrong role version accepted: %v", err)
	}
	if err := os.Remove(filepath.Join(resources, "global-roles/vendor.role/meta/.galaxy_install_info")); err != nil {
		t.Fatal(err)
	}
	if err := validateAnsiblePayload(requirements, resources); !os.IsNotExist(err) {
		t.Fatalf("missing role accepted: %v", err)
	}
}
