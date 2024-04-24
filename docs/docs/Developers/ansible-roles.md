---
title: Ansible Roles
---

# Ansible Roles for Ludus

## Role structure

Ansible roles should follow the [standard structure](https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_reuse_roles.html#role-directory-structure) and must have a `meta` folder with a `main.yml` file.

:::tip

Use the [ludus role template](https://github.com/badsectorlabs/ludus_ansible_role_template) to quickly get started.
:::

Example roles can be found in the table on the [roles page](../roles)

If you've build a cool role you'd like to share with us, let us know [via email](mailto:info@badsectorlabs.com), ping us on X ([@badsectorlabs](https://twitter.com/badsectorlabs)), or in our [Discord](https://discord.gg/HryzhdUSYT) server and submit a pull request to have it added to the [roles page](../roles).

## Testing roles

:::note

Requires Ludus server 1.3.0 or later

:::

To quickly test roles, use the `-t user-defined-roles`, `--limit` and `--only-roles` flags to execute only the role you are testing on the machine you are testing it on.

For example, given the following range config that begins:

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64-1"
    hostname: "{{ range_id }}-DC01-2022"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 6
    cpus: 4
    windows:
      sysprep: true
    domain:
      fqdn: ludus.domain
      role: primary-dc
    // highlight-next-line
    roles:
    // highlight-next-line
      - testing_role
      - a_stable_role
      - another_stable_role
...
```

If you wish to only run the `testing_role` role on `JD-ad-dc-win2022-server-x64-1` (assuming range_id is JD) you would run:

```shell-session
#terminal-command-local
ludus range deploy -t user-defined-roles --limit localhost,JD-ad-dc-win2022-server-x64-1 \
 --only-roles testing_role
```

Note that you must include `localhost` in your limit argument due to the way Ludus uses localhost to parse the range config.

This command construct enables the rapid testing of ansible roles in a loop such as:

1. Update role code locally in an editor
2. Update role code on the server with `ludus ansible roles add -d ./testing_role --force`
3. Run just the role on the test host with the command described above
4. Examine logs with `ludus range logs -f` or `ludus range errors`
5. Goto: 1

## Ludus specific variables

:::note

Requires Ludus server 1.1.3 or later

:::

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
