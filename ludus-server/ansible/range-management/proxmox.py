#!/usr/bin/env python3

# Copyright (C) 2014  Mathieu GAUTHIER-LAFAYE <gauthierl@lapth.cnrs.fr>
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU General Public License for more details.
#
# You should have received a copy of the GNU General Public License
# along with this program.  If not, see <http://www.gnu.org/licenses/>.

# Updated 2016 by Matt Harris <matthaeus.harris@gmail.com>
#
# Added support for Proxmox VE 4.x
# Added support for using the Notes field of a VM to define groups and variables:
# A well-formatted JSON object in the Notes field will be added to the _meta
# section for that VM.  In addition, the "groups" key of this JSON object may be
# used to specify group membership:
#
# { "groups": ["utility", "databases"], "a": false, "b": true }

# Updated 2024 by the Ludus authors
#
# Added error handling
# Added Windows os name normalization
# Added macOS "support"
# Fixed version detection logic for Proxmox VE >= 8.0.0
# Added Ludus config integration to select IP address based on VM name

from six.moves.urllib import request, parse, error

import ast
try:
    import json
except ImportError:
    import simplejson as json
import ipaddress
import os
import sys
import socket
import re
import time
from optparse import OptionParser
import yaml

from six import iteritems

from six.moves.urllib.error import HTTPError

from ansible.module_utils.urls import open_url

compiled_range_id_regex = re.compile(r"{{\s+range_id\s+}}", re.IGNORECASE)


class ProxmoxNodeList(list):
    def get_names(self):
        return [node['node'] for node in self]


class ProxmoxVM(dict):
    def get_variables(self):
        variables = {}
        for key, value in iteritems(self):
            variables['proxmox_' + key] = value
        return variables


class ProxmoxVMList(list):
    def __init__(self, data=[], pxmxver=0.0):
        self.ver = pxmxver
        for item in data:
            self.append(ProxmoxVM(item))

    def get_names(self):
        if self.ver >= 4.0:
            return [vm['name'] for vm in self if 'template' in vm and vm['template'] != 1]
        else:
            return [vm['name'] for vm in self]

    def get_by_name(self, name):
        results = [vm for vm in self if vm['name'] == name]
        return results[0] if len(results) > 0 else None

    def get_variables(self):
        variables = {}
        for vm in self:
            variables[vm['name']] = vm.get_variables()

        return variables


class ProxmoxPoolList(list):
    def get_names(self):
        return [pool['poolid'] for pool in self]


class ProxmoxVersion(dict):
    def get_version(self):
        # Handle versions with `-` in them (works on versions without as well)
        version = self['version'].split('-')[0]
        if len(version.split('.')) > 2:
            return float('.'.join(version.split('.')[0:2]))
        else:
            return float(self['version'])


class ProxmoxPool(dict):
    def get_members_name(self):
        return [member['name'] for member in self['members'] if (member['type'] == 'qemu' or member['type'] == 'lxc') and member['template'] != 1]


