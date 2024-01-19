---
title: Ansible Issues
---

# Ansible Issues

## General Ansible Errors

Just up arrow and hit enter!

But really, Ludus actions are [idempotent](https://en.wikipedia.org/wiki/Idempotence#Computer_science_meaning), and these VMs are complex beasts. Sometimes things don't work on the first try. No harm in trying again!

## Ansible Temporary Directory Error

Error such as:
```
TASK [Gathering Facts] *********************************************************
fatal: [JD-ad-dc-win2019-server-x64]: UNREACHABLE! => {"changed": false, "msg": 
"Failed to create temporary directory. In some cases, you may have been able to
authenticate and did not have permissions on the target directory. Consider
changing the remote tmp path in ansible.cfg to a path rooted in \"/tmp\",
for more error information use -vvv. Failed command was: ( umask 77 && 
mkdir -p \"` echo /home/ludus/.ansible/tmp `\"&& mkdir \"`
echo /home/ludus/.ansible/tmp/ansible-tmp-1704235290.5345225-913183-44415051184218 `\"
&& echo ansible-tmp-1704235290.5345225-913183-44415051184218=\"`
echo /home/ludus/.ansible/tmp/ansible-tmp-1704235290.5345225-913183-44415051184218 `\" ),
exited with result 1", "unreachable": true}
```

Re-run ansible (`ludus range deploy`). The ansible temporary directory is set for every ansible commands, and yet on rare occasions this error occurs.
Please submit an issue/pull request if you can figure out why this happens.