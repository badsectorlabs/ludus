variable "iso_checksum" {
  type    = string
  default = "sha512:4460ef6470f6d8ae193c268e213d33a6a5a0da90c2d30c1024784faa4e4473f0c9b546a41e2d34c43fbbd43542ae4fb93cfd5cb6ac9b88a476f1a6877c478674"
}

# The operating system. Can be wxp, w2k, w2k3, w2k8, wvista, win7, win8, win10, l24 (Linux 2.4), l26 (Linux 2.6+), solaris or other. Defaults to other.
variable "os" {
  type    = string
  default = "l26"
}

variable "iso_url" {
  type    = string
  default = "https://cdimage.debian.org/cdimage/archive/11.7.0/amd64/iso-cd/debian-11.7.0-amd64-netinst.iso"
}

variable "vm_cpu_cores" {
  type    = string
  default = "2"
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
  default = "debian-11-x64-server-template"
}

variable "ssh_password" {
  type    = string
  default = "debian"
}

variable "ssh_username" {
  type    = string
  default = "debian"
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
####

locals {
  template_description = "Debian 11 template built ${legacy_isotime("2006-01-02 03:04:05")}"
}

source "proxmox-iso" "debian11" {
  boot_command = [
    "<down><tab><wait>", # non-graphical install
    "preseed/url=http://{{ .HTTPIP }}:{{ .HTTPPort }}/debian-11-preseed.cfg ",
    "<wait>language=en locale=en_US.UTF-8 ",
    "<wait>country=US keymap=us ",
    "<wait>hostname=debian11 domain=local ",
    "<enter><wait>",
  ]
  boot_key_interval = "100ms"
  boot_wait = "15s"
  http_directory = "./http"

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
    bridge = "vmbr0"
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
  sources = ["source.proxmox-iso.debian11"]
}

