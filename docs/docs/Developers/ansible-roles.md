---
title: Ansible Roles
---

# Ansible Roles for Ludus

## Role structure

Ansible roles should follow the [standard structure](https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_reuse_roles.html#role-directory-structure) and must have a `meta` folder with a `main.yml` file.

An example role is [ludus_adcs](https://github.com/bad-sector-labs/ludus_adcs).

## Ludus specific variables

When developing a role for Ludus, you may want to access information about a host for use in your role.
The following variables are available for your use and reflect the values for the specific host that is executing your role:

```
ludus_dns_server          # Will always be the .254 of this VMs VLAN (i.e. 10.2.10.254 for a VM in VLAN 10)
ludus_domain_fqdn         # The full domain, if the VM has a domain defined, (i.e. ludus.internal.domain)
ludus_domain_netbios_name # The netbios part of the VM's domain, if the VM has a domain defined (i.e. ludus)
ludus_domain_fqdn_tail    # The non-netbios part of the VM's domain, if the VM has a domain defined (i.e. internal.domain)
ludus_dc_vm_name          # The name of the VM that is the primary DC for this VM's domain, if the VM has a domain defined
ludus_dc_ip               # The IP of the VM that is the primary DC for this VM's domain, if the VM has a domain defined
ludus_dc_hostname         # The hostname of the VM that is the primary DC for this VM's domain, if the VM has a domain defined
```

All other ansible variables (i.e. `ansible_hostname`) and Ludus variables are also available to custom roles, such as `defaults`, `ludus`, or `network` as defined in the user's config.