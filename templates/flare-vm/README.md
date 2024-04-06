# Flare VM template

To use this template you must have the [`badsectorlabs.ludus_flarevm`](https://github.com/badsectorlabs/ludus_flarevm) role added by the Ludus user that builds the template.

```bash
ludus ansible role add badsectorlabs.ludus_flarevm
git clone https://gitlab.com/badsectorlabs/ludus.git
cd ludus/templates
ludus templates add -d flare-vm
ludus templates build
# Wait for the template to successfully build
# You can watch the logs with `ludus template logs -f`
# Or check the status with `ludus template status` and `ludus templates list`
```