---
sidebar_position: 4
title: "🗃️ PocketBase"
---

Ludus uses [PocketBase](https://pocketbase.io/) as part of the backend to manage data. By default, the PocketBase web UI is disabled to prevent users from modifying the database directly which can cause unintended consequences.

However, there are use cases that require an administrator to access the database and manipulate it directly or view logs.

To do this, the administrator can enable the PocketBase UI by running the following commands on your Ludus host.

```shell-session
#terminal-command-ludus-root
systemctl set-environment LUDUS_ENABLE_SUPERADMIN=ill-be-careful
#terminal-command-ludus-root
systemctl restart ludus
```

You can then browse to the PocketBase admin page at `https://<Ludus IP>:8080/admin`

Log in with the username `root@ludus.internal` and the password the full ROOT API key from `/opt/ludus/install/root-api-key`.

![The PocketBase login screen](/img/pocketbase/pocketbase-login.png)