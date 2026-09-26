package ludusapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDefaultSourceBSLSeedOnlineUsesGitHub(t *testing.T) {
	seed, err := resolveDefaultSourceBSLSeed(false, filepath.Join(t.TempDir(), "missing.tar.gz"))
	if err != nil {
		t.Fatalf("resolve online seed: %v", err)
	}
	if seed.sourceType != "git" {
		t.Fatalf("expected git source, got %q", seed.sourceType)
	}
	if seed.sourceURL != defaultSourceBSLURL {
		t.Fatalf("expected default GitHub URL %q, got %q", defaultSourceBSLURL, seed.sourceURL)
	}
	if seed.archivePath != "" {
		t.Fatalf("online source unexpectedly uses archive %q", seed.archivePath)
	}
}

func TestResolveDefaultSourceBSLSeedAirgappedUsesBundle(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "ludus-source-bsl.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive fixture"), 0600); err != nil {
		t.Fatalf("write archive fixture: %v", err)
	}

	seed, err := resolveDefaultSourceBSLSeed(true, archivePath)
	if err != nil {
		t.Fatalf("resolve air-gapped seed: %v", err)
	}
	if seed.sourceType != "upload" {
		t.Fatalf("expected upload source, got %q", seed.sourceType)
	}
	if seed.sourceURL != "" {
		t.Fatalf("air-gapped source must not have a remote URL, got %q", seed.sourceURL)
	}
	if seed.archivePath != archivePath {
		t.Fatalf("expected archive %q, got %q", archivePath, seed.archivePath)
	}
}

func TestResolveDefaultSourceBSLSeedAirgappedRequiresBundle(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "missing.tar.gz")
	_, err := resolveDefaultSourceBSLSeed(true, archivePath)
	if err == nil {
		t.Fatal("expected missing bundled archive to fail")
	}
	if !strings.Contains(err.Error(), "bundled source archive is unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}
