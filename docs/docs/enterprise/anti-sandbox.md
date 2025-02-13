---
sidebar_position: 3
title: "🚫🏖️ Anti-Sandbox"
---

# 🚫🏖️ Anti-Sandbox

:::note[🏛️ `Available as an add-on to Ludus Enterprise`]
:::

Ludus Enterprise can optionally include a plugin that enables the use of the Anti-Sandbox measures.

## What is a VM sandbox?

A VM sandbox is a virtual machine that is used for malware research or other purposes. It often includes software and other tools that are used to perform malware analysis, such as a debugger, memory analyzer, or disassembler.

However, some malware or other software may specifically look for artifacts of a virtual machine that are not normally present on "real" hosts. This allows the malware to change its behavior, and potentially mislead the analyst or otherwise not perform the same actions as it would on a "real" host.

## What is the Ludus Anti-Sandbox plugin?

The Ludus Anti-Sandbox plugin uses custom compiled QEMU and OVMF packages that have sandbox artifacts (i.e. QEMU strings, etc) removed to create a VM that appears to be a "real" host. Additionally, the Ludus Anti-Sandbox plugin modifies specified VMs in the following ways:

* Drop and configure realistic user files:
  * Adds random numbers of PDF, DOC, PPTX, and XLSX files to Desktop and Downloads folders
  * Sets random creation/modification dates on files spanning the last 5 years
  * Opens random files to create usage artifacts and recent files history

* Modifies system timestamps and registration:
  * Sets a random Windows installation date between 2021-2024
  * Can configure custom registered organization and owner information

* Removes virtualization artifacts:
  * Uninstalls VirtIO Serial Driver
  * Removes QEMU Guest Agent and related services
  * Deletes RedHat registry keys
  * Removes virtualization-related folders (C:\ludus, C:\Tools, C:\QEMU-ga)

* Configures a more realistic desktop environment:
  * Restores default Windows wallpaper
  * Removes Ludus-specific background configurations

## Comparison

This is a comparison of the Ludus Anti-Sandbox plugin and the standard Windows 11 template.

## Standard Windows 11 VM

Notice the QEMU strings and other virtualization artifacts in the standard Windows 11 VM.

![A screenshot showing the VM artifacts in a standard VM](/img/enterprise/normal-vm.png)

## Ludus Anti-Sandbox VM

The VM artifacts have been replace with realistic artifacts that are present in a "real" host.
![A screenshot showing the realistic artifacts in an anti-sandbox VM](/img/enterprise/anti-sandbox-enabled.png)


## How to use

:::note

Ludus Anti-Sandbox is not supported on macOS or Linux VMs at this time. Contact us if you need this feature for those platforms.

:::

The Ludus Anti-Sandbox plugin works best with Windows VMs that have the bare minimum required to function in a hypervisor. One such template is included in the plugin: `win11-22h2-x64-enterprise-antisandbox`.

To use the Ludus Anti-Sandbox plugin, first build the `win11-22h2-x64-enterprise-antisandbox` template with the Ludus Enterprise plugin:

```shell-session
#terminal-command-local
ludus templates build -n win11-22h2-x64-enterprise-antisandbox-template
[INFO]  Template building started
```

You can now use the `win11-22h2-x64-enterprise-antisandbox` template in ranges.
You should also set `force_ip: true` in the range config to ensure the VMs maintain their IP addresses for ansible after the QEMU guest agent is removed.

```yaml
ludus:
    ...
    template: win11-22h2-x64-enterprise-antisandbox
    ...
    force_ip: true
    ...
```

To take full advantage of the Anti-Sandbox feature, you must install the custom QEMU and OVMF packages:

```shell-session
#terminal-command-local
ludus antisandbox install-custom
[INFO]  Anti-Sandbox QEMU and OVMF installed - will take effect on VM's next power cycle
```

:::note

The custom QEMU and OVMF packages apply to the entire Ludus host.

:::

Once your range config is updated to use the `win11-22h2-x64-enterprise-antisandbox` template, deploy the range:

```shell-session
#terminal-command-local
ludus range deploy
[INFO]  Range deploy started
```

When the range is fully deployed, make any modifications to the VMs you want before enabling Anti-Sandbox (take a snapshot as well).
When you are ready to enable Anti-Sandbox, note the VMID for the VM and run the following command. Multiple VMs can be specified with a comma separated list.

```shell-session
#terminal-command-local
ludus antisandbox enable -n 179
[INFO]  Enabling Anti-Sandbox settings for VM(s), this can take some time. Please wait.
[INFO]  Successfully enabled anti-sandbox for VM(s): 179
```

