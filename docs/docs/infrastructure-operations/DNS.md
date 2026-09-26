---
sidebar_position: 4
title: "📟 DNS"
---

# 📟 DNS

DNS inside a Ludus range is provided by the user's router, which is running [Blocky](https://github.com/0xERR0R/blocky).

Blocky provides the features Ludus needs:

- DNS rewrites ("pinning")
- DNS blocking/allowing based on requested domain and requesting client
- Configuration through a local YAML file

Ludus manages Blocky by writing `/etc/blocky/config.yml` on the router and restarting the `blocky` service. Blocky's HTTP API is bound to `127.0.0.1:4000` on the router and is not exposed as a user-facing web UI.

:::info

Windows VMs, when joined to a domain, will use their primary domain controller for DNS. The domain controller will forward queries
outside of its domain to the router.

:::

By default, the router forwards DNS through Cloudflare DNS-over-HTTPS. You can change this with `router.upstream_dns` in your range config:

```yaml
router:
  upstream_dns:
    - 1.1.1.1
    - https://1.1.1.1/dns-query
```

Entries can be bare IP addresses or `https://` DNS-over-HTTPS URLs.

## Query Log

Blocky writes DNS query logs on the router under `/var/log/blocky`.

:::note

If testing mode is not enabled, VMs may make DNS queries to an external DNS server, or use DNS over TLS/HTTPS to resolve domains.
These queries will not appear in the Query Log.
:::

## DNS rewrites (pins)

Ludus renders DNS rewrite rules into Blocky's config. By default, Ludus adds all VM names, and VM names appended with `home.arpa`, to the DNS rewrite list.

Range config values in `dns_rewrites` are also written to Blocky. Exact names such as `example.com` resolve only that name. Wildcards such as `*.example.com` resolve both `example.com` and all subdomains because Blocky's config-native wildcard mapping includes the apex domain.

## Testing Mode DNS Rules

Ludus uses Blocky allowlists to enforce testing mode DNS behavior. When testing mode starts, range clients are restricted to explicitly allowed domains, while VMs configured with `testing.block_internet: false` keep unrestricted DNS resolution.

Allowed domains are added to `/etc/blocky/config.yml` and pinned to the IP address Ludus resolved when the allow rule was created. Removing the allow rule removes that DNS pin.
