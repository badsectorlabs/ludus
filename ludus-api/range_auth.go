package ludusapi

import (
	"bytes"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

type ludusAuthMaterialPaths struct {
	RangesDir      string
	MachineCredDir string
	SSHKeyDir      string
	SSHKeyPath     string
	SSHPubKeyPath  string
}

var authMaterialMu sync.Mutex

// ensureLudusAuthMaterial publishes a complete credential set atomically. Existing
// material is never regenerated: a failed deployment must not rotate VM identities.
func ensureLudusAuthMaterial(rangeID string) error {
	paths, err := authMaterialPathsForRange(rangeID)
	if err != nil {
		return err
	}
	return ensureAuthMaterial(paths)
}

func ensureAuthMaterial(paths ludusAuthMaterialPaths) error {
	authMaterialMu.Lock()
	defer authMaterialMu.Unlock()
	if _, err := os.Lstat(paths.MachineCredDir); err == nil {
		if err := validateAuthMaterial(paths); err != nil {
			return err
		}
		if err := setAuthMaterialOwner(paths); err != nil {
			return err
		}
		return markAuthMaterialInitialized(paths)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := validateAuthParents(paths); err != nil {
		return err
	}
	if _, err := os.Lstat(authMaterialMarkerPath(paths)); err == nil {
		return fmt.Errorf("previously provisioned machine credentials are missing; restore the original material")
	} else if !os.IsNotExist(err) {
		return err
	}

	stagingDir, err := os.MkdirTemp(filepath.Dir(paths.MachineCredDir), ".machine-credentials-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingDir)
	stagingPaths := authMaterialPaths(paths.RangesDir, stagingDir)
	if err := generateAuthMaterial(stagingPaths); err != nil {
		return err
	}
	if err := validateAuthMaterial(stagingPaths); err != nil {
		return err
	}
	if err := setAuthMaterialOwner(stagingPaths); err != nil {
		return err
	}
	if err := os.Rename(stagingDir, paths.MachineCredDir); err != nil {
		// Another server process may have won publication. Never replace its keys.
		if validationErr := validateAuthMaterial(paths); validationErr != nil {
			return fmt.Errorf("failed to publish machine credentials: %w", err)
		}
	}
	return markAuthMaterialInitialized(paths)
}

func authMaterialMarkerPath(paths ludusAuthMaterialPaths) string {
	return filepath.Join(filepath.Dir(paths.MachineCredDir), ".machine-credentials-initialized")
}

func markAuthMaterialInitialized(paths ludusAuthMaterialPaths) error {
	marker, err := os.OpenFile(authMaterialMarkerPath(paths), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	syncErr := marker.Sync()
	closeErr := marker.Close()
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	dir, err := os.Open(filepath.Dir(paths.MachineCredDir))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func authMaterialPathsForRange(rangeID string) (ludusAuthMaterialPaths, error) {
	if !regexp.MustCompile(ProxmoxPoolNameRegexString).MatchString(rangeID) {
		return ludusAuthMaterialPaths{}, fmt.Errorf("invalid range ID %q", rangeID)
	}

	rangesDir := filepath.Join(ludusInstallPath, "ranges")
	machineCredDir := filepath.Join(rangesDir, filepath.FromSlash(rangeID), "machine-credentials")
	return authMaterialPaths(rangesDir, machineCredDir), nil
}

func authMaterialPaths(rangesDir, machineCredDir string) ludusAuthMaterialPaths {
	sshKeyDir := filepath.Join(machineCredDir, "ssh")
	return ludusAuthMaterialPaths{
		RangesDir:      rangesDir,
		MachineCredDir: machineCredDir,
		SSHKeyDir:      sshKeyDir,
		SSHKeyPath:     filepath.Join(sshKeyDir, "ludus_ed25519"),
		SSHPubKeyPath:  filepath.Join(sshKeyDir, "ludus_ed25519.pub"),
	}
}

// MachineCredentialsDirForRange returns only complete, valid existing SSH keys.
// It never generates keys or changes permissions.
func MachineCredentialsDirForRange(rangeID string) (string, error) {
	paths, err := authMaterialPathsForRange(rangeID)
	if err != nil {
		return "", err
	}
	if err := validateAuthMaterial(paths); err != nil {
		return "", err
	}
	return paths.MachineCredDir, nil
}

func generateAuthMaterial(paths ludusAuthMaterialPaths) error {
	if err := os.Mkdir(paths.SSHKeyDir, 0700); err != nil {
		return err
	}
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	sshPrivKey, err := ssh.MarshalPrivateKey(privKey, "ludus")
	if err != nil {
		return err
	}
	sshPubKey, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return err
	}
	files := []struct {
		path string
		data []byte
		mode os.FileMode
	}{
		{paths.SSHKeyPath, pem.EncodeToMemory(sshPrivKey), 0600},
		{paths.SSHPubKeyPath, ssh.MarshalAuthorizedKey(sshPubKey), 0644},
	}
	for _, file := range files {
		f, err := os.OpenFile(file.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.mode)
		if err != nil {
			return err
		}
		_, writeErr := f.Write(file.data)
		syncErr := f.Sync()
		closeErr := f.Close()
		if writeErr != nil {
			return writeErr
		}
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func validateAuthParents(paths ludusAuthMaterialPaths) error {
	// Reject symlinked range directories as well as symlinked credential entries.
	base := paths.RangesDir
	relative, err := filepath.Rel(base, filepath.Dir(paths.MachineCredDir))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("machine credentials are outside the ranges directory")
	}
	dir := base
	for _, part := range append([]string{"."}, strings.Split(relative, string(filepath.Separator))...) {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("credential parent %s is not a directory", dir)
		}
	}
	return nil
}

func validateAuthMaterial(paths ludusAuthMaterialPaths) error {
	if err := validateAuthParents(paths); err != nil {
		return err
	}
	for _, dir := range []string{paths.MachineCredDir, paths.SSHKeyDir} {
		info, err := os.Lstat(dir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("credential directory %s must be a private directory (0700)", dir)
		}
	}
	for _, path := range []string{paths.SSHKeyPath, paths.SSHPubKeyPath} {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 64*1024 || info.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("invalid credential file %s", path)
		}
		if path == paths.SSHKeyPath && info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("private key %s must not be accessible by group or other users", path)
		}
	}
	privatePEM, err := os.ReadFile(paths.SSHKeyPath)
	if err != nil {
		return err
	}
	privateKey, err := ssh.ParseRawPrivateKey(privatePEM)
	if err != nil {
		return fmt.Errorf("invalid SSH private key: %w", err)
	}
	if _, ok := privateKey.(*ed25519.PrivateKey); !ok {
		return fmt.Errorf("SSH private key must be Ed25519")
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return err
	}
	publicBytes, err := os.ReadFile(paths.SSHPubKeyPath)
	if err != nil {
		return err
	}
	publicKey, _, options, rest, err := ssh.ParseAuthorizedKey(publicBytes)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || !bytes.Equal(publicKey.Marshal(), signer.PublicKey().Marshal()) {
		return fmt.Errorf("SSH public and private keys do not match")
	}
	return nil
}

func setAuthMaterialOwner(paths ludusAuthMaterialPaths) error {
	if os.Geteuid() != 0 {
		return nil
	}
	for _, path := range []string{paths.MachineCredDir, paths.SSHKeyDir, paths.SSHKeyPath, paths.SSHPubKeyPath} {
		if err := changeFileOwner(path, "ludus"); err != nil {
			return fmt.Errorf("failed to make machine credentials readable by ludus: %w", err)
		}
	}
	return nil
}
