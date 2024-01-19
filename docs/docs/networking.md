---
sidebar_position: 6
---

# Networking

!['The Ludus Network'](/img/network/network.png)

Ludus requires a static IP for access (a requirement inherited from Proxmox) which is set during install.

## Default Networks

Ludus uses [RFC 5735](https://www.rfc-editor.org/rfc/rfc5735#section-4) defined `TEST-NET` networks to hopefully avoid IP conflicts with the network Ludus is deployed into. 

### WireGuard Network (wg0)

The WireGuard interface, `198.51.100.0/24` on `wg0`, is set up during Ludus install. The Ludus host is always `198.51.100.1`. Users will be assigned static IPs in this network starting at the `.2` and incrementing as users are added. The last octet of a user's WireGuard IP will match their range number.

The user's router VM only allows traffic in from the user's WireGuard IP. Additionally, the router VM always allows related or established traffic back out to the user's WireGuard IP, regardless of any user defined rules or testing status. This allows a user to RDP, SSH, VNC, or otherwise access VMs at all times.

### NAT'd Network (vmbr0)

There is a single default network for VMs created during install: `192.0.2.0/24`, on interface `vmbr0`. This network has DHCP and DNS thanks to a `dnsmasq` service running on the Ludus host.
This network's "router" is at `.254` and offers DHCP IPs in the range of `.50` to `.100`. This DHCP pool is used for template creation.

Ludus user routers are assigned a static IP in this network where their range number + 100 is the last octet. If a user has range number 2, their router has a "WAN" interface at `192.0.2.102`. This is what limits Ludus to 153 concurrent users, because each user will take one IP from this pool of 100-253. Ludus sets up a static route for the user's /16 through this "WAN" interface, allowing ansible and wireguard clients to access the range VMs.

#### Reserved IPs in the NAT'd Network

If the Nexus cache server was created by a Ludus admin, it has a static IP of `192.0.2.2` in order to be available to all Ludus user's VMs.

The remaining 49 IPs in the `.1 - .50` range are reserved for future use.

### CI/CD Network (vmbr1)

If the Ludus admin has set up [CI/CD](cicd), the CI/CD network is `203.0.113.0/24` on interface `vmbr1`. This network is necessary because the CI/CD Ludus VMs will themselves set up a `vmbr0` with a range of `192.0.2.0/24` which would conflict with the "public" IP of the CI/CD Ludus VM if it was in the host's `vmbr0` (a DHCP'd `192.0.2.50-100` IP).

## User Networks

Ludus assigns a unique Linux bridge interface in Proxmox to each user which is capable of supporting 255 VLANs (1-255). The user's `vmbr` number is 1000 + their Ludus range number (e.g. a Ludus user with range number 2 would have `vmbr1002`).
This interface can be thought of conceptually as a virtual switch.

All user networks are /24 with the format `10.{{ ludus range number }}.{{ VLAN }}.{{ ip_last_octet }}`. Because all user networks are within 10.0.0.0/8, admins deploying Ludus into a network within 10.0.0.0/8 will need to avoid issuing users with a range number that overlaps the existing range. As range numbers are issued sequentially, it is up to the Ludus admin to recognize this conflict and create a dummy user for any 10.0.0.0/16 networks that overlap with the network Ludus itself is deploy into. For example, if the Ludus server itself has an IP of 10.10.0.123, the tenth created user will cause routing issues if the user is also on the 10.10.0.0/16 network. This user should be created as a dummy user and not an actual user of Ludus.

VMs are limited to a single network interface when deployed with Ludus.

Users simply define which VLAN each VM is a member of in their Ludus config file and the interface, DHCP/DNS, and routing is configured on the router automatically. 

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2019-server-x64"
    hostname: "{{ range_id }}-DC01-2019"
    template: win2019-server-x64-template
    // highlight-next-line
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4
    domain:
      fqdn: ludus.network
      role: primary-dc
    windows:
      sysprep: true
```

## User defined firewall rules

For basic setups the defaults, allow internet and allow inter-vlan-routing, will be suitable for many users and no `network` key needs to be set in the user's config.

Users are able to define custom firewall rules that will always be enforced regardless of testing status. Testing rules will be inserted above user defined rules and take precedence over them.
Rules are defined in the `network.rules` object.
Two keys exists to set the default for traffic leaving the network: `external_default`, and traffic between VLANs: `inter_vlan_default`. By default both of these values are `ACCEPT`.

Rules can act on entire vlans by not defining `ip_last_octet_src` or `ip_last_octet_dst`, ranges of machines `ip_last_octet_src: 21-25`, or single machines `ip_last_octet_src: 21`.

`vlan_dst` can accept the special value `public` which allows traffic to the internet, but not other VLANs. This is useful for cases where the `external_default` is `REJECT`.

The `ports` key is optional, and if omitted `all` is assumed. A range of ports can be defined using the `start:end` syntax. These values should be quoted to avoid yaml interpreting them as hex values.

NOTE: When these rules are deployed, first all the user defined rules are removed before being re-created in order to prevent rules removed from the config from remaining active. During this time the FORWARD chain is set to drop all traffic, so VMs may lose connectivity briefly during this time.

An example of a different types of user defined firewall rules are listed below.

```yaml
network:
  external_default: ACCEPT
  inter_vlan_default: REJECT
  rules:
  - name: Only allow TCP 443 from VLAN 10 to VLAN 20
    vlan_src: 10
    vlan_dst: 20
    protocol: tcp
    ports: 443
  - name: Allow VLAN 20 out to internet using any protocol (and any port) - only useful when external_default is set to REJECT
    vlan_src: 20
    vlan_dst: public
    protocol: all
    ports: all
  - name: Allow VLAN 30 to all VLANs using TCP on port 80
    vlan_src: 30
    vlan_dst: all
    protocol: tcp
    ports: 80
  - name: Only allow the .21 on VLAN 20 to hit port 445 of the .31 on VLAN 10 using TCP
    vlan_src: 20
    ip_last_octet_src: 21
    vlan_dst: 10
    ip_last_octet_dst: 31
    protocol: tcp
    ports: 445
  - name: Allow the .21 to .25 machines on VLAN 20 to access the .21 on VLAN 10 using TCP
    vlan_src: 20
    ip_last_octet_src: 21-25
    vlan_dst: 10
    ip_last_octet_dst: 21
    protocol: tcp
    ports: 445
  - name: Allow the .11 on VLAN 10 to access the .21 to .25 machines on VLAN 20 using TCP port 8080
    vlan_src: 10
    ip_last_octet_src: 11
    vlan_dst: 20
    ip_last_octet_dst: 21-25
    protocol: tcp
    ports: 8080
  - name: Allow TCP ports 8080 to 8088 from VLAN 10 to VLAN 20
    vlan_src: 10
    vlan_dst: 20
    protocol: tcp
    ports: "8080:8088"
```