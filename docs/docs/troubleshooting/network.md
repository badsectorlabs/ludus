---
title: Network Issues
---

# Network Issues

## Templates cannot connect to the internet

If your templates cannot connect to the internet or are getting Automatic Private IP Addressing (APIPA) addresses that start with `169.254`, your Ludus nat interface may be down.

First, get your `ludus_nat_interface` value from `/opt/ludus/config.yml`

```plain
root@ludus:~# cat /opt/ludus/config.yml
---
proxmox_node: ludus
proxmox_interface: vmbr0
proxmox_local_ip: 10.98.108.3
proxmox_public_ip: 10.98.108.3
proxmox_gateway: 10.98.108.1
proxmox_netmask: 255.255.255.0
proxmox_vm_storage_pool: local
proxmox_vm_storage_format: qcow2
proxmox_iso_storage_pool: local
//highlight-next-line
ludus_nat_interface: vmbr1000
prevent_user_ansible_add: false
```

To check if this interface is up, run the following command on the Ludus host:

```plain showLineNumbers
root@ludus:~# ip a
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
       valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host noprefixroute
       valid_lft forever preferred_lft forever
2: ens18: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc pfifo_fast master vmbr0 state UP group default qlen 1000
    link/ether bc:24:11:de:d2:b0 brd ff:ff:ff:ff:ff:ff
    altname enp0s18
3: vmbr0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UP group default qlen 1000
    link/ether bc:24:11:de:d2:b0 brd ff:ff:ff:ff:ff:ff
    inet 10.98.108.3/24 scope global vmbr0
       valid_lft forever preferred_lft forever
    inet6 fe80::be24:11ff:fede:d2b0/64 scope link
       valid_lft forever preferred_lft forever
5: wg0: <POINTOPOINT,NOARP,UP,LOWER_UP> mtu 1420 qdisc noqueue state UNKNOWN group default qlen 1000
    link/none
    inet 198.51.100.1/24 scope global wg0
       valid_lft forever preferred_lft forever
//highlight-next-line
10: vmbr1000: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UNKNOWN group default qlen 1000
    link/ether 82:a4:7a:9a:10:35 brd ff:ff:ff:ff:ff:ff
    inet 192.0.2.254/24 scope global ludus
       valid_lft forever preferred_lft forever
    inet6 fe80::80a4:7aff:fe9a:1035/64 scope link
       valid_lft forever preferred_lft forever
11: vmbr1002: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc noqueue state UNKNOWN group default qlen 1000
    link/ether fa:04:2a:a5:84:d0 brd ff:ff:ff:ff:ff:ff
    inet6 fe80::f804:2aff:fea5:84d0/64 scope link
       valid_lft forever preferred_lft forever
```

If line 21 (or the line that corresponds with your ludus_nat_interface) shows `DOWN` inside the angle brackets for the ludus interface, run the following command to bring the interface `UP`:

```plain
ifup vmbr1000
```

Run `ip a` again to verify the interface is up. Next, check that the MASQUERADE rule is in place:

```plain showLineNumbers
root@proxtest:~# iptables -nvL -t nat
Chain PREROUTING (policy ACCEPT 236 packets, 14443 bytes)
 pkts bytes target     prot opt in     out     source               destination

Chain INPUT (policy ACCEPT 226 packets, 13898 bytes)
 pkts bytes target     prot opt in     out     source               destination

Chain OUTPUT (policy ACCEPT 328 packets, 22056 bytes)
 pkts bytes target     prot opt in     out     source               destination

Chain POSTROUTING (policy ACCEPT 328 packets, 22056 bytes)
 pkts bytes target     prot opt in     out     source               destination
//highlight-next-line
    0     0 MASQUERADE  0    --  *      vmbr0   192.0.2.0/24         0.0.0.0/0
```

Line 13 shows the MASQUERADE rule is in place for the Ludus network range of 192.0.2.0/24. Ludus VMs should now have internet access.

If VMs still are unable to obtain an IP in the 192.0.2.0/24 after this, check the status of the `dnsmasq` service on the Ludus server.

```
systemctl status dnsmasq
```

In some cases, other programs such as `conmand` listen on port 53 which causes a conflict with `dnsmasq`.
Resolve this conflict and restart `dnsmasq`.
Once `dnsmasq` is running, VMs should be able to get an IP address via DHCP and access the internet.

For more information about how this all works, learn more about [Ludus' networking](../networking.md).
