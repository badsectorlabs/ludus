---
title: How to Cleanup Ludus Packer Cache
---

# How to Cleanup Ludus Packer Cache

## Introduction

This guide proposes one possible approach to address the issue of accumulating ISO files in the Ludus Packer cache directory. The suggestion uses a time-based file rotation tool called Rotafile, though similar solutions could be implemented using other tools or custom scripts.

## Problem Overview

The Ludus Packer cache directory (typically at `/opt/ludus/users/USERNAME/packer/packer_cache`, where USERNAME is your Ludus username) accumulates:
- ISO files ranging from ~400MB to ~6.9GB each
- Significant disk space usage that grows over time

## Installing a Time-Based File Rotation Tool

For this example, we'll use Rotafile, but similar functionality could be achieved with standard Linux tools like `find` with `cron` jobs, or other utilities:

```bash
# Clone the repository
git clone https://github.com/aancw/rotafile.git
cd rotafile

# Make scripts executable
chmod +x rotafile.sh install.sh

# Install the script (optional)
sudo ./install.sh
```

If you prefer not to install external tools, you could also use built-in Linux commands like `find` with the `-mtime` option to achieve similar results.

## Understanding File Rotation Parameters

If using Rotafile, it uses this syntax:

```
rotafile [directory] [time_period] [file_pattern] [options]
```

Similar approaches with standard Linux tools would look like:

```bash
# Using find to locate and delete files older than 30 days
find /path/to/directory -type f -name "*.iso" -mtime +30 -delete

# Using find with exec to implement a dry-run
find /path/to/directory -type f -name "*.iso" -mtime +30 -exec ls -la {} \;
```

The example commands in this guide will use Rotafile for simplicity, but could be adapted to use built-in tools instead.

## Basic Approaches for Ludus Cache Cleanup

### Analyzing the Cache (Preview First)

Before implementing any deletion, it's always wise to first see what would be affected:

```bash
# Determine your Ludus username
LUDUS_USER="USERNAME_HERE"

# Using Rotafile to analyze without deleting
./rotafile.sh /opt/ludus/users/$LUDUS_USER/packer/packer_cache 30d "*.iso" --dry-run

# Alternative with standard find command
find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -ls
```

This will show which files would be deleted and how much space would be freed, but won't actually delete anything.

### Manual Cleanup Options

To manually clean up the cache:

```bash
# Determine your Ludus username
LUDUS_USER="USERNAME_HERE"

# Option 1: Using Rotafile
./rotafile.sh /opt/ludus/users/$LUDUS_USER/packer/packer_cache 30d "*.iso"

# Option 2: Using standard find command
find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -delete
```

### Logging for Auditing Purposes

It's advisable to maintain logs of cleanup operations:

```bash
# Determine your Ludus username
LUDUS_USER="USERNAME_HERE"

# Option 1: Using Rotafile with built-in logging
./rotafile.sh /opt/ludus/users/$LUDUS_USER/packer/packer_cache 30d "*.iso" --log=/var/log/ludus/packer-cache-iso.log

# Option 2: Using find with redirection to log file
find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -ls > /var/log/ludus/find-log.txt
find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -delete >> /var/log/ludus/find-log.txt
```

## Setting Up Automated Cleanup

For regular maintenance, automated cleanup can be implemented using cron:

```bash
# Edit crontab
sudo crontab -e
```

Add a line to run weekly cleanup (using either approach):

```
# Get your Ludus username
LUDUS_USER="USERNAME_HERE"

# Option 1: Using Rotafile
0 2 * * 0 /path/to/rotafile.sh /opt/ludus/users/$LUDUS_USER/packer/packer_cache 30d "*.iso" --force --log=/var/log/ludus/packer-cache-$(date +\%Y\%m\%d).log

# Option 2: Using find directly
0 2 * * 0 find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -ls > /var/log/ludus/packer-cache-$(date +\%Y\%m\%d).log 2>&1 && find /opt/ludus/users/$LUDUS_USER/packer/packer_cache -type f -name "*.iso" -mtime +30 -delete >> /var/log/ludus/packer-cache-$(date +\%Y\%m\%d).log 2>&1
```

Replace `/path/to/rotafile.sh` with the actual path where Rotafile is installed.

## Additional Tips for Ludus-Specific Usage

### Monitoring Cleanup Results

To verify cleanup operations:

```bash
# View the logs
cat /var/log/ludus/packer-cache-*.log

# Determine your Ludus username
LUDUS_USER="USERNAME_HERE"

# Check current cache size
du -sh /opt/ludus/users/$LUDUS_USER/packer/packer_cache
```

Whether implemented with specialized tools or built-in commands, this approach provides a practical solution to the Packer cache growth issue while ensuring necessary files remain available during active development periods.

Issue Reference: [issues#97](https://gitlab.com/badsectorlabs/ludus/-/issues/97)