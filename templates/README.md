# Ludus Templates

These directories are self-contained Ludus templates.

For more information see: https://docs.ludus.cloud/docs/templates

## Install pre-reqs and add all templates to Ludus

```
ludus ansible role add badsectorlabs.ludus_commandovm
ludus ansible role add badsectorlabs.ludus_flarevm
ludus ansible role add badsectorlabs.ludus_remnux
for DIR in $(ls .); do if [[ "$DIR" == "manual-setup-required" || "$DIR" == "README.md" ]]; then continue; else echo "Adding $DIR" && ludus templates add -d $DIR; fi; done
```