You can also specify `--drop-files` to populate the autologon user's desktop and download folders with random files (PPTX, DOC, XLSX, and PDF). The `--org` and `--owner` flags can be used to specify the organization and owner of the Machine set in the registry.

If there are any errors during the enable process, you can check the logs with `ludus range logs` or `ludus range errors`.


## Example Anti-Sandbox Configuration

```yaml
ludus:
  - vm_name: "{{ range_id }}-ad-dc-win2022-server-x64"
    hostname: "NYC-DC01-ACQ34"
    template: win2022-server-x64-template
    vlan: 10
    ip_last_octet: 11
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: company.com
      role: primary-dc
  - vm_name: "{{ range_id }}-ad-win11-22h2-enterprise-x64-1"
    hostname: "Q2VZX232CY"
    template: win11-22h2-x64-enterprise-antisandbox-template
    vlan: 10
    ip_last_octet: 21
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      install_additional_tools: false
      chocolatey_ignore_checksums: true # Chrome is always out of date
      chocolatey_packages:
        - googlechrome
        - firefox
        - adobereader
        - zoom
        - microsoft-teams-new-bootstrapper
        - webex
        - slack
        - bitwarden
        - 7zip
      office_version: 2021
      office_arch: 64bit
      autologon_user: DA-john.doe
      autologon_password: password
    domain:
      fqdn: company.com
      role: member
  - vm_name: "{{ range_id }}-ad-win11-22h2-enterprise-x64-2"
    hostname: "BAVZ2532VD"
    template: win11-22h2-x64-enterprise-antisandbox-template
    vlan: 10
    ip_last_octet: 22
    force_ip: true
    ram_gb: 8
    cpus: 4
    windows:
      install_additional_tools: false
      chocolatey_ignore_checksums: true # Chrome is always out of date
      chocolatey_packages:
        - googlechrome
        - firefox
        - adobereader
        - zoom
        - microsoft-teams-new-bootstrapper
        - webex
        - slack
        - bitwarden
        - 7zip      
      office_version: 2021
      office_arch: 64bit
      autologon_user: john.doe
      autologon_password: password
    domain:
      fqdn: company.com
      role: member

defaults:
  snapshot_with_RAM: true
  stale_hours: 0
  ad_domain_functional_level: Win2012R2
  ad_forest_functional_level: Win2012R2
  ad_domain_admin: DA-john.doe
  ad_domain_admin_password: password
  ad_domain_user: john.doe
  ad_domain_user_password: password
  ad_domain_safe_mode_password: password
  timezone: America/New_York
```


## Sandbox Detection Output

<details>
  <summary>Pafish (Paranoid Fish) Output</summary>

