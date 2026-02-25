---
sidebar_position: 5
---

# Create Additional Users

## Using the Ludus client to create a Ludus user

:::warning

If the user name you specify (converted to lowercase and spaces replaced with `-`) exists
on the system already, Ludus will refuse to overwrite it. Choose a new user name for your Ludus user.

:::



```shell-session
#terminal-command-ludus
ludus user add --name "John Smith" --userid JS --email john.smith@example.com
Enter password for the user (leave empty to generate a random password):
[INFO]  Adding user to Ludus, this can take up to a minute. Please wait.
+--------+------------------+-------+---------------------------------------------+
| USERID | PROXMOX USERNAME | ADMIN |                   API KEY                   |
+--------+------------------+-------+---------------------------------------------+
| JS     | john-smith       | false | JS.EdhoQGqRv7wxZvfiFFfzp3t2xEmTgTsmNBeduqxp |
+--------+------------------+-------+---------------------------------------------+
```

## Get Proxmox Credentials

Ludus is built on top of the [Proxmox](https://www.proxmox.com/en/) hypervisor which has a web interface.
It's available at `https://<ludus IP>:8006` and the credentials for the web GUI can be retrieved with `ludus --user JS user creds get`.

:::tip

Since we (John Doe) are an admin, we have full access to all users and can impersonate them with the `--user` option.

Without this, we would get our own (John Doe's) credentials.

:::

```shell-session
#terminal-command-ludus
ludus --user JS user creds get
+------------------------+------------------+---------------+------------------------+
|      LUDUS EMAIL       | PROXMOX USERNAME | PROXMOX REALM | PROXMOX/LUDUS PASSWORD |
+------------------------+------------------+---------------+------------------------+
| john.smith@example.com | john-smith       | pam           | xHC3sxk49xWHWrF        |
+------------------------+------------------+---------------+------------------------+
```

:::info

As a reminder, you can still log into the proxmox console as the `root` user with the password you set during Debian installation for more advanced configurations such as adding a disk to your Proxmox server or adding a separate NIC to a virtual machine. Careful not to break anything!

:::
