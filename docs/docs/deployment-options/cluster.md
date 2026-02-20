---
sidebar_position: 7
title: "Proxmox Cluster"
---

# Proxmox Cluster Deployment

Ludus supports deployment on Proxmox clusters, allowing you to span ranges across multiple nodes for better resource utilization.

## Cluster vs Standalone Mode

Ludus automatically detects whether the Proxmox host is part of a cluster and adjusts its networking approach accordingly:

| Mode | Detection | Networking | Bridge Names |
|------|-----------|------------|--------------|
| **Cluster** | Multiple nodes in cluster | Proxmox SDN (VXLAN) | `r1`, `r2`, `ludusnat` |
| **Standalone** | Single node | vmbr management | `vmbr1001`, `vmbr1002`, `vmbr1000` |

:::tip Standalone Installations
If you're running Ludus on a single Proxmox node (not part of a cluster), **no additional configuration is required**. Ludus will automatically use the vmbr network management that has been available since Ludus 1.x. The SDN features described on this page only apply to cluster deployments.
:::

## Prerequisites

Before deploying Ludus on a Proxmox cluster, ensure you have:

1. **A working Proxmox cluster** - All nodes must be joined and healthy
2. **Shared storage or replicated local storage** - Templates must be accessible from all nodes
3. **Network connectivity between nodes** - Required for VXLAN overlay networking

## Installation

### Installing Ludus on an Existing Proxmox Cluster

When you install Ludus on a node that is already part of a Proxmox cluster, you must first manually create an SDN zone in Proxmox with the correct VXLAN peer IPs for your network topology.

#### Step 1: Create the SDN Zone in Proxmox

Before running the Ludus installer, create a VXLAN SDN zone in Proxmox:

1. In the Proxmox web UI, go to **Datacenter → SDN → Zones**
2. Click **Add** and select **VXLAN**
3. Configure the zone:
   - **ID**: `ludus` (or your preferred name - set this in `/opt/ludus/config.yml` as `sdn_zone`)
   - **Peers**: Enter the IP addresses of all cluster nodes that should participate in the VXLAN overlay (comma-separated)
   - These should be IPs on a network with connectivity between all nodes (typically your cluster network)
4. Click **Add** to create the zone
5. Click **Apply** in the SDN section to apply the configuration

:::warning Important
The peer IPs must be correct for your network topology. Use IPs on a network where all cluster nodes can communicate (e.g., your cluster/corosync network). Using incorrect IPs will result in VMs being unable to communicate across nodes.
:::

#### Step 2: Configure Ludus

If you used a zone name other than `ludus`, create or edit `/opt/ludus/config.yml`:

```yaml
sdn_zone: your-zone-name
```

#### Step 3: Run the Ludus Installer

```bash
# SSH to any node in your Proxmox cluster
ssh root@your-proxmox-node

# Run the Ludus installer
curl -s https://ludus.cloud/install | bash
```

Ludus will:
1. Detect that the node is part of a cluster
2. Verify that the configured SDN zone exists (error if not)
3. Create the `ludusnat` VNet for NAT traffic in the existing zone
4. Create range VNets (e.g., `r1`, `r2`) as needed in the existing zone

### Template Storage

For cluster deployments, you must ensure templates are available on all nodes where VMs will be deployed. You have two options:

1. **Shared Storage** (Recommended)
   - Use a shared storage backend (NFS, Ceph, etc.) for templates
   - Configure `proxmox_vm_storage_pool` in `/opt/ludus/config.yml` to point to this shared storage
   - Templates are automatically available on all nodes

2. **Replicated Local Storage**
   - Keep templates on local storage with the same name on each node
   - Manually replicate templates to each node
   - This is useful when shared storage isn't available or for performance reasons

## Node Selection

Ludus supports flexible node selection for VM deployment with a three-tier priority system:

| Priority | Source | Description |
|----------|--------|-------------|
| 1 (Highest) | Per-VM `target_node` | Specify a node for individual VMs |
| 2 | Range-level `target_node` | Set a default node for the entire range |
| 3 (Default) | Auto-select | Node with lowest resource usage (80% RAM + 20% CPU weighted) |

### Auto-Selection Algorithm

When no `target_node` is specified, Ludus automatically selects the optimal node based on:
- **80% weight on RAM usage** - Memory is typically the constraining factor
- **20% weight on CPU usage** - CPU is generally more abundant

Lower usage scores result in higher selection priority.

### Configuration Examples

#### Deploy Entire Range to a Specific Node

```yaml
# Range-level target_node - all VMs deploy to 'pve-node-2'
target_node: pve-node-2

ludus:
  - vm_name: "{{ range_id }}-dc01"
    hostname: "dc01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4
```

