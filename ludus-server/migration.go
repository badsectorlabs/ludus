package main

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/tools/security"
	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v2"
)

// Version 1 is a state archive, not an installation backup. Runtime payloads
// are always supplied by the new appliance, never by this archive.
type migrationRange struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
}
type migrationVM struct {
	VMID        int    `json:"vmid"`
	RangeNumber int    `json:"range_number"`
	IsRouter    bool   `json:"is_router"`
	Name        string `json:"name"`
}
type migrationFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Mode      uint32 `json:"mode"`
	Directory bool   `json:"directory,omitempty"`
}
type migrationManifest struct {
	Version                int              `json:"version"`
	Ranges                 []migrationRange `json:"ranges"`
	VMs                    []migrationVM    `json:"vms"`
	Port                   int              `json:"port"`
	AdminPort              int              `json:"admin_port"`
	ExposeAdminPort        bool             `json:"expose_admin_port"`
	WireguardPort          int              `json:"wireguard_port"`
	WireguardEndpoint      string           `json:"wireguard_endpoint"`
	ProxmoxUserRealm       string           `json:"proxmox_user_realm"`
	ProxmoxVMStoragePool   string           `json:"proxmox_vm_storage_pool"`
	ProxmoxVMStorageFormat string           `json:"proxmox_vm_storage_format"`
	ProxmoxISOStoragePool  string           `json:"proxmox_iso_storage_pool"`
	RequiresPlugin         bool             `json:"requires_plugin"`
	RouterTemplate         string           `json:"router_template"`
	CustomTLS              bool             `json:"custom_tls"`
	Files                  []migrationFile  `json:"files,omitempty"`
}