```
* Pafish (Paranoid Fish) *

[-] Windows version: 6.2 build 9200
[-] Running in WoW64: False
[-] CPU: AuthenticAMD
    CPU brand: AMD Ryzen 7 8845HS w/ Radeon 780M Graphics

[-] Debuggers detection
[*] Using IsDebuggerPresent() ... OK
[*] Using BeingDebugged via PEB access ... OK

[-] CPU information based detections
[*] Checking the difference between CPU timestamp counters (rdtsc) ... OK
[*] Checking the difference between CPU timestamp counters (rdtsc) forcing VM exit ... traced!
[*] Checking hypervisor bit in cpuid feature bits ... OK
[*] Checking cpuid hypervisor vendor for known VM vendors ... OK

[-] Generic reverse turing tests
[*] Checking mouse presence ... OK
[*] Checking mouse movement ... OK
[*] Checking mouse speed ... OK
[*] Checking mouse click activity ... OK
[*] Checking mouse double click activity ... OK
[*] Checking dialog confirmation ... OK
[*] Checking plausible dialog confirmation ... OK

[-] Generic sandbox detection
[*] Checking username ... OK
[*] Checking file path ... OK
[*] Checking common sample names in drives root ... OK
[*] Checking if disk size <= 60GB via DeviceIoControl() ... OK
[*] Checking if disk size <= 60GB via GetDiskFreeSpaceExA() ... OK
[*] Checking if Sleep() is patched using GetTickCount() ... OK
[*] Checking if NumberOfProcessors is < 2 via PEB access ... OK
[*] Checking if NumberOfProcessors is < 2 via GetSystemInfo() ... OK
[*] Checking if pysical memory is < 1Gb ... OK
[*] Checking operating system uptime using GetTickCount() ... OK
[*] Checking if operating system IsNativeVhdBoot() ... OK

[-] Sandboxie detection
[*] Using GetModuleHandle(sbiedll.dll) ... OK

[-] Wine detection
[*] Using GetProcAddress(wine_get_unix_file_name) from kernel32.dll ... OK
[*] Reg key (HKCU\SOFTWARE\Wine) ... OK

[-] VirtualBox detection
[*] Scsi port->bus->target id->logical unit id-> 0 identifier ... OK
[*] Reg key (HKLM\HARDWARE\Description\System "SystemBiosVersion") ... OK
[*] Reg key (HKLM\SOFTWARE\Oracle\VirtualBox Guest Additions) ... OK
[*] Reg key (HKLM\HARDWARE\Description\System "VideoBiosVersion") ... OK
[*] Reg key (HKLM\HARDWARE\ACPI\DSDT\VBOX__) ... OK
[*] Reg key (HKLM\HARDWARE\ACPI\FADT\VBOX__) ... OK
[*] Reg key (HKLM\HARDWARE\ACPI\RSDT\VBOX__) ... OK
[*] Reg key (HKLM\SYSTEM\ControlSet001\Services\VBox*) ... OK
[*] Reg key (HKLM\HARDWARE\DESCRIPTION\System "SystemBiosDate") ... OK
[*] Driver files in C:\WINDOWS\system32\drivers\VBox* ... OK
[*] Additional system files ... OK
[*] Looking for a MAC address starting with 08:00:27 ... OK
[*] Looking for pseudo devices ... OK
[*] Looking for VBoxTray windows ... OK
[*] Looking for VBox network share ... OK
[*] Looking for VBox processes (vboxservice.exe, vboxtray.exe) ... OK
[*] Looking for VBox devices using WMI ... OK

[-] VMware detection
[*] Scsi port 0,1,2 ->bus->target id->logical unit id-> 0 identifier ... OK
[*] Reg key (HKLM\SOFTWARE\VMware, Inc.\VMware Tools) ... OK
[*] Looking for C:\WINDOWS\system32\drivers\vmmouse.sys ... OK
[*] Looking for C:\WINDOWS\system32\drivers\vmhgfs.sys ... OK
[*] Looking for a MAC address starting with 00:05:69, 00:0C:29, 00:1C:14 or 00:50:56 ... OK
[*] Looking for network adapter name ... OK
[*] Looking for pseudo devices ... OK
[*] Looking for VMware serial number ... OK

[-] Qemu detection
[*] Scsi port->bus->target id->logical unit id-> 0 identifier ... OK
[*] Reg key (HKLM\HARDWARE\Description\System "SystemBiosVersion") ... OK
[*] cpuid CPU brand string 'QEMU Virtual CPU' ... OK

[-] Bochs detection
[*] Reg key (HKLM\HARDWARE\Description\System "SystemBiosVersion") ... OK
[*] cpuid AMD wrong value for processor name ... OK
[*] cpuid Intel wrong value for processor name ... OK

[-] Pafish has finished analyzing the system, check the log file for more information
    and visit the project's site:

    https://github.com/a0rtega/pafish
```
</details>

<details>
  <summary>Al-Khaser Output</summary>
