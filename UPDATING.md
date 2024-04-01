# Upgrading from Ludus < 1.3.0

1. Upgrade Ludus normally (`./ludus-server --update`)
2. Upgrade ansible:

As root on the Ludus host run the following commands:

```
# Upgrade ansible and netaddr for the whole system
python3 -m pip install ansible==9.3.0 netaddr==1.2.1 --break-system-packages
# Upgrade ansible roles/collections for root
ANSIBLE_HOME=/opt/ludus/users/root/.ansible ansible-galaxy install -r /opt/ludus/ansible/requirements.yml --force

# For all users, make sure all roles are owned by the user (lae.proxmox was previously installed by root), then upgrade them for all users, as the ludus user
cd /opt/ludus/users
for USERS in $(ls .); do if [[ "$USERS" == "root" ]]; then continue; else chown -R ludus:users /opt/ludus/users/$USERS/.ansible/roles; fi; done
su ludus -
for USERS in $(ls .); do if [[ "$USERS" == "root" ]]; then continue; else ANSIBLE_HOME=/opt/ludus/users/$USERS/.ansible ansible-galaxy install -r /opt/ludus/ansible/requirements.yml --force; fi; done
exit
```
