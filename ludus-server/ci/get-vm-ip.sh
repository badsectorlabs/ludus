#!/bin/bash

# /opt/ludus/ci/get-vm-ip.sh
#
# DEPRECATED: VM resolution is now handled by resolve_vm() in base.sh.
# This file is kept for backward compatibility but should not be sourced directly.
# Use: source base.sh; source claim-pool.sh; resolve_vm
#
# The get_vm_ip_by_vmid() function in base.sh replaces this script's functionality.

echo "WARNING: get-vm-ip.sh is deprecated. Use resolve_vm() from base.sh instead." >&2
