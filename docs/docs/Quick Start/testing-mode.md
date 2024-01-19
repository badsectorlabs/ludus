---
sidebar_position: 5
---

# Testing mode

Ludus is more than a simple infrastructure deployment tool - it allows users to test tools and techniques safely without allowing potentially unwanted outbound network communications.

This is accomplished by enabling "testing." When a user enables testing, the following actions take place:
1. VMs without a `testing` key defined (default) or VMs with a `testing.snapshot` key that is set to `true` are snapshotted in Proxmox.
2. VMs without a `testing` key defined (default) or VMs with a `testing.block_internet` key that is set to `true` are blocked from sending traffic outside of the Ludus range.

## Entering Testing Mode

To enter testing mode, run `ludus testing start`. You can check testing status with `ludus testing status`.

```
local:~$ ludus testing start
[INFO]  testing started
local:~$ ludus testing status
+-----------------+--------------------+------------------------+
| TESTING ENABLED |    ALLOWED IPS     |    ALLOWED DOMAINS     |
+-----------------+--------------------+------------------------+
|      TRUE       | No IPs are allowed | No domains are allowed |
+-----------------+--------------------+------------------------+
```

## Allowing Domains and IPs During Testing

Sometimes when testing, select internet access is required. In these situations, domains or IPs can be allowed out from machines with `block_internet` set (or testing unset as safety is the default).

`ludus testing allow` accepts a comma separate list of domains with `-d`, a comma separated list of IPs with `-i`, or a file containing domains and/or IPs with `-f`.

:::tip Why did Ludus allow 4 domains when I asked for 1?!

Allowing a domain will also allow any domains listed as certificate revocation list domains and [OCSP](https://en.wikipedia.org/wiki/Online_Certificate_Status_Protocol) domains in the specified domain's certificate. This is required to allow applications to accept the certificate.

:::

:::warning IP "Pinning"

Allowing a domain will "pin" the domain's IP (and the domain's CRL IPs) in DNS provided by the router VM.
This prevents clients from looking up a domain, getting a different IP than the "allowed" IP and being unable to reach the domain.

This pinned IP is shown in parenthesis in the Allowed Domains column of `testing status`.

If a domain's IP changes while in testing mode, deny it then allow it again to update the pinned IP.
:::

```
local:~$ ludus testing allow -d example.com
[INFO]  Allowed: example.com
[INFO]  Allowed: crl3.digicert.com
[INFO]  Allowed: crl4.digicert.com
[INFO]  Allowed: ocsp.digicert.com
local:~$ ludus testing status
+-----------------+--------------------+---------------------------------------+
| TESTING ENABLED |    ALLOWED IPS     |            ALLOWED DOMAINS            |
+-----------------+--------------------+---------------------------------------+
|      TRUE       | No IPs are allowed |      example.com (93.184.216.34)      |
|                 |                    |  crl3.digicert.com (192.229.211.108)  |
|                 |                    |  crl4.digicert.com (192.229.211.108)  |
|                 |                    |  ocsp.digicert.com (192.229.211.108)  |
+-----------------+--------------------+---------------------------------------+
```

## Denying Previously Allowed Domains and IPs During Testing

Similarly, domains and IPs can be denied during testing. This only applies to manually allowed domains and IPs, as all domains and IPs are denied by default.

The `testing deny` command takes the same arguments as `testing allow`.

## Exiting Testing Mode

To revert all the testing VMs back to their snapshots and allow them to connect to any domain or IP again, run `ludus testing stop`.

:::danger

Exiting/Stopping testing mode reverts VMs without a `testing` key defined (default) or VMs with
a `testing.snapshot` key that is set to `true` to a snapshot taken when testing mode was started.

Be sure to save off any files/notes/code from VMs before stopping testing!