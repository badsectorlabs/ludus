package ludusapi

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractOptions tunes the unified archive extractor. Zero value is the
// permissive legacy behavior; harden explicitly for untrusted uploads.
type ExtractOptions struct {
	// MaxBytes caps total decompressed bytes; 0 disables.
	MaxBytes int64

	// StripSingleRoot drops a leading common directory when every entry
	// shares it (the shape `tar czf out.tgz ./dir` produces).
	StripSingleRoot bool

	// RejectSymlinks fails extraction on symlink/hardlink entries.
	RejectSymlinks bool
}

func ExtractTarFile(tarPath, destDir string, opts ExtractOptions) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %w", err)
	}
	defer file.Close()
	return extractTarReader(tar.NewReader(file), destDir, opts)
}

func ExtractTarGzFile(archivePath, destDir string, opts ExtractOptions) error {
	root, rootErr := tarGzSingleRoot(archivePath, opts.StripSingleRoot)
	if rootErr != nil {
		return rootErr
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	var src io.Reader = gz
	var capBudget *int64
	if opts.MaxBytes > 0 {
		// +1 slack so we can detect "exceeded" instead of stopping exactly at the cap.
		limited := &io.LimitedReader{R: gz, N: opts.MaxBytes + 1}
		src = limited
		capBudget = &limited.N
	}

	return extractTarReaderWithRoot(tar.NewReader(src), destDir, opts, root, capBudget)
}

func ExtractZipFile(archivePath, destDir string, opts ExtractOptions) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	root := ""
	if opts.StripSingleRoot {
		names := make([]string, 0, len(r.File))
		for _, f := range r.File {
			names = append(names, f.Name)
		}
		root = detectSingleRoot(names)
	}

	var written int64
	for _, f := range r.File {
		if opts.RejectSymlinks && f.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains symlink %q (unsupported)", f.Name)
		}
		stripped := stripPrefixSegment(f.Name, root)
		target, pathErr := safeExtractPath(destDir, stripped)
		if pathErr != nil {
			return pathErr
		}
		if target == "" {
			continue
		}
		if isMacOSAppleDouble(stripped) {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		out, oErr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if oErr != nil {
			return oErr
		}
		rc, rErr := f.Open()
		if rErr != nil {
			out.Close()
			return rErr
		}
		var n int64
		var copyErr error
		if opts.MaxBytes > 0 {
			remaining := opts.MaxBytes - written + 1
			n, copyErr = io.CopyN(out, rc, remaining)
		} else {
			n, copyErr = io.Copy(out, rc)
		}
		rc.Close()
		out.Close()
		if copyErr != nil && copyErr != io.EOF {
			return copyErr
		}
		written += n
		if opts.MaxBytes > 0 && written > opts.MaxBytes {
			return fmt.Errorf("archive exceeds %d-byte decompressed limit", opts.MaxBytes)
		}
	}
	return nil
}

// extractTarReader handles a pre-opened *tar.Reader. StripSingleRoot
// requires a pre-scan that streaming readers can't do; use ExtractTarFile
// or ExtractTarGzFile for that.
func extractTarReader(tr *tar.Reader, destDir string, opts ExtractOptions) error {
	if opts.StripSingleRoot {
		return fmt.Errorf("StripSingleRoot needs a seekable archive; use ExtractTarFile or ExtractTarGzFile")
	}
	return extractTarReaderWithRoot(tr, destDir, opts, "", nil)
}

func extractTarReaderWithRoot(tr *tar.Reader, destDir string, opts ExtractOptions, root string, capBudget *int64) error {
	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return fmt.Errorf("failed to resolve destination directory: %w", err)
	}
	absDest = filepath.Clean(absDest)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("malformed tar: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeReg:
			// supported
		case tar.TypeSymlink, tar.TypeLink:
			if opts.RejectSymlinks {
				return fmt.Errorf("archive contains unsupported entry type (%c) for %q", hdr.Typeflag, hdr.Name)
			}
			continue
		default:
			if opts.RejectSymlinks {
				return fmt.Errorf("archive contains unsupported entry type (%c) for %q", hdr.Typeflag, hdr.Name)
			}
			continue
		}

		stripped := stripPrefixSegment(hdr.Name, root)
		target, pathErr := safeExtractPath(absDest, stripped)
		if pathErr != nil {
			return pathErr
		}
		if target == "" {
			continue
		}
		if isMacOSAppleDouble(stripped) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			out.Close()
			if capBudget != nil && *capBudget <= 0 {
				return fmt.Errorf("archive exceeds %d-byte decompressed limit", opts.MaxBytes)
			}
		}
	}
}

// safeExtractPath returns "" for empty/dot-only names so callers can skip them.
func safeExtractPath(destDir, name string) (string, error) {
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == "" {
		return "", nil
	}
	if filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return "", fmt.Errorf("archive contains unsafe path %q", name)
	}
	target := filepath.Join(destDir, cleaned)
	if !strings.HasPrefix(target+string(filepath.Separator), destDir+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes destination", name)
	}
	return target, nil
}

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

// stripPrefixSegment returns "" for the segment itself so callers skip the standalone root entry.
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

func isMacOSAppleDouble(name string) bool {
	return strings.HasPrefix(filepath.Base(name), "._")
}

// tarGzSingleRoot reads the archive twice (enumerate then extract); fine
// because the caller has already capped the compressed size.
func tarGzSingleRoot(archivePath string, stripSingleRoot bool) (string, error) {
	if !stripSingleRoot {
		return "", nil
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		names = append(names, hdr.Name)
	}
	return detectSingleRoot(names), nil
}
