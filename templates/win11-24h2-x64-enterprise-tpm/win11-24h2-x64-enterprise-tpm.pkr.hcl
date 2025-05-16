packer {
  required_plugins {
    proxmox = {
      source  = "github.com/hashicorp/proxmox"
      version = ">= 1.2.2"
    }
  }
}

variable "iso_checksum" {
  type    = string
  default = "sha256:F9013861CB88C168941FE1164DB500157A803E3A7F33A546252BA314A4C76AAD"
}

# https://github.com/proxmox/qemu-server/blob/9b1971c5c991540f27270022e586aec5082b0848/PVE/QemuServer.pm#L412
variable "os" {
  type    = string
  default = "win11"
}

variable "iso_url" {
  type    = string
  default = "https://software-static.download.prss.microsoft.com/dbazure/888969d5-f34g-4e03-ac9d-1f9786c66749/26100.1.240331-1435.ge_release_CLIENTENTERPRISEEVAL_OEMRET_x64FRE_en-us.iso"
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
  default = "win11-24h2-x64-enterprise-tpm-template"
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
variable "ludus_nat_interface" {
  type = string
}
####

locals {
  template_description = "Windows 11 24H2 64-bit Enterprise template built ${legacy_isotime("2006-01-02 03:04:05")} username:password => localuser:password"
}

source "proxmox-iso" "win11-24H2-x64-enterprise-tpm" {
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
  boot_iso {
    iso_checksum             = "${var.iso_checksum}"
    #iso_file                 = "${var.iso_storage_pool}:iso/26100.1.240331-1435.ge_release_SERVER_EVAL_x64FRE_en-us.iso"
    iso_url                  = "${var.iso_url}"
    iso_storage_pool         = "${var.iso_storage_pool}"
    #iso_download_pve         = true
    unmount                  = true
    keep_cdrom_device        = true
  }
  additional_iso_files {
    type              = "sata"
    index             = "3"
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
    type              = "sata"
    index             = "4"
    iso_checksum     = "sha256:ebd48258668f7f78e026ed276c28a9d19d83e020ffa080ad69910dc86bbcbcc6"
    iso_url          = "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.240-1/virtio-win-0.1.240.iso"
    iso_storage_pool = "${var.iso_storage_pool}"
    #iso_download_pve  = true
    unmount          = true
  }
  # Required for Win11
  bios          = "ovmf"
  qemu_agent    = true
  efi_config {
    efi_storage_pool  = "${var.proxmox_storage_pool}"
    pre_enrolled_keys = true
    efi_type          = "4m"
  }
  tpm_config {
    tpm_storage_pool = "${var.proxmox_storage_pool}"
    tpm_version      = "v2.0"
  }
  # End Win11 required option

  communicator    = "winrm"
  cores           = "${var.vm_cpu_cores}"
  cpu_type        = "host"
  scsi_controller = "lsi"
  disks {
    disk_size         = "${var.vm_disk_size}"
    format            = "${var.proxmox_storage_format}"
    storage_pool      = "${var.proxmox_storage_pool}"
    type              = "sata"
    discard           = true
    io_thread         = false
  }
  pool                     = "${var.proxmox_pool}"
  insecure_skip_tls_verify = "${var.proxmox_skip_tls_verify}"
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
  winrm_insecure       = true
  winrm_password       = "${var.winrm_password}"
  winrm_use_ssl        = true
  winrm_username       = "${var.winrm_username}"
  winrm_timeout        = "60m"
  task_timeout         = "20m" // On slow disks the imgcopy operation takes > 1m
}

build {
  sources = ["source.proxmox-iso.win11-24H2-x64-enterprise-tpm"]

  provisioner "ansible" {
    playbook_file = "ansible/windows_update_security_updates.yml"
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
    scripts = ["scripts/disablewinupdate.bat"]
  }

  provisioner "powershell" {
    scripts = ["scripts/disable-hibernate.ps1"]
  }

  provisioner "powershell" {
    scripts = ["scripts/install-virtio-drivers.ps1"]
  }

}
