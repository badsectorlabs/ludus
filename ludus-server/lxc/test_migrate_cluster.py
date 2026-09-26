#!/usr/bin/env python3
import base64
import importlib.util
import json
import pathlib
import subprocess
import tarfile
import tempfile
import unittest
from unittest import mock

HERE = pathlib.Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('migrate_cluster', HERE/'migrate-cluster.py')
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class MigrationBoundaries(unittest.TestCase):
    def test_custom_database_is_final_archive_source(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory).resolve()
            source = root/'custom database'
            source.mkdir()
            (source/'ludus.db').write_bytes(b'latest durable state')
            wg = root/'wireguard'
            wg.mkdir()
            (wg/'wg0.conf').write_text('private-key-preserved')
            info = {'data_directory': str(source)}
            for name in ('info.json', 'cutover-info.json'):
                (root/name).write_text(json.dumps(info))
            migration.final_state(root, wg, root/'absent-leases')
            with tarfile.open(root/'final-state.tar.gz') as archive:
                self.assertEqual(archive.extractfile('opt/ludus/db/ludus.db').read(), b'latest durable state')
                self.assertEqual(archive.extractfile('etc/wireguard/wg0.conf').read(), b'private-key-preserved')
                self.assertFalse(any(name.startswith(str(source).lstrip('/')) for name in archive.getnames()))
            (root/'final-state.tar.gz').unlink()
            changed = root/'different-db'
            changed.mkdir()
            (root/'cutover-info.json').write_text(json.dumps({'data_directory': str(changed)}))
            with self.assertRaisesRegex(RuntimeError, 'Original data directory changed'):
                migration.final_state(root, wg, root/'absent-leases')
            self.assertFalse((root/'final-state.tar.gz').exists())

    def test_cluster_requires_all_nodes_and_quorum(self):
        nodes = [{'type': 'node', 'name': 'a', 'online': 1}, {'type': 'node', 'name': 'b', 'online': 1}]
        with mock.patch.object(migration, 'api', return_value=nodes+[{'type': 'cluster', 'quorate': 0}]):
            with self.assertRaisesRegex(RuntimeError, 'quorum'):
                migration.cluster_nodes()
        nodes[1]['online'] = 0
        with mock.patch.object(migration, 'api', return_value=nodes+[{'type': 'cluster', 'quorate': 1}]):
            with self.assertRaisesRegex(RuntimeError, 'All cluster nodes'):
                migration.cluster_nodes()

    def test_cutover_removes_only_legacy_gateway_and_range_routes(self):
        text = ('auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.5/24\n'
                'iface ludusnat\n\taddress 192.0.2.254\n\tnetmask 255.255.255.0\n'
                '\tpost-up ip route add 10.7.0.0/16 via 192.0.2.107 dev ludusnat\n'
                '\tpost-up ip route add 10.70.0.0/16 via 192.0.2.170 dev ludusnat\n')
        result = migration.remove_legacy_hooks(text, 'ludusnat', [{'number': 7}])
        self.assertNotIn('192.0.2.254', result)
        self.assertNotIn('10.7.0.0/16', result)
        self.assertIn('address 203.0.113.5/24', result)
        self.assertIn('ip route add 10.70.0.0/16 via 192.0.2.170', result)

    def test_tap_verification_rejects_wrong_firewall_uplink(self):
        real_path = type(pathlib.Path())
        with tempfile.TemporaryDirectory() as directory:
            root = real_path(directory)
            for name in ('tap901i0', 'fwbr901i0', 'fwpr901p0', 'r7', 'r8'):
                (root/name).mkdir()
            (root/'tap901i0/master').symlink_to(root/'fwbr901i0')
            (root/'fwpr901p0/master').symlink_to(root/'r8')
            def path(*parts):
                if parts and str(parts[0]) == '/sys/class/net':
                    return real_path(root, *parts[1:])
                return real_path(*parts)
            with mock.patch.object(migration.pathlib, 'Path', side_effect=path), mock.patch.object(migration, 'run', return_value='status: running\n'):
                with self.assertRaisesRegex(RuntimeError, 'expected r7'):
                    migration.verify_tap(901, 'net0', 'virtio=00:11:22:33:44:55,bridge=r7,firewall=1')
                (root/'fwpr901p0/master').unlink()
                (root/'fwpr901p0/master').symlink_to(root/'r7')
                migration.verify_tap(901, 'net0', 'virtio=00:11:22:33:44:55,bridge=r7,firewall=1')
                (root/'tap901i0/master').unlink()
                with self.assertRaisesRegex(RuntimeError, 'tap is detached'):
                    migration.verify_tap(901, 'net0', 'virtio=00:11:22:33:44:55,bridge=r7,firewall=1')

    def test_node_restore_reports_service_failure_after_restoring_files(self):
        with tempfile.TemporaryDirectory() as directory:
            network = pathlib.Path(directory)
            (network/'interfaces.d').mkdir()
            (network/migration.FRAGMENT).write_text('migration gateway')
            original = b'auto vmbr0\niface vmbr0 inet static\n\taddress 203.0.113.9/24\n'
            saved = {'files': {'interfaces': {'data': base64.b64encode(original).decode(), 'mode': 0o640}},
                     'addresses': [], 'routes': [], 'ip_forward': '1', 'iptables': '*filter\nCOMMIT\n',
                     'services': {'ludus': {'active': True, 'enablement': 'enabled'}}}
            def command(args, **kwargs):
                return subprocess.CompletedProcess(args, 1 if tuple(args) == ('systemctl', 'start', 'ludus') else 0)
            with mock.patch.object(migration, 'NETWORK', network), mock.patch.object(migration, 'run', return_value='[]'), mock.patch.object(migration.subprocess, 'run', side_effect=command):
                with self.assertRaisesRegex(RuntimeError, 'systemctl start ludus'):
                    migration.node_action('restore', {'saved': saved, 'ranges': [], 'gateway': '192.0.2.49'})
            self.assertEqual((network/'interfaces').read_bytes(), original)
            self.assertEqual((network/'interfaces').stat().st_mode & 0o777, 0o640)
            self.assertFalse((network/migration.FRAGMENT).exists())

    def test_owned_candidate_must_stop_and_disable_before_host_restores(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            (root/'network.json').write_text('{}')
            (root/'vmid').write_text('2010\n')
            (root/'container-created').touch()
            script = '''
source "$1"
MIGRATION_DIR=$2
migration_api_bridge() { return 0; }
pct() {
  case "$1" in
    status) echo "status: ${CANDIDATE_STATE}" ;;
    stop) return 7 ;;
    set) return 8 ;;
  esac
}
python3() { echo UNSAFE_HOST_RESTORE; }
migration_reset_wireguard_sessions() { return 0; }
migration_rollback
'''
            for state, error in (('running', 'Cannot stop candidate'), ('stopped', 'Cannot disable candidate')):
                with self.subTest(state=state):
                    result = subprocess.run(['bash', '-c', 'CANDIDATE_STATE='+state+'\n'+script, 'test', str(HERE/'migrate-host.sh'), str(root)], capture_output=True, text=True)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn(error, result.stderr)
                    self.assertNotIn('UNSAFE_HOST_RESTORE', result.stdout)


if __name__ == '__main__':
    unittest.main()
