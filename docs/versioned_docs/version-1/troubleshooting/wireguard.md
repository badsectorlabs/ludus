---
title: Wireguard
---


## Debugging WireGuard

### Enable Debug in the kernel on the Ludus host
```
echo module wireguard +p > /sys/kernel/debug/dynamic_debug/control
```

### Watch logs
```
dmesg -HwT | grep wireguard
```

### Disable debug
```
echo module wireguard -p > /sys/kernel/debug/dynamic_debug/control
```

## Issues and remediation
### Client sees: `Error: Invalid handshake initiation from ...`
1. Comment out the user's peer details from `/etc/wireguard/wg0.conf`
2. Sync the config with the kernel module with `wg syncconf wg0 <(wg-quick strip wg0)`
3. Uncomment the user's peer details from `/etc/wireguard/wg0.conf`
4. Sync the config with the kernel module with `wg syncconf wg0 <(wg-quick strip wg0)`

The user should be able to reconnect immediately.

### TCP connections hang

This can be an issue if you are running your Ludus wireguard tunnel inside another VPN (not recommended).

Run this on the Ludus server to enable [MSS clamping](https://www.cloudflare.com/learning/network-layer/what-is-mss/)

```
/sbin/iptables -t mangle -A FORWARD -p tcp -m tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
```

If that alone does not solve the problem, lower the WireGuard MTU values on both the server and client until TCP is functional.
You'll want to use the largest MTU values that works in order to limit packet fragmentation.

```
root@ludus:~# cat /etc/wireguard/wg0.conf
# Ansible managed
[Interface]
PrivateKey = ODcsR+U927qnFnAeREoCUAMfcuGlZwcLpOxttSCI33o=
Address = 198.51.100.1/24
ListenPort = 51820
MTU = 1284 # Add this line and edit the value (default is 1400)
```