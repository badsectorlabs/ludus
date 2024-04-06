# REMnux template

To use this template you must have the [`badsectorlabs.ludus_remnux`](https://github.com/badsectorlabs/ludus_remnux) role added by the Ludus user that builds the template.

```bash
ludus ansible role add badsectorlabs.ludus_remnux
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus/templates
ludus templates add -d remnux
ludus templates build
# Wait for the template to successfully build
# You can watch the logs with `ludus template logs -f`
# Or check the status with `ludus template status` and `ludus templates list`
```

You can watch the template build by accessing the VM's console in Proxmox, and switching to tty2 (alt+F2 or cmd+F2), logging in with `localuser:password` and running

```
watch -n 2 'ps auxwww --sort start_time'
```

This will show you the processes being started by the REMnux installer as it does its work.