#!/usr/bin/python3

from ansible.module_utils.basic import AnsibleModule
from collections import OrderedDict

DOCUMENTATION = r'''
---
module: ludus_dependency_resolver
short_description: Resolve dependencies in Ludus configuration
description:
    - This module reads a Ludus configuration array and returns an ordered array of VM names and role names
      that satisfies all the 'depends_on' restrictions.
    - If a circular dependency is found, it returns an error message.
    - The result is deterministic and consistent across multiple runs.
options:
    ludus_config_object:
        description:
            - the Ludus configuration object
        required: true
        type: list
'''

EXAMPLES = r'''
- name: Resolve Ludus dependencies
  ludus_dependency_resolver:
    ludus_config_object: "{{ ludus }}"
  register: result
'''

RETURN = r'''
order:
    description: Ordered array of VM names and role names
    type: list
    elements: dict
    returned: success
    sample: [
        {"vm_name": "vm1", "role_name": "role1"},
        {"vm_name": "vm2", "role_name": "role2"}
    ]
error:
    description: Error message if a circular dependency is found
    type: str
    returned: on error
    sample: "ERROR: Circular dependency found for vm1:role1"
'''

def parse_ludus_config(ludus):
    graph = OrderedDict()
    nodes = OrderedDict()

    for vm in ludus: # config.get('ludus', []):
        vm_name = vm['vm_name']
        roles = vm.get('roles', [])

        for role in roles:
            if isinstance(role, str):
                role_name = role
                nodes[(vm_name, role_name)] = True
                graph[(vm_name, role_name)] = []
            elif isinstance(role, dict):
                role_name = role['name']
                nodes[(vm_name, role_name)] = True
                graph[(vm_name, role_name)] = []
                for dep in role.get('depends_on', []):
                    dep_vm = dep['vm_name']
                    dep_role = dep['role']
                    graph[(vm_name, role_name)].append((dep_vm, dep_role))

    return graph, nodes

def topological_sort(graph, nodes):
    result = []
    permanent = set()
    temporary = set()

    def visit(node):
        if node in permanent:
            return
        if node in temporary:
            raise ValueError(f"ERROR: Circular dependency found for {node[0]}:{node[1]}")

        temporary.add(node)

        try:
            for neighbor in sorted(graph[node]):  # Sort neighbors for deterministic order
                visit(neighbor)
        except KeyError:
            raise ValueError(f"ERROR: Dependency not found {node[0]}:{node[1]}")
            

        temporary.remove(node)
        permanent.add(node)
        result.append({"vm_name": node[0], "role_name": node[1]})

    for node in nodes:
        if node not in permanent:
            visit(node)

    return result

def main():
    module = AnsibleModule(
        argument_spec=dict(
            ludus_config_object=dict(type='list', required=True)
        )
    )

    ludus = module.params['ludus_config_object']
    try:
        graph, nodes = parse_ludus_config(ludus)
        order = topological_sort(graph, nodes)

        module.exit_json(changed=False, order=order)
    except ValueError as e:
        module.fail_json(msg=str(e))
    except Exception as e:
        module.fail_json(msg=f"An error occurred: {str(e)}")

if __name__ == '__main__':
    main()