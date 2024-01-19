#!/bin/bash

# Returns the 203.0.113.X IP for a given RUNNER_VM_NAME
# If RUNNER_VM_NAME is not found, the most recent will be used

# Check if PROXMOX_USERNAME and PROXMOX_PASSWORD are set and not empty
if [[ -z "$PROXMOX_USERNAME" || -z "$PROXMOX_PASSWORD" ]]; then
  echo "Error: PROXMOX_USERNAME or PROXMOX_PASSWORD not set or empty"
  exit 1
fi

# Authenticate to Proxmox API and get PVEAuthCookie
LOGIN_DATA="username=$PROXMOX_USERNAME&password=$PROXMOX_PASSWORD"
TICKET_RESPONSE=$(curl -s -k -d "username=${PROXMOX_USERNAME}" --data-urlencode "password=$PROXMOX_PASSWORD" https://127.0.0.1:8006/api2/json/access/ticket)
COOKIE=$(echo ${TICKET_RESPONSE}| jq -r '.data.ticket')
CSRF_PREVENT=$(echo ${TICKET_RESPONSE}| jq -r '.data.CSRFPreventionToken')

# Check if the CI commit message included a VMID, if so, return that
if [[ ! -z "$CUSTOM_ENV_CI_COMMIT_MESSAGE" ]]; then
  VMID_EXTRACTED=$(echo "$CUSTOM_ENV_CI_COMMIT_MESSAGE" | grep -Po '(?<=VMID-)[0-9]+')
fi
if [[ ! -z "$VMID_EXTRACTED" ]]; then
  VM_ID=$VMID_EXTRACTED
else
  VM_ID=$(curl -s -k -b "PVEAuthCookie=$COOKIE" https://127.0.0.1:8006/api2/json/nodes/$PROXMOX_NODE/qemu | jq -r ".data[] | select(.name==\"$RUNNER_VM_NAME\")? | .vmid")
fi

# No vm for this pipline, use the most recent runner vm
# This is the case if we are not testing install related changes
if [[ -z "$VM_ID" ]]; then
  # Use jq to find the runner VM with the lowest uptime that isn't 0 (the template has 0 as its uptime)
  VM_ID=$(curl -s -k -b "PVEAuthCookie=$COOKIE" https://127.0.0.1:8006/api2/json/nodes/$PROXMOX_NODE/qemu | jq -r '.data | map(select(.uptime > 0)) | min_by(.uptime).vmid')
fi

# If we didn't find any VM, error
if [[ -z "$VM_ID" || "$VM_ID" == "null" ]]; then
  echo "No runner VMs found! Looked for: $RUNNER_VM_NAME"
  unset VM_ID
else
  # Try 5 times to get an IP (sometimes this is too quick after a reboot to get an IP)
  for i in {1..5}; do
    VM_IP=$(curl -s -k -b "PVEAuthCookie=$COOKIE" https://127.0.0.1:8006/api2/json/nodes/$PROXMOX_NODE/qemu/$VM_ID/agent/network-get-interfaces | jq -r '.data.result[] | ."ip-addresses" | .[]? | ."ip-address"' | grep 203.0.113) && break || sleep 5
  done
  export VM_ID
  export VM_IP
fi