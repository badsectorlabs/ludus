---
title: Create user with ROOT API Key
---

# Creating an admin user with the ROOT API Key

The ROOT API key can be used to create an admin user in the event that the initial interactive admin user was not created successfully, or if Ludus is installed non-interactively. The ROOT API key is only readable by the `root` user.

```shell-session
#terminal-command-ludus-root
export LUDUS_API_KEY=$(cat /opt/ludus/install/root-api-key)
#terminal-command-ludus-root
ludus user add --name "John Smith" --userid JS --email john.smith@example.com
Enter password for the user (leave empty to generate a random password):
[INFO]  Adding user to Ludus, this can take up to a minute. Please wait.
+--------+------------------+-------+---------------------------------------------+
| USERID | PROXMOX USERNAME | ADMIN |                   API KEY                   |
+--------+------------------+-------+---------------------------------------------+
| JS     | john-smith       | false | JS.EdhoQGqRv7wxZvfiFFfzp3t2xEmTgTsmNBeduqxp |
+--------+------------------+-------+---------------------------------------------+
```