```
[al-khaser version 0.82]
-------------------------[Initialisation]-------------------------

[*] You are running: Microsoft Windows 10  (build 22621) 64-bit
[*] All APIs present and accounted for.

-------------------------[TLS Callbacks]-------------------------
[*] TLS process attach callback                                                                    [ GOOD ]
[*] TLS thread attach callback                                                                     [ GOOD ]

-------------------------[Debugger Detection]-------------------------
[*] Checking IsDebuggerPresent API                                                                 [ GOOD ]
[*] Checking PEB.BeingDebugged                                                                     [ GOOD ]
[*] Checking CheckRemoteDebuggerPresent API                                                        [ GOOD ]
[*] Checking PEB.NtGlobalFlag                                                                      [ GOOD ]
[*] Checking ProcessHeap.Flags                                                                     [ GOOD ]
[*] Checking ProcessHeap.ForceFlags                                                                [ GOOD ]
[*] Checking Low Fragmentation Heap                                                                [ GOOD ]
[*] Checking NtQueryInformationProcess with ProcessDebugPort                                       [ GOOD ]
[*] Checking NtQueryInformationProcess with ProcessDebugFlags                                      [ GOOD ]
[*] Checking NtQueryInformationProcess with ProcessDebugObject                                     [ GOOD ]
[*] Checking WudfIsAnyDebuggerPresent API                                                          [ GOOD ]
[*] Checking WudfIsKernelDebuggerPresent API                                                       [ GOOD ]
[*] Checking WudfIsUserDebuggerPresent API                                                         [ GOOD ]
[*] Checking NtSetInformationThread with ThreadHideFromDebugger                                    [ GOOD ]
[*] Checking CloseHandle with an invalide handle                                                   [ GOOD ]
[*] Checking NtSystemDebugControl                                                                  [ GOOD ]
[*] Checking UnhandledExcepFilterTest                                                              [ GOOD ]
[*] Checking OutputDebugString                                                                     [ GOOD ]
[*] Checking Hardware Breakpoints                                                                  [ GOOD ]
[*] Checking Software Breakpoints                                                                  [ GOOD ]
[*] Checking Interupt 0x2d                                                                         [ GOOD ]
[*] Checking Interupt 1                                                                            [ GOOD ]
[*] Checking trap flag                                                                             [ GOOD ]
[*] Checking Memory Breakpoints PAGE GUARD                                                         [ GOOD ]
[*] Checking If Parent Process is explorer.exe                                                     [ GOOD ]
[*] Checking SeDebugPrivilege                                                                      [ GOOD ]
[*] Checking NtQueryObject with ObjectTypeInformation                                              [ GOOD ]
[*] Checking NtQueryObject with ObjectAllTypesInformation                                          [ GOOD ]
[*] Checking NtYieldExecution                                                                      [ GOOD ]
[*] Checking CloseHandle protected handle trick                                                    [ GOOD ]
[*] Checking NtQuerySystemInformation with SystemKernelDebuggerInformation                         [ GOOD ]
[*] Checking SharedUserData->KdDebuggerEnabled                                                     [ GOOD ]
[*] Checking if process is in a job                                                                [ GOOD ]
[*] Checking VirtualAlloc write watch (buffer only)                                                [ GOOD ]
[*] Checking VirtualAlloc write watch (API calls)                                                  [ GOOD ]
[*] Checking VirtualAlloc write watch (IsDebuggerPresent)                                          [ GOOD ]
[*] Checking VirtualAlloc write watch (code write)                                                 [ GOOD ]
[*] Checking for page exception breakpoints                                                        [ GOOD ]
[*] Checking for API hooks outside module bounds                                                   [ GOOD ]

-------------------------[DLL Injection Detection]-------------------------
[*] Enumerating modules with EnumProcessModulesEx [32-bit]                                         [ GOOD ]
[*] Enumerating modules with EnumProcessModulesEx [64-bit]                                         [ GOOD ]
[*] Enumerating modules with EnumProcessModulesEx [ALL]                                            [ GOOD ]
[*] Enumerating modules with ToolHelp32                                                            [ GOOD ]
[*] Enumerating the process LDR via LdrEnumerateLoadedModules                                      [ GOOD ]
[*] Enumerating the process LDR directly                                                           [ GOOD ]
[*] Walking process memory with GetModuleInformation                                               [ GOOD ]
[*] Walking process memory for hidden modules                                                      [ GOOD ]
[*] Walking process memory for .NET module structures                                              [ GOOD ]

-------------------------[Generic Sandboxe/VM Detection]-------------------------
[*] Checking if process loaded modules contains: avghookx.dll                                      [ GOOD ]
[*] Checking if process loaded modules contains: avghooka.dll                                      [ GOOD ]
[*] Checking if process loaded modules contains: snxhk.dll                                         [ GOOD ]
[*] Checking if process loaded modules contains: sbiedll.dll                                       [ GOOD ]
[*] Checking if process loaded modules contains: dbghelp.dll                                       [ GOOD ]
[*] Checking if process loaded modules contains: api_log.dll                                       [ GOOD ]
[*] Checking if process loaded modules contains: dir_watch.dll                                     [ GOOD ]
[*] Checking if process loaded modules contains: pstorec.dll                                       [ GOOD ]
[*] Checking if process loaded modules contains: vmcheck.dll                                       [ GOOD ]
[*] Checking if process loaded modules contains: wpespy.dll                                        [ GOOD ]
[*] Checking if process loaded modules contains: cmdvrt64.dll                                      [ GOOD ]
[*] Checking if process loaded modules contains: cmdvrt32.dll                                      [ GOOD ]
[*] Checking if process file name contains: sample.exe                                             [ GOOD ]
[*] Checking if process file name contains: bot.exe                                                [ GOOD ]
[*] Checking if process file name contains: sandbox.exe                                            [ GOOD ]
[*] Checking if process file name contains: malware.exe                                            [ GOOD ]
[*] Checking if process file name contains: test.exe                                               [ GOOD ]
[*] Checking if process file name contains: klavme.exe                                             [ GOOD ]
[*] Checking if process file name contains: myapp.exe                                              [ GOOD ]
[*] Checking if process file name contains: testapp.exe                                            [ GOOD ]
[*] Checking if process file name looks like a hash: al-khaser_x64                                 [ GOOD ]
[*] Checking if username matches : CurrentUser                                                     [ GOOD ]
[*] Checking if username matches : Sandbox                                                         [ GOOD ]
[*] Checking if username matches : Emily                                                           [ GOOD ]
[*] Checking if username matches : HAPUBWS                                                         [ GOOD ]
[*] Checking if username matches : Hong Lee                                                        [ GOOD ]
[*] Checking if username matches : IT-ADMIN                                                        [ GOOD ]
[*] Checking if username matches : Johnson                                                         [ GOOD ]
[*] Checking if username matches : Miller                                                          [ GOOD ]
[*] Checking if username matches : milozs                                                          [ GOOD ]
[*] Checking if username matches : Peter Wilson                                                    [ GOOD ]
[*] Checking if username matches : timmy                                                           [ GOOD ]
[*] Checking if username matches : user                                                            [ GOOD ]
[*] Checking if username matches : sand box                                                        [ GOOD ]
[*] Checking if username matches : malware                                                         [ GOOD ]
[*] Checking if username matches : maltest                                                         [ GOOD ]
[*] Checking if username matches : test user                                                       [ GOOD ]
[*] Checking if username matches : virus                                                           [ GOOD ]
[*] Checking if username matches : John Doe                                                        [ GOOD ]
[*] Checking if hostname matches : SANDBOX                                                         [ GOOD ]
[*] Checking if hostname matches : 7SILVIA                                                         [ GOOD ]
[*] Checking if hostname matches : HANSPETER-PC                                                    [ GOOD ]
[*] Checking if hostname matches : JOHN-PC                                                         [ GOOD ]
[*] Checking if hostname matches : MUELLER-PC                                                      [ GOOD ]
[*] Checking if hostname matches : WIN7-TRAPS                                                      [ GOOD ]
[*] Checking if hostname matches : FORTINET                                                        [ GOOD ]
[*] Checking if hostname matches : TEQUILABOOMBOOM                                                 [ GOOD ]
[*] Checking whether username is 'Wilber' and NetBIOS name starts with 'SC' or 'SW'                [ GOOD ]
[*] Checking whether username is 'admin' and NetBIOS name is 'SystemIT'                            [ GOOD ]
[*] Checking whether username is 'admin' and DNS hostname is 'KLONE_X64-PC'                        [ GOOD ]
[*] Checking whether username is 'John' and two sandbox files exist                                [ GOOD ]
[*] Checking whether four known sandbox 'email' file paths exist                                   [ GOOD ]
[*] Checking whether three known sandbox 'foobar' files exist                                      [ GOOD ]
[*] Checking Number of processors in machine                                                       [ GOOD ]
[*] Checking Interupt Descriptor Table location                                                    [ GOOD ]
[*] Checking Local Descriptor Table location                                                       [ BAD  ]
[*] Checking Global Descriptor Table location                                                      [ GOOD ]
[*] Checking Store Task Register                                                                   [ GOOD ]
[*] Checking Number of cores in machine using WMI                                                  [ GOOD ]
[*] Checking hard disk size using WMI                                                              [ GOOD ]
[*] Checking hard disk size using DeviceIoControl                                                  [ GOOD ]
[*] Checking SetupDi_diskdrive                                                                     [ GOOD ]
[*] Checking mouse movement                                                                        [ GOOD ]
[*] Checking lack of user input                                                                    [ GOOD ]
[*] Checking memory space using GlobalMemoryStatusEx                                               [ GOOD ]
[*] Checking disk size using GetDiskFreeSpaceEx                                                    [ GOOD ]
[*] Checking if CPU hypervisor field is set using cpuid(0x1)                                       [ GOOD ]
[*] Checking hypervisor vendor using cpuid(0x40000000)                                             [ GOOD ]
[*] Check if time has been accelerated                                                             [ GOOD ]
[*] VM Driver Services                                                                             [ GOOD ]
[*] Checking SerialNumber from BIOS using WMI                                                      [ GOOD ]
[*] Checking Model from ComputerSystem using WMI                                                   [ GOOD ]
[*] Checking Manufacturer from ComputerSystem using WMI                                            [ GOOD ]
[*] Checking Current Temperature using WMI                                                         [ BAD  ]
[*] Checking ProcessId using WMI                                                                   [ GOOD ]
[*] Checking power capabilities                                                                    [ GOOD ]
[*] Checking CPU fan using WMI                                                                     [ BAD  ]
[*] Checking NtQueryLicenseValue with Kernel-VMDetection-Private                                   [ GOOD ]
[*] Checking Win32_CacheMemory with WMI                                                            [ BAD  ]
[*] Checking Win32_PhysicalMemory with WMI                                                         [ GOOD ]
[*] Checking Win32_MemoryDevice with WMI                                                           [ BAD  ]
[*] Checking Win32_MemoryArray with WMI                                                            [ GOOD ]
[*] Checking Win32_VoltageProbe with WMI                                                           [ BAD  ]
[*] Checking Win32_PortConnector with WMI                                                          [ BAD  ]
[*] Checking Win32_SMBIOSMemory with WMI                                                           [ GOOD ]
[*] Checking ThermalZoneInfo performance counters with WMI                                         [ BAD  ]
[*] Checking CIM_Memory with WMI                                                                   [ BAD  ]
[*] Checking CIM_Sensor with WMI                                                                   [ BAD  ]
[*] Checking CIM_NumericSensor with WMI                                                            [ BAD  ]
[*] Checking CIM_TemperatureSensor with WMI                                                        [ BAD  ]
[*] Checking CIM_VoltageSensor with WMI                                                            [ BAD  ]
[*] Checking CIM_PhysicalConnector with WMI                                                        [ BAD  ]
[*] Checking CIM_Slot with WMI                                                                     [ BAD  ]
[*] Checking if Windows is Genuine                                                                 [ GOOD ]
[*] Checking Services\Disk\Enum entries for VM strings                                             [ GOOD ]
[*] Checking Enum\IDE and Enum\SCSI entries for VM strings                                         [ GOOD ]
[*] Checking SMBIOS tables                                                                         [ BAD  ]

-------------------------[VirtualBox Detection]-------------------------
[*] Checking reg key HARDWARE\Description\System - Identifier is set to VBOX                       [ GOOD ]
[*] Checking reg key HARDWARE\Description\System - SystemBiosVersion is set to VBOX                [ GOOD ]
[*] Checking reg key HARDWARE\Description\System - VideoBiosVersion is set to VIRTUALBOX           [ GOOD ]
[*] Checking reg key HARDWARE\Description\System - SystemBiosDate is set to 06/23/99               [ GOOD ]
[*] Checking VirtualBox Guest Additions directory                                                  [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\VBoxMouse.sys                                        [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\VBoxGuest.sys                                        [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\VBoxSF.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\VBoxVideo.sys                                        [ GOOD ]
[*] Checking file C:\Windows\System32\vboxdisp.dll                                                 [ GOOD ]
[*] Checking file C:\Windows\System32\vboxhook.dll                                                 [ GOOD ]
[*] Checking file C:\Windows\System32\vboxmrxnp.dll                                                [ GOOD ]
[*] Checking file C:\Windows\System32\vboxogl.dll                                                  [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglarrayspu.dll                                          [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglcrutil.dll                                            [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglerrorspu.dll                                          [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglfeedbackspu.dll                                       [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglpackspu.dll                                           [ GOOD ]
[*] Checking file C:\Windows\System32\vboxoglpassthroughspu.dll                                    [ GOOD ]
[*] Checking file C:\Windows\System32\vboxservice.exe                                              [ GOOD ]
[*] Checking file C:\Windows\System32\vboxtray.exe                                                 [ GOOD ]
[*] Checking file C:\Windows\System32\VBoxControl.exe                                              [ GOOD ]
[*] Checking reg key HARDWARE\ACPI\DSDT\VBOX__                                                     [ GOOD ]
[*] Checking reg key HARDWARE\ACPI\FADT\VBOX__                                                     [ GOOD ]
[*] Checking reg key HARDWARE\ACPI\RSDT\VBOX__                                                     [ GOOD ]
[*] Checking reg key SOFTWARE\Oracle\VirtualBox Guest Additions                                    [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VBoxGuest                                       [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VBoxMouse                                       [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VBoxService                                     [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VBoxSF                                          [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VBoxVideo                                       [ GOOD ]
[*] Checking Mac Address start with 08:00:27                                                       [ GOOD ]
[*] Checking MAC address (Hybrid Analysis)                                                         [ GOOD ]
[*] Checking device \\.\VBoxMiniRdrDN                                                              [ GOOD ]
[*] Checking device \\.\VBoxGuest                                                                  [ GOOD ]
[*] Checking device \\.\pipe\VBoxMiniRdDN                                                          [ GOOD ]
[*] Checking device \\.\VBoxTrayIPC                                                                [ GOOD ]
[*] Checking device \\.\pipe\VBoxTrayIPC                                                           [ GOOD ]
[*] Checking VBoxTrayToolWndClass / VBoxTrayToolWnd                                                [ GOOD ]
[*] Checking VirtualBox Shared Folders network provider                                            [ GOOD ]
[*] Checking VirtualBox process vboxservice.exe                                                    [ GOOD ]
[*] Checking VirtualBox process vboxtray.exe                                                       [ GOOD ]
[*] Checking Win32_PnPDevice DeviceId from WMI for VBox PCI device                                 [ GOOD ]
[*] Checking Win32_PnPDevice Name from WMI for VBox controller hardware                            [ GOOD ]
[*] Checking Win32_PnPDevice Name from WMI for VBOX names                                          [ GOOD ]
[*] Checking Win32_Bus from WMI                                                                    [ GOOD ]
[*] Checking Win32_BaseBoard from WMI                                                              [ GOOD ]
[*] Checking MAC address from WMI                                                                  [ GOOD ]
[*] Checking NTEventLog from WMI                                                                   [ GOOD ]
[*] Checking SMBIOS firmware                                                                       [ GOOD ]
[*] Checking ACPI tables                                                                           [ GOOD ]

-------------------------[VMWare Detection]-------------------------
[*] Checking reg key HARDWARE\DEVICEMAP\Scsi\Scsi Port 0\Scsi Bus 0\Target Id 0\Logical Unit Id 0  [ GOOD ]
[*] Checking reg key HARDWARE\DEVICEMAP\Scsi\Scsi Port 1\Scsi Bus 0\Target Id 0\Logical Unit Id 0  [ GOOD ]
[*] Checking reg key HARDWARE\DEVICEMAP\Scsi\Scsi Port 2\Scsi Bus 0\Target Id 0\Logical Unit Id 0  [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Control\SystemInformation                                [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Control\SystemInformation                                [ GOOD ]
[*] Checking reg key SOFTWARE\VMware, Inc.\VMware Tools                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmnet.sys                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmmouse.sys                                          [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmusb.sys                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vm3dmp.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmci.sys                                             [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmhgfs.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmmemctl.sys                                         [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmx86.sys                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmrawdsk.sys                                         [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmusbmouse.sys                                       [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmkdb.sys                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmnetuserif.sys                                      [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vmnetadapter.sys                                     [ GOOD ]
[*] Checking MAC starting with 00:05:69                                                            [ GOOD ]
[*] Checking MAC starting with 00:0c:29                                                            [ GOOD ]
[*] Checking MAC starting with 00:1C:14                                                            [ GOOD ]
[*] Checking MAC starting with 00:50:56                                                            [ GOOD ]
[*] Checking VMWare network adapter name                                                           [ GOOD ]
[*] Checking device \\.\HGFS                                                                       [ GOOD ]
[*] Checking device \\.\vmci                                                                       [ GOOD ]
[*] Checking VMWare directory                                                                      [ GOOD ]
[*] Checking SMBIOS firmware                                                                       [ GOOD ]
[*] Checking ACPI tables                                                                           [ GOOD ]

-------------------------[Virtual PC Detection]-------------------------
[*] Checking Virtual PC processes VMSrvc.exe                                                       [ GOOD ]
[*] Checking Virtual PC processes VMUSrvc.exe                                                      [ GOOD ]
[*] Checking reg key SOFTWARE\Microsoft\Virtual Machine\Guest\Parameters                           [ GOOD ]

-------------------------[QEMU Detection]-------------------------
[*] Checking reg key HARDWARE\DEVICEMAP\Scsi\Scsi Port 0\Scsi Bus 0\Target Id 0\Logical Unit Id 0  [ GOOD ]
[*] Checking reg key HARDWARE\Description\System                                                   [ GOOD ]
[*] Checking qemu processes qemu-ga.exe                                                            [ GOOD ]
[*] Checking qemu processes vdagent.exe                                                            [ GOOD ]
[*] Checking qemu processes vdservice.exe                                                          [ GOOD ]
[*] Checking QEMU directory C:\Program Files\qemu-ga                                               [ GOOD ]
[*] Checking QEMU directory C:\Program Files\SPICE Guest Tools                                     [ GOOD ]
[*] Checking SMBIOS firmware                                                                       [ GOOD ]
[*] Checking ACPI tables                                                                           [ GOOD ]

-------------------------[Xen Detection]-------------------------
[*] Checking Citrix Xen process xenservice.exe                                                     [ GOOD ]
[*] Checking Mac Address start with 08:16:3E                                                       [ GOOD ]

-------------------------[Xen Detection]-------------------------
[*] Checking file C:\Windows\System32\drivers\balloon.sys                                          [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\netkvm.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\pvpanic.sys                                          [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\viofs.sys                                            [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\viogpudo.sys                                         [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vioinput.sys                                         [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\viorng.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vioscsi.sys                                          [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\vioser.sys                                           [ GOOD ]
[*] Checking file C:\Windows\System32\drivers\viostor.sys                                          [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\vioscsi                                         [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\viostor                                         [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VirtIO-FS Service                               [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\VirtioSerial                                    [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\BALLOON                                         [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\BalloonService                                  [ GOOD ]
[*] Checking reg key SYSTEM\ControlSet001\Services\netkvm                                          [ GOOD ]
[*] Checking KVM virio directory                                                                   [ GOOD ]

-------------------------[Wine Detection]-------------------------
[*] Checking Wine via dll exports                                                                  [ GOOD ]
[*] Checking reg key SOFTWARE\Wine                                                                 [ GOOD ]

-------------------------[Paralles Detection]-------------------------
[*] Checking Parallels processes: prl_cc.exe                                                       [ GOOD ]
[*] Checking Parallels processes: prl_tools.exe                                                    [ GOOD ]
[*] Checking Mac Address start with 00:1C:42                                                       [ GOOD ]

-------------------------[Hyper-V Detection]-------------------------
[*] Checking for Hyper-V driver objects                                                            [ GOOD ]
[*] Checking for Hyper-V global objects                                                            [ BAD  ]

-------------------------[Timing-attacks]-------------------------

[*] Delay value is set to 10 minutes ...
[*] Performing a sleep using NtDelayExecution ...                                                  [ GOOD ]
[*] Performing a sleep() in a loop ...                                                             [ GOOD ]
[*] Delaying execution using SetTimer ...                                                          [ GOOD ]
[*] Delaying execution using timeSetEvent ...                                                      [ GOOD ]
[*] Delaying execution using WaitForSingleObject ...                                               [ GOOD ]
[*] Delaying execution using WaitForMultipleObjects ...                                            [ GOOD ]
[*] Delaying execution using IcmpSendEcho ...                                                      [ GOOD ]
[*] Delaying execution using CreateWaitableTimer ...                                               [ GOOD ]
[*] Delaying execution using CreateTimerQueueTimer ...                                             [ GOOD ]
[*] Checking RDTSC Locky trick                                                                     [ GOOD ]
[*] Checking RDTSC which force a VM Exit (cpuid)                                                   [ BAD  ]

-------------------------[Analysis-tools]-------------------------
[*] Checking process of malware analysis tool: ollydbg.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: ProcessHacker.exe                                   [ GOOD ]
[*] Checking process of malware analysis tool: tcpview.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: autoruns.exe                                        [ GOOD ]
[*] Checking process of malware analysis tool: autorunsc.exe                                       [ GOOD ]
[*] Checking process of malware analysis tool: filemon.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: procmon.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: regmon.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: procexp.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: idaq.exe                                            [ GOOD ]
[*] Checking process of malware analysis tool: idaq64.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: ImmunityDebugger.exe                                [ GOOD ]
[*] Checking process of malware analysis tool: Wireshark.exe                                       [ GOOD ]
[*] Checking process of malware analysis tool: dumpcap.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: HookExplorer.exe                                    [ GOOD ]
[*] Checking process of malware analysis tool: ImportREC.exe                                       [ GOOD ]
[*] Checking process of malware analysis tool: PETools.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: LordPE.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: SysInspector.exe                                    [ GOOD ]
[*] Checking process of malware analysis tool: proc_analyzer.exe                                   [ GOOD ]
[*] Checking process of malware analysis tool: sysAnalyzer.exe                                     [ GOOD ]
[*] Checking process of malware analysis tool: sniff_hit.exe                                       [ GOOD ]
[*] Checking process of malware analysis tool: windbg.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: joeboxcontrol.exe                                   [ GOOD ]
[*] Checking process of malware analysis tool: joeboxserver.exe                                    [ GOOD ]
[*] Checking process of malware analysis tool: joeboxserver.exe                                    [ GOOD ]
[*] Checking process of malware analysis tool: ResourceHacker.exe                                  [ GOOD ]
[*] Checking process of malware analysis tool: x32dbg.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: x64dbg.exe                                          [ GOOD ]
[*] Checking process of malware analysis tool: Fiddler.exe                                         [ GOOD ]
[*] Checking process of malware analysis tool: httpdebugger.exe                                    [ GOOD ]
[*] Checking process of malware analysis tool: cheatengine-i386.exe                                [ GOOD ]
[*] Checking process of malware analysis tool: cheatengine-x86_64.exe                              [ GOOD ]
[*] Checking process of malware analysis tool: cheatengine-x86_64-SSE4-AVX2.exe                    [ GOOD ]
[*] Checking process of malware analysis tool: frida-helper-32.exe                                 [ GOOD ]
[*] Checking process of malware analysis tool: frida-helper-64.exe                                 [ GOOD ]
Begin AntiDisassmConstantCondition
Begin AntiDisassmAsmJmpSameTarget
Begin AntiDisassmImpossibleDiasassm
Begin AntiDisassmFunctionPointer
Begin AntiDisassmReturnPointerAbuse

-------------------------[Anti Dumping]-------------------------
[*] Erasing PE header from memory
[*] Increasing SizeOfImage in PE Header to: 0x100000


Analysis done, I hope you didn't get red flags :)
```
</details>