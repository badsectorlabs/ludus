variable "iso_checksum" {
  type    = string
  default = "sha256:eef8d26018f4fcc0dc101c468f65cbf588f2184900c556f243802e9698e56729"
}

# The operating system. Can be wxp, w2k, w2k3, w2k8, wvista, win7, win8, win10, l24 (Linux 2.4), l26 (Linux 2.6+), solaris or other. Defaults to other.
variable "os" {
  type    = string
  default = "l26"
}

variable "iso_url" {
  type    = string
  default = "https://download.rockylinux.org/pub/rocky/9/isos/x86_64/Rocky-9.3-x86_64-minimal.iso"
}

variable "vm_cpu_cores" {
  type    = string
  default = "4"
}

variable "vm_disk_size" {
  type    = string
  default = "60G"
}

variable "vm_memory" {
  type    = string
  default = "8192"
}

variable "vm_name" {
  type    = string
  default = "rocky-9-x64-server-template"
}

variable "ssh_password" {
  type    = string
  default = "root"
}

variable "ssh_username" {
  type    = string
  default = "root"
}

# This block has to be in each file or packer won't be able to use the variables
variable "proxmox_url" {
  type =  string
}
variable "proxmox_host" {
  type =  string
}
variable "proxmox_username" {
  type =  string
}
variable "proxmox_password" {
  type =  string
  sensitive = true
}
variable "proxmox_storage_pool" {
  type =  string
}
variable "proxmox_storage_format" {
  type =  string
}
variable "proxmox_skip_tls_verify" {
  type =  bool
}
variable "proxmox_pool" {
  type = string
}
variable "iso_storage_pool" {
  type =  string
}
variable "ansible_home" {
  type =  string
}
####

locals {
  template_description = "Rocky 9 template built ${legacy_isotime("2006-01-02 03:04:05")}"
}

source "proxmox-iso" "rocky85" {
  boot_command = [
     "<tab> text inst.ks=http://{{ .HTTPIP }}:{{ .HTTPPort }}/rocky-9-preseed.cfg<enter><wait>"
  ]
  boot_key_interval = "100ms"
  http_directory    = "./http"

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
  sources = ["source.proxmox-iso.rocky85"]
}