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
