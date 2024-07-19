---
sidebar_position: 7
title: "🚇 Outbound WireGuard"
---

# 🚇 Outbound WireGuard

:::note[🏛️ `Available in Ludus Enterprise`]
:::

## Setup

This feature routes range traffic out over a WireGuard tunnel specified in the range configuration.
This can be useful for OPSEC, OSINT, or malware research. 

**While enabled, Ludus users can still interact directly VMs via RDP, SSH, etc via their Ludus WireGuard tunnel, and Ludus can still reach the VMs to configure them.**

To enable this feature, specify the `router` item in your configuration and populate the `outbound_wireguard_config` and `outbound_wireguard_vlans` keys.


The `AllowedIPs` value in your WireGuard configuration should always be `0.0.0.0/0`.
Ludus does not support "split tunnel" WireGuard configurations for otubound Wireguard at this time. Please contact us if this feature is required in your environment.

```yaml title="range-config.yml"
...
router:
  outbound_wireguard_config: |-
    [Interface]
    PrivateKey = XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX=
    Address = 10.0.38.224/32
    DNS = 91.231.153.2, 192.211.0.2

    [Peer]
    PublicKey = XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX=
    AllowedIPs = 0.0.0.0/0
    Endpoint = my.wireguard.provider.net:51820
  outbound_wireguard_vlans: # Specify which VLANs should be routed over the WireGuard tunnel
    - 10
...
```

:::warning

IPv6 addresses in the `Address` or `AllowedIPs` fields are not supported

:::

## How does it work?

In order to route traffic over the WireGuard tunnel, the Linux (Debian) router marks packets from the `outbound_wireguard_vlans` (except those destined for `192.0.2.254` which is the Ludus host, or `198.51.100.0/24` which are client WireGuard addresses) using iptables. It then uses an `ip` rule to use a special `outbound_wg` routing table for these packets.

In the following example, the `ens19` interface is the interface for VLAN 10 in `outbound_wireguard_vlans`.

