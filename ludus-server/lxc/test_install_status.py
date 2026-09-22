import contextlib
import importlib.machinery
import io
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

status = importlib.machinery.SourceFileLoader(
    'install_status', str(Path(__file__).parent / 'files/ludus-install-status')).load_module()


class StatusTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.install = root / 'install'
        self.install.mkdir()
        self.metadata = root / 'metadata.json'
        for name, value in [('INSTALL', self.install), ('METADATA', self.metadata)]:
            p = patch.object(status, name, value)
            p.start()
            self.addCleanup(p.stop)
        p = patch.object(status.os, 'geteuid', return_value=0)
        p.start()
        self.addCleanup(p.stop)

    def run_status(self, args=None):
        with contextlib.redirect_stdout(io.StringIO()) as output:
            result = status.main(args or [])
        return result, output.getvalue()

    def test_host_dispatch_preserves_exit_and_credentials_flag(self):
        self.metadata.write_text('{"vmid": 901}')
        with patch.object(status.subprocess, 'call', return_value=7) as call:
            self.assertEqual(self.run_status(['--credentials'])[0], 7)
            call.assert_called_once_with(['pct', 'exec', '901', '--',
                                         '/usr/local/bin/ludus-install-status', '--credentials'])

    def test_bad_metadata_does_not_fall_back_to_host(self):
        for value in ['"900; touch /tmp/bad"', 'true', '0']:
            self.metadata.write_text('{"vmid":' + value + '}')
            with self.assertRaises(ValueError):
                self.run_status()

    def test_incomplete_bootstrap(self):
        self.assertEqual(self.run_status()[0], 1)

    def test_migrated_no_marker_and_custom_ports(self):
        (self.install / '.bootstrap-complete').touch()
        with patch.object(status.subprocess, 'call', return_value=0), \
             patch.object(status, 'configured_ports', return_value=[9090, 9091]), \
             patch.object(status, 'request') as request:
            result, output = self.run_status(['--credentials'])
            self.assertEqual(result, 0)
            self.assertIn('retain their existing users', output)
            self.assertEqual(request.call_args_list, [unittest.mock.call(9090, '/api/health'),
                                                      unittest.mock.call(9091, '/api/health')])

    def test_credentials_are_opt_in_and_never_rotate(self):
        (self.install / '.bootstrap-complete').touch()
        (self.install / 'initial-admin-userid').write_text('ADMIN')
        (self.install / 'root-api-key').write_text('secret')
        with patch.object(status.subprocess, 'call', return_value=0), \
             patch.object(status, 'configured_ports', return_value=[8080, 8081]), \
             patch.object(status, 'request', return_value='credentials') as request:
            self.run_status()
            self.assertEqual(request.call_count, 2)
            self.run_status(['--credentials'])
            request.assert_called_with(8081, '/api/v2/user/credentials?userID=ADMIN', 'secret')
            self.assertFalse(any('apikey' in str(call) for call in request.call_args_list))

    def test_inactive_services_do_not_query_api(self):
        (self.install / '.bootstrap-complete').touch()
        with patch.object(status.subprocess, 'call', return_value=3), \
             patch.object(status, 'request') as request:
            self.assertEqual(self.run_status()[0], 1)
            request.assert_not_called()

    def test_one_active_service_is_not_healthy(self):
        (self.install / '.bootstrap-complete').touch()
        with patch.object(status.subprocess, 'call', side_effect=[0, 3]) as call, \
             patch.object(status, 'request') as request:
            self.assertEqual(self.run_status()[0], 1)
            self.assertEqual(call.call_args_list, [
                unittest.mock.call(['systemctl', 'is-active', '--quiet', 'ludus']),
                unittest.mock.call(['systemctl', 'is-active', '--quiet', 'ludus-admin'])])
            request.assert_not_called()


if __name__ == '__main__':
    unittest.main()
