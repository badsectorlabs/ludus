package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func enterprisePackageFixture(t *testing.T, entries map[string]string) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), "ludus-enterprise.zip")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for name, content := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return filename
}

func TestEnterprisePackageAcceptsOnlyTheEnterpriseLayout(t *testing.T) {
	manifest := func(id string) string {
		return `{"schemaVersion":1,"protocolVersion":1,"id":"` + id + `","name":"Ludus Enterprise","version":"1.2.3","level":"system","runAs":"root","executable":"plugin","vmHooks":{}}`
	}
	valid := map[string]string{"plugin.json": manifest("ludus-enterprise"), "plugin": "#!/bin/sh\n", "ui/index.html": "<html></html>"}
	if err := validateEnterprisePackage(enterprisePackageFixture(t, valid)); err != nil {
		t.Fatal(err)
	}
	for name, entries := range map[string]map[string]string{
		"missing executable": {"plugin.json": manifest("ludus-enterprise")},
		"missing manifest":   {"plugin": "#!/bin/sh\n"},
		"other plugin":       {"plugin.json": manifest("ludus-other"), "plugin": "#!/bin/sh\n"},
		"unexpected entry":   {"plugin.json": manifest("ludus-enterprise"), "plugin": "#!/bin/sh\n", "../plugin": "x"},
		"legacy Go plugin":   {"ludus-enterprise.plugin": "binary"},
		"malformed manifest": {"plugin.json": `{"id":"ludus-enterprise"}`, "plugin": "#!/bin/sh\n"},
	} {
		if err := validateEnterprisePackage(enterprisePackageFixture(t, entries)); err == nil {
			t.Errorf("accepted %s", name)
		}
	}
	notZip := filepath.Join(t.TempDir(), "ludus-enterprise.plugin")
	if err := os.WriteFile(notZip, []byte("legacy executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateEnterprisePackage(notZip); err == nil {
		t.Error("accepted a bare executable instead of a package ZIP")
	}
}
