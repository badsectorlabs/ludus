"""Run as root on Linux; uses a fake builder, not a network/DAB build."""
import os
from pathlib import Path
import pwd
import shutil
import subprocess
import tempfile
import unittest


@unittest.skipUnless(os.geteuid() == 0, 'requires root to reproduce CI ownership')
class IsolatedBuildTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='ludus-build-test-')
        self.addCleanup(self.tmp.cleanup)
        self.base = Path(self.tmp.name)
        self.base.chmod(0o755)
        self.repo = self.base / 'repo'
        self.lxc = self.repo / 'ludus-server/lxc'
        self.lxc.mkdir(parents=True)
        ci = self.repo / 'ludus-server/ci'
        ci.mkdir()
        self.wrapper = ci / 'build-lxc-isolated.sh'
        shutil.copyfile(Path(__file__).with_name('build-lxc-isolated.sh'), self.wrapper)
        for directory in ['files', '../ansible', '../packer', '../../binaries']:
            (self.lxc / directory).mkdir()
        for name in ['Makefile', 'dab.conf', 'migrate-host.sh', 'python-requirements.txt', '../../binaries/ludus-server']:
            (self.lxc / name).write_text('fixture\n')
        (self.lxc / 'build.sh').write_text('''#!/bin/bash
set -eu
cd "$(dirname "$0")"
pwd > "$AUDIT"
mkdir -p rootfs/private deps "$DAB_CACHE_DIR"
chmod 000 rootfs/private
if [ "$MODE" != success ]; then exit 23; fi
artifact="../../ludus-${LUDUS_VERSION}-debian13-amd64.tar.zst"
printf fixture > "$artifact"
sha256sum "$artifact" > "$artifact.sha256"
''')
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        (self.bin / 'dab').write_text('#!/bin/bash\nexit 1\n')
        (self.bin / 'dab').chmod(0o755)
        self.account = pwd.getpwnam('gitlab-runner')
        for path in [self.repo, *self.repo.rglob('*')]:
            os.chown(path, self.account.pw_uid, self.account.pw_gid)
        self.env = dict(os.environ, LUDUS_VERSION='test', AUDIT=str(self.base / 'audit'),
                        PATH=str(self.bin)+':'+os.environ['PATH'])

    def execute(self, mode):
        result = subprocess.run(['bash', str(self.wrapper)], env=dict(self.env, MODE=mode),
                                capture_output=True, text=True)
        self.work = Path((self.base / 'audit').read_text().strip()).parents[1]
        return result

    def test_success_artifacts_are_runner_owned_and_cleanable(self):
        result = self.execute('success')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.work.exists())
        self.assertFalse((self.lxc / 'rootfs').exists())
        for artifact in self.repo.glob('*.tar.zst*'):
            self.assertEqual(artifact.stat().st_uid, self.account.pw_uid)
        subprocess.run(['runuser', '-u', 'gitlab-runner', '--', 'git', '-C', str(self.repo), 'init'], check=True, capture_output=True)
        # Scope actual git clean to generated artifacts in this isolated fixture.
        subprocess.run(['runuser', '-u', 'gitlab-runner', '--', 'git', '-C', str(self.repo),
                        'clean', '-ffdx', '--', 'ludus-test-debian13-amd64.tar.zst',
                        'ludus-test-debian13-amd64.tar.zst.sha256'], check=True, capture_output=True)
        self.assertFalse(list(self.repo.glob('*.tar.zst*')))

    def test_failed_build_preserves_exit_and_cleans_private_files(self):
        self.assertEqual(self.execute('failure').returncode, 23)
        self.assertFalse(self.work.exists())
        self.assertFalse(list(self.repo.glob('*.tar.zst*')))

    def test_remaining_mount_prevents_recursive_cleanup(self):
        (self.bin / 'findmnt').write_text('#!/bin/bash\nprintf "%s/rootfs/proc\\n" "$(cat "$AUDIT")"\n')
        (self.bin / 'findmnt').chmod(0o755)
        try:
            result = self.execute('failure')
            self.assertEqual(result.returncode, 23)
            self.assertIn('Mount remains; retaining', result.stderr)
            self.assertTrue(self.work.exists())
        finally:
            # findmnt was mocked: this fixture has no real mounts.
            if hasattr(self, 'work') and self.work.exists():
                shutil.rmtree(self.work)


if __name__ == '__main__':
    unittest.main()
