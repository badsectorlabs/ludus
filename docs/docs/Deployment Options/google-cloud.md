---
title: Google Cloud Platform (GCP)
---

# Google Cloud Platform (GCP)

## Deploying Debian 12 Using `gcloud`

The following command will create a GCP VM with nested virtualization enabled, 500GB of disk space, 16 CPUs and 72 GB of RAM. Adjust the values as required.

You will need to replace `{InstanceName}`, `{Zone}`, `{ProjectID}`, `{GCP UserName}`, `{UserName}`, and `{SSH KEY}`- don't forget to escape spaces with a `\`.

The `{GCP Username}` will the be the user that is assigned the SSH key for the VM. Log in as this user.

```
gcloud compute instances create {InstanceName} \
  --enable-nested-virtualization \
  --zone={Zone} \
  --create-disk=auto-delete=yes,boot=yes,device-name={InstanceName},image=projects/debian-cloud/global/images/debian-12-bookworm-v20240213,mode=rw,size=500,type=projects/{ProjectID}/zones/us-central1-a/diskTypes/pd-balanced  \
  --visible-core-count 4 \
  --custom-cpu 16 \
  --custom-memory 72 \
  --metadata=ssh-keys={GCP UserName}:{Protocol}\ \
{Key}\ {UserName}
```

Example:
```
gcloud compute instances create ludus \
  --enable-nested-virtualization \
  --zone=us-central1 \
  --create-disk=auto-delete=yes,boot=yes,device-name=ludus,image=projects/debian-cloud/global/images/debian-12-bookworm-v20240213,mode=rw,size=500,type=projects/myproject/zones/us-central1-a/diskTypes/pd-balanced  \
  --visible-core-count 4 \
  --custom-cpu 16 \
  --custom-memory 72 \
  --metadata=ssh-keys=myname:ssh-ed25519\ AAAAC3NzaC1lZDI1NTE5AAAAIHm8UFxzLleq30n+CFdsPGZtOoGjZQus53ffCD9Zik3D\ username@host
```

## Deploying Debian 12 Using Terraform

You will need to replace `{InstanceName}`, `{region}`, `{Zone}`, `{project_id}`, `{SSH_User} and `{SSH_KEY}`.
This will install a new Debian 12 server with 24 cores, 82GB Memory, 500GB SSD and nested virtualization enabled (on Intel Haswell chip)

```
provider "google" {
  region = "{region}"
  project = "{project_id}"
}

