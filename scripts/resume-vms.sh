#!/bin/bash

set -e
# set -x

NODE_NAME=ludus

# Check if PROXMOX_USERNAME and PROXMOX_PASSWORD are set and not empty
if [[ -z "$PROXMOX_USERNAME" || -z "$PROXMOX_PASSWORD" ]]; then
  echo "Error: PROXMOX_USERNAME or PROXMOX_PASSWORD not set or empty"
  exit 1
fi

# Authenticate to Proxmox API and get PVEAuthCookie
LOGIN_DATA="username=$PROXMOX_USERNAME&password=$PROXMOX_PASSWORD"
TICKET_RESPONSE=$(curl -s -k -d "username=${PROXMOX_USERNAME}@pam" --data-urlencode "password=$PROXMOX_PASSWORD" https://127.0.0.1:8006/api2/json/access/ticket)
COOKIE=$(echo ${TICKET_RESPONSE}| jq -r '.data.ticket')
CSRF_PREVENT=$(echo ${TICKET_RESPONSE}| jq -r '.data.CSRFPreventionToken')

# Read list of suspended VMs from file
SUSPENDED_VMS=$(cat suspended_vms.txt)

# Resume all suspended VMs
COUNT=0
for vmid in $SUSPENDED_VMS
do
  curl -s -k -b "PVEAuthCookie=$COOKIE" -H "CSRFPreventionToken: ${CSRF_PREVENT}" -X POST \
    "https://127.0.0.1:8006/api2/json/nodes/$NODE_NAME/qemu/$vmid/status/start" > /dev/null
  echo "Resumed VM $vmid"
  COUNT=$(( COUNT + 1 ))
  sleep 15
done

echo "All suspended VMs have been resumed."
echo "Resumed ${COUNT} VMs total."

