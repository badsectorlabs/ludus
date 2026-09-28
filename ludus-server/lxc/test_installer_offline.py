#!/usr/bin/env python3
"""Offline media integrity and entry-point isolation; never touches a real PVE host."""
import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
OFFLINE = ROOT / 'install-offline.sh'
BASH = shutil.which('bash')


@unittest.skipUnless(BASH and shutil.which('openssl'), 'requires bash and openssl')
class OfflineInstaller(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.keys = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.keys.cleanup)
        cls.private_key = Path(cls.keys.name) / 'signing.pem'
        cls.public_key = Path(cls.keys.name) / 'release.pem'
        subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt',
                        'rsa_keygen_bits:2048', '-out', str(cls.private_key)],
                       check=True, capture_output=True)
        subprocess.run(['openssl', 'pkey', '-in', str(cls.private_key), '-pubout',
                        '-out', str(cls.public_key)], check=True, capture_output=True)

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.tools = self.root / 'tools'
        self.tools.mkdir()
        self.io_log = self.root / 'io.log'
        # Fail and record any unexpected downloads, escalation, or PVE changes.
        for name in ('curl', 'wget', 'sudo', 'apt-get', 'pveversion', 'pvesh', 'pvesm', 'pveum', 'pct'):
            tool = self.tools / name
            tool.write_text('#!/bin/sh\nprintf "%s\\n" "${0##*/}" >> "$IO_LOG"\nexit 97\n')
            tool.chmod(0o755)
        for name in ('dirname', 'grep'):
            (self.tools / name).symlink_to(shutil.which(name))
        self.env = {key: value for key, value in os.environ.items()
                    if not key.startswith('LUDUS_') and key not in (
                        'TEMPLATE_FILE', 'ISO_DIRECTORY', 'ISO_STORAGE', 'LICENSE_FILE', 'LICENSE',
                        'ENTERPRISE_PLUGIN', 'IMPORT_DB', 'CA_CERTIFICATE', 'CHECKSUM_FILE',
                        'CHECKSUM_SIGNATURE', 'CHECKSUM_PUBLIC_KEY', 'SKIP_VERIFICATION',
                        'SERVER_ONLY', 'MIGRATE_HOST', 'PREFIX')}
        self.env.update(PATH=str(self.tools) + os.pathsep + os.environ['PATH'],
                        IO_LOG=str(self.io_log), HOME=str(self.root), TMPDIR=str(self.root),
                        SHELL='/bin/sh', NO_PROMPT='1')
        names = self.shell('printf "%s\\0" "${AIRGAPPED_ISO_FILENAMES[@]}"')
        self.assertEqual(names.returncode, 0, names.stderr)
        self.isos = [self.root / name for name in names.stdout.split('\0') if name]
        self.image = self.root / 'ludus-2.4.0-debian13-amd64.tar.zst'
        self.plugin = self.root / 'ludus-enterprise.plugin'
        self.license = self.root / 'license.lic'
        self.state = self.root / 'state.tar.gz'
        self.artifacts = [self.image, self.plugin, self.license, self.state, *self.isos]
        for artifact in self.artifacts:
            artifact.write_bytes(('release artifact: ' + artifact.name + '\n').encode())
        self.manifest = self.root / 'checksums.txt'
        self.signature = self.root / 'checksums.sig'
        self.manifest.write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n'
                                         for p in self.artifacts))
        self.sign_manifest()

    def sign_manifest(self):
        subprocess.run(['openssl', 'dgst', '-sha256', '-sign', str(self.private_key),
                        '-out', str(self.signature), str(self.manifest)], check=True, capture_output=True)

    def media_env(self):
        return dict(self.env, TEMPLATE_FILE=str(self.image), ISO_DIRECTORY=str(self.root),
                    ISO_STORAGE='shared-isos', ENTERPRISE_PLUGIN=str(self.plugin),
                    LICENSE_FILE=str(self.license), LICENSE='fixture-license-key',
                    IMPORT_DB=str(self.state), CHECKSUM_FILE=str(self.manifest),
                    CHECKSUM_SIGNATURE=str(self.signature), CHECKSUM_PUBLIC_KEY=str(self.public_key))

    def shell(self, command, env=None):
        return subprocess.run([BASH, '-c', 'source "$1"; ' + command, 'offline-test', str(OFFLINE)],
                              env=env or self.env, capture_output=True, text=True, timeout=30)

    def cli(self, script, *args):
        return subprocess.run([BASH, str(script), *args], env=self.env,
                              capture_output=True, text=True, timeout=30)

    def assert_no_io(self):
        self.assertFalse(self.io_log.exists(), self.io_log.read_text() if self.io_log.exists() else '')

    def test_public_offline_options_are_rejected_before_io(self):
        for flag in ('--airgapped', '--iso-directory', '--license-file'):
            with self.subTest(flag=flag):
                result = self.cli(ROOT / 'install.sh', flag)
                self.assertNotEqual(result.returncode, 0)
                self.assert_no_io()
        standalone = self.root / 'install-beta.sh'
        shutil.copyfile(ROOT / 'install-beta.sh', standalone)
        result = self.cli(standalone, '--iso-directory', str(self.root))
        self.assertNotEqual(result.returncode, 0)
        self.assert_no_io()

    def test_missing_or_incompatible_shared_installer_is_not_executed(self):
        wrapper = self.root / 'install-offline.sh'
        shutil.copyfile(OFFLINE, wrapper)
        self.assertNotEqual(self.cli(wrapper, '--help').returncode, 0)
        marker = self.root / 'executed-old-installer'
        (self.root / 'install.sh').write_text(f'#!/bin/bash\ntouch "{marker}"\n')
        self.assertNotEqual(self.cli(wrapper, '--help').returncode, 0)
        self.assertFalse(marker.exists())
        self.assert_no_io()

    def test_missing_media_or_version_cannot_fall_back_to_discovery(self):
        for args in ((), ('--template-file', str(self.root / 'custom.tar.zst')),
                     ('--template-file', str(self.root / 'ludus--debian13-amd64.tar.zst'))):
            with self.subTest(args=args):
                self.assertNotEqual(self.cli(OFFLINE, *args).returncode, 0)
                self.assert_no_io()

    def test_signed_media_rejects_corrupted_image_iso_license_and_state(self):
        env = self.media_env()
        result = self.shell('offline_verify_inputs', env)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        for artifact in (self.image, self.isos[0], self.license, self.state):
            with self.subTest(artifact=artifact.name):
                original = artifact.read_bytes()
                artifact.write_bytes(b'corrupted release artifact\n')
                self.assertNotEqual(self.shell('prepare_install_inputs', env).returncode, 0)
                self.assert_no_io()
                artifact.write_bytes(original)

    def test_invalid_signature_and_ambiguous_manifest_entries_are_rejected(self):
        original = self.manifest.read_text()
        self.manifest.write_text(original + '\n')
        self.assertNotEqual(self.shell('prepare_install_inputs', self.media_env()).returncode, 0)
        self.assert_no_io()
        self.manifest.write_text(original + original.splitlines()[0] + '\n')
        self.sign_manifest()
        self.assertNotEqual(self.shell('prepare_install_inputs', self.media_env()).returncode, 0)
        self.assert_no_io()

    def test_required_offline_inputs_fail_before_storage_mutation(self):
        for key in ('TEMPLATE_FILE', 'ISO_STORAGE', 'CHECKSUM_FILE', 'CHECKSUM_SIGNATURE',
                    'CHECKSUM_PUBLIC_KEY', 'ENTERPRISE_PLUGIN', 'LICENSE'):
            with self.subTest(missing=key):
                env = self.media_env()
                env.pop(key)
                self.assertNotEqual(self.shell('prepare_install_inputs', env).returncode, 0)
                self.assert_no_io()
        self.isos[-1].unlink()
        self.assertNotEqual(self.shell('prepare_install_inputs', self.media_env()).returncode, 0)
        self.assert_no_io()

    def test_explicit_development_bypass_still_requires_all_media(self):
        env = self.media_env()
        env.update(SKIP_VERIFICATION='1')
        for key in ('CHECKSUM_FILE', 'CHECKSUM_SIGNATURE', 'CHECKSUM_PUBLIC_KEY'):
            env.pop(key)
        self.image.write_bytes(b'unsigned development image\n')
        self.assertEqual(self.shell('offline_verify_inputs', env).returncode, 0)
        self.isos[0].unlink()
        self.assertNotEqual(self.shell('prepare_install_inputs', env).returncode, 0)
        self.assert_no_io()

    def test_missing_migration_dependency_is_never_downloaded(self):
        env = dict(self.env, PATH=str(self.tools))
        self.assertNotEqual(self.shell('prepare_migration_dependencies', env).returncode, 0)
        self.assert_no_io()


if __name__ == '__main__':
    unittest.main()