resource "google_compute_instance" "{InstanceName}" {
  boot_disk {
    auto_delete = true
    device_name = "{InstanceName}"

    initialize_params {
      image = "projects/debian-cloud/global/images/debian-12-bookworm-v20240213"
      size  = 500
      type  = "pd-balanced"
    }

    mode = "READ_WRITE"
  }

  can_ip_forward      = false
  deletion_protection = false
  enable_display      = false

  labels = {
    goog-ec-src = "vm_add-tf"
  }

  machine_type     = "custom-24-81920"
  min_cpu_platform = "Intel Haswell"

  metadata = {
    ssh-keys = "{SSH_USER}:{SSH KEY}"
  }

  name = "{InstanceName}"

  network_interface {
    access_config {
      network_tier = "PREMIUM"
    }

    queue_count = 0
    stack_type  = "IPV4_ONLY"
    subnetwork  = "projects/{project_id}/regions/{region}/subnetworks/default"
  }

  scheduling {
    automatic_restart   = true
    on_host_maintenance = "MIGRATE"
    preemptible         = false
    provisioning_model  = "STANDARD"
  }


  shielded_instance_config {
    enable_integrity_monitoring = true
    enable_secure_boot          = false
    enable_vtpm                 = true
  }

  zone = "{Zone}"
  advanced_machine_features {
    enable_nested_virtualization   = true
  }
}
```

## Install

1. Copy the `ludus-server` binary to the VM once it has deployed.
2. Make the `ludus-server` binary executable with `chmod +x ludus-server`.
3. Run the `ludus-server` binary: `./ludus-server` but do not agree to the prompt - the public IP is likely wrong.
4. Edit the config file at `/opt/ludus/config.yml` and update the public IP to the public IP of the GCP instance.
5. Run `/opt/ludus/ludus-server` and agree to the prompt to start the install.
6. When the VM reboots, SSH back in and run `ludus-install-status` as root to monitor the install.
7. Once the install succeeds, follow the Quick start guide as normal starting at [Create a User](../Quick%20Start/create-a-user).

## Troubleshooting

This error will automatically be recovered (as of v.1.1.3):

```
TASK [lae.proxmox : Install Proxmox VE and related packages] *******************
FAILED - RETRYING: [127.0.0.1]: Install Proxmox VE and related packages (2 retries left).
FAILED - RETRYING: [127.0.0.1]: Install Proxmox VE and related packages (1 retries left).
fatal: [127.0.0.1]: FAILED! => {"attempts": 2, "cache_update_time": 1708546691, "cache_updated": false, "changed": false, "msg": "'/usr/bin/apt-get -y -o \"Dpkg::Options::=--force-confdef\" -o \"Dpkg::Options::=--force-confold\"       install 'proxmox-ve=8.1.0'' failed: E: Sub-process /usr/bin/dpkg returned an error code (1)\n", "rc": 100, "stderr": "E: Sub-process /usr/bin/dpkg returned an error code (1)\n", "stderr_lines": ["E: Sub-process /usr/bin/dpkg returned an error code (1)"], "stdout": "Reading package lists...\nBuilding dependency tree...\nReading state information...\nproxmox-ve is already the newest version (8.1.0).\nThe following package was automatically installed and is no longer required:\n  python3-distro-info\nUse 'apt autoremove' to remove it.\n0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n4 not fully installed or removed.\nAfter this operation, 0 B of additional disk space will be used.\nSetting up ifupdown2 (3.2.0-1+pmx8) ...\r\n\r\nnetwork config changes have been detected for ifupdown2 compatibility.\r\nSaved in /etc/network/interfaces.new for hot-apply or next reboot.\r\n\r\nReloading network config on first install\r\nerror: Another instance of this program is already running.\r\ndpkg: error processing package ifupdown2 (--configure):\r\n installed ifupdown2 package post-installation script subprocess returned error exit status 89\r\nSetting up grub-pc (2.06-13+pmx1) ...\r\ngrub-pc: Running grub-install ...\r\nYou must correct your GRUB install devices before proceeding:\r\n\r\n  DEBIAN_FRONTEND=dialog dpkg --configure grub-pc\r\n  dpkg --configure -a\r\ndpkg: error processing package grub-pc (--configure):\r\n installed grub-pc package post-installation script subprocess returned error exit status 1\r\ndpkg: dependency problems prevent configuration of pve-manager:\r\n pve-manager depends on ifupdown2 (>= 3.0) | ifenslave (>= 2.6); however:\r\n  Package ifupdown2 is not configured yet.\r\n  Package ifenslave is not installed.\r\n\r\ndpkg: error processing package pve-manager (--configure):\r\n dependency problems - leaving unconfigured\r\ndpkg: dependency problems prevent configuration of proxmox-ve:\r\n proxmox-ve depends on pve-manager (>= 8.0.4); however:\r\n  Package pve-manager is not configured yet.\r\n\r\ndpkg: error processing package proxmox-ve (--configure):\r\n dependency problems - leaving unconfigured\r\nErrors were encountered while processing:\r\n ifupdown2\r\n grub-pc\r\n pve-manager\r\n proxmox-ve\r\n", "stdout_lines": ["Reading package lists...", "Building dependency tree...", "Reading state information...", "proxmox-ve is already the newest version (8.1.0).", "The following package was automatically installed and is no longer required:", "  python3-distro-info", "Use 'apt autoremove' to remove it.", "0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.", "4 not fully installed or removed.", "After this operation, 0 B of additional disk space will be used.", "Setting up ifupdown2 (3.2.0-1+pmx8) ...", "", "network config changes have been detected for ifupdown2 compatibility.", "Saved in /etc/network/interfaces.new for hot-apply or next reboot.", "", "Reloading network config on first install", "error: Another instance of this program is already running.", "dpkg: error processing package ifupdown2 (--configure):", " installed ifupdown2 package post-installation script subprocess returned error exit status 89", "Setting up grub-pc (2.06-13+pmx1) ...", "grub-pc: Running grub-install ...", "You must correct your GRUB install devices before proceeding:", "", "  DEBIAN_FRONTEND=dialog dpkg --configure grub-pc", "  dpkg --configure -a", "dpkg: error processing package grub-pc (--configure):", " installed grub-pc package post-installation script subprocess returned error exit status 1", "dpkg: dependency problems prevent configuration of pve-manager:", " pve-manager depends on ifupdown2 (>= 3.0) | ifenslave (>= 2.6); however:", "  Package ifupdown2 is not configured yet.", "  Package ifenslave is not installed.", "", "dpkg: error processing package pve-manager (--configure):", " dependency problems - leaving unconfigured", "dpkg: dependency problems prevent configuration of proxmox-ve:", " proxmox-ve depends on pve-manager (>= 8.0.4); however:", "  Package pve-manager is not configured yet.", "", "dpkg: error processing package proxmox-ve (--configure):", " dependency problems - leaving unconfigured", "Errors were encountered while processing:", " ifupdown2", " grub-pc", " pve-manager", " proxmox-ve"]}
```