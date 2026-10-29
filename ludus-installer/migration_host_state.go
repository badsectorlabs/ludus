package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

func migrationWriteFinalState(destination, before, after, wireguard, leases string) (result error) {
	if before != after || !filepath.IsAbs(before) || filepath.Clean(before) != before || before == "/" {
		return errors.New("original data directory changed or became unavailable before final synchronization")
	}
	resolved, err := filepath.EvalSymlinks(before)
	if err != nil || resolved != before {
		return errors.New("original data directory changed or became unavailable before final synchronization")
	}
	info, err := os.Stat(before)
	if err != nil || !info.IsDir() {
		return errors.New("original data directory changed or became unavailable before final synchronization")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			os.Remove(destination)
		}
	}()
	gzipWriter := gzip.NewWriter(file)
	writer := tar.NewWriter(gzipWriter)
	sources := [][2]string{{before, "opt/ludus/db"}, {wireguard, "etc/wireguard"}}
	if info, err := os.Lstat(leases); err == nil {
		if !info.Mode().IsRegular() {
			writer.Close()
			gzipWriter.Close()
			file.Close()
			return errors.New("dnsmasq leases must be a regular file")
		}
		sources = append(sources, [2]string{leases, "var/lib/misc/dnsmasq.leases"})
	} else if !errors.Is(err, os.ErrNotExist) {
		writer.Close()
		gzipWriter.Close()
		file.Close()
		return err
	}
	for _, source := range sources {
		err = filepath.WalkDir(source[0], func(filename string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("final state contains unsupported special file: %s", filename)
			}
			relative, err := filepath.Rel(source[0], filename)
			if err != nil {
				return err
			}
			name := source[1]
			if relative != "." {
				name = path.Join(name, filepath.ToSlash(relative))
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = name
			header.Uid = 0
			header.Gid = 0
			header.Uname = ""
			header.Gname = ""
			if err = writer.WriteHeader(header); err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				input, err := os.Open(filename)
				if err != nil {
					return err
				}
				_, copyErr := io.Copy(writer, input)
				return errors.Join(copyErr, input.Close())
			}
			return nil
		})
		if err != nil {
			break
		}
	}
	return errors.Join(err, writer.Close(), gzipWriter.Close(), file.Close())
}
func migrationFinalArchiveName(name string, directory bool) bool {
	name = strings.TrimSuffix(name, "/")
	if name == "" || path.Clean(name) != name || strings.Contains(name, "\\") || strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") {
		return false
	}
	if name == "opt/ludus/db" || strings.HasPrefix(name, "opt/ludus/db/") || name == "etc/wireguard" || strings.HasPrefix(name, "etc/wireguard/") {
		return true
	}
	if name == "var/lib/misc/dnsmasq.leases" {
		return !directory
	}
	return directory && (name == "opt" || name == "opt/ludus" || name == "etc" || name == "var" || name == "var/lib" || name == "var/lib/misc")
}
func migrationExtractFinalState(archive, destination string) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		directory := header.Typeflag == tar.TypeDir
		if (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA && !directory) || !migrationFinalArchiveName(header.Name, directory) {
			return fmt.Errorf("unsafe final-state archive member: %q", header.Name)
		}
		name := strings.TrimSuffix(header.Name, "/")
		if seen[name] {
			return fmt.Errorf("duplicate final-state archive member: %s", name)
		}
		seen[name] = true
		target := filepath.Join(destination, filepath.FromSlash(name))
		if directory {
			if err = os.MkdirAll(target, 0700); err != nil {
				return err
			}
			if err = os.Chmod(target, os.FileMode(header.Mode)&0777); err != nil {
				return err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode)&0777)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, reader, header.Size)
		if err = errors.Join(copyErr, out.Close()); err != nil {
			return err
		}
	}
	if _, err = io.Copy(io.Discard, gzipReader); err != nil {
		return err
	}
	for _, root := range []string{"opt/ludus/db", "etc/wireguard"} {
		info, err := os.Stat(filepath.Join(destination, root))
		if err != nil || !info.IsDir() {
			return fmt.Errorf("final-state archive lacks %s", root)
		}
	}
	return nil
}
func migrationChownTree(root, account string) error {
	user, err := user.Lookup(account)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(user.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(user.Gid)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chown(path, uid, gid)
	})
}
func migrationImportFinalState(archive string) error {
	if !fileExists("/opt/ludus/install/.bootstrap-complete") {
		return errors.New("final-state synchronization is only supported inside a bootstrapped Ludus candidate")
	}
	stage, err := os.MkdirTemp("/opt/ludus", ".final-state.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err = migrationExtractFinalState(archive, stage); err != nil {
		return err
	}
	database := filepath.Join(stage, "opt/ludus/db")
	if err = migrationChownTree(database, "ludus"); err != nil {
		return err
	}
	// Stage WireGuard on its own filesystem before the atomic replacement.
	wireguard, err := os.MkdirTemp("/etc", ".wireguard-final.")
	if err != nil {
		return err
	}
	defer os.RemoveAll(wireguard)
	if err = migrationCopyTree(filepath.Join(stage, "etc/wireguard"), wireguard); err != nil {
		return err
	}
	if err = migrationChownTree(wireguard, "root"); err != nil {
		return err
	}
	if err = os.Chmod(wireguard, 0700); err != nil {
		return err
	}
	dbBackup, err := os.MkdirTemp("/opt/ludus", ".previous-db.")
	if err != nil {
		return err
	}
	if err = os.Remove(dbBackup); err != nil {
		return err
	}
	wgBackup, err := os.MkdirTemp("/etc", ".previous-wireguard.")
	if err != nil {
		return err
	}
	if err = os.Remove(wgBackup); err != nil {
		return err
	}
	if err = os.Rename("/opt/ludus/db", dbBackup); err != nil {
		return err
	}
	if err = os.Rename(database, "/opt/ludus/db"); err != nil {
		return errors.Join(err, os.Rename(dbBackup, "/opt/ludus/db"))
	}
	restoreDatabase := func() error {
		if err := os.Rename("/opt/ludus/db", database); err != nil {
			return err
		}
		return os.Rename(dbBackup, "/opt/ludus/db")
	}
	if err = os.Rename("/etc/wireguard", wgBackup); err != nil {
		return errors.Join(err, restoreDatabase())
	}
	if err = os.Rename(wireguard, "/etc/wireguard"); err != nil {
		return errors.Join(err, os.Rename(wgBackup, "/etc/wireguard"), restoreDatabase())
	}
	leases := filepath.Join(stage, "var/lib/misc/dnsmasq.leases")
	if fileExists(leases) {
		if err = os.MkdirAll("/var/lib/misc", 0755); err == nil {
			err = copyFile(leases, "/var/lib/misc/dnsmasq.leases", 0644)
		}
		if err == nil {
			err = migrationChownTree("/var/lib/misc/dnsmasq.leases", "dnsmasq")
		}
		if err != nil {
			return err
		}
	}
	if err = errors.Join(os.RemoveAll(dbBackup), os.RemoveAll(wgBackup)); err != nil {
		return err
	}
	return os.Remove(archive)
}
