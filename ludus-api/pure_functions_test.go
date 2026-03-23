// Phase 1: Pure function tests — no mocking, no DB, no setup beyond TestMain.
package ludusapi

import (
	"strings"
	"testing"
)

func TestValidateBlueprintID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		// Valid
		{"simple", "my-blueprint", false},
		{"with slash", "team/windows", false},
		{"two slashes", "org/team/blueprint", false},

		// Invalid
		{"empty", "", true},
		{"starts with number", "1blueprint", true},
		{"three slashes", "a/b/c/d", true},
		{"special chars", "blueprint@1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBlueprintID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateBlueprintID(%q) error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	original := "sensitive-proxmox-token-value"

	encrypted, err := EncryptStringForDatabase(original)
	if err != nil {
		t.Fatalf("EncryptStringForDatabase failed: %v", err)
	}
	if encrypted == original {
		t.Error("encrypted value should differ from original")
	}

	decrypted, err := DecryptStringFromDatabase(encrypted)
	if err != nil {
		t.Fatalf("DecryptStringFromDatabase failed: %v", err)
	}
	if decrypted != original {
		t.Errorf("round-trip failed: got %q, want %q", decrypted, original)
	}
}

func TestApplyBlockInFile_InsertAndReplace(t *testing.T) {
	marker := "# {mark} LUDUS MANAGED BLOCK test"

	// Insert into file with no existing block
	original := "line1\nline2\n"
	result, changed := applyBlockInFile(original, marker, "new-content", true)
	if !changed {
		t.Error("expected changed=true when inserting new block")
	}
	for _, want := range []string{"# BEGIN LUDUS MANAGED BLOCK test", "new-content", "# END LUDUS MANAGED BLOCK test"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}

	// Replace existing block
	result2, changed2 := applyBlockInFile(result, marker, "updated-content", true)
	if !changed2 {
		t.Error("expected changed=true when replacing block")
	}
	if strings.Contains(result2, "new-content") {
		t.Error("old block content should have been replaced")
	}
	if !strings.Contains(result2, "updated-content") {
		t.Error("new block content should be present")
	}
}
