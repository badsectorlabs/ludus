# Packer

If you want to use ansible you must include the ansible_home var and set it, since everything outside of the install dir is read-only. You have to set the local tmp, control path, and ssh control path dir as well.
kali.pkr.hcl has an example.
You also need to set `skip_version_check = true` since the env variables are not set before the version check.

Ludus template builds export `ANSIBLE_ROLES_PATH` and `ANSIBLE_COLLECTIONS_PATH` for Packer's Ansible provisioners. Each path searches the initiating user's installed roles or collections first, then the instance-wide global directory, matching range deployments. Templates do not need to add these variables to `ansible_env_vars`; setting them explicitly overrides the inherited paths.

Template builds also load Ludus's `packer/ansible/vars_plugins/ludus_winrm_shell.py` through `ANSIBLE_VARS_PLUGINS`. It changes Packer's legacy inventory setting `ansible_shell_type=powershell` to `cmd` for WinRM connections, including the `ansible.builtin.winrm` and `ansible.legacy.winrm` names. WinRM starts commands through `cmd.exe`; Windows modules still run PowerShell. This applies to existing HCL and JSON templates without modifying them. SSH and PSRP connections are unchanged, and this plugin is not enabled for range deployments.

The vars-plugin path retains the user's and system's standard plugin directories and any inherited custom paths. Templates that replace `ANSIBLE_VARS_PLUGINS` must retain the inherited Ludus path to keep this correction. Normal Ansible variable precedence still applies: play/task variables and `--extra-vars` can override the corrected inventory value. An explicit shell override for WinRM must use `cmd` with ansible-core 2.21.

```
variable "ansible_home" {
  type =  string
}
...
  provisioner "ansible" {
    user = "${var.ssh_username}"
    use_proxy = false
    extra_arguments = ["-v", "--extra-vars", "{ansible_python_interpreter: /usr/bin/python3, ansible_password: ${var.ssh_password}, ansible_sudo_pass: ${var.ssh_password}}"]
    ansible_env_vars = ["ANSIBLE_HOME=${var.ansible_home}", "ANSIBLE_LOCAL_TEMP=${var.ansible_home}/tmp", "ANSIBLE_PERSISTENT_CONTROL_PATH_DIR=${var.ansible_home}/pc", "ANSIBLE_SSH_CONTROL_PATH_DIR=${var.ansible_home}/cp"]
    skip_version_check = true
    playbook_file   = "ansible/kali.yml"
  }
```