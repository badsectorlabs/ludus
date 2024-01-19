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

# Get list of running VMs from Proxmox API
VM_LIST=$(curl -s -k -b "PVEAuthCookie=$COOKIE" \
 "https://127.0.0.1:8006/api2/json/nodes/$NODE_NAME/qemu" | jq -r '.data[] | select(.status=="running") | .vmid')

# Check if each running VM is suspended
RUNNING_VMS=""
newline=$'\n'
PAUSED=0
for VMID in $VM_LIST; do
  QMP_STATUS=$(curl -s -k -b "PVEAuthCookie=$COOKIE" "https://127.0.0.1:8006/api2/json/nodes/$NODE_NAME/qemu/$VMID/status/current" | jq -r '.data.qmpstatus')
  if [ "$QMP_STATUS" == "running" ]; then
    echo "VM $VMID is currently running and not suspended"
    RUNNING_VMS+="${VMID}${newline}"
  elif [ "$QMP_STATUS" == "paused" ]; then
    PAUSED=$(( PAUSED + 1 ))
  fi
done

# Suspend all running VMs and save list of suspended VMs to a file
COUNT=0
rm suspended_vms.txt || true
for vmid in $RUNNING_VMS
do
  curl -s -k -b "PVEAuthCookie=$COOKIE" -H "CSRFPreventionToken: ${CSRF_PREVENT}" -X POST \
    "https://127.0.0.1:8006/api2/json/nodes/$NODE_NAME/qemu/$vmid/status/suspend?todisk=1" > /dev/null
  echo "Suspended VM $vmid"
  sleep 25
  echo "$vmid" >> suspended_vms.txt
  COUNT=$(( COUNT + 1 ))
done

echo "Suspended ${COUNT} VMs total"
echo "${PAUSED} VMs were already suspended when this script was run."
