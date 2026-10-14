#!/usr/bin/python3

from ansible.module_utils.basic import AnsibleModule
from collections import OrderedDict

DOCUMENTATION = r'''
---
module: ludus_role_dependency_resolver
short_description: Resolve dependencies in Ludus configuration
description:
    - This module reads a Ludus configuration array and returns an ordered array of VM names and role names
      that satisfies all the 'depends_on' restrictions.
    - Roles on the same VM (or the router) run in the order they are listed, unless an explicit 'depends_on'
      chain requires a later role to run first. A role pulled forward by a dependency brings the roles listed
      above it on its VM along with it.
    - If a circular dependency is found, it returns an error message.
    - The result is deterministic and consistent across multiple runs.
options:
    ludus_config_object:
        description:
            - the Ludus configuration object
        required: true
        type: list
    router_config_object:
        description:
            - the Router configuration object
        required: false
        type: dict
    default_router_vm_name:
        description:
            - Default router VM name to use when router.vm_name is not set
        required: true
        type: str
'''

EXAMPLES = r'''
- name: Resolve Ludus dependencies
  ludus_role_dependency_resolver:
    ludus_config_object: "{{ ludus }}"
    router_config_object: "{{ router | default({}) }}"
    default_router_vm_name: "{{ default_router_vm_name }}"
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

def add_vm_roles(vm_name, roles, graph, nodes):
    """Add one VM's roles and their explicit depends_on edges. Returns the VM's role nodes in list order."""
    ordered = []
    for role in roles:
        if isinstance(role, str):
            role_name = role
            depends_on = []
        elif isinstance(role, dict):
            role_name = role['name']
            depends_on = role.get('depends_on', [])
        else:
            continue
        node = (vm_name, role_name)
        nodes[node] = True
        graph[node] = [(dep['vm_name'], dep['role']) for dep in depends_on]
        ordered.append(node)
    return ordered

def depends_on_transitively(graph, node, target):
    """True if node already depends on target through any chain of edges."""
    stack = [node]
    seen = set()
    while stack:
        current = stack.pop()
        if current == target:
            return True
        if current in seen:
            continue
        seen.add(current)
        # Unknown dependencies are reported by topological_sort
        stack.extend(graph.get(current, []))
    return False

def add_list_order_edges(graph, ordered_roles_per_vm):
    """Make each role depend on the role listed above it on the same VM, unless explicit depends_on edges
    already require the opposite order. Runs after all explicit edges exist so cross-VM chains are seen."""
    for ordered in ordered_roles_per_vm:
        for prev, node in zip(ordered, ordered[1:]):
            if not depends_on_transitively(graph, prev, node):
                graph[node].append(prev)

def parse_ludus_config(ludus, router, default_router_vm_name):
    graph = OrderedDict()
    nodes = OrderedDict()
    ordered_roles_per_vm = []

    for vm in ludus:
        ordered_roles_per_vm.append(add_vm_roles(vm['vm_name'], vm.get('roles', []), graph, nodes))
    # Also check router roles
    if router is not None:
        router_vm_name = router.get('vm_name', default_router_vm_name)
        ordered_roles_per_vm.append(add_vm_roles(router_vm_name, router.get('roles', []), graph, nodes))

    add_list_order_edges(graph, ordered_roles_per_vm)
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
            ludus_config_object=dict(type='list', required=True),
            router_config_object=dict(type='dict', required=False),
            default_router_vm_name=dict(type='str', required=True),
        )
    )

    ludus = module.params['ludus_config_object']
    router = module.params['router_config_object']
    default_router_vm_name = module.params['default_router_vm_name']
    try:
        graph, nodes = parse_ludus_config(ludus, router, default_router_vm_name)
        order = topological_sort(graph, nodes)

        module.exit_json(changed=False, order=order)
    except ValueError as e:
        module.fail_json(msg=str(e))
    except Exception as e:
        module.fail_json(msg=f"An error occurred: {str(e)}")

if __name__ == '__main__':
    main()