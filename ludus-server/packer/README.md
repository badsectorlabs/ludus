# Packer

If you want to use ansible you must include the ansible_home var and set it, since everything outside of the install dir is read-only. You have to set the local tmp, control path, and ssh control path dir as well.
kali.pkr.hcl has an example.
You also need to set `skip_version_check = true` since the env variables are not set before the version check.

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