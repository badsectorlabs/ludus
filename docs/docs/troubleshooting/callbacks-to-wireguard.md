---
title: Getting callbacks to WireGuard clients
---

# How to enable callbacks to WireGuard clients

:::tip

Ludus 2 changed the `wireguard_vlan_default` from `ACCEPT` to `REJECT`

:::

If you want range VMs to be able to initiate connections to WireGuard clients, you must set

```yaml
network:
    wireguard_vlan_default: ACCEPT
```

Alternatively if you want to control which WireGuard clients can receive callbacks from specific IPs/VLANs with specific network rules

```yaml
network:
  rules:
    - name: Allow traffic from a VLAN to any wireguard client
      vlan_src: 10
      vlan_dst: wireguard
      protocol: all
      ports: all
      action: ACCEPT
    - name: Allow traffic from a specific IP to any wireguard client
      vlan_src: 10
      ip_last_octet_src: 11
      vlan_dst: wireguard
      protocol: all
      ports: all
      action: ACCEPT
    - name: Allow traffic from a specific IP to a specific wireguard client
      vlan_src: 10
      ip_last_octet_src: 11
      vlan_dst: wireguard
      ip_last_octet_dst: 2
      protocol: all
      ports: all
      action: ACCEPT
```