#### Per-VM Node Selection

```yaml
ludus:
  # High-memory VM on a high-RAM node
  - vm_name: "{{ range_id }}-sql-server"
    hostname: "sql01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 20
    ram_gb: 32
    cpus: 8
    target_node: pve-node-highmem  # This VM goes to the high-memory node

  # Standard VMs on auto-selected nodes
  - vm_name: "{{ range_id }}-workstation"
    hostname: "ws01"
    template: win10-22h2-x64-enterprise-template
    vlan: 20
    ip_last_octet: 21
    ram_gb: 4
    cpus: 2
    # No target_node - auto-selected based on resources
```

#### Mixed Approach

```yaml
# Default to a specific node
target_node: pve-node-1

ludus:
  # Uses range default (pve-node-1)
  - vm_name: "{{ range_id }}-dc01"
    hostname: "dc01"
    template: win2019-server-x64-template
    vlan: 10
    ip_last_octet: 11
    ram_gb: 8
    cpus: 4

  # Override for this specific VM
  - vm_name: "{{ range_id }}-heavy-workload"
    hostname: "heavy01"
    template: ubuntu-22.04-x64-server-template
    vlan: 20
    ip_last_octet: 30
    ram_gb: 64
    cpus: 16
    target_node: pve-node-3  # Override range default
```

## SDN Architecture (Cluster Mode Only)

Ludus uses Proxmox SDN (Software-Defined Networking) **only for cluster deployments**. Standalone (single-node) installations continue to use the legacy vmbr bridge management.

### Networking Comparison

| Feature | Cluster Mode (SDN) | Standalone Mode (Legacy) |
|---------|-------------------|--------------------------|
| Range Networks | VNets: `r1`, `r2`, etc. | Bridges: `vmbr1001`, `vmbr1002`, etc. |
| NAT Network | VNet: `ludusnat` | Bridge: `vmbr1000` (configurable) |
| Cross-node Communication | VXLAN overlay | N/A (single node) |
| Configuration | Proxmox API | `/etc/network/interfaces` |
| Zone Requirement | VXLAN zone required | None |

### VNet Naming (Cluster Mode)

- **NAT VNet**: `ludusnat` - Used for template provisioning and router WAN
- **Range VNets**: `r1`, `r2`, etc. - One per range, based on range number

### VXLAN Tags (Cluster Mode)

Each range VNet requires a unique VXLAN tag (VNI - VXLAN Network Identifier). By default, Ludus uses the range number as the VXLAN tag:

- Range 1 → VXLAN tag 1
- Range 2 → VXLAN tag 2
- etc.

If your cluster has pre-existing VXLAN VNets using low tag numbers, you can configure a base offset using `vxlan_tag_base` in `/opt/ludus/config.yml`:

```yaml
# Offset Ludus VXLAN tags to avoid conflicts with existing VNets
vxlan_tag_base: 10000
```

With this configuration:
- Range 1 → VXLAN tag 10001
- Range 2 → VXLAN tag 10002
- etc.

VXLAN tags are 24-bit values (0-16,777,215), so there is ample space to position Ludus tags wherever needed to avoid conflicts.

### Bridge Naming (Standalone Mode)

- **NAT Bridge**: `vmbr1000` (or value of `ludus_nat_interface` in config)
- **Range Bridges**: `vmbr1001`, `vmbr1002`, etc. - One per range (`vmbr{1000+range_number}`)

### Subnets (Both Modes)

- **NAT Subnet**: `192.0.2.0/24` (with SNAT in SDN mode)
- **Range Subnets**: `10.{range_number}.0.0/16` per range

## Upgrading from Standalone to Cluster

If you started with a standalone Ludus installation and want to join it to a cluster, you'll need to migrate from the vmbr network management to Proxmox SDN.

:::note Why Migration is Needed
Standalone Ludus installations use local vmbr bridges (e.g., `vmbr1001`) which only exist on a single node. Cluster deployments require SDN VNets that span all nodes via VXLAN overlay networking.
:::

### Prerequisites

1. Backup your existing ranges and configurations
2. Ensure shared storage is configured for templates
3. Join the Proxmox node to the cluster (this triggers Ludus to detect cluster mode)

### Migration Steps

1. **Create the SDN Zone in Proxmox**

Before migrating, you must manually create a VXLAN SDN zone:

   - Make sure each node's `/etc/network/interfaces` file contains the line `source /etc/network/interfaces.d/*`
   - In the Proxmox web UI, go to **Datacenter → SDN → Zones**
   - Click **Add** and select **VXLAN**
   - Configure the zone:
     - **ID**: `ludus` (or set a custom name in `/opt/ludus/config.yml` as `sdn_zone`)
     - **Peers**: Enter the IP addresses of all cluster nodes (comma-separated)
   - Click **Add** and then **Apply**

