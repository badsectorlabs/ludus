variable "iso_checksum" {
  type    = string
  default = "sha256:f11bda2f2caed8f420802b59f382c25160b114ccc665dbac9c5046e7fceaced2"
}

# The operating system. Can be wxp, w2k, w2k3, w2k8, wvista, win7, win8, win10, win11, l24 (Linux 2.4), l26 (Linux 2.6+), solaris or other. Defaults to other.
variable "os" {
  type    = string
  default = "l26"
}

variable "iso_url" {
  type    = string
  default = "https://cdimage.ubuntu.com/ubuntu-legacy-server/releases/20.04/release/ubuntu-20.04.1-legacy-server-amd64.iso"
}

variable "vm_cpu_cores" {
  type    = string
  default = "4"
}

variable "vm_disk_size" {
  type    = string
  default = "200G"
}

variable "vm_memory" {
  type    = string
  default = "4096"
}

variable "vm_name" {
  type    = string
  default = "remnux-template"
}

variable "ssh_password" {
  type    = string
  default = "password"
}

variable "ssh_username" {
  type    = string
  default = "localuser"
}

# This block has to be in each file or packer won't be able to use the variables
variable "proxmox_url" {
  type = string
}
variable "proxmox_host" {
  type = string
}
variable "proxmox_username" {
  type = string
}
variable "proxmox_password" {
  type      = string
  sensitive = true
}
variable "proxmox_storage_pool" {
  type = string
}
variable "proxmox_storage_format" {
  type = string
}
variable "proxmox_skip_tls_verify" {
  type = bool
}
variable "proxmox_pool" {
  type = string
}
variable "iso_storage_pool" {
  type = string
}
variable "ansible_home" {
  type = string
}
variable "ludus_nat_interface" {
  type = string
}
####

locals {
  template_description = "REMnux (Ubutntu 20.04) template built ${legacy_isotime("2006-01-02 03:04:05")} username:password => localuser:password"
}

source "proxmox-iso" "remnux" {
  boot_command = [
    "<esc><wait>",
    "<esc><wait>",
    "<enter><wait>",
    "/install/vmlinuz <wait>",
    "auto <wait>",
    "console-setup/ask_detect=false ",
    "debconf/frontend=noninteractive ",
    "debian-installer=en_US ",
    "fb=false ",
    "hostname=remnux ",
    "initrd=/install/initrd.gz ",
    "kbd-chooser/method=us ",
    "keyboard-configuration/modelcode=SKIP ",
    "keyboard-configuration/layout=USA ",
    "keyboard-configuration/variant=USA ",
    "locale=en_US ",
    "passwd/username=localuser ",
    "passwd/user-fullname=localuser ",
    "passwd/user-password=password ",
    "passwd/user-password-again=password ",
    "noapic ",
    "preseed/url=http://{{.HTTPIP}}:{{.HTTPPort}}/remnux.cfg ",
    " -- <enter>"
  ]
  boot_key_interval      = "20ms"
  boot_keygroup_interval = "20ms"
  http_directory         = "./http"

  communicator    = "ssh"
  cores           = "${var.vm_cpu_cores}"
  cpu_type        = "host"
  scsi_controller = "virtio-scsi-single"
  disks {
    disk_size         = "${var.vm_disk_size}"
    format            = "${var.proxmox_storage_format}"
    storage_pool      = "${var.proxmox_storage_pool}"
    type              = "scsi"
    ssd               = true
    discard           = true
    io_thread         = true
  }
  pool                     = "${var.proxmox_pool}"
  insecure_skip_tls_verify = "${var.proxmox_skip_tls_verify}"
  iso_checksum             = "${var.iso_checksum}"
  iso_url                  = "${var.iso_url}"
  iso_storage_pool         = "${var.iso_storage_pool}"
  memory                   = "${var.vm_memory}"
  network_adapters {
    bridge = "${var.ludus_nat_interface}"
    model  = "virtio"
  }
  node                 = "${var.proxmox_host}"
  os                   = "${var.os}"
  password             = "${var.proxmox_password}"
  proxmox_url          = "${var.proxmox_url}"
  template_description = "${local.template_description}"
  username             = "${var.proxmox_username}"
  vm_name              = "${var.vm_name}"
  ssh_password         = "${var.ssh_password}"
  ssh_username         = "${var.ssh_username}"
  ssh_wait_timeout     = "30m"
  unmount_iso          = true
  task_timeout         = "20m" // On slow disks the imgcopy operation takes > 1m
}

build {
  sources = ["source.proxmox-iso.remnux"]

  provisioner "ansible" {
    playbook_file = "ansible/post-boot-config.yml"
    use_proxy     = false
    user = "${var.ssh_username}"
    extra_arguments = ["--extra-vars", "{ansible_python_interpreter: /usr/bin/python3, ansible_password: ${var.ssh_password}, ansible_sudo_pass: ${var.ssh_password}}"]
    ansible_env_vars = ["ANSIBLE_HOME=${var.ansible_home}", "ANSIBLE_LOCAL_TEMP=${var.ansible_home}/tmp", "ANSIBLE_PERSISTENT_CONTROL_PATH_DIR=${var.ansible_home}/pc", "ANSIBLE_SSH_CONTROL_PATH_DIR=${var.ansible_home}/cp"]
    skip_version_check = true
  }

  provisioner "ansible" {
    playbook_file = "ansible/install-remnux.yml"
    use_proxy     = false
    user = "${var.ssh_username}"
    extra_arguments = ["--extra-vars", "{ansible_python_interpreter: /usr/bin/python3, ansible_password: ${var.ssh_password}, ansible_sudo_pass: ${var.ssh_password}}"]
    ansible_env_vars = ["ANSIBLE_HOME=${var.ansible_home}", "ANSIBLE_LOCAL_TEMP=${var.ansible_home}/tmp", "ANSIBLE_PERSISTENT_CONTROL_PATH_DIR=${var.ansible_home}/pc", "ANSIBLE_SSH_CONTROL_PATH_DIR=${var.ansible_home}/cp"]
    skip_version_check = true
    timeout = "4h"
  }
}
