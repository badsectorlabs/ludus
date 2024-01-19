#!/bin/bash

# Check if PROXMOX_USERNAME and PROXMOX_PASSWORD are set and not empty
if [[ -z "$PROXMOX_USERNAME" || -z "$PROXMOX_PASSWORD" ]]; then
  echo "Error: PROXMOX_USERNAME or PROXMOX_PASSWORD not set or empty"
  exit 1
fi

# Get the GITLAB_URL and GITLAB_TOKEN values from the user interactvely
read -p "Enter Gitlab runner registration token: " GITLAB_TOKEN

# Prompt the user for GITLAB_URL, and set a default if not provided
read -p "Enter your Gitlab URL (default: https://gitlab.com): " GITLAB_URL
GITLAB_URL=${GITLAB_URL:-"https://gitlab.com"}

# Check if either value is empty
if [ -z "$GITLAB_TOKEN" ] || [ -z "$GITLAB_URL" ]; then
  echo "Error: Both GITLAB_TOKEN and GITLAB_URL must be provided."
  exit 1
fi

# Export variables needed for dynamic inventory
export PROXMOX_URL=https://127.0.0.1:8006/
export PROXMOX_NODE=ludus
export PROXMOX_INVALID_CERT=true
export PROXMOX_HOSTNAME=127.0.0.1
export PROXMOX_VM_STORAGE_POOL=local
export LUDUS_DIR=/opt/ludus

# Do it
ansible-playbook -i /opt/ludus/ansible/range-management/proxmox.py \
--extra-vars "@$LUDUS_DIR/config.yml" --extra-vars "api_user=$PROXMOX_USERNAME \
api_password='$PROXMOX_PASSWORD' api_host=$PROXMOX_HOSTNAME node_name=$PROXMOX_NODE \
ludus_install_path=$LUDUS_DIR proxmox_vm_storage_pool=$PROXMOX_VM_STORAGE_POOL \
gitlab_registration_token=$GITLAB_TOKEN gitlab_url=$GITLAB_URL" \
$LUDUS_DIR/ci/ci-setup.yml
