# Ludus-TPM-Templates
Here are my Packer Templates for [Ludus](https://gitlab.com/badsectorlabs/ludus/-/tree/main?ref_type=heads) with TPM support.
[https://github.com/Patrick-DE/Ludus-TPM-Templates](https://github.com/Patrick-DE/Ludus-TPM-Templates)

The following systems are supported:
* Windows Server 2025 - Evaluation
* Windows 11 24H2 - Evaluation

# ⚠️Support
As of today, Ludus does not support the creation machines with TPM. This is due to the outdated [packer-plugin-proxmox](https://github.com/hashicorp/packer-plugin-proxmox).

For TPM support the plugin version `1.2+` is required.

The reason why the maintainer of Ludus cannot upgrade to version 1.2.2 is because the `cpu_type` is not being passed to proxmox resulting in all hosts taking the default value regardless of cpu_type setting. See this [issue](https://github.com/hashicorp/packer-plugin-proxmox/issues/307).

Therefore, you need to manually build the binary until the fix has been applied and a new version published.

# Build the plugin
To make it work and build a compatible version of the packer plugin `proxmox` follow the steps below.

1. Clone the official GitHub repo:  
    ```
    git clone https://github.com/hashicorp/packer-plugin-proxmox
    ```    
2. Install GO as seen [here](https://go.dev/doc/install).
3. Go to the file `packer-plugin-proxmox/builder/proxmox/common/step_start_vm.go`.
4. Add after line 133 this line as seen [here](https://github.com/hashicorp/packer-plugin-proxmox/pull/308/files).
   ```
   Type:    (*proxmox.CpuType)(&c.CPUType),
   ```
5. Build the plugin via
   ```
   go build
   ```
6. Upon successful compilation, a `packer-plugin-proxmox` plugin file can be found in the root directory.
7. Upload it to `/opt/ludus/resources/packer/plugins/github.com/hashicorp/proxmox/` with the name `packer-plugin-proxmox_v1.2.3_x5.0_linux_amd64`.
8. Add the hash of the binary to allow the plugin to load. 
    ```
    cat /opt/ludus/resources/packer/plugins/github.com/hashicorp/proxmox/packer-plugin-proxmox_v1.2.3_x5.0_linux_amd64 | sha256sum \
    > /opt/ludus/resources/packer/plugins/github.com/hashicorp/proxmox/packer-plugin-proxmox_v1.2.3_x5.0_linux_amd64_SHA256SUM
    ```
9. Now you can just run `ludus templates build` without any issues.