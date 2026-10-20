package ludus

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// InstallFiles writes every file of the given filesystems into
// env.InstallPath, preserving their layout. Directories are created 0755 and
// new files 0644. Only system-level plugins may install files. Writes go
// through an os.Root, so no path or symlink can lead outside the install tree.
func InstallFiles(env Env, filesystems ...fs.FS) error {
	if env.Level != System {
		return errors.New("installing files requires level system")
	}
	if !filepath.IsAbs(env.InstallPath) {
		return fmt.Errorf("install path %q is not absolute", env.InstallPath)
	}
	if err := os.MkdirAll(env.InstallPath, 0o755); err != nil {
		return fmt.Errorf("create install path: %w", err)
	}
	root, err := os.OpenRoot(env.InstallPath)
	if err != nil {
		return fmt.Errorf("open install path: %w", err)
	}
	defer root.Close()

	for index, fsys := range filesystems {
		if fsys == nil {
			continue
		}
		if err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == "." {
				return nil
			}
			destination := filepath.FromSlash(path)
			if entry.IsDir() {
				return root.MkdirAll(destination, 0o755)
			}
			data, err := fs.ReadFile(fsys, path)
			if err != nil {
				return err
			}
			return root.WriteFile(destination, data, 0o644)
		}); err != nil {
			return fmt.Errorf("install filesystem %d: %w", index+1, err)
		}
	}
	return nil
}
