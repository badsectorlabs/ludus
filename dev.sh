#!/bin/bash

# This script is used to build and run Ludus in a development environment
# It assumes you are on a macOS or Linux host and have root SSH access to the target machine

# Parse command line arguments
while getopts "hlap:t:n:cdwsSDCPLv:" opt; do
  case $opt in
    h)
      echo "Usage: $0 [-h] [-l] [-a] [-t target] [-n lines] [-c] [-d] [-p] [-w] [-s] [-D] [-C] [-v version]"
      echo "  -h  Show this help message"
      echo "  -l  Show Ludus service logs (default 100 lines)"
      echo "  -a  Show Ludus admin service logs (requires -l)"
      echo "  -n  Number of log lines to show (default 100)"
      echo "  -t  Target development hostname (default: lkdev2)"
      echo "  -p  Port to use for SSH/rsync"
      echo "  -c  Build and install client locally"
      echo "  -C  Build and install client remotely"
      echo "  -w  Build and install web UI"
      echo "  -s  Skip plugins"
      echo "  -S  Skip building the server, just sync the code"
      echo "  -d  Enable debug mode for Ludus server"
      echo "  -D  Enable debug mode for database"
      echo "  -P  Enable debug mode for Proxmox"
      echo "  -L  Enable debug mode for license requests"
      echo "  -v  Version string to use for server and client builds"
      echo ""
      echo "Examples:"
      echo "  $0 -t ludus-dev-hostname -C -d -s # Build and install client remotely, Build and install Ludus server with debug mode, skip plugins"
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
    d)
      DEBUG_MODE=true
      ;;
    p)
      PORT=$OPTARG
      ;;
    w)
      BUILD_WEB_UI=true
      ;;
    s)
      SKIP_PLUGINS=true
      ;;
    S)
      SKIP_SERVER=true
      ;;
    D)
      DEBUG_DATABASE=true
      ;;
    C)
      BUILD_CLIENT_REMOTELY=true
      ;;
    P)
      DEBUG_PROXMOX=true
      ;;
    L)
      DEBUG_LICENSE=true
      ;;
    v)
      VERSION_STRING=$OPTARG
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

if [ -z "${PORT}" ]; then
  PORT=22
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
    --exclude='webUI/' \
    --exclude='ludus-gui/node_modules/' \
    --exclude='ludus-gui/.next/' \
    --include='ludus-antisandbox-plugin/' \
    --include='ludus-enterprise-plugin/' \
    --filter=':- ./*/.gitignore' \
    --delete \
    -e "ssh -p $PORT" \
    . "$DEVELOPMENT_HOSTNAME":~/ludus-dev

# If the enterprise plugin exists, build it first
if [ -d "./ludus-enterprise-plugin" ] && [ "$SKIP_PLUGINS" != true ]; then
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-enterprise-plugin && ./dev.sh"
fi

# If the anti-sandbox plugin exists, build it before the Ludus server
if [ -d "./ludus-antisandbox-plugin" ] && [ "$SKIP_PLUGINS" != true ]; then
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-antisandbox-plugin && ./dev.sh"
fi

# If the web UI exists, build it before the Ludus server
if [ -d "./ludus-gui" ] && [ "$BUILD_WEB_UI" = true ]; then
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-gui && ./dev.sh"
fi

# SSH into the target machine and build the ludus server binary\
if [ "$DEBUG_MODE" = true ]; then
    DEBUG_FLAGS="-d"
else
    DEBUG_FLAGS=""
fi

if [ "$DEBUG_DATABASE" = true ]; then
    DEBUG_FLAGS="${DEBUG_FLAGS} -D"
fi

if [ "$DEBUG_PROXMOX" = true ]; then
    DEBUG_FLAGS="${DEBUG_FLAGS} -P"
fi

if [ "$DEBUG_LICENSE" = true ]; then
    DEBUG_FLAGS="${DEBUG_FLAGS} -L"
fi

SERVER_VERSION_ARG=""
if [ -n "$VERSION_STRING" ]; then
    SERVER_VERSION_ARG="-v $VERSION_STRING"
fi

if [ "$SKIP_SERVER" != true ]; then
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-server && ./dev.sh $DEBUG_FLAGS $SERVER_VERSION_ARG"
else
    echo "[-] Skipping server build"
fi

# Build the client remotely if requested
if [ "$BUILD_CLIENT_REMOTELY" = true ]; then
    CLIENT_VERSION_ARG=""
    if [ -n "$VERSION_STRING" ]; then
        CLIENT_VERSION_ARG="-v $VERSION_STRING"
    fi
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "cd ~/ludus-dev/ludus-client && ./dev.sh $CLIENT_VERSION_ARG"
fi

# Build the client locally if requested
if [ "$BUILD_CLIENT" = true ]; then
    CLIENT_VERSION_ARG=""
    if [ -n "$VERSION_STRING" ]; then
        CLIENT_VERSION_ARG="-v $VERSION_STRING"
    fi
    ./ludus-client/dev.sh $CLIENT_VERSION_ARG
fi

# Handle log viewing
if [ "$SHOW_LOGS" = true ]; then
  if [ -z "$NUM_LINES" ]; then
    NUM_LINES=100
  fi
  
  # Wait 1 second for the server to start and load plugins
  sleep 1

  if [ "$ADMIN_LOGS" = true ]; then
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "journalctl -u ludus-admin -n $NUM_LINES"
    exit 0
  else
    ssh -p $PORT "$DEVELOPMENT_HOSTNAME" "journalctl -u ludus -n $NUM_LINES"
    exit 0
  fi
fi