var migrationTrees = []string{"ranges", "users", "templates", "resources", "blueprints", "sources", "tls"}
var migrationLooseFiles = []string{"license.lic", "cert.pem", "key.pem"}
var migrationCredentials = []string{".ssh", ".gitconfig", ".git-credentials"}
var migrationServices = []string{"ludus", "ludus-admin"}
var migrationID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*(/[A-Za-z0-9_-]+){0,2}$`)
var migrationEnvName = regexp.MustCompile(`^(LUDUS_DB_ENCRYPTION_PASSWORD|LUDUS_SECRET_[A-Za-z0-9_]+)$`)

func migrationReadYAML(filename string) (map[string]interface{}, error) {
	b, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var values map[string]interface{}
	if yaml.UnmarshalStrict(b, &values) != nil || values == nil {
		return nil, fmt.Errorf("invalid YAML mapping in %s", filename)
	}
	return values, nil
}
func migrationString(values map[string]interface{}, name, fallback string) (string, error) {
	v, ok := values[name]
	if !ok {
		return fallback, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("configuration %s must be a string", name)
	}
	return s, nil
}
func migrationPort(values map[string]interface{}, name string, fallback int) (int, error) {
	v, ok := values[name]
	if !ok {
		return fallback, nil
	}
	n, ok := v.(int)
	if !ok || n < 1 || n > 65535 {
		return 0, fmt.Errorf("configuration %s must be a port between 1 and 65535", name)
	}
	return n, nil
}
func migrationBool(values map[string]interface{}, name string) (bool, error) {
	v, ok := values[name]
	if !ok {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("configuration %s must be a boolean", name)
	}
	return b, nil
}
func migrationDataDir(values map[string]interface{}) (string, error) {
	dir, err := migrationString(values, "data_directory", "/opt/ludus/db")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" || dir == "/opt" || dir == "/opt/ludus" {
		return "", errors.New("data_directory must be an absolute dedicated database directory")
	}
	return dir, nil
}
func migrationRequireFile(filename string) error {
	st, err := os.Stat(filename)
	if err != nil {
		return fmt.Errorf("required migration file %s: %w", filename, err)
	}
	if !st.Mode().IsRegular() || st.Size() == 0 {
		return fmt.Errorf("required migration file %s is empty or not regular", filename)
	}
	return nil
}

func migrationStopped() error {
	for _, service := range migrationServices {
		b, err := exec.Command("systemctl", "show", service, "--property=ActiveState", "--value").Output()
		if err != nil {
			return fmt.Errorf("cannot inspect %s service state", service)
		}
		state := strings.TrimSpace(string(b))
		if state != "inactive" && state != "failed" {
			return fmt.Errorf("stop %s before transferring migration state (state: %s)", service, state)
		}
	}
	// Child jobs outlive API requests and may still modify range/template state.
	cmd := exec.Command("pgrep", "-f", "(^|/)(ansible-playbook|ansible-galaxy|packer)( |$)")
	if err := cmd.Run(); err == nil {
		return errors.New("Ansible or Packer is still running; finish or stop jobs before migration")
	} else if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
		return errors.New("cannot check for active Ansible/Packer jobs")
	}
	return nil
}

// Copy links as regular data only when their target stays within this source
// tree. Archives never contain links, devices, or sockets. SSH control sockets
// are ephemeral; all other unsupported file types are rejected.
func migrationCopyTree(src, dst string) error {
	root, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	var visit func(string, string, map[string]bool) error
	visit = func(from, to string, ancestors map[string]bool) error {
		resolved, err := filepath.EvalSymlinks(from)
		if err != nil {
			return err
		}
		if resolved != root && !strings.HasPrefix(resolved, root+string(os.PathSeparator)) {
			return fmt.Errorf("migration symlink escapes source tree: %s", from)
		}
		st, err := os.Stat(resolved)
		if err != nil {
			return err
		}
		if st.IsDir() {
			if ancestors[resolved] {
				return fmt.Errorf("migration symlink cycle: %s", from)
			}
			ancestors[resolved] = true
			defer delete(ancestors, resolved)
			if err := os.MkdirAll(to, st.Mode().Perm()|0700); err != nil {
				return err
			}
			entries, err := os.ReadDir(resolved)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err := visit(filepath.Join(resolved, entry.Name()), filepath.Join(to, entry.Name()), ancestors); err != nil {
					return err
				}
			}
			return os.Chmod(to, st.Mode().Perm())
		}
		if st.Mode()&os.ModeSocket != 0 && strings.Contains(from, "/.ansible/cp/") {
			return nil
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("unsupported migration file type: %s", from)
		}
		return migrationCopyFile(resolved, to, st.Mode().Perm())
	}
	return visit(src, dst, map[string]bool{})
}
func migrationCopyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err != nil {
		return err
	}
	return closeErr
}
func migrationOptionalCopy(src, dst string) error {
	if _, err := os.Lstat(src); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return migrationCopyTree(src, dst)
}

func migrationDatabase(root string, values map[string]interface{}) (migrationManifest, error) {
	m := migrationManifest{Version: 1, Ranges: []migrationRange{}, VMs: []migrationVM{}}
	var err error
	if m.Port, err = migrationPort(values, "port", 8080); err != nil {
		return m, err
	}
	if m.AdminPort, err = migrationPort(values, "admin_port", 8081); err != nil {
		return m, err
	}
	if m.ExposeAdminPort, err = migrationBool(values, "expose_admin_port"); err != nil {
		return m, err
	}
	if m.WireguardPort, err = migrationPort(values, "wireguard_port", 51820); err != nil {
		return m, err
	}
	if m.Port == m.AdminPort {
		return m, errors.New("API and admin ports must differ")
	}
	if m.WireguardEndpoint, err = migrationString(values, "wireguard_endpoint", ""); err != nil {
		return m, err
	}
	if m.WireguardEndpoint == "" {
		m.WireguardEndpoint, err = migrationString(values, "proxmox_public_ip", "")
	}
	if err != nil || m.WireguardEndpoint == "" {
		return m, errors.New("legacy config must contain wireguard_endpoint or proxmox_public_ip")
	}
	if m.ProxmoxUserRealm, err = migrationString(values, "proxmox_user_realm", "pam"); err != nil {
		return m, err
	}
	if m.ProxmoxVMStoragePool, err = migrationString(values, "proxmox_vm_storage_pool", "local"); err != nil {
		return m, err
	}
	if m.ProxmoxVMStorageFormat, err = migrationString(values, "proxmox_vm_storage_format", "qcow2"); err != nil {
		return m, err
	}
	if m.ProxmoxISOStoragePool, err = migrationString(values, "proxmox_iso_storage_pool", "local"); err != nil {
		return m, err
	}
	key, err := migrationString(values, "database_encryption_key", "hZD6RwYxrcQ7CS4lRxjdKI7thWp3jg48")
	if err != nil || len(key) != 32 {
		return m, errors.New("legacy database_encryption_key must contain 32 bytes")
	}
	license, err := migrationString(values, "license_key", "")
	if err != nil {
		return m, err
	}
	m.RequiresPlugin = license != "" && license != "community"
	if _, err := os.Stat(filepath.Join(root, "opt/ludus/license.lic")); err == nil {
		m.RequiresPlugin = true
	}
	databasePath := filepath.Join(root, "opt/ludus/db/data.db")
	if root == "/" {
		dataDir, err := migrationDataDir(values)
		if err != nil {
			return m, err
		}
		databasePath = filepath.Join(dataDir, "data.db")
	}
	if err := migrationRequireFile(databasePath); err != nil {
		return m, err
	}
	for _, required := range []string{"opt/ludus/install/root-api-key", "etc/wireguard/server-private-key", "etc/wireguard/wg0.conf"} {
		if err := migrationRequireFile(filepath.Join(root, required)); err != nil {
			return m, err
		}
	}
	if err := migrationValidateWireguard(filepath.Join(root, "etc/wireguard"), m.WireguardPort); err != nil {
		return m, err
	}
	u := url.URL{Scheme: "file", Path: databasePath, RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return m, err
	}
	defer db.Close()
	var integrity string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		return m, errors.New("PocketBase data.db failed SQLite integrity check")
	}
	var collections int
	if err := db.QueryRow("SELECT count(*) FROM _collections WHERE name IN ('users','ranges','vms')").Scan(&collections); err != nil || collections != 3 {
		return m, errors.New("migration requires an initialized PocketBase database; complete the legacy SQLite-to-PocketBase upgrade first")
	}
	var rootHash string
	if err := db.QueryRow("SELECT hashedAPIKey FROM users WHERE userID='ROOT'").Scan(&rootHash); err != nil {
		return m, errors.New("PocketBase ROOT user is missing")
	}
	rootKey, err := os.ReadFile(filepath.Join(root, "opt/ludus/install/root-api-key"))
	if err != nil {
		return m, err
	}
	if bcrypt.CompareHashAndPassword([]byte(rootHash), []byte(strings.TrimSpace(string(rootKey)))) != nil {
		return m, errors.New("ROOT API key does not match the PocketBase ROOT user")
	}
	secretRows, err := db.Query("SELECT proxmoxTokenSecret,proxmoxPassword FROM users")
	if err != nil {
		return m, errors.New("PocketBase users credential schema is incompatible")
	}
	for secretRows.Next() {
		var token, password string
		if err := secretRows.Scan(&token, &password); err != nil {
			secretRows.Close()
			return m, errors.New("cannot read encrypted user credentials")
		}
		for _, encrypted := range []string{token, password} {
			if encrypted != "" {
				if _, err := security.Decrypt(encrypted, key); err != nil {
					secretRows.Close()
					return m, errors.New("legacy database encryption key cannot decrypt stored user credentials; preserve the effective original key before migration")
				}
			}
		}
	}
	if err := secretRows.Err(); err != nil {
		secretRows.Close()
		return m, err
	}
	secretRows.Close()
	rows, err := db.Query("SELECT rangeID,rangeNumber FROM ranges ORDER BY rangeNumber")
	if err != nil {
		return m, errors.New("PocketBase ranges schema is incompatible")
	}
	for rows.Next() {
		var r migrationRange
		if err := rows.Scan(&r.ID, &r.Number); err != nil {
			rows.Close()
			return m, err
		}
		if !migrationID.MatchString(r.ID) || r.Number < 1 || r.Number > 255 {
			rows.Close()
			return m, errors.New("PocketBase contains an unsafe range ID or range number")
		}
		if err := migrationRequireFile(filepath.Join(root, "opt/ludus/ranges", r.ID, "range-config.yml")); err != nil {
			rows.Close()
			return m, err
		}
		m.Ranges = append(m.Ranges, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return m, err
	}
	rows.Close()
	rows, err = db.Query("SELECT v.proxmoxID,r.rangeNumber,v.isRouter,v.name FROM vms v LEFT JOIN ranges r ON v.range=r.id ORDER BY v.proxmoxID")
	if err != nil {
		return m, errors.New("PocketBase VM schema is incompatible")
	}
	seen := map[int]bool{}
	for rows.Next() {
		var v migrationVM
		if err := rows.Scan(&v.VMID, &v.RangeNumber, &v.IsRouter, &v.Name); err != nil {
			rows.Close()
			return m, errors.New("PocketBase contains a VM without a valid range")
		}
		if v.VMID < 100 || v.Name == "" || seen[v.VMID] {
			rows.Close()
			return m, errors.New("PocketBase contains invalid or duplicate VM identities")
		}
		seen[v.VMID] = true
		m.VMs = append(m.VMs, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return m, err
	}
	rows.Close()
	rows, err = db.Query("SELECT proxmoxUsername FROM users WHERE userID != 'ROOT'")
	if err != nil {
		return m, errors.New("PocketBase users schema is incompatible")
	}
	defer rows.Close()
	for rows.Next() {
		var username string
		if err := rows.Scan(&username); err != nil {
			return m, err
		}
		if username == "" || username == "." || username == ".." || strings.ContainsAny(username, "/\\") {
			return m, errors.New("PocketBase contains an unsafe user directory")
		}
		st, err := os.Stat(filepath.Join(root, "opt/ludus/users", username))
		if err != nil || !st.IsDir() {
			return m, errors.New("required PocketBase user state directory is missing")
		}
	}
	return m, rows.Err()
}

// Decode systemctl-show's C-escaped Environment output without invoking a shell.
// EnvironmentFile has different quoting rules and is parsed separately below.
func migrationWords(input string) ([]string, error) {
	var words []string
	var b strings.Builder
	var quote byte
	started := false
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '\\' && quote != '\'' {
			i++
			if i == len(input) {
				return nil, errors.New("unterminated systemd environment escape")
			}
			c = input[i]
			switch c {
			case 'a':
				c = '\a'
			case 'b':
				c = '\b'
			case 'f':
				c = '\f'
			case 'v':
				c = '\v'
			case 'x', 'u', 'U':
				r, _, rest, err := strconv.UnquoteChar(input[i-1:], '"')
				if err != nil {
					return nil, errors.New("invalid systemd environment character escape")
				}
				b.WriteRune(r)
				i = len(input) - len(rest) - 1
				started = true
				continue
			case 'n':
				c = '\n'
			case 'r':
				c = '\r'
			case 't':
				c = '\t'
			case 's':
				c = ' '
			case '\\', '"', '\'', ' ', '$', '`', '%':
			default:
				return nil, errors.New("unsupported systemd environment escape")
			}
			b.WriteByte(c)
			started = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			} else {
				b.WriteByte(c)
			}
			started = true
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			started = true
			continue
		}
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			if started {
				words = append(words, b.String())
				b.Reset()
				started = false
			}
			continue
		}
		b.WriteByte(c)
		started = true
	}
	if quote != 0 {
		return nil, errors.New("unterminated systemd environment quote")
	}
	if started {
		words = append(words, b.String())
	}
	return words, nil
}