class ProxmoxAPI(object):
    def __init__(self, options, config_path):
        self.options = options
        self.credentials = None

        if not options.url or not options.username or not options.password:
            if os.path.isfile(config_path):
                with open(config_path, "r") as config_file:
                    config_data = json.load(config_file)
                    if not options.url:
                        try:
                            options.url = config_data["url"]
                        except KeyError:
                            options.url = None
                    if not options.username:
                        try:
                            options.username = config_data["username"]
                        except KeyError:
                            options.username = None
                    if not options.password:
                        try:
                            options.password = config_data["password"]
                        except KeyError:
                            options.password = None

        if not options.url:
            raise Exception('Missing mandatory parameter --url (or PROXMOX_URL or "url" key in config file).')
        elif not options.username:
            raise Exception(
                'Missing mandatory parameter --username (or PROXMOX_USERNAME or "username" key in config file).')
        elif not options.password:
            raise Exception(
                'Missing mandatory parameter --password (or PROXMOX_PASSWORD or "password" key in config file).')
        
        # URL should end with a trailing slash
        if not options.url.endswith("/"):
            options.url = options.url + "/"

    def auth(self):
        request_path = '{0}api2/json/access/ticket'.format(self.options.url)

        request_params = parse.urlencode({
            'username': self.options.username,
            'password': self.options.password,
        })

        data = json.load(open_url(request_path, data=request_params,
                                  validate_certs=self.options.validate))

        self.credentials = {
            'ticket': data['data']['ticket'],
            'CSRFPreventionToken': data['data']['CSRFPreventionToken'],
        }

    def get(self, url, data=None):
        request_path = '{0}{1}'.format(self.options.url, url)

        headers = {'Cookie': 'PVEAuthCookie={0}'.format(self.credentials['ticket'])}
        request = open_url(request_path, data=data, headers=headers,
                           validate_certs=self.options.validate)

        response = json.load(request)
        return response['data']

    def nodes(self):
        return ProxmoxNodeList(self.get('api2/json/nodes'))

    def vms_by_type(self, node, type):
        return ProxmoxVMList(self.get('api2/json/nodes/{0}/{1}'.format(node, type)), self.version().get_version())

    def vm_description_by_type(self, node, vm, type):
        return self.get('api2/json/nodes/{0}/{1}/{2}/config'.format(node, type, vm))

    def node_qemu(self, node):
        return self.vms_by_type(node, 'qemu')

    def node_qemu_description(self, node, vm):
        return self.vm_description_by_type(node, vm, 'qemu')

    def node_lxc(self, node):
        return self.vms_by_type(node, 'lxc')

    def node_lxc_description(self, node, vm):
        return self.vm_description_by_type(node, vm, 'lxc')

    def node_openvz(self, node):
        return self.vms_by_type(node, 'openvz')

    def node_openvz_description(self, node, vm):
        return self.vm_description_by_type(node, vm, 'openvz')

    def pools(self):
        return ProxmoxPoolList(self.get('api2/json/pools'))

    def pool(self, poolid):
        return ProxmoxPool(self.get('api2/json/pools/{0}'.format(poolid)))
    
    def qemu_agent(self, node, vm):
        try:
            info = self.get('api2/json/nodes/{0}/qemu/{1}/agent/info'.format(node, vm))
            if info is not None:
                return True
        except HTTPError as error:
            return False

    def openvz_ip_address(self, node, vm):
        try:
            config = self.get('api2/json/nodes/{0}/lxc/{1}/config'.format(node, vm))
        except HTTPError:
            return False
        
        try:
            ip_address = re.search('ip=(\d*\.\d*\.\d*\.\d*)', config['net0']).group(1)
            return ip_address
        except:
            return False
    
    def version(self):
        return ProxmoxVersion(self.get('api2/json/version'))

    def qemu_agent_info(self, node, vm, proxmox_name):
        system_info = SystemInfo()
        osinfo = self.get('api2/json/nodes/{0}/qemu/{1}/agent/get-osinfo'.format(node, vm))['result']
        if osinfo:
            if 'id' in osinfo:
                if osinfo['id'] == 'mswindows':
                    system_info.id = 'windows'
                else:
                    system_info.id = osinfo['id']

            if 'name' in osinfo:
                system_info.name = osinfo['name']

            if 'machine' in osinfo:
                system_info.machine = osinfo['machine']

            if 'kernel-release' in osinfo:
                system_info.kernel = osinfo['kernel-release']

            if 'version-id' in osinfo:
                system_info.version_id = osinfo['version-id']

        all_ip_addresses = []
        try:
            networks = self.get('api2/json/nodes/{0}/qemu/{1}/agent/network-get-interfaces'.format(node, vm))['result']
        except HTTPError:
            time.sleep(0.5) # sometimes this fails, so sleep a little and try again?
            try:
                networks = self.get('api2/json/nodes/{0}/qemu/{1}/agent/network-get-interfaces'.format(node, vm))['result']
            except HTTPError:
                networks = None # If a second try doesn't work, fail gracefully
        if networks:
            if type(networks) is dict:
                for network in networks:
                    for ip_address in ['ip-address']:
                        try:
                            # IP address validation
                            if socket.inet_aton(ip_address):
                                all_ip_addresses.append(network['ip-address'])
                        except socket.error:
                            pass
            elif type(networks) is list:
                for network in networks:
                    if 'ip-addresses' in network:
                        for ip_address in network['ip-addresses']:
                            try:
                                # IP address validation
                                if socket.inet_aton(ip_address['ip-address']):
                                    all_ip_addresses.append(ip_address['ip-address'])
                            except socket.error:
                                pass
        if all_ip_addresses:
            system_info.ip_address = check_ip_addresses(proxmox_name, all_ip_addresses)

        return system_info

def check_ip_addresses(vm_name, ip_addresses):
    ludus_range_config = os.environ.get('LUDUS_RANGE_CONFIG')
    range_number = os.environ.get('LUDUS_RANGE_NUMBER')
    range_id = os.environ.get('LUDUS_RANGE_ID')

    valid_ips = []
    config_ip = None
    force_ip = False

    if ludus_range_config and range_number and range_id:
        try:
            with open(ludus_range_config, 'r') as config_file:
                config = yaml.safe_load(config_file)

                for vm in config.get('ludus', []):
                    resolved_vm_name = compiled_range_id_regex.sub(range_id, vm['vm_name'])
                    if resolved_vm_name == vm_name:
                        config_ip = f"10.{range_number}.{vm['vlan']}.{vm['ip_last_octet']}"
                        if 'force_ip' in vm:
                            force_ip = vm['force_ip']
                        break

        except (FileNotFoundError, json.JSONDecodeError, KeyError) as e:
            print(e, file=sys.stderr)
            pass

    for ip_address in ip_addresses:
        # print(config_ip, file=sys.stderr)
        try:
            ip = ipaddress.ip_address(ip_address)
            if ip_address == config_ip:
                return ip_address
            if not ip.is_loopback and not ip.is_link_local and ip not in ipaddress.ip_network('172.16.0.0/12'):
                valid_ips.append(ip_address)
        except ValueError:
            continue

    # Check if any of the valid IPs are in the 192.0.2.0/24 range
    for ip in valid_ips:
        if ipaddress.ip_address(ip) in ipaddress.ip_network('192.0.2.0/24'):
            return ip

    # If we have any valid IPs, return the first one
    if valid_ips:
        return valid_ips[0]

    # If the user has specified force_ip, return the IP from the config if there are no other valid IPs
    if force_ip and config_ip is not None:
        return config_ip

    # If all else fails, return None
    return None


def get_os_info_from_config(vm_name):
    ludus_range_config = os.environ.get('LUDUS_RANGE_CONFIG')
    range_number = os.environ.get('LUDUS_RANGE_NUMBER')
    range_id = os.environ.get('LUDUS_RANGE_ID')

    if ludus_range_config and range_number and range_id:
        try:
            with open(ludus_range_config, 'r') as config_file:
                config = yaml.safe_load(config_file)

                for vm in config.get('ludus', []):
                    resolved_vm_name = compiled_range_id_regex.sub(range_id, vm['vm_name'])
                    if resolved_vm_name == vm_name:
                        if 'windows' in vm:
                            return 'windows'
                        elif 'linux' in vm:
                            return 'linux'
                        elif 'macOS' in vm:
                            return 'macos'

        except (FileNotFoundError, json.JSONDecodeError, KeyError) as e:
            print(e, file=sys.stderr)
            pass
    
    return None

class SystemInfo(object):
    id = ""
    name = ""
    machine = ""
    kernel = ""
    version_id = ""
    ip_address = ""


def main_list(options, config_path):
    results = {
        'all': {
            'hosts': [],
        },
        '_meta': {
            'hostvars': {},
        }
    }

    # Get range filtering settings
    range_id = os.environ.get('LUDUS_RANGE_ID')
    return_all_ranges = os.environ.get('LUDUS_RETURN_ALL_RANGES', '').lower() in ('true', '1', 'yes')

    proxmox_api = ProxmoxAPI(options, config_path)
    proxmox_api.auth()

    # Get valid VMIDs from the matching pool if range_id is set
    valid_vmids = set()
    if range_id and not return_all_ranges:
        try:
            pool = proxmox_api.pool(range_id)
            for member in pool['members']:
                if member['type'] in ('qemu', 'lxc'):
                    valid_vmids.add(str(member['vmid']))
        except HTTPError:
            # If pool doesn't exist, we'll return empty results
            pass

    # Keep track of all groups for filtering later
    all_groups = set()

    for node in proxmox_api.nodes().get_names():
        try:
            qemu_list = proxmox_api.node_qemu(node)
        except HTTPError as error:
            # the API raises code 595 when target node is unavailable, skip it
            if error.code == 595 or error.code == 596:
                continue
            # if it was some other error, reraise it
            raise error
        results['all']['hosts'] += qemu_list.get_names()
        results['_meta']['hostvars'].update(qemu_list.get_variables())
        if proxmox_api.version().get_version() >= 4.0:
            lxc_list = proxmox_api.node_lxc(node)
            results['all']['hosts'] += lxc_list.get_names()
            results['_meta']['hostvars'].update(lxc_list.get_variables())
        else:
            openvz_list = proxmox_api.node_openvz(node)
            results['all']['hosts'] += openvz_list.get_names()
            results['_meta']['hostvars'].update(openvz_list.get_variables())

        # Merge QEMU and Containers lists from this node
        node_hostvars = qemu_list.get_variables().copy()
        if proxmox_api.version().get_version() >= 4.0:
            node_hostvars.update(lxc_list.get_variables())
        else:
            node_hostvars.update(openvz_list.get_variables())

        # Check only VM/containers from the current node
        for vm in node_hostvars:
            vmid = results['_meta']['hostvars'][vm]['proxmox_vmid']
            try:
                type = results['_meta']['hostvars'][vm]['proxmox_type']
            except KeyError:
                type = 'qemu'
                results['_meta']['hostvars'][vm]['proxmox_type'] = 'qemu'
            try:
                description = proxmox_api.vm_description_by_type(node, vmid, type)['description']
            except KeyError:
                description = None

            try:
                metadata = json.loads(description)
            except TypeError:
                metadata = {}
            except ValueError:
                try:
                    metadata = ast.literal_eval(description) # This is SUPER YOLO, but the docs say "Safely evaluate an expression" so...  ¯\_(ツ)_/¯
                except TypeError:
                    metadata = {}
                except ValueError:
                    metadata = {
                        'notes': description
                    }
                except SyntaxError:
                    metadata = {
                        'notes': description
                    }
            
            if type == 'qemu':
                # Retrieve information from QEMU agent if installed
                if proxmox_api.qemu_agent(node, vmid):
                    try:
                        system_info = proxmox_api.qemu_agent_info(node, vmid, results['_meta']['hostvars'][vm]['proxmox_name'])
                    except Exception as e:
                        time.sleep(0.5)
                        system_info = proxmox_api.qemu_agent_info(node, vmid, results['_meta']['hostvars'][vm]['proxmox_name'])
                    results['_meta']['hostvars'][vm]['ansible_host'] = system_info.ip_address
                    # Pull macos from VM name since macOS doesn't have QEMU guest agent support
                    if system_info.id == '' and 'macos' in results['_meta']['hostvars'][vm]['proxmox_name'].lower():
                        results['_meta']['hostvars'][vm]['proxmox_os_id'] = 'macos'
                    else:
                        results['_meta']['hostvars'][vm]['proxmox_os_id'] = system_info.id
                    results['_meta']['hostvars'][vm]['proxmox_os_name'] = system_info.name
                    results['_meta']['hostvars'][vm]['proxmox_os_machine'] = system_info.machine
                    results['_meta']['hostvars'][vm]['proxmox_os_kernel'] = system_info.kernel
                    results['_meta']['hostvars'][vm]['proxmox_os_version_id'] = system_info.version_id
                else:
                    # If we don't have a functional guest agent but the VM is running, use the IP address from the config if the user set force_ip
                    if results['_meta']['hostvars'][vm]['proxmox_status'] == 'running':
                        results['_meta']['hostvars'][vm]['ansible_host'] = check_ip_addresses(results['_meta']['hostvars'][vm]['proxmox_name'], [])
                        # If nothing is returned, delete the ansible_host IP address field
                        if results['_meta']['hostvars'][vm]['ansible_host'] is None:
                            del results['_meta']['hostvars'][vm]['ansible_host']
                        # Also get the proxmox_os_id as it will be used for grouping, and thus group_vars
                        results['_meta']['hostvars'][vm]['proxmox_os_id'] = get_os_info_from_config(results['_meta']['hostvars'][vm]['proxmox_name'])
                        if results['_meta']['hostvars'][vm]['proxmox_os_id'] is None:
                            del results['_meta']['hostvars'][vm]['proxmox_os_id'] 
            else:
                results['_meta']['hostvars'][vm]['ansible_host'] = proxmox_api.openvz_ip_address(node, vmid)
            if 'groups' in metadata:
                for group in metadata['groups']:
                    all_groups.add(group)
                    if group not in results:
                        results[group] = {
                            'hosts': []
                        }
                    results[group]['hosts'] += [vm]

            # Create group 'running'
            # so you can: --limit 'running'
            status = results['_meta']['hostvars'][vm]['proxmox_status']
            if status == 'running':
                all_groups.add('running')
                if 'running' not in results:
                    results['running'] = {
                        'hosts': []
                    }
                results['running']['hosts'] += [vm]

            if 'proxmox_os_id' in results['_meta']['hostvars'][vm]:
                osid = results['_meta']['hostvars'][vm]['proxmox_os_id']
                if osid:
                    all_groups.add(osid)
                    if osid not in results:
                        results[osid] = {
                            'hosts': []
                        }
                    results[osid]['hosts'] += [vm]

            results['_meta']['hostvars'][vm].update(metadata)

    # pools
    for pool in proxmox_api.pools().get_names():
        all_groups.add(pool)
        results[pool] = {
            'hosts': proxmox_api.pool(pool).get_members_name(),
        }

    # Filter based on VMIDs if needed
    if not return_all_ranges and range_id and valid_vmids:
        # First filter the _meta hostvars
        filtered_hostvars = {}
        filtered_all_hosts = []
        
        for host, vars in results['_meta']['hostvars'].items():
            if str(vars.get('proxmox_vmid')) in valid_vmids:
                filtered_hostvars[host] = vars
                filtered_all_hosts.append(host)
        
        # Update the _meta and all sections
        results['_meta']['hostvars'] = filtered_hostvars
        results['all']['hosts'] = filtered_all_hosts
        
        # Now filter all groups to only include valid hosts
        filtered_results = {
            'all': results['all'],
            '_meta': results['_meta']
        }
        
        # Filter each group to only include valid hosts
        for group in results:
            if group not in ('all', '_meta'):
                valid_hosts = [host for host in results[group]['hosts'] if host in filtered_all_hosts]
                if valid_hosts:  # Only keep groups that have valid hosts
                    filtered_results[group] = {'hosts': valid_hosts}
        
        results = filtered_results

    return results


def main_host(options, config_path):
    proxmox_api = ProxmoxAPI(options, config_path)
    proxmox_api.auth()

    for node in proxmox_api.nodes().get_names():
        qemu_list = proxmox_api.node_qemu(node)
        qemu = qemu_list.get_by_name(options.host)
        if qemu:
            return qemu.get_variables()

    return {}


def main():
    config_path = os.path.join(
        os.path.dirname(os.path.abspath(__file__)),
        os.path.splitext(os.path.basename(__file__))[0] + ".json"
    )

    bool_validate_cert = True
    if os.path.isfile(config_path):
        with open(config_path, "r") as config_file:
            config_data = json.load(config_file)
            try:
                bool_validate_cert = config_data["validateCert"]
            except KeyError:
                pass
    if 'PROXMOX_INVALID_CERT' in os.environ:
        bool_validate_cert = False

    parser = OptionParser(usage='%prog [options] --list | --host HOSTNAME')
    parser.add_option('--list', action="store_true", default=False, dest="list")
    parser.add_option('--host', dest="host")
    parser.add_option('--url', default=os.environ.get('PROXMOX_URL'), dest='url')
    parser.add_option('--username', default=os.environ.get('PROXMOX_USERNAME'), dest='username')
    parser.add_option('--password', default=os.environ.get('PROXMOX_PASSWORD'), dest='password')
    parser.add_option('--pretty', action="store_true", default=False, dest='pretty')
    parser.add_option('--trust-invalid-certs', action="store_false", default=bool_validate_cert, dest='validate')
    (options, args) = parser.parse_args()

    if options.list:
        data = main_list(options, config_path)
    elif options.host:
        data = main_host(options, config_path)
    else:
        parser.print_help()
        sys.exit(1)

    indent = None
    if options.pretty:
        indent = 2

    print((json.dumps(data, indent=indent)))


if __name__ == '__main__':
    main()
