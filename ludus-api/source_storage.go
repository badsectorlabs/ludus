package ludusapi

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func SourceCheckoutDir(sourceRecordID string) string {
	return filepath.Join(ludusInstallPath, "sources", sourceRecordID)
}

func CloneOrUpdateGit(checkoutDir, gitURL, ref string) error {
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git is required to register git-backed sources but was not found in PATH; install git or use a tarball/upload source instead")
	}
	if ref == "" {
		ref = "HEAD"
	}
	if _, err := os.Stat(filepath.Join(checkoutDir, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(checkoutDir), 0755); err != nil {
			return err
		}
		args := []string{"clone", "--depth", "1"}
		if ref != "HEAD" {
			args = append(args, "--branch", ref)
		}
		args = append(args, gitURL, checkoutDir)
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnvWithSafeDirectory()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git clone failed: %s: %w", strings.TrimSpace(string(out)), err)
		}
		return nil
	} else if err != nil {
		return err
	}
	for _, args := range [][]string{
		{"-C", checkoutDir, "fetch", "--depth", "1", "origin", ref},
		{"-C", checkoutDir, "checkout", ref},
		{"-C", checkoutDir, "reset", "--hard", "FETCH_HEAD"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnvWithSafeDirectory()
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v failed: %s: %w", args, strings.TrimSpace(string(out)), err)
		}
	}
	return nil
}

// gitEnvWithSafeDirectory inherits the service environment (HOME included —
// systemd sets it from User=ludus) and injects safe.directory=* via env
// because `git -c safe.directory=*` does not propagate to subprocesses git
// spawns. Any gitconfig, credential helpers, or SSH keys configured for the
// `ludus` user are picked up normally; private-repo support is therefore an
// operator setup task (see docs/using-ludus/sources.md), not a code change.
func gitEnvWithSafeDirectory() []string {
	return append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=*",
	)
}

// ExtractArchive replaces checkoutDir's contents with the tar.gz/tgz/zip at
// archivePath. Files are written 0644/0755 regardless of recorded mode bits —
// blueprint sources must not contain executables.
func ExtractArchive(checkoutDir, archivePath string) error {
	if err := os.RemoveAll(checkoutDir); err != nil {
		return err
	}
	if err := os.MkdirAll(checkoutDir, 0755); err != nil {
		return err
	}
	low := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(low, ".tar.gz"), strings.HasSuffix(low, ".tgz"):
		return extractTarGz(checkoutDir, archivePath)
	case strings.HasSuffix(low, ".zip"):
		return extractZip(checkoutDir, archivePath)
	}
	return fmt.Errorf("unsupported archive type %q (expected .tar.gz, .tgz, or .zip)", filepath.Ext(archivePath))
}

// maxExtractedArchiveBytes is 10× the 50 MB compressed cap (archiveOverLimit) to defeat archive bombs.
const maxExtractedArchiveBytes = 500 * 1024 * 1024

var hardenedExtractOptions = ExtractOptions{
	MaxBytes:        maxExtractedArchiveBytes,
	StripSingleRoot: true,
	RejectDangerousEntries: true,
}

func extractTarGz(dest, archive string) error {
	return ExtractTarGzFile(archive, dest, hardenedExtractOptions)
}

func extractZip(dest, archive string) error {
	return ExtractZipFile(archive, dest, hardenedExtractOptions)
}
