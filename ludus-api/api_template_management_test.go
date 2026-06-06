package ludusapi

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTemplateNameFromHCLUsesSourceVMName(t *testing.T) {
	hclFile := writeTempPackerFile(t, `
variable "vm_name" {
  type    = string
  default = "variable-template"
}

source "proxmox-iso" "test" {
  vm_name = "source-template"
}
`)

	got, err := extractTemplateNameFromHCL(hclFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "source-template" {
		t.Fatalf("expected source vm_name, got %q", got)
	}
}

func TestExtractTemplateNameFromHCLResolvesVariableVMName(t *testing.T) {
	hclFile := writeTempPackerFile(t, `
# stale-comment-template
variable "vm_name" {
  type    = string
  default = "actual-template"
}

source "proxmox-iso" "test" {
  vm_name = "${var.vm_name}"
}
`)

	got, err := extractTemplateNameFromHCL(hclFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "actual-template" {
		t.Fatalf("expected resolved variable vm_name, got %q", got)
	}
}

func TestExtractTemplateNameFromHCLResolvesSourceInterpolation(t *testing.T) {
	hclFile := writeTempPackerFile(t, `
variable "template_prefix" {
  type    = string
  default = "interpolated"
}

source "proxmox-iso" "test" {
  vm_name = "${var.template_prefix}-template"
}
`)

	got, err := extractTemplateNameFromHCL(hclFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "interpolated-template" {
		t.Fatalf("expected interpolated source vm_name, got %q", got)
	}
}

func TestExtractTemplateNameFromHCLFallsBackToVariableVMName(t *testing.T) {
	hclFile := writeTempPackerFile(t, `
variable "vm_name" {
  type    = string
  default = "fallback-template"
}

source "proxmox-iso" "test" {
  os = "l26"
}
`)

	got, err := extractTemplateNameFromHCL(hclFile)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got != "fallback-template" {
		t.Fatalf("expected variable vm_name fallback, got %q", got)
	}
}

func TestExtractTemplateNameFromHCLIgnoresCommentOnlyMatches(t *testing.T) {
	hclFile := writeTempPackerFile(t, `
# comment-only-template

source "proxmox-iso" "test" {
  os = "l26"
}
`)

	got, err := extractTemplateNameFromHCL(hclFile)
	if err == nil || got != "" {
		t.Fatalf("expected error, got nil and %q", got)
	}
}

func writeTempPackerFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "template.pkr.hcl")
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write temp packer file: %v", err)
	}
	return path
}
