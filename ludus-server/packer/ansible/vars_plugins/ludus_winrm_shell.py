"""Correct Packer's legacy WinRM shell setting without editing templates."""

from ansible.inventory.helpers import get_group_vars
from ansible.inventory.host import Host
from ansible.plugins.vars import BaseVarsPlugin


class VarsModule(BaseVarsPlugin):
    # Loaded only through the vars-plugin path exported for template builds.
    REQUIRES_ENABLED = False
    is_stateless = True

    def get_vars(self, loader, path, entities):
        super().get_vars(loader, path, entities)
        if not isinstance(entities, list):
            entities = [entities]

        for entity in entities:
            if not isinstance(entity, Host):
                continue
            variables = get_group_vars(entity.get_groups())
            variables.update(entity.get_vars())
            if (
                variables.get("ansible_connection")
                in ("winrm", "ansible.builtin.winrm", "ansible.legacy.winrm")
                and variables.get("ansible_shell_type") == "powershell"
            ):
                # WinRS starts cmd.exe; PowerShell quoting breaks EncodedCommand.
                return {"ansible_shell_type": "cmd"}
        return {}
