---
title: Ansible Issues
---

# Ansible Issues

## General Ansible Errors

Just up arrow and hit enter!

But really, Ludus actions are [idempotent](https://en.wikipedia.org/wiki/Idempotence#Computer_science_meaning), and these VMs are complex beasts. Sometimes things don't work on the first try. No harm in trying again!

Ansible errors can be parsed and made more readable with the `ludus range errors` command.

## Ansible "Failed to create temporary directory" Error

```
TASK [Gathering Facts] *********************************************************
fatal: [JD-ad-dc-win2019-server-x64]: UNREACHABLE! => {"changed": false, "msg": 
"Failed to create temporary directory. In some cases, you may have been able to
authenticate and did not have permissions on the target directory. Consider
changing the remote tmp path in ansible.cfg to a path rooted in \"/tmp\",
for more error information use -vvv. Failed command was: ( umask 77 && 
mkdir -p \"` echo /home/ludus/.ansible/tmp `\"&& mkdir \"`
echo /home/ludus/.ansible/tmp/ansible-tmp-1704235290.5345225-913183-44415051184218 `\"
&& echo ansible-tmp-1704235290.5345225-913183-44415051184218=\"`
echo /home/ludus/.ansible/tmp/ansible-tmp-1704235290.5345225-913183-44415051184218 `\" ),
exited with result 1", "unreachable": true}
```

This is a long error message, but the key is `"unreachable": true`.

Check that the VM that failed is powered on and reachable. Power cycle the VM if needed. Re-run the ansible that caused this error.

## Unable to retrieve API task ID from node
### Error:
`Unable to retrieve API task ID from node <node name> HTTPSConnectionPool(host='<node name>', port=8006): Read timed out. (read timeout=5)`

### Resolution:

This issue has been seen on existing Proxmox installs. 

Try to `curl https://<node name>:8006/`

If you get an ssl error (`SSL certificate problem: unable to get local issuer certificate`) try copying the `/etc/pve/pve-root-ca.pem` file to `/usr/local/share/ca-certificates/pve-root-ca.crt` (make sure to change the `.pem` extension to `.crt` and run `update-ca-certificates`).

Then try again to `curl https://<node name>:8006/`. If the ssl error issue is gone, chances are ansible API task ID error will be resolved.


## Multiple VMs with name found

### Error:

`Multiple VMs with name ... found, provide vmid instead`

### Resolution:

This issue occurs when there are multiple VMs accessible to the user with the exact same name. This has been seen when a duplicate VM template was created but never fully converted to template (failed build). Make sure that all VM names are unique in both the `SHARED` pool (templates) and the user's range pool.