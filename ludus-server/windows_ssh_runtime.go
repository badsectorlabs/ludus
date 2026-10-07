package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Keep the system Ansible 2.16 installation for Python 2 guests. Windows SSH
// support needs core 2.18, including during the initial password/WinRM bootstrap.
var windowsSSHRuntimePackages = []string{
	"ansible==11.13.0",
	"ansible-core==2.18.19",
	"pywinrm==0.5.0",
	"proxmoxer==2.2.0",
	"requests==2.32.5",
	"netaddr==1.2.1",
	"dnspython==2.8.0",
	"jmespath==1.0.1",
	"passlib==1.7.4",
}

func ensureWindowsSSHRuntime() error {
	runtimePath := filepath.Join(ludusInstallPath, "runtimes", "ansible-windows-ssh")
	python := filepath.Join(runtimePath, "bin", "python3")
	// Check installed packages rather than a marker: interrupted installs must
	// be repaired, while development updates need no network when already ready.
	check := `import importlib.metadata as m, sys
import ansible, winrm, proxmoxer, requests, netaddr, dns, jmespath, passlib
assert (3, 11) <= sys.version_info[:2] <= (3, 13)
for requirement in sys.argv[1:]:
    name, version = requirement.split('==')
    assert m.version(name) == version, requirement
`
	checkArgs := append([]string{"-c", check}, windowsSSHRuntimePackages...)
	if exec.Command(python, checkArgs...).Run() == nil {
		return nil
	}

	log.Println("Installing isolated Ansible runtime for Windows SSH")
	// Debian 12/13 supply the supported controller Python versions. Do not
	// silently fall back to the legacy runtime if a controller is too old.
	if err := exec.Command("/usr/bin/python3", "-c", "import sys; assert (3, 11) <= sys.version_info[:2] <= (3, 13)").Run(); err != nil {
		return fmt.Errorf("Windows SSH Ansible runtime requires controller Python 3.11–3.13 (Debian 12 or 13): %w", err)
	}
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("installing Windows SSH runtime (%s %s): %w", name, strings.Join(args, " "), err)
		}
		return nil
	}
	if err := run("apt-get", "update", "-qq"); err != nil {
		return err
	}
	if err := run("apt-get", "install", "-y", "-qq", "python3-venv"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0755); err != nil {
		return err
	}
	if err := run("/usr/bin/python3", "-m", "venv", runtimePath); err != nil {
		return err
	}
	if err := run(python, append([]string{"-m", "pip", "install", "--disable-pip-version-check"}, windowsSSHRuntimePackages...)...); err != nil {
		return err
	}
	if err := run(python, checkArgs...); err != nil {
		return err
	}
	return run(python, "-m", "pip", "check")
}
