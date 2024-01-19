variable "iso_checksum" {
  type    = string
  default = "sha256:549bca46c055157291be6c22a3aaaed8330e78ef4382c99ee82c896426a1cee1"
}

variable "os" {
  type    = string
  default = "win10"
}

variable "iso_url" {
  type    = string
  default = "https://software-download.microsoft.com/download/pr/17763.737.190906-2324.rs5_release_svc_refresh_SERVER_EVAL_x64FRE_en-us_1.iso"
}

variable "vm_cpu_cores" {
  type    = string
  default = "2"
}

variable "vm_disk_size" {
  type    = string
  default = "250G"
}

variable "vm_memory" {
  type    = string
  default = "4096"
}

variable "vm_name" {
  type    = string
  default = "win2019-server-x64-template"
}

variable "winrm_password" {
  type    = string
  default = "password"
}

variable "winrm_username" {
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
####

locals {
  template_description = "Windows Server 2019 64-bit template built ${legacy_isotime("2006-01-02 03:04:05")}"
}

source "proxmox-iso" "win2019-server-x64" {
  additional_iso_files {
    device           = "sata3"
    iso_storage_pool = "${var.iso_storage_pool}"
    unmount          = true
    cd_label         = "PROVISION"
    cd_files = [
      "../common/setup-for-ansible.ps1",
      "../common/win-updates.ps1",
      "../common/windows-common-setup.ps1",
      "Autounattend.xml",
    ]
  }
  additional_iso_files {
    device           = "sata4"
    iso_checksum     = "sha256:c88a0dde34605eaee6cf889f3e2a0c2af3caeb91b5df45a125ca4f701acbbbe0"
    iso_url          = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.229-1/virtio-win-0.1.229.iso"
    iso_storage_pool = "${var.iso_storage_pool}"
    unmount          = true
  }
  communicator    = "winrm"
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
  winrm_insecure       = true
  winrm_password       = "${var.winrm_password}"
  winrm_use_ssl        = true
  winrm_username       = "${var.winrm_username}"
  unmount_iso          = true
  winrm_timeout        = "6h" // Sometimes the boot and/or updates can be really really slow
  task_timeout         = "20m" // On slow disks the imgcopy operation takes > 1m
}

build {
  sources = ["source.proxmox-iso.win2019-server-x64"]

  provisioner "ansible" {
    playbook_file = "../ansible/windows_update_security_updates.yml"
    use_proxy     = false
    user          = "${var.winrm_username}"
    extra_arguments = [
      "-e", "ansible_winrm_server_cert_validation=ignore",
      "-e", "ansible_winrm_connection_timeout=300"
    ]
    ansible_env_vars   = ["ANSIBLE_HOME=${var.ansible_home}"]
    skip_version_check = true
  }

  provisioner "windows-shell" {
    scripts = ["../scripts/disablewinupdate.bat"]
  }

  provisioner "powershell" {
    scripts = ["../scripts/disable-hibernate.ps1"]
  }

  provisioner "powershell" {
    scripts = ["../scripts/install-virtio-drivers.ps1"]
  }

}
