package ludusapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitRun runs a git command in dir and fails the test on error.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v (in %s) failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// newGitRepo initializes a git repo at dir with a deterministic identity.
func newGitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.email", "test@ludus.local")
	gitRun(t, dir, "config", "user.name", "ludus-test")
}

// writeRepoFile writes content to dir/rel, creating parent directories.
func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// allowFileProtocol points git's global/system config at a throwaway file that
// enables the local file:// transport for submodules (disabled by default since
// git 2.38) and trusts all directories. Scoped to the test via t.Setenv, so the
// git subprocesses spawned by CloneOrUpdateGit (which inherit os.Environ) pick
// it up too.
func allowFileProtocol(t *testing.T) {
	t.Helper()
	gc := filepath.Join(t.TempDir(), "gitconfig")
	cfg := "[protocol \"file\"]\n\tallow = always\n" +
		"[safe]\n\tdirectory = *\n" +
		"[user]\n\temail = test@ludus.local\n\tname = ludus-test\n"
	if err := os.WriteFile(gc, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

// makeSourceWithSubmodule builds a "role" repo and a "source" repo that vendors
// it as a submodule at roles/myrole, and returns a file:// URL for the source.
func makeSourceWithSubmodule(t *testing.T) (sourceURL, roleRepo string) {
	t.Helper()
	roleRepo = filepath.Join(t.TempDir(), "role")
	newGitRepo(t, roleRepo)
	writeRepoFile(t, roleRepo, "meta/main.yml", "galaxy_info:\n  role_name: myrole\n")
	gitRun(t, roleRepo, "add", "-A")
	gitRun(t, roleRepo, "commit", "-q", "-m", "init role")

	srcRepo := filepath.Join(t.TempDir(), "source")
	newGitRepo(t, srcRepo)
	writeRepoFile(t, srcRepo, "source.yml", "manifest_version: 1\nname: test\n")
	gitRun(t, srcRepo, "add", "-A")
	gitRun(t, srcRepo, "commit", "-q", "-m", "init source")
	gitRun(t, srcRepo, "submodule", "add", "file://"+roleRepo, "roles/myrole")
	gitRun(t, srcRepo, "commit", "-q", "-m", "add role submodule")

	return "file://" + srcRepo, roleRepo
}

func TestCloneOrUpdateGitRecursesSubmodulesOnClone(t *testing.T) {
	allowFileProtocol(t)
	sourceURL, _ := makeSourceWithSubmodule(t)

	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := CloneOrUpdateGit(checkout, sourceURL, ""); err != nil {
		t.Fatalf("CloneOrUpdateGit: %v", err)
	}

	// The submodule content must be materialized, not an empty placeholder dir.
	got := filepath.Join(checkout, "roles", "myrole", "meta", "main.yml")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected submodule file %s to exist after clone: %v", got, err)
	}
}

func TestCloneOrUpdateGitSyncsSubmodulesOnUpdate(t *testing.T) {
	allowFileProtocol(t)
	sourceURL, roleRepo := makeSourceWithSubmodule(t)
	srcRepo := sourceURL[len("file://"):] // local path of the source repo

	// First add: clone path (already recurses after Task 1).
	checkout := filepath.Join(t.TempDir(), "checkout")
	if err := CloneOrUpdateGit(checkout, sourceURL, ""); err != nil {
		t.Fatalf("initial CloneOrUpdateGit: %v", err)
	}

	// Advance the role repo, then bump the source's submodule pointer to it.
	writeRepoFile(t, roleRepo, "tasks/main.yml", "- debug: msg=hi\n")
	gitRun(t, roleRepo, "add", "-A")
	gitRun(t, roleRepo, "commit", "-q", "-m", "add tasks")
	subInSource := filepath.Join(srcRepo, "roles", "myrole")
	gitRun(t, subInSource, "fetch", "-q", "origin")
	gitRun(t, subInSource, "checkout", "-q", "origin/main")
	gitRun(t, srcRepo, "add", "roles/myrole")
	gitRun(t, srcRepo, "commit", "-q", "-m", "bump role submodule")

	// Second add: update path must sync the submodule to the new commit.
	if err := CloneOrUpdateGit(checkout, sourceURL, ""); err != nil {
		t.Fatalf("update CloneOrUpdateGit: %v", err)
	}

	got := filepath.Join(checkout, "roles", "myrole", "tasks", "main.yml")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("expected updated submodule file %s after re-sync: %v", got, err)
	}
}