<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 600">
  <!-- Background -->
  <rect width="800" height="600" fill="#f0f0f0"/>
  
  <!-- Debian Router Box -->
  <rect x="200" y="100" width="400" height="400" fill="#ffffff" stroke="#000000" stroke-width="2"/>
  <text x="400" y="130" font-family="Arial, sans-serif" font-size="20" text-anchor="middle">Debian Router</text>
  
  <!-- Interfaces -->
  <rect x="180" y="200" width="80" height="40" fill="#a0d0ff" stroke="#000000"/>
  <text x="220" y="225" font-family="Arial, sans-serif" font-size="14" text-anchor="middle">ens18</text>
  
  <rect x="180" y="300" width="80" height="40" fill="#ffcca0" stroke="#000000"/>
  <text x="220" y="325" font-family="Arial, sans-serif" font-size="14" text-anchor="middle">ens19</text>
  
  <rect x="180" y="400" width="80" height="40" fill="#a0ffa0" stroke="#000000"/>
  <text x="220" y="425" font-family="Arial, sans-serif" font-size="14" text-anchor="middle">ens20</text>
  
  <rect x="540" y="300" width="80" height="40" fill="#ffa0a0" stroke="#000000"/>
  <text x="580" y="325" font-family="Arial, sans-serif" font-size="14" text-anchor="middle">wg0</text>
  
  <!-- Routing Table -->
  <rect x="300" y="200" width="200" height="240" fill="#ffffcc" stroke="#000000"/>
  <text x="400" y="225" font-family="Arial, sans-serif" font-size="16" text-anchor="middle">Routing Tables</text>
  <text x="310" y="260" font-family="Arial, sans-serif" font-size="14">Main Table</text>
  <text x="310" y="320" font-family="Arial, sans-serif" font-size="14">WG Table (fwmark 0x1)</text>
  
  <!-- Arrows -->
  <line x1="260" y1="220" x2="300" y2="260" stroke="#0000ff" stroke-width="2" marker-end="url(#arrowhead)"/>
  <line x1="260" y1="320" x2="300" y2="320" stroke="#ff6600" stroke-width="2" marker-end="url(#arrowhead)"/>
  <line x1="260" y1="420" x2="300" y2="260" stroke="#00ff00" stroke-width="2" marker-end="url(#arrowhead)"/>
  <line x1="500" y1="320" x2="540" y2="320" stroke="#ff0000" stroke-width="2" marker-end="url(#arrowhead)"/>
  
  <!-- Arrow for default route -->
  <path d="M 500 260 C 550 260, 550 200, 260 200" fill="none" stroke="#800080" stroke-width="2" marker-end="url(#arrowhead)"/>
  <text x="420" y="190" font-family="Arial, sans-serif" font-size="12" fill="#800080">Default Route</text>
  
  <!-- Labels -->
  <text x="280" y="190" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">Internet</text>
  <text x="280" y="290" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">VLAN 10</text>
  <text x="280" y="390" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">VLAN 99</text>
  <text x="520" y="290" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">WireGuard</text>
  <text x="520" y="350" font-family="Arial, sans-serif" font-size="12" text-anchor="middle">Tunnel</text>
  
  <!-- Legend -->
  <rect x="600" y="450" width="180" height="150" fill="#ffffff" stroke="#000000"/>
  <text x="610" y="470" font-family="Arial, sans-serif" font-size="14" font-weight="bold">Legend</text>
  <rect x="610" y="480" width="20" height="20" fill="#a0d0ff"/>
  <text x="635" y="495" font-family="Arial, sans-serif" font-size="12">ens18 (Internet)</text>
  <rect x="610" y="505" width="20" height="20" fill="#ffcca0"/>
  <text x="635" y="520" font-family="Arial, sans-serif" font-size="12">ens19 (VLAN 10)</text>
  <rect x="610" y="530" width="20" height="20" fill="#a0ffa0"/>
  <text x="635" y="545" font-family="Arial, sans-serif" font-size="12">ens20 (VLAN 99)</text>
  <rect x="610" y="555" width="20" height="20" fill="#ffa0a0"/>
  <text x="635" y="570" font-family="Arial, sans-serif" font-size="12">outbound_wg (WireGuard)</text>
  <rect x="610" y="580" width="20" height="20" fill="#800080"/>
  <text x="635" y="595" font-family="Arial, sans-serif" font-size="12">Default Route</text>
  
  <!-- Arrow definition -->
  <defs>
    <marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto">
      <polygon points="0 0, 10 3.5, 0 7" />
    </marker>
  </defs>
</svg>

This is accomplished with 2 iptables rules in the `MANGLE` table's `PREROUTING` chain, and the modification of the `NAT` table's `POSTROUTING` rule for the user specified vlan's interfaces.


```plaintext title="Normal MANGLE table PREROUTING"
Chain PREROUTING (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination
```

```plaintext title="Normal NAT table POSTROUTING"
Chain POSTROUTING (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination
  146  8279 MASQUERADE  all  --  *      ens18   10.2.10.0/24        !198.51.100.0/24
    1    76 MASQUERADE  all  --  *      ens18   10.2.99.0/24        !198.51.100.0/24
```

After the outbound WireGuard tunnel is enabled:

```plaintext title="Outbound WireGuard enabled for VLAN 10 MANGLE table PREROUTING"
Chain PREROUTING (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination
    0     0 RETURN     all  --  *      *       10.2.10.0/24         192.0.2.254
   11   646 MARK       all  --  *      *       10.2.10.0/24        !198.51.100.0/24      MARK set 0x1
```

```plaintext title="Outbound WireGuard enabled for VLAN 10 NAT table POSTROUTING"
Chain POSTROUTING (policy ACCEPT 0 packets, 0 bytes)
 pkts bytes target     prot opt in     out     source               destination
  146  8279 MASQUERADE  all  --  *      outbound_wg   10.2.10.0/24        !198.51.100.0/24
    1    76 MASQUERADE  all  --  *      ens18   10.2.99.0/24        !198.51.100.0/24
```