// Accept single-line EnvironmentFile assignments only. Parse every assignment,
// including unrelated variables, so a multiline value cannot hide a secret
// assignment on its next line. Never include secret contents in errors.
func migrationEnvironmentFile(input string) (map[string]string, error) {
	if !utf8.ValidString(input) {
		return nil, errors.New("EnvironmentFile must be UTF-8")
	}
	for _, r := range input {
		if r == 0 || r == '\ufeff' || (r >= 0xfdd0 && r <= 0xfdef) || r&0xffff == 0xfffe || r&0xffff == 0xffff {
			return nil, errors.New("EnvironmentFile contains unsupported Unicode")
		}
	}
	values := map[string]string{}
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimLeft(line, " \t\r")
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		name, raw, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimRight(name, " \t\r")
		raw = strings.TrimLeft(raw, " \t\r")
		var quote byte
		if len(raw) > 0 && (raw[0] == '\'' || raw[0] == '"') {
			quote, raw = raw[0], raw[1:]
		}
		var value strings.Builder
		keep := 0
		closed := quote == 0
		for i := 0; i < len(raw); i++ {
			c := raw[i]
			if quote != 0 && c == quote {
				if strings.Trim(raw[i+1:], " \t\r") != "" {
					return nil, errors.New("unsupported EnvironmentFile text after quoted value")
				}
				closed = true
				break
			}
			if c == '\\' && quote != '\'' {
				i++
				if i == len(raw) {
					return nil, errors.New("unsupported EnvironmentFile line continuation")
				}
				c = raw[i]
				if quote == '"' && !strings.ContainsRune("\"\\`$", rune(c)) {
					value.WriteByte('\\')
				}
				value.WriteByte(c)
				keep = value.Len()
				continue
			}
			value.WriteByte(c)
			if quote != 0 || (c != ' ' && c != '\t' && c != '\r') {
				keep = value.Len()
			}
		}
		if !closed {
			return nil, errors.New("unsupported EnvironmentFile multiline or unterminated quoted value")
		}
		if migrationEnvName.MatchString(name) {
			values[name] = value.String()[:keep]
		}
	}
	return values, nil
}
func migrationEnvironments() (map[string]map[string]string, error) {
	all := map[string]map[string]string{}
	for _, service := range migrationServices {
		values := map[string]string{}
		readProperty := func(name string) (string, error) {
			b, err := exec.Command("systemctl", "show", service, "--property="+name, "--value").Output()
			if err != nil {
				return "", fmt.Errorf("cannot read %s service %s", service, name)
			}
			return strings.TrimSpace(string(b)), nil
		}
		apply := func(assignment string) {
			name, value, ok := strings.Cut(assignment, "=")
			if ok && migrationEnvName.MatchString(name) {
				values[name] = value
			}
		}
		env, err := readProperty("Environment")
		if err != nil {
			return nil, err
		}
		words, err := migrationWords(env)
		if err != nil {
			return nil, fmt.Errorf("unsupported %s Environment syntax", service)
		}
		for _, word := range words {
			apply(word)
		}
		files, err := readProperty("EnvironmentFiles")
		if err != nil {
			return nil, err
		}
		for files != "" {
			end := strings.Index(files, " (ignore_errors=")
			if end < 0 {
				return nil, fmt.Errorf("unsupported %s EnvironmentFiles syntax", service)
			}
			filename := files[:end]
			rest := files[end+len(" (ignore_errors="):]
			close := strings.IndexByte(rest, ')')
			if close < 0 || !filepath.IsAbs(filename) || strings.ContainsAny(filename, "%\n\r") {
				return nil, fmt.Errorf("unsupported %s EnvironmentFiles path", service)
			}
			optional := rest[:close] == "yes"
			files = strings.TrimSpace(rest[close+1:])
			b, err := os.ReadFile(filename)
			if optional && os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("cannot read %s EnvironmentFile", service)
			}
			fileValues, err := migrationEnvironmentFile(string(b))
			if err != nil {
				return nil, fmt.Errorf("unsupported %s EnvironmentFile: %w", service, err)
			}
			for name, value := range fileValues {
				values[name] = value
			}
		}
		unset, err := readProperty("UnsetEnvironment")
		if err != nil {
			return nil, err
		}
		words, err = migrationWords(unset)
		if err != nil {
			return nil, fmt.Errorf("unsupported %s UnsetEnvironment syntax", service)
		}
		for _, word := range words {
			name, value, assignment := strings.Cut(word, "=")
			if !assignment || values[name] == value {
				delete(values, name)
			}
		}
		all[service] = values
	}
	return all, nil
}

