#!/usr/bin/env python3
"""Exercise release selection and integrity using real HTTP and client installs."""
import hashlib
import http.server
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile
import threading
import unittest
from urllib.parse import urlsplit, parse_qs

ROOT = Path(__file__).resolve().parents[2]


class InstallerChannels(unittest.TestCase):
    def setUp(self):
        # These tests install clients only; never offer server changes on a PVE host.
        if shutil.which('pveversion'):
            self.skipTest('client fixtures must not run on a Proxmox host')
        if platform.system() not in ('Darwin', 'Linux') or not shutil.which('curl'):
            self.skipTest('requires a supported client OS and curl')
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.prefix = self.root / 'bin'
        self.prefix.mkdir()
        self.routes = {}
        routes = self.routes

        class Handler(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                url = urlsplit(self.path)
                key = url.path
                if key.endswith('/repository/tags'):
                    key += '?page=' + parse_qs(url.query)['page'][0]
                body = routes.get(key)
                if body is None:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, *_):
                pass

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.addCleanup(self.stop_server)
        self.base = f'http://127.0.0.1:{self.server.server_port}'
        self.env = dict(os.environ, R2_BUCKET_BASE_URL=self.base, SHELL='/bin/sh',
                        HOME=str(self.root), TMPDIR=str(self.root),
                        LUDUS_RELEASE_CHANNEL='stable')
        for name in ('LUDUS_VERSION', 'PREFIX'):
            self.env.pop(name, None)
        self.os_name = 'macOS' if platform.system() == 'Darwin' else 'linux'
        self.arch = {'x86_64': 'amd64', 'aarch64': 'arm64', 'arm64': 'arm64'}.get(platform.machine())
        if not self.arch:
            self.skipTest('unsupported fixture architecture')

    def stop_server(self):
        self.server.shutdown()
        self.thread.join()
        self.server.server_close()

    def release(self, tag, beta=True):
        name = f'ludus-client_{self.os_name}-{self.arch}'
        payload = f'#!/bin/sh\nprintf "%s\\n" "{tag}"\n'.encode()
        base = f'/{tag}' if beta else f'/api/v4/projects/54052321/packages/generic/ludus/{tag}'
        self.routes[base + '/' + name + ('' if beta else '-' + tag)] = payload
        self.routes[base + f'/ludus_{tag}_checksums.txt'] = (
            hashlib.sha256(payload).hexdigest() + '  ' + name + '\n').encode()
        self.routes[f'/{tag}/install.sh'] = (ROOT / 'install.sh').read_bytes()
        return payload

    def run_installer(self, script, *args):
        return subprocess.run(['bash', str(script), '-p', str(self.prefix), '--no-prompt', *args],
                              env=self.env, capture_output=True, text=True, timeout=30)

    def test_standalone_beta_explicit_version_does_not_require_latest(self):
        tag = '2.4.0-beta.7'
        payload = self.release(tag)
        standalone = self.root / 'install-beta.sh'
        shutil.copyfile(ROOT / 'install-beta.sh', standalone)
        result = self.run_installer(standalone, '--version', tag)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.prefix / 'ludus').read_bytes(), payload)

    def test_beta_latest_and_bad_checksum_preserve_previous_client(self):
        tag = '2.4.0-beta.8'
        payload = self.release(tag)
        self.routes['/latest.txt'] = (tag + '\n').encode()
        result = self.run_installer(ROOT / 'install-beta.sh')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.prefix / 'ludus').read_bytes(), payload)
        self.routes[f'/{tag}/ludus-client_{self.os_name}-{self.arch}'] = b'corrupt download\n'
        result = self.run_installer(ROOT / 'install-beta.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.prefix / 'ludus').read_bytes(), payload)

    def test_stable_discovery_continues_after_beta_only_page(self):
        self.routes['/api/v4/projects/54052321/repository/tags?page=1'] = json.dumps(
            [{'name': f'2.4.0-beta.{index}'} for index in range(100, 0, -1)]).encode()
        self.routes['/api/v4/projects/54052321/repository/tags?page=2'] = json.dumps(
            [{'name': '2.4.0-beta.0'}, {'name': '2.3.9'}, {'name': '2.3.8'}]).encode()
        payload = self.release('2.3.9', beta=False)
        # Redirect only the fixed production origin; transfer/checksum/install are real.
        tools = self.root / 'tools'
        tools.mkdir()
        curl = tools / 'curl'
        curl.write_text('#!/usr/bin/env python3\nimport os,sys\n'
                        f'args=[v.replace("https://gitlab.com", {self.base!r}) for v in sys.argv[1:]]\n'
                        f'os.execv({shutil.which("curl")!r}, ["curl", *args])\n')
        curl.chmod(0o755)
        self.env['PATH'] = str(tools) + os.pathsep + os.environ['PATH']
        result = self.run_installer(ROOT / 'install.sh')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.prefix / 'ludus').read_bytes(), payload)
        # A pin must override discovery for the client as well as the server.
        self.routes.clear()
        pinned = self.release('2.3.8', beta=False)
        result = self.run_installer(ROOT / 'install.sh', '--version', '2.3.8')
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual((self.prefix / 'ludus').read_bytes(), pinned)

    def test_failed_beta_discovery_never_installs_client(self):
        result = self.run_installer(ROOT / 'install-beta.sh')
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.prefix / 'ludus').exists())


if __name__ == '__main__':
    unittest.main()
