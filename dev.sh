#!/bin/bash

# This script is used to build and run Ludus in a development environment
# It assumes you are on a macOS or Linux host and have root SSH access to the target machine

# Parse command line arguments
while getopts "hlat:n:c" opt; do
  case $opt in
    h)
      echo "Usage: $0 [-h] [-l] [-a] [-t target] [-n lines] [-c]"
      echo "  -h  Show this help message"
      echo "  -l  Show Ludus service logs (default 100 lines)" 
      echo "  -a  Show Ludus admin service logs (requires -l)"
      echo "  -t  Target development hostname (default: lkdev2)"
      echo "  -n  Number of log lines to show (default 100)"
      echo "  -c  Build and install client locally"      
      exit 0
      ;;
    l)
      SHOW_LOGS=true
      ;;
    a)
      ADMIN_LOGS=true
      ;;
    t)
      DEVELOPMENT_HOSTNAME=$OPTARG
      ;;
    n)
      NUM_LINES=$OPTARG
      ;;
    c)
      BUILD_CLIENT=true
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

# Set default hostname if not specified
if [ -z "$DEVELOPMENT_HOSTNAME" ]; then
  DEVELOPMENT_HOSTNAME="lkdev2"
fi

# Add the plugins to the go workspace if they exist
if [ -d "./ludus-enterprise-plugin" ]; then
    go work use ./ludus-enterprise-plugin
fi

if [ -d "./ludus-antisandbox-plugin" ]; then
    go work use ./ludus-antisandbox-plugin
fi

# rsync the Ludus source code to the target machine
# excluding and files from .gitignore
rsync -av --progress \
    --no-owner --no-group \
    --exclude='.vscode/' \
    --exclude='docs/' \
    --exclude='scripts/' \
    --include='ludus-antisandbox-plugin/' \
    --include='ludus-enterprise-plugin/' \
    --filter=':- ./*/.gitignore' \
    --delete \
    . "$DEVELOPMENT_HOSTNAME":~/ludus-dev

# If the enterprise plugin exists, build it first
if [ -d "./ludus-enterprise-plugin" ]; then
    ssh "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-enterprise-plugin && ./dev.sh"
fi

# If the anti-sandbox plugin exists, build it before the Ludus server
if [ -d "./ludus-antisandbox-plugin" ]; then
    ssh "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-antisandbox-plugin && ./dev.sh"
fi

# SSH into the target machine and build the ludus server binary
ssh "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-server && ./dev.sh"

# Build the client locally if requested
if [ "$BUILD_CLIENT" = true ]; then
    ./ludus-client/dev.sh
fi

# Handle log viewing
if [ "$SHOW_LOGS" = true ]; then
  if [ -z "$NUM_LINES" ]; then
    NUM_LINES=100
  fi
  
  # Wait 1 second for the server to start and load plugins
  sleep 1

  if [ "$ADMIN_LOGS" = true ]; then
    ssh "$DEVELOPMENT_HOSTNAME" "journalctl -u ludus-admin -n $NUM_LINES"
    exit 0
  else
    ssh "$DEVELOPMENT_HOSTNAME" "journalctl -u ludus -n $NUM_LINES"
    exit 0
  fi
fi