func migrationRouterTemplate(defaultsPath, playbookPath string) (string, error) {
	defaults, err := migrationReadYAML(defaultsPath)
	if err != nil {
		return "", fmt.Errorf("read original router defaults: %w", err)
	}
	name, err := migrationString(defaults, "default_router_template_name", "")
	if err != nil || name != "" {
		return name, err
	}
	// Releases before the Debian 13 default stored the literal template in
	// the router play, rather than in server-config.yml.
	content, err := os.ReadFile(playbookPath)
	if err != nil {
		return "", err
	}
	var plays []struct {
		Vars struct {
			Template string `yaml:"template_vm_name"`
		} `yaml:"vars"`
	}
	if err := yaml.Unmarshal(content, &plays); err != nil {
		return "", err
	}
	literal := regexp.MustCompile(`router\.template\s*\|\s*default\(\s*['"]([A-Za-z0-9][A-Za-z0-9_.-]*)['"]\s*\)`)
	for _, play := range plays {
		if match := literal.FindStringSubmatch(play.Vars.Template); match != nil {
			return match[1], nil
		}
	}
	return "", nil
}

func migrationCollectCredentials(stage, home string) error {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || home == "/" {
		return errors.New("Ludus service account must have a clean, dedicated home directory")
	}
	for _, name := range migrationCredentials {
		src := filepath.Join(home, name)
		st, err := os.Lstat(src)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("unsupported symlink at Ludus private-source credential root")
		}
		if (name == ".ssh" && !st.IsDir()) || (name != ".ssh" && !st.Mode().IsRegular()) {
			return errors.New("unsupported Ludus private-source credential file type")
		}
		// Absolute references to the old home would stop working after relocation.
		// Keep credential bytes unchanged; require manual conversion to ~/ paths.
		if home != "/home/ludus" && (name == ".ssh" || name == ".gitconfig") {
			err := filepath.Walk(src, func(filename string, st os.FileInfo, err error) error {
				if os.IsNotExist(err) {
					return nil
				}
				if err != nil {
					return err
				}
				if st.IsDir() {
					return nil
				}
				if !st.Mode().IsRegular() {
					return errors.New("unsupported Ludus private-source credential file type")
				}
				b, err := os.ReadFile(filename)
				if err != nil {
					return err
				}
				if strings.Contains(string(b), home+"/") {
					return errors.New("private-source credentials reference the old Ludus home; use home-relative paths before migration")
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if stage != "" {
			if err := migrationOptionalCopy(src, filepath.Join(stage, "home/ludus", name)); err != nil {
				return fmt.Errorf("copy Ludus private-source credentials: %w", err)
			}
		}
	}
	return nil
}

func migrationTLS(values map[string]interface{}, root string) (string, string, error) {
	cert, err := migrationString(values, "tls_cert_file", "")
	if err != nil {
		return "", "", err
	}
	key, err := migrationString(values, "tls_key_file", "")
	if err != nil {
		return "", "", err
	}
	if (cert == "") != (key == "") {
		return "", "", errors.New("legacy TLS requires both tls_cert_file and tls_key_file")
	}
	if cert != "" {
		return cert, key, nil
	}
	node, err := migrationString(values, "proxmox_node", "")
	if err != nil {
		return "", "", err
	}
	if node == "" || node == "." || node == ".." || strings.ContainsAny(node, "/\\\x00") {
		return "", "", errors.New("legacy TLS fallback requires a valid configured proxmox_node")
	}
	nodeDir := filepath.Join(root, "etc/pve/nodes", node)
	for _, pair := range [][2]string{
		{filepath.Join(nodeDir, "pveproxy-ssl.pem"), filepath.Join(nodeDir, "pveproxy-ssl.key")},
		{filepath.Join(nodeDir, "pve-ssl.pem"), filepath.Join(nodeDir, "pve-ssl.key")},
		{filepath.Join(root, "opt/ludus/cert.pem"), filepath.Join(root, "opt/ludus/key.pem")},
	} {
		complete := true
		for _, filename := range pair {
			st, err := os.Stat(filename)
			if os.IsNotExist(err) {
				complete = false
				continue
			}
			if err != nil {
				return "", "", err
			}
			if !st.Mode().IsRegular() {
				return "", "", errors.New("legacy TLS candidate is not a regular file")
			}
		}
		if complete {
			return pair[0], pair[1], nil
		}
	}
	return "", "", errors.New("legacy TLS certificate and private key are required to preserve client trust")
}

func migrationCollect(stage string) (migrationManifest, error) {
	values, err := migrationReadYAML("/opt/ludus/config.yml")
	if err != nil {
		return migrationManifest{}, err
	}
	dataDir, err := migrationDataDir(values)
	if err != nil {
		return migrationManifest{}, err
	}
	account, err := user.Lookup("ludus")
	if err != nil {
		return migrationManifest{}, errors.New("legacy Ludus service account is missing")
	}
	if err := migrationCollectCredentials(stage, account.HomeDir); err != nil {
		return migrationManifest{}, err
	}
	// Metadata preflight reads the live installation without copying its data.
	// Only the quiesced export takes a complete snapshot.
	root := stage
	if stage == "" {
		root = "/"
	} else {
		if err := migrationCopyTree(dataDir, filepath.Join(stage, "opt/ludus/db")); err != nil {
			return migrationManifest{}, fmt.Errorf("copy complete PocketBase data directory: %w", err)
		}
		for _, name := range migrationTrees {
			if err := migrationOptionalCopy("/opt/ludus/"+name, filepath.Join(stage, "opt/ludus", name)); err != nil {
				return migrationManifest{}, err
			}
		}
		for _, name := range append(append([]string{}, migrationLooseFiles...), "config.yml", "install/root-api-key") {
			if err := migrationOptionalCopy("/opt/ludus/"+name, filepath.Join(stage, "opt/ludus", name)); err != nil {
				return migrationManifest{}, err
			}
		}
		if err := migrationCopyTree("/etc/wireguard", filepath.Join(stage, "etc/wireguard")); err != nil {
			return migrationManifest{}, err
		}
		if err := migrationOptionalCopy("/usr/local/share/ca-certificates", filepath.Join(stage, "usr/local/share/ca-certificates")); err != nil {
			return migrationManifest{}, err
		}
		if err := migrationOptionalCopy("/var/lib/misc/dnsmasq.leases", filepath.Join(stage, "var/lib/misc/dnsmasq.leases")); err != nil {
			return migrationManifest{}, err
		}
	}
	m, err := migrationDatabase(root, values)
	if err != nil {
		return m, err
	}
	m.RouterTemplate, err = migrationRouterTemplate("/opt/ludus/ansible/server-config.yml", "/opt/ludus/ansible/range-management/ludus.yml")
	if err != nil {
		return m, err
	}
	if m.RouterTemplate == "" {
		return m, errors.New("cannot determine the original router template; set default_router_template_name in the original ansible/server-config.yml before migrating")
	}
	plugins, err := filepath.Glob("/opt/ludus/plugins/enterprise/*.so")
	if err != nil {
		return m, err
	}
	m.RequiresPlugin = m.RequiresPlugin || len(plugins) > 0
	cert, key, err := migrationTLS(values, "/")
	if err != nil {
		return m, err
	}
	if cert != "" {
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			return m, errors.New("legacy TLS certificate/private key is missing, invalid, or mismatched")
		}
		if stage != "" {
			if err := migrationCopyFile(cert, filepath.Join(stage, "migration-tls/server.crt"), 0644); err != nil {
				return m, err
			}
			if err := migrationCopyFile(key, filepath.Join(stage, "migration-tls/server.key"), 0600); err != nil {
				return m, err
			}
		}
		m.CustomTLS = true
	}
	environments, err := migrationEnvironments()
	if err != nil {
		return m, err
	}
	if stage == "" {
		return m, nil
	}
	b, err := json.Marshal(environments)
	if err != nil {
		return m, err
	}
	if err := os.WriteFile(filepath.Join(stage, "migration-environments.json"), b, 0600); err != nil {
		return m, err
	}
	return m, nil
}

func migrationInventory(stage string) ([]migrationFile, error) {
	var files []migrationFile
	err := filepath.Walk(stage, func(filename string, st os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filename == stage {
			return nil
		}
		rel, err := filepath.Rel(stage, filename)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "migration.json" {
			return nil
		}
		f := migrationFile{Path: rel, Mode: uint32(st.Mode().Perm()), Directory: st.IsDir()}
		if !st.IsDir() {
			if !st.Mode().IsRegular() {
				return fmt.Errorf("unsupported staged file: %s", rel)
			}
			in, err := os.Open(filename)
			if err != nil {
				return err
			}
			hash := sha256.New()
			f.Size, err = io.Copy(hash, in)
			in.Close()
			if err != nil {
				return err
			}
			f.SHA256 = hex.EncodeToString(hash.Sum(nil))
		}
		files = append(files, f)
		return nil
	})
	return files, err
}
func exportMigrationState(destination string, infoOnly bool) (err error) {
	if infoOnly {
		m, err := migrationCollect("")
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(m)
	}
	if err := migrationStopped(); err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "ludus-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	m, err := migrationCollect(stage)
	if err != nil {
		return err
	}
	if err := migrationStopped(); err != nil {
		return err
	}
	m.Files, err = migrationInventory(stage)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "migration.json"), b, 0600); err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create migration archive (refusing overwrite): %w", err)
	}
	complete := false
	defer func() {
		out.Close()
		if !complete {
			os.Remove(destination)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	err = filepath.Walk(stage, func(filename string, st os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filename == stage {
			return nil
		}
		rel, err := filepath.Rel(stage, filename)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(st, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Uid = 0
		header.Gid = 0
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if st.IsDir() {
			return nil
		}
		in, err := os.Open(filename)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(tw, in)
		return err
	})
	if err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	complete = true
	return nil
}

func migrationAllowed(name string, directory bool) bool {
	if name == "migration.json" || name == "migration-environments.json" || name == "migration-tls/server.crt" || name == "migration-tls/server.key" {
		return !directory
	}
	if name == "var/lib/misc/dnsmasq.leases" {
		return !directory
	}
	for _, credential := range migrationCredentials {
		p := "home/ludus/" + credential
		if name == p {
			return directory == (credential == ".ssh")
		}
		if credential == ".ssh" && strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	if directory {
		for _, p := range []string{"opt", "opt/ludus", "opt/ludus/install", "etc", "usr", "usr/local", "usr/local/share", "var", "var/lib", "var/lib/misc", "migration-tls", "home", "home/ludus"} {
			if name == p {
				return true
			}
		}
	}
	for _, p := range []string{"opt/ludus/db", "etc/wireguard", "usr/local/share/ca-certificates"} {
		if name == p {
			return directory
		}
		if strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	for _, tree := range migrationTrees {
		p := "opt/ludus/" + tree
		if name == p {
			return directory
		}
		if strings.HasPrefix(name, p+"/") {
			return true
		}
	}
	for _, file := range append(append([]string{}, migrationLooseFiles...), "config.yml", "install/root-api-key") {
		if name == "opt/ludus/"+file {
			return !directory
		}
	}
	return false
}
func migrationExtract(archive, stage string) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		dir := h.Typeflag == tar.TypeDir
		if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") || seen[name] || !migrationAllowed(name, dir) || (h.Typeflag != tar.TypeReg && !dir) || h.Mode < 0 || h.Mode&^0777 != 0 || h.Size < 0 {
			return errors.New("migration archive contains an unsafe, duplicate, or unsupported entry")
		}
		seen[name] = true
		dst := filepath.Join(stage, filepath.FromSlash(name))
		if dir {
			if h.Size != 0 {
				return errors.New("migration archive directory has file data")
			}
			if err := os.MkdirAll(dst, 0700); err != nil {
				return err
			}
			if err := os.Chmod(dst, os.FileMode(h.Mode)|0700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(h.Mode))
		if err != nil {
			return err
		}
		if err := out.Chmod(os.FileMode(h.Mode)); err != nil {
			out.Close()
			return err
		}
		_, err = io.CopyN(out, tr, h.Size)
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// Consume the gzip trailer so truncation/checksum errors cannot be hidden
	// behind tar's end-of-archive blocks.
	if _, err := io.Copy(io.Discard, gz); err != nil {
		return err
	}
	return nil
}

func migrationMergeConfig(old, generated map[string]interface{}) (map[string]interface{}, error) {
	// Copy only explicit platform settings from the installer. Absence in the
	// original must not accidentally inherit a generated encryption/license key.
	merged := make(map[string]interface{}, len(old)+len(generated))
	for key, value := range old {
		merged[key] = value
	}
	for _, key := range []string{"proxmox_node", "proxmox_hostname", "proxmox_endpoints", "proxmox_token_id", "proxmox_token_secret", "proxmox_invalid_cert", "proxmox_user_realm", "proxmox_vm_storage_pool", "proxmox_vm_storage_format", "proxmox_iso_storage_pool", "ludus_nat_interface", "ludus_nat_ip", "ludus_nat_gateway", "ludus_dns_server", "sdn_zone", "vxlan_tag_base", "tls_cert_file", "tls_key_file"} {
		if value, ok := generated[key]; ok {
			merged[key] = value
		}
	}
	endpoint, err := migrationString(old, "wireguard_endpoint", "")
	if err != nil {
		return nil, err
	}
	if endpoint == "" {
		endpoint, err = migrationString(old, "proxmox_public_ip", "")
		if err != nil {
			return nil, err
		}
	}
	merged["wireguard_endpoint"] = endpoint
	merged["data_directory"] = "/opt/ludus/db"
	delete(merged, "proxmox_url")
	delete(merged, "proxmox_public_ip")
	for _, key := range []string{"proxmox_endpoints", "proxmox_token_id", "proxmox_token_secret", "proxmox_node", "tls_cert_file", "tls_key_file"} {
		if value, ok := generated[key]; !ok || value == nil || value == "" {
			return nil, fmt.Errorf("generated LXC configuration must explicitly set %s", key)
		}
	}
	for _, key := range []string{"tls_cert_file", "tls_key_file"} {
		filename, err := migrationString(merged, key, "")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(filename, "/opt/ludus/tls/") || filepath.Clean(filename) != filename {
			return nil, fmt.Errorf("generated %s must be a clean path below /opt/ludus/tls", key)
		}
	}
	if merged["tls_cert_file"] == merged["tls_key_file"] {
		return nil, errors.New("TLS certificate and key paths must differ")
	}
	return merged, nil
}
func migrationAnnotateRouters(stage string, m migrationManifest) error {
	for _, r := range m.Ranges {
		var router string
		for _, v := range m.VMs {
			if v.RangeNumber == r.Number && v.IsRouter {
				if router != "" {
					return errors.New("range has multiple router VMs; resolve before migration")
				}
				router = v.Name
			}
		}
		filename := filepath.Join(stage, "opt/ludus/ranges", r.ID, "range-config.yml")
		values, err := migrationReadYAML(filename)
		if err != nil {
			return err
		}
		routerValues := map[interface{}]interface{}{}
		if value, ok := values["router"]; ok && value != nil {
			var valid bool
			routerValues, valid = value.(map[interface{}]interface{})
			if !valid {
				return errors.New("range router configuration must be a mapping")
			}
		}
		if router != "" {
			routerValues["vm_name"] = router
		}
		if _, ok := routerValues["template"]; !ok {
			routerValues["template"] = m.RouterTemplate
		}
		values["router"] = routerValues
		b, err := yaml.Marshal(values)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filename, b, 0600); err != nil {
			return err
		}
	}
	return nil
}

func migrationValidateStage(stage string) (migrationManifest, map[string]interface{}, error) {
	var m migrationManifest
	b, err := os.ReadFile(filepath.Join(stage, "migration.json"))
	if err != nil {
		return m, nil, err
	}
	if json.Unmarshal(b, &m) != nil || m.Version != 1 || len(m.Files) == 0 || m.RouterTemplate == "" || !m.CustomTLS {
		return m, nil, errors.New("unsupported or incomplete migration manifest (original TLS identity is required)")
	}
	inventory, err := migrationInventory(stage)
	if err != nil {
		return m, nil, err
	}
	// Extraction uses traversable directories until verification completes.
	// Compare content and regular file modes against the declared inventory;
	// directory modes are applied only after all staging work is complete.
	declared := map[string]migrationFile{}
	for _, f := range m.Files {
		if _, ok := declared[f.Path]; ok || !migrationAllowed(f.Path, f.Directory) || f.Path == "migration.json" || f.Mode&^0777 != 0 {
			return m, nil, errors.New("invalid migration file inventory")
		}
		declared[f.Path] = f
	}
	if len(inventory) != len(declared) {
		return m, nil, errors.New("migration archive does not match its complete file inventory")
	}
	for _, f := range inventory {
		expected, ok := declared[f.Path]
		if f.Directory {
			f.Mode = expected.Mode
		}
		if !ok || f != expected {
			return m, nil, fmt.Errorf("migration archive content mismatch: %s", f.Path)
		}
	}
	for i := len(m.Files) - 1; i >= 0; i-- {
		f := m.Files[i]
		if f.Directory {
			if err := os.Chmod(filepath.Join(stage, f.Path), os.FileMode(f.Mode)); err != nil {
				return m, nil, err
			}
		}
	}
	values, err := migrationReadYAML(filepath.Join(stage, "opt/ludus/config.yml"))
	if err != nil {
		return m, nil, err
	}
	actual, err := migrationDatabase(stage, values)
	if err != nil {
		return m, nil, err
	}
	if !reflect.DeepEqual(actual.Ranges, m.Ranges) || !reflect.DeepEqual(actual.VMs, m.VMs) || actual.Port != m.Port || actual.AdminPort != m.AdminPort || actual.ExposeAdminPort != m.ExposeAdminPort || actual.WireguardPort != m.WireguardPort || actual.WireguardEndpoint != m.WireguardEndpoint || actual.ProxmoxUserRealm != m.ProxmoxUserRealm || actual.ProxmoxVMStoragePool != m.ProxmoxVMStoragePool || actual.ProxmoxVMStorageFormat != m.ProxmoxVMStorageFormat || actual.ProxmoxISOStoragePool != m.ProxmoxISOStoragePool || (actual.RequiresPlugin && !m.RequiresPlugin) {
		return m, nil, errors.New("migration metadata does not match the archived database/configuration")
	}
	if m.CustomTLS {
		if _, err := tls.LoadX509KeyPair(filepath.Join(stage, "migration-tls/server.crt"), filepath.Join(stage, "migration-tls/server.key")); err != nil {
			return m, nil, errors.New("archived TLS certificate/key is invalid or inconsistent")
		}
	} else if _, err := os.Stat(filepath.Join(stage, "migration-tls")); err == nil {
		return m, nil, errors.New("unmanifested TLS material")
	}
	return m, values, nil
}

func migrationStageEnvironments(stage string) error {
	b, err := os.ReadFile(filepath.Join(stage, "migration-environments.json"))
	if err != nil {
		return err
	}
	var all map[string]map[string]string
	if json.Unmarshal(b, &all) != nil || len(all) != len(migrationServices) {
		return errors.New("missing or invalid migration service environments")
	}
	for _, service := range migrationServices {
		values, ok := all[service]
		if !ok || values == nil {
			return errors.New("missing migration service environment")
		}
		keys := make([]string, 0, len(values))
		for key := range values {
			if !migrationEnvName.MatchString(key) || strings.ContainsRune(values[key], 0) {
				return errors.New("unsupported migration environment variable")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		var content strings.Builder
		content.WriteString("[Service]\n")
		for _, key := range keys {
			// Environment= expands specifiers but not shell variables. Escape %
			// and C-quote the complete assignment, including embedded newlines.
			assignment := strings.ReplaceAll(key+"="+values[key], "%", "%%")
			content.WriteString("Environment=" + strconv.Quote(assignment) + "\n")
		}
		filename := filepath.Join(stage, "etc/systemd/system", service+".service.d/90-ludus-migration.conf")
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filename, []byte(content.String()), 0600); err != nil {
			return err
		}
	}
	return nil
}

// Replace whole state roots with same-filesystem renames. Every previous root
// remains in a rollback directory until all replacements and post-copy hooks
// succeed. No imported file is written through a pre-existing destination link.
func migrationCommit(stage string, roots []string, finish func() error) (err error) {
	type replacement struct {
		dst, backup, tmp string
		hadOld           bool
	}
	var applied []replacement
	defer func() {
		if err != nil {
			for i := len(applied) - 1; i >= 0; i-- {
				r := applied[i]
				if removeErr := os.RemoveAll(r.dst); removeErr != nil {
					err = errors.Join(err, removeErr)
					continue
				}
				if r.hadOld {
					if restoreErr := os.Rename(r.backup, r.dst); restoreErr != nil {
						err = errors.Join(err, fmt.Errorf("restore %s from %s: %w", r.dst, r.backup, restoreErr))
						continue
					}
				}
				if removeErr := os.RemoveAll(r.tmp); removeErr != nil {
					err = errors.Join(err, removeErr)
				}
			}
		} else {
			for _, r := range applied {
				if removeErr := os.RemoveAll(r.tmp); removeErr != nil {
					err = errors.Join(err, fmt.Errorf("import succeeded but could not remove backup %s: %w", r.tmp, removeErr))
				}
			}
		}
	}()
	for _, rel := range roots {
		src := filepath.Join(stage, rel)
		if _, statErr := os.Stat(src); os.IsNotExist(statErr) {
			continue
		} else if statErr != nil {
			return statErr
		}
		dst := "/" + rel
		parent := filepath.Dir(dst)
		if err := os.MkdirAll(parent, 0755); err != nil {
			return err
		}
		resolved, err := filepath.EvalSymlinks(parent)
		if err != nil || resolved != parent {
			return fmt.Errorf("migration destination parent is a symlink: %s", parent)
		}
		tmp, err := os.MkdirTemp(parent, ".ludus-migration-")
		if err != nil {
			return err
		}
		incoming := filepath.Join(tmp, "incoming")
		if err := migrationCopyTree(src, incoming); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		// Preserve the staged logical ownership during the cross-filesystem copy.
		if err := migrationOwnership(incoming, rel); err != nil {
			os.RemoveAll(tmp)
			return err
		}
		r := replacement{dst: dst, backup: filepath.Join(tmp, "previous"), tmp: tmp}
		if _, err := os.Lstat(dst); err == nil {
			r.hadOld = true
			if err := os.Rename(dst, r.backup); err != nil {
				os.RemoveAll(tmp)
				return err
			}
		} else if !os.IsNotExist(err) {
			os.RemoveAll(tmp)
			return err
		}
		applied = append(applied, r)
		if err := os.Rename(incoming, dst); err != nil {
			return err
		}
	}
	return finish()
}
func migrationOwnership(root, relative string) error {
	uid, gid := 0, 0
	accountName := ""
	if (strings.HasPrefix(relative, "opt/ludus/") && relative != "opt/ludus/install/root-api-key") || strings.HasPrefix(relative, "home/ludus/") {
		accountName = "ludus"
	}
	if relative == "var/lib/misc/dnsmasq.leases" {
		accountName = "dnsmasq"
	}
	if accountName != "" {
		account, err := user.Lookup(accountName)
		if err != nil {
			return fmt.Errorf("LXC %s service account is missing", accountName)
		}
		uid, err = strconv.Atoi(account.Uid)
		if err != nil {
			return err
		}
		gid, err = strconv.Atoi(account.Gid)
		if err != nil {
			return err
		}
	}
	return filepath.Walk(root, func(filename string, st os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := os.Chown(filename, uid, gid); err != nil {
			return err
		}
		if relative == "opt/ludus/install/root-api-key" {
			return os.Chmod(filename, 0400)
		}
		return nil
	})
}

// Keep new appliance payloads (Blocky, Packer plugins and bundled sources) that
// did not exist on the host, alongside its custom resources.
func migrationMergeResources(stage string) error {
	const source = "/opt/ludus/resources"
	return filepath.Walk(source, func(filename string, st os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		dst := filepath.Join(stage, "opt/ludus/resources", rel)
		if existing, err := os.Lstat(dst); err == nil {
			if st.IsDir() != existing.IsDir() {
				return fmt.Errorf("imported resource conflicts with appliance directory: %s", rel)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		if st.IsDir() {
			return os.MkdirAll(dst, st.Mode().Perm())
		}
		return migrationCopyTree(filename, dst)
	})
}

func importMigrationState(archive string) error {
	if err := migrationStopped(); err != nil {
		return err
	}
	if _, err := exec.LookPath("pveversion"); err == nil {
		return errors.New("import-state must run inside the new Ludus appliance, not on the Proxmox host")
	}
	if _, err := os.Stat(bootstrapMarker); err == nil {
		return errors.New("refusing to import over an already bootstrapped appliance")
	} else if !os.IsNotExist(err) {
		return err
	}
	generated, err := migrationReadYAML("/opt/ludus/config.yml")
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp("", "ludus-import-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := migrationExtract(archive, stage); err != nil {
		return fmt.Errorf("stage migration archive: %w", err)
	}
	m, old, err := migrationValidateStage(stage)
	if err != nil {
		return err
	}
	account, err := user.Lookup("ludus")
	if err != nil || account.HomeDir != "/home/ludus" {
		return errors.New("migration requires the LXC Ludus service account home to be /home/ludus")
	}
	if m.RequiresPlugin {
		plugins, _ := filepath.Glob("/opt/ludus/plugins/enterprise/*.so")
		if len(plugins) == 0 {
			return errors.New("legacy license/plugins require a compatible enterprise plugin; supply it to install.sh before migration")
		}
	}
	merged, err := migrationMergeConfig(old, generated)
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(merged)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "opt/ludus/config.yml"), b, 0600); err != nil {
		return err
	}
	if err := migrationAnnotateRouters(stage, m); err != nil {
		return err
	}
	if err := migrationMergeResources(stage); err != nil {
		return fmt.Errorf("preserve appliance resources: %w", err)
	}
	if m.CustomTLS {
		for _, pair := range [][2]string{{"migration-tls/server.crt", "tls_cert_file"}, {"migration-tls/server.key", "tls_key_file"}} {
			dst := filepath.Join(stage, strings.TrimPrefix(merged[pair[1]].(string), "/"))
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
			mode := os.FileMode(0644)
			if pair[1] == "tls_key_file" {
				mode = 0600
			}
			if err := migrationCopyFile(filepath.Join(stage, pair[0]), dst, mode); err != nil {
				return err
			}
		}
	}
	if err := migrationStageEnvironments(stage); err != nil {
		return err
	}
	// Preserve installer-injected trust alongside host trust. Do not replace the
	// whole machine's certificate directory or systemd service definitions.
	var roots []string
	roots = append(roots, "opt/ludus/db", "opt/ludus/config.yml", "opt/ludus/install/root-api-key", "etc/wireguard", "var/lib/misc/dnsmasq.leases")
	for _, tree := range migrationTrees {
		roots = append(roots, "opt/ludus/"+tree)
	}
	for _, file := range migrationLooseFiles {
		roots = append(roots, "opt/ludus/"+file)
	}
	for _, credential := range migrationCredentials {
		roots = append(roots, "home/ludus/"+credential)
	}
	for _, service := range migrationServices {
		roots = append(roots, "etc/systemd/system/"+service+".service.d/90-ludus-migration.conf")
	}
	caRoot := filepath.Join(stage, "usr/local/share/ca-certificates")
	if _, err := os.Stat(caRoot); err == nil {
		if err := filepath.Walk(caRoot, func(filename string, st os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if st.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(stage, filename)
			if err != nil {
				return err
			}
			if filename != caRoot && strings.HasSuffix(filename, ".crt") {
				if filepath.Base(filename) == "ludus-injected-ca.crt" {
					if _, err := os.Stat("/" + rel); err == nil {
						return nil
					}
				}
				roots = append(roots, filepath.ToSlash(rel))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := migrationStopped(); err != nil {
		return err
	}
	err = migrationCommit(stage, roots, func() error {
		if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
			return errors.New("reload migrated service environments failed")
		}
		if err := exec.Command("update-ca-certificates").Run(); err != nil {
			return errors.New("refresh migrated CA trust failed")
		}
		return nil
	})
	if err != nil {
		if reloadErr := exec.Command("systemctl", "daemon-reload").Run(); reloadErr != nil {
			err = errors.Join(err, errors.New("reload restored service environments failed"))
		}
		if trustErr := exec.Command("update-ca-certificates").Run(); trustErr != nil {
			err = errors.Join(err, errors.New("refresh restored CA trust failed"))
		}
	}
	return err
}

func refuseLegacyHostUpdate() error {
	if _, err := os.Stat(bootstrapMarker); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := exec.LookPath("pveversion"); err == nil {
		return errors.New("this is a legacy host installation; --update cannot replace it with an LXC binary. Run install.sh --migrate-host instead (services were not stopped)")
	}
	return nil
}

// The CA is injected outside the embedded packer payload. Re-seed every HTTP
// seed directory after replacing packer during an ordinary appliance update.
func preserveInjectedCA() error {
	const source = "/usr/local/share/ca-certificates/ludus-injected-ca.crt"
	if _, err := os.Stat(source); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.Walk("/opt/ludus/packer", func(filename string, st os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Name() != "http" {
			return nil
		}
		dst := filepath.Join(filename, "ludus-injected-ca.crt")
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := migrationCopyFile(source, dst, 0644); err != nil {
			return err
		}
		return filepath.SkipDir
	})
}

func migrationValidateWireguard(dir string, port int) error {
	key, err := os.ReadFile(filepath.Join(dir, "server-private-key"))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(dir, "wg0.conf"))
	if err != nil {
		return err
	}
	privateKey := ""
	listenPort := 0
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		switch strings.ToLower(name) {
		case "preup", "postup", "predown", "postdown":
			return errors.New("legacy WireGuard has shell hooks; replace host-only hooks with reviewed LXC networking before migration")
		case "saveconfig":
			if strings.EqualFold(value, "true") {
				return errors.New("disable WireGuard SaveConfig before migration to keep exported configuration stable")
			}
		case "privatekey":
			if privateKey != "" {
				return errors.New("WireGuard contains duplicate private keys")
			}
			privateKey = value
		case "listenport":
			var err error
			listenPort, err = strconv.Atoi(value)
			if err != nil {
				return errors.New("WireGuard listen port is invalid")
			}
		}
	}
	if privateKey == "" || privateKey != strings.TrimSpace(string(key)) {
		return errors.New("WireGuard configuration private key does not match server-private-key")
	}
	if listenPort != port {
		return errors.New("WireGuard listen port does not match legacy configuration")
	}
	return nil
}
