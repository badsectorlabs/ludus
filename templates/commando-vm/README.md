# Commando VM template

To use this template you must have the [`badsectorlabs.ludus_commandovm`](https://github.com/badsectorlabs/ludus_commandovm) role added by the Ludus user that builds the template.

```bash
ludus ansible role add badsectorlabs.ludus_commandovm
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus/templates
ludus templates add -d commando-vm
ludus templates build
# Wait for the template to successfully build
# You can watch the logs with `ludus template logs -f`
# Or check the status with `ludus template status` and `ludus templates list`
```