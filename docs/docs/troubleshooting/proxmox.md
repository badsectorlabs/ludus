---
title: Proxmox Issues
---

# Proxmox Issues

## Proxmox Web Interface is blank or returns 500 errors

In the web console this is shown as an error loading `/PVE/StdWorkspace.js`.

If the Proxmox web interface is blank after accepting the certificate warning, try to run `apt install --reinstall proxmox-widget-toolkit` as root on your Ludus host, then reload the web page.

This is an issue some users have with Proxmox and has been reported [on the Proxmox forum](https://forum.proxmox.com/threads/blank-webgui.130366/) as well as the [Ludus issue tracker](http://gitlab.com/badsectorlabs/ludus/-/issues/109).