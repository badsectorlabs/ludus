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

You will need to replace `{InstanceName}`, `{region}`, `{Zone}`, `{project_id}`, `{SSH_User}` and `{SSH_Key}`.
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

Ludus requires Proxmox VE, so install Proxmox VE on the GCP VM first.

1. Install Proxmox VE on the VM. The examples above use Debian 12, which supports [Proxmox VE 8 on Debian 12 (Bookworm)](https://pve.proxmox.com/wiki/Install_Proxmox_VE_on_Debian_12_Bookworm). For Proxmox VE 9, create the VM from a Debian 13 image and follow [Proxmox VE 9 on Debian 13 (Trixie)](https://pve.proxmox.com/wiki/Install_Proxmox_VE_on_Debian_13_Trixie). Leave the VM's network interface as GCP configured it; do not move its address onto a bridge.
2. Create a private NAT bridge for the Ludus container as described in [Hosts with a single public address](./proxmox-lxc.md#hosts-with-a-single-public-address).
3. Follow [Install Ludus](../quick-start/install-ludus) on the Proxmox host. Set the WireGuard endpoint to the VM's external IP; the default is its internal VPC address.
4. Allow WireGuard through the VPC firewall, for example: `gcloud compute firewall-rules create ludus-wireguard --allow=udp:51820`.
5. Once the install succeeds, follow the Quick start guide as normal starting at [Create a User](../quick-start/create-a-user).

## Troubleshooting

On GCP's Debian images, installing the `proxmox-ve` package can stop with `ifupdown2` reporting `Another instance of this program is already running`, or `grub-pc` reporting `You must correct your GRUB install devices before proceeding`. Select the boot disk with `DEBIAN_FRONTEND=dialog dpkg --configure grub-pc`, then finish the package configuration with `dpkg --configure -a`.