---
title: Proxmox Issues
---

# Proxmox Issues

## Proxmox Web Interface is blank or returns 500 errors

In the web console this is shown as an error loading `/PVE/StdWorkspace.js`.

If the Proxmox web interface is blank after accepting the certificate warning, try to run `apt install --reinstall proxmox-widget-toolkit` as root on your Ludus host, then reload the web page.

This is an issue some users have with Proxmox and has been reported [on the Proxmox forum](https://forum.proxmox.com/threads/blank-webgui.130366/) as well as the [Ludus issue tracker](http://gitlab.com/badsectorlabs/ludus/-/issues/109).

## Proxmox API returns 596 timeout errors

On massive deployments (500+ VMs) some API calls may exceed the hardcoded 30 second timeout that `pveproxy` imposes.

Either use `pvesh` from a root shell as it bypasses `pveproxy`, or modify `/usr/share/perl5/PVE/APIServer/AnyEvent.pm` and change line 833

```plain showLineNumbers=830
         $w = http_request(
             $method => $target,
             headers => $headers,
//highlight-next-line
             timeout => 90, # was previously 30
             proxy => undef, # avoid use of $ENV{HTTP_PROXY}
             persistent => $persistent,
```

then restart `pveproxy` with `systemctl restart pveproxy`.