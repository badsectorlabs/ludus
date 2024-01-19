variable "iso_checksum" {
  type    = string
  default = "sha256:c8dbc96b61d04c8b01faf6ce0794fdf33965c7b350eaa3eb1e6697019902945c"
}

# https://github.com/proxmox/qemu-server/blob/9b1971c5c991540f27270022e586aec5082b0848/PVE/QemuServer.pm#L412
variable "os" {
  type    = string
  default = "win11"
}

variable "iso_url" {
  type    = string
  default = "https://software-static.download.prss.microsoft.com/dbazure/888969d5-f34g-4e03-ac9d-1f9786c66749/22631.2428.231001-0608.23H2_NI_RELEASE_SVC_REFRESH_CLIENTENTERPRISEEVAL_OEMRET_x64FRE_en-us.iso"
}

variable "iso_storage_pool" {
  type    = string
  default = "local"
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
  default = "win11-23h2-x64-enterprise-template"
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
variable "ansible_home" {
  type = string
}
####

locals {
  template_description = "Windows 11 23H2 64-bit Enterprise template built ${legacy_isotime("2006-01-02 03:04:05")}"
}

source "proxmox-iso" "win11-23h2-x64-enterprise" {
  # Hit the "Press any key to boot from CD ROM"
  boot_wait = "-1s" # To set boot_wait to 0s, use a negative number, such as "-1s"
  boot_command = [  # 120 seconds of enters to cover all different speeds of disks as windows boots
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>",
    "<return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait><return><wait>"
  ]
  additional_iso_files {
    device           = "sata3"
    iso_storage_pool = "${var.iso_storage_pool}"
    unmount          = true
    cd_label         = "PROVISION"
    cd_files = [
      "iso/setup-for-ansible.ps1",
      "iso/win-updates.ps1",
      "iso/windows-common-setup.ps1",
      "Autounattend.xml",
    ]
  }
  additional_iso_files {
    device           = "sata4"
    iso_checksum     = "sha256:ebd48258668f7f78e026ed276c28a9d19d83e020ffa080ad69910dc86bbcbcc6"
    iso_url          = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.240-1/virtio-win-0.1.240.iso"
    iso_storage_pool = "${var.iso_storage_pool}"
    unmount          = true
  }
  # Required for Win11
  bios = "ovmf"
  efi_config {
    efi_storage_pool  = "local"
    pre_enrolled_keys = true
    efi_type          = "4m"
  }
  # End Win11 required option

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
  winrm_timeout        = "60m"
  unmount_iso          = true
  task_timeout         = "20m" // On slow disks the imgcopy operation takes > 1m
}

build {
  sources = ["source.proxmox-iso.win11-23h2-x64-enterprise"]

  provisioner "windows-shell" {
    scripts = ["scripts/disablewinupdate.bat"]
  }

  provisioner "powershell" {
    scripts = ["scripts/disable-hibernate.ps1"]
  }

  provisioner "powershell" {
    scripts = ["scripts/install-virtio-drivers.ps1"]
  }

}
