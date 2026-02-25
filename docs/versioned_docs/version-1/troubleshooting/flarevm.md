---
title: Flare-VM
---

## Disable Defender 1 Error (Blocked by antivirus)

```
 TASK [badsectorlabs.ludus_flarevm : Disable Defender 1] ************************
fatal: [flare]: FAILED! => {"changed": true, "debug": [], "error": [{"category_info": {"activity": "", "category": "ParserError", "category_id": 17, "reason": "ParentContainsErrorRecordException", "target_name": "", "target_type": ""}, "error_details": null, "exception": {"help_link": null, "hresult": -2146233087, "inner_exception": null, "message": "At line:1 char:1\r\n+ Add-MpPreference -ExclusionPath 'C:\\'\r\n+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\nThis script contains malicious content and has been blocked by your antivirus software.", "source": null, "type": "System.Management.Automation.ParentContainsErrorRecordException"}, "fully_qualified_error_id": "ScriptContainedMaliciousContent", "output": "At line:1 char:1\r\n+ Add-MpPreference -ExclusionPath 'C:\\'\r\n+ ~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~~\r\nThis script contains malicious content and has been blocked by your antivirus software.\r\n    + CategoryInfo          : ParserError: (:) [], ParentContainsErrorRecordException\r\n    + FullyQualifiedErrorId : ScriptContainedMaliciousContent\r\n \r\n", "pipeline_iteration_info": [], "script_stack_trace": "", "target_object": null}], "host_err": "", "host_out": "", "information": [], "output": [], "result": {}, "verbose": [], "warning": []}
```

If you encounter the issue when following this tutorial https://docs.ludus.cloud/docs/environment-guides/malware-lab, here is the solution:

1. Use flare-vm template instead of win11-xxx-template.

```bash
#terminal-command-local
git clone https://gitlab.com/badsectorlabs/ludus.git
#terminal-command-local
cd ludus/templates
#terminal-command-local
ludus templates add -d flare-vm
#terminal-command-local
ludus templates build
# Wait for the template to successfully build
# You can watch the logs with `ludus template logs -f`
# Or check the status with `ludus template status` and `ludus templates list`
```

2. After successfully building, change the template value in `config.yml` to `flare-vm-template`

```yaml title="config.yml"
- vm_name: "{{ range_id }}-flare"
    hostname: "{{ range_id }}-FLARE"
    template: flare-vm-template
    vlan: 99
    ip_last_octet: 3
    ram_gb: 4
    cpus: 2
    windows:
      install_additional_tools: false
    testing:
      snapshot: true
      block_internet: true
    roles:
      - badsectorlabs.ludus_flarevm
 ```

3. Set this config and force deploy it.

```bash
#terminal-command-local
ludus range config set -f config.yml
#terminal-command-local
ludus range deploy
# Wait for the range to successfully deploy
# You can watch the logs with `ludus range logs -f`
# Or check the status with `ludus range status`
```

Issue reference: [Issue 86](https://gitlab.com/badsectorlabs/ludus/-/issues/86)