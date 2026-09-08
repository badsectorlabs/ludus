Use a **gretap** as the virtual TAP (Ethernet-in-GRE to the sensor), attach **clsact** on the **range bridge** (`vmbr10XX`, not `vmbr0`), and let a cBPF classifier decide what gets mirrored. Encrypted WireGuard never hits that bridge; what you drop is decrypted WG-client traffic (`198.51.100.0/24`) plus Ansible/management from the host (`192.0.2.254`).

Range bridges are VLAN-aware, so the filter must use `vlan` and `protocol all`. Compiling BPF against `any` or `ip` will use the wrong header offsets and match nothing.

## 1. On the Ludus host

Replace the three values, then run as root.

```bash
RANGE_BR=vmbr1002          # 1000 + range number
LOCAL_IP=10.98.108.3       # Ludus address on vmbr0
SENSOR_IP=10.98.108.50     # sensor that will receive the tap
```

```bash
modprobe ip_gre

ip link add name range-tap type gretap \
  local "$LOCAL_IP" remote "$SENSOR_IP" \
  ttl 64 nopmtudisc ignore-df
ip link set range-tap up
ip link set range-tap mtu 1462
```

Do **not** enslave `range-tap` to `vmbr10XX` or give it an IP. It is only a mirror target. GRE egress uses `vmbr0`, so you do not create a loop.

Compile the keep-filter (match = mirror; no match = skip):

```bash
tcpdump -y EN10MB -ddd \
  'vlan and not host 192.0.2.254 and not net 198.51.100.0/24' \
  | tr '\n' ',' > /tmp/range-keep.bpf
```

Attach the mirror. `skip_hw` is required on a software bridge. `protocol all` is required because tagged frames are `0x8100`, not `0x0800`.

```bash
tc qdisc add dev "$RANGE_BR" clsact

tc filter add dev "$RANGE_BR" ingress protocol all pref 1 skip_hw \
  bpf bytecode-file /tmp/range-keep.bpf \
  action mirred egress mirror dev range-tap

tc filter add dev "$RANGE_BR" egress protocol all pref 1 skip_hw \
  bpf bytecode-file /tmp/range-keep.bpf \
  action mirred egress mirror dev range-tap
```

If you later see every packet twice, drop the `egress` filter and keep `ingress` only.

Allow GRE out (IP proto 47) if the host firewall is not already open:

```bash
iptables -I OUTPUT -p gre -d "$SENSOR_IP" -j ACCEPT
```

## 2. On the sensor

```bash
modprobe ip_gre

ip link add name range-tap type gretap \
  local 10.98.108.50 remote 10.98.108.3 \
  ttl 64 nopmtudisc ignore-df
ip link set range-tap up
ip link set range-tap arp off

# proto 47 from the Ludus host
iptables -I INPUT -p gre -s 10.98.108.3 -j ACCEPT

tcpdump -ni range-tap -e
```

Inner frames still have 802.1Q tags (`vlan 10`, `vlan 20`, …). Point Zeek/Suricata/dumpcap at `range-tap`.

## 3. Confirm the BPF is doing what you think

```bash
tcpdump -y EN10MB -d 'vlan and not host 192.0.2.254 and not net 198.51.100.0/24'
tc filter show dev vmbr1002 ingress
tc -s filter show dev vmbr1002 ingress
```

`tcpdump -d` should show jumps that reject `0xc00002fe` (`192.0.2.254`) and `0xc6336400/0xffffff00` (`198.51.100.0/24`).

## 4. Tear-down

```bash
tc qdisc del dev vmbr1002 clsact
ip link del range-tap
```

Same `ip link del range-tap` on the sensor.

---

**If you also want to drop the whole NAT net** (templates, KMS, Nexus, not just `.254`), change the expression to:

`vlan and not net 192.0.2.0/24 and not net 198.51.100.0/24`

**If some frames are untagged**, add a second filter at `pref 2` compiled from the same expression without `vlan`.

**MTU:** GRE adds ~38 bytes. `nopmtudisc ignore-df` lets the outer packet fragment so 1500-byte inner frames still arrive. If the sensor needs unfragmented outer packets, put both hosts on a jumbo path or accept truncation.

**Persistence:** Ludus rewrites the managed `vmbr` block on range create/delete. Put this in a systemd oneshot that runs after `ifup` of that bridge, not inside the `# LUDUS MANAGED INTERFACE` block.