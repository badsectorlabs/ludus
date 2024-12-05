---
title: Kali
---

# Kali APT error

As of 2024-12-05 there is an APT error with Kali that prevents any packages from being installed after initial install.

A [bug](https://bugs.kali.org/view.php?id=9027) has been reported and is being tracked by the Kali maintainers.

In the meantime, you can comment out the provisioner part of the kali hcl at `/opt/ludus/packer/kali/kali.pkr.hcl`

```
build {
  sources = ["source.proxmox-iso.kali"]

//  provisioner "ansible" {
//    user               = "${var.ssh_username}"
//    use_proxy          = false
//    extra_arguments    = ["--extra-vars", "{ansible_python_interpreter: /usr/bin/python3, ansible_password: ${var.ssh_password}, ansible_sudo_pass: ${var.ssh_password}}"]
//    playbook_file      = "./kali.yml"
//    ansible_env_vars   = ["ANSIBLE_HOME=${var.ansible_home}", "ANSIBLE_LOCAL_TEMP=${var.ansible_home}/tmp", "ANSIBLE_PERSISTENT_CONTROL_PATH_DIR=${var.ansible_home}/pc", "ANSIBLE_SSH_CONTROL_PATH_DIR=${var.ansible_home}/cp"]
//    skip_version_check = true
//  }

}

```
That at least gets you a base Kali tempalte, but without KasmVNC.
You can install the KasmVNC manually, but ansible won't go past the error


# Kali GRUB install error

Your Kali install may fail with a GRUB boot loader error (as of 2024-02-08)

![](/img/troubleshooting/kali/kali-grub-1.png)

Packer will wait 30 minutes from boot for SSH to become available, so you need to perform the following steps to complete the installation until the `dpkg` package is fixed by the Kali maintainers.

1. Log into your Ludus host's Proxmox web interface (https://< ludus IP>:8006), click on the Kali VM, and click on Console. Click the noVNC tab in the center left of the screen. Click the "A" icon and then click "Alt" and press F2 on your keyboard.

![](/img/troubleshooting/kali/kali-grub-2.png)

2. Press enter at the new screen to get a console

![](/img/troubleshooting/kali/kali-grub-3.png)

3. Click "ALT" again to deselect it

![](/img/troubleshooting/kali/kali-grub-4.png)

4. Type the following commands:

```
chroot /target bash
echo -e "#!/bin/bash\nexec true" > /sbin/start-stop-daemon
chmod +x /sbin/start-stop-daemon
```

![](/img/troubleshooting/kali/kali-grub-5.png)

5. Run `apt reinstall dpkg`

![](/img/troubleshooting/kali/kali-grub-6.png)

6. Active "Alt" again and press F1 on your keyboard

![](/img/troubleshooting/kali/kali-grub-7.png)

7. This will bring you back to the red screen. Deselect "Alt" and press Enter to continue.

![](/img/troubleshooting/kali/kali-grub-8.png)

8. Press enter with "Install the GRUB boot loader" highlighted to finish the Kali install. Packer will pick up on reboot and complete the template creation process.

![](/img/troubleshooting/kali/kali-grub-9.png)