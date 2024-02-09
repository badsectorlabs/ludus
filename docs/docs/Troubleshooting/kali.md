---
title: Kali
---

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