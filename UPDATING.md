# Upgrading from Ludus < 1.3.0

1. Upgrade Ludus normally (`./ludus-server --update`)
2. Upgrade ansible:

As root on the Ludus host run the following commands:

```
python3 -m pip install ansible==9.3.0 netaddr==1.2.1 --break-system-packages
ANSIBLE_HOME=/opt/ludus/users/root/.ansible ansible-galaxy install -r /opt/ludus/ansible/requirements.yml --force
cd /opt/ludus/users
su ludus -
for USERS in $(ls .); do if [[ "$USERS" == "root" ]]; then continue; else ANSIBLE_HOME=/opt/ludus/users/$USERS/.ansible ansible-galaxy install -r /opt/ludus/ansible/requirements.yml --force; fi; done
exit
```

3. OPTIONAL - Set the firewall:

Existing users will not be prevented from accessing other's ranges if they have SSH access to the Ludus host.

To prevent this, as root on the Ludus host run the following commands:

```
# For each user
iptables -A OUTPUT -d 10.5.0.0/16 ! -o ens18 -m owner --uid-owner {{ UID }} -m comment --comment "Allow {{ USERNAME }} to reach their range" -j ACCEPT

# After all user rules have been added
iptables -A OUTPUT -d 10.0.0.0/8 ! -o ens18 -m owner --uid-owner 0-999 -m comment --comment "Ludus: allow system processes to 10/8" -j ACCEPT
iptables -A OUTPUT -d 10.0.0.0/8 ! -o ens18 -m owner --uid-owner {{ LUDUS USER UID }} -m comment --comment "Ludus: default allow ludus access to all user ranges" -j ACCEPT
iptables -A OUTPUT -d 10.0.0.0/8 ! -o ens18 -m comment --comment "Ludus: default deny access to user ranges" -j DROP

iptables-save /etc/iptables/rules.v4
```