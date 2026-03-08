# Upgrading from Ludus < 2.0.0

1. Upgrade Ludus normally (`./ludus-server --update`)
2. Wait for the database migration to complete. You can watch the status with `journalctl -u ludus-admin -n 20 -f`