:::warning
The peer IPs must be on a network where all cluster nodes can communicate. Incorrect IPs will prevent cross-node VM communication.
:::

2. **Run the SDN migration**

Once the node is part of a cluster, Ludus will automatically detect cluster mode and use SDN for new operations. To migrate existing ranges:

```bash
# Check migration status
ludus migrate sdn status

# Run the migration (will prompt for confirmation)
ludus migrate sdn run

# Or run without confirmation prompt
ludus migrate sdn run --no-prompt
```

The migration will:
- Verify the SDN zone exists (created in step 1)
- Create the NAT VNet (`ludusnat`)
- Create VNets for each existing range (`r1`, `r2`, etc.)
- Update VM network interfaces to use VNets

4. **Manual cleanup**

After verifying all VMs work correctly:
- Remove old `vmbr1XXX` entries from `/etc/network/interfaces`
- Reboot to apply changes

:::tip Automatic Detection
After joining a cluster, Ludus automatically detects cluster mode and will use SDN for all new range operations. The migration is only needed for existing ranges that were created before joining the cluster.
:::

## Troubleshooting

### SDN Zone Not Found Error

If you see an error like "cluster mode requires a pre-configured SDN zone":

1. Create the VXLAN zone in Proxmox (see [Installation](#step-1-create-the-sdn-zone-in-proxmox))
2. Ensure the zone name matches `sdn_zone` in `/opt/ludus/config.yml` (default: `ludus`)
3. Click **Apply** in the Proxmox SDN section after creating the zone

### VMs Can't Communicate Across Nodes

1. Verify the SDN zone is applied: `pvesh get /cluster/sdn/zones`
2. Check VXLAN peer IPs are correct: `pvesh get /cluster/sdn/zones/ludus`
   - Peer IPs should be on a network where all nodes can communicate
   - Update peer IPs in Proxmox UI if incorrect: **Datacenter → SDN → Zones → Edit**
3. Ensure UDP port 4789 is open between nodes
4. Test connectivity between peer IPs from each node

### Template Not Found on Target Node

1. Verify the template exists on the target node
2. Check that `proxmox_vm_storage_pool` is accessible from all nodes
3. Consider using shared storage for templates

### Auto-Selection Always Picks Same Node

1. Check node resource usage: `pvesh get /nodes/{node}/status`
2. Verify all nodes are online and healthy
3. Check for node maintenance mode

## Configuration Reference

### Server Configuration (`/opt/ludus/config.yml`)

```yaml
# Cluster mode is auto-detected based on the number of nodes in the Proxmox cluster
# - Single node: Uses legacy vmbr management (no SDN)
# - Multiple nodes: Uses Proxmox SDN with VXLAN
# This setting can be overridden, but is not recommended
cluster_mode: false  # Auto-detected; do not set manually unless necessary

# SDN zone name (default: ludus)
# ONLY used in cluster mode - ignored for standalone installations
# In cluster mode, this zone MUST be pre-created in Proxmox with correct VXLAN peer IPs
sdn_zone: ludus

# Base VXLAN tag (VNI) added to the range number (default: 0)
# ONLY used in cluster mode - ignored for standalone installations
# The final VXLAN tag for a range = vxlan_tag_base + range_number
# Use this to avoid conflicts with pre-existing VXLAN VNets on your cluster
# Valid range: 0 - 16777215 (VXLAN VNI is 24-bit)
vxlan_tag_base: 0

# NAT interface for standalone mode (default: vmbr1000)
# Only used in non-cluster mode - ignored for cluster installations
ludus_nat_interface: vmbr1000

# Storage pool for VM disks (should be shared for clusters)
proxmox_vm_storage_pool: local

# Storage pool for ISOs (should be shared for clusters)
proxmox_iso_storage_pool: local
```

:::info Automatic Mode Detection
Ludus automatically detects cluster mode by checking if the Proxmox host has more than one node. You do **not** need to manually set `cluster_mode` in your configuration.

- **Standalone (single node)**: Uses legacy vmbr bridges - no SDN configuration needed
- **Cluster (multiple nodes)**: Uses Proxmox SDN - requires manual zone creation before installation
:::

:::warning Cluster Mode SDN Zone
In cluster mode, Ludus does **not** automatically create the SDN zone because it cannot reliably determine the correct VXLAN peer IPs for your network topology. You must create the zone manually in Proxmox before running Ludus setup or migration.
:::

### Range Configuration Fields

| Field | Level | Description |
|-------|-------|-------------|
| `target_node` | Range | Default node for all VMs in the range |
| `target_node` | VM | Override node for a specific VM |
