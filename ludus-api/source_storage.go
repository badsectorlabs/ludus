package ludusapi

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func SourceCheckoutDir(sourceRecordID string) string {
	return filepath.Join(ludusInstallPath, "sources", sourceRecordID)
}

func CloneOrUpdateGit(checkoutDir, gitURL, ref string) error {
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

// gitEnvWithSafeDirectory: the systemd unit runs without HOME so user-level
// gitconfig isn't read. `git -c safe.directory=*` doesn't propagate to
// subprocesses git spawns either. The env vars below are picked up by every
// git in the process tree.
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

// detectSingleRoot returns "" when the archive is flat or "./"-rooted (the
// shape `tar -C dir czf foo.tgz .` produces); otherwise returns the leading
// directory segment shared by every entry.
func detectSingleRoot(names []string) string {
	root := ""
	for _, n := range names {
		n = strings.TrimPrefix(n, "./")
		if n == "" {
			continue
		}
		first := n
		if idx := strings.Index(n, "/"); idx >= 0 {
			first = n[:idx]
		}
		if first == "." || first == "" {
			return ""
		}
		if root == "" {
			root = first
			continue
		}
		if first != root {
			return ""
		}
	}
	return root
}

func stripPrefixSegment(name, segment string) string {
	name = strings.TrimPrefix(name, "./")
	if segment == "" {
		return name
	}
	prefix := segment + "/"
	switch {
	case name == segment:
		return ""
	case strings.HasPrefix(name, prefix):
		return strings.TrimPrefix(name, prefix)
	default:
		return name
	}
}

// maxExtractedArchiveBytes caps the total decompressed size of a source
// archive to defeat archive-bomb uploads. The compressed cap (handled by
// archiveOverLimit) is 50 MB; we allow 10× decompressed.
const maxExtractedArchiveBytes = 500 * 1024 * 1024

// safeExtractPath cleans name, rejects absolute paths and traversal, and
// returns the absolute target under dest. Returns "" when the cleaned name is
// empty (e.g. a "./"-only entry) so callers can skip.
func safeExtractPath(dest, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("archive contains unsafe path %q", name)
	}
	target := filepath.Join(dest, cleaned)
	if !strings.HasPrefix(target+string(filepath.Separator), dest+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes destination", name)
	}
	return target, nil
}

func extractTarGz(dest, archive string) error {
	names, err := tarGzEntryNames(archive)
	if err != nil {
		return err
	}
	root := detectSingleRoot(names)

	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	// Cap total decompressed bytes to defeat archive bombs.
	limited := &io.LimitedReader{R: gz, N: maxExtractedArchiveBytes + 1}
	tr := tar.NewReader(limited)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Reject anything that isn't a regular file or directory entry —
		// symlinks/hardlinks could escape the bundle dir.
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeReg:
		default:
			return fmt.Errorf("archive contains unsupported entry type (%c) for %q", hdr.Typeflag, hdr.Name)
		}
		stripped := stripPrefixSegment(hdr.Name, root)
		target, err := safeExtractPath(dest, stripped)
		if err != nil {
			return err
		}
		if target == "" {
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
			if limited.N <= 0 {
				return fmt.Errorf("archive exceeds %d-byte decompressed limit", maxExtractedArchiveBytes)
			}
		}
	}
}

func tarGzEntryNames(archive string) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
		names = append(names, hdr.Name)
	}
}

func extractZip(dest, archive string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	names := make([]string, 0, len(r.File))
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	root := detectSingleRoot(names)

	var written int64
	for _, f := range r.File {
		// Reject entries that aren't regular files or directories — zip's
		// FileInfo.Mode() exposes the symlink bit on archives that carry it.
		if f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symlink %q (unsupported)", f.Name)
		}
		stripped := stripPrefixSegment(f.Name, root)
		target, pathErr := safeExtractPath(dest, stripped)
		if pathErr != nil {
			return pathErr
		}
		if target == "" {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		// Cap per-entry copy by the remaining bomb budget so an oversized
		// member fails fast rather than filling the disk.
		remaining := maxExtractedArchiveBytes - written + 1
		n, err := io.CopyN(out, rc, remaining)
		rc.Close()
		out.Close()
		if err != nil && err != io.EOF {
			return err
		}
		written += n
		if written > maxExtractedArchiveBytes {
			return fmt.Errorf("archive exceeds %d-byte decompressed limit", maxExtractedArchiveBytes)
		}
	}
	return nil
}
