from pathlib import Path
import os
import subprocess
import tempfile
import unittest

INSTALLER = Path(__file__).with_name('install-slack-cli.sh')
REPO = INSTALLER.parents[2]

class SlackInstallerTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.mocks = self.root / 'mocks'
        self.mocks.mkdir()
        self.prefix = self.root / 'tools'
        (self.prefix / 'bin').mkdir(parents=True)
        self.env = dict(os.environ, PATH=str(self.mocks) + ':' + os.environ['PATH'])
        self.write_executable(self.mocks / 'uname', '#!/bin/sh\nif [ "$1" = -s ]; then echo Linux; else echo x86_64; fi\n')

    def write_executable(self, path, text):
        path.write_text(text)
        path.chmod(0o755)

    def run_installer(self, prefix=None):
        return subprocess.run(['bash', str(INSTALLER), str(prefix or self.prefix)], env=self.env, capture_output=True, text=True)

    def test_matching_version_does_not_download_or_change_binary(self):
        binary = self.prefix / 'bin/slack'
        script = '#!/bin/sh\necho "Using slack v4.8.0"\n'
        self.write_executable(binary, script)
        self.write_executable(self.mocks / 'curl', '#!/bin/sh\nexit 97\n')
        result = self.run_installer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(binary.read_text(), script)

    def test_bad_checksum_preserves_existing_install(self):
        binary = self.prefix / 'bin/slack'
        script = '#!/bin/sh\necho "Using slack v0.0.1"\n'
        self.write_executable(binary, script)
        archive = self.root / 'tampered-archive'
        archive.write_bytes(b'untrusted replacement binary')
        self.env['TEST_ARCHIVE'] = str(archive)
        self.write_executable(self.mocks / 'curl', '#!/bin/sh\nfor arg in "$@"; do destination="$arg"; done\ncp "$TEST_ARCHIVE" "$destination"\n')
        result = self.run_installer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('checksum', result.stderr)
        self.assertEqual(binary.read_text(), script)

    def test_relative_prefix_rejected(self):
        self.assertNotEqual(self.run_installer('relative').returncode, 0)

    def test_server_deploys_and_images_use_shared_pinned_installer(self):
        for filename in ['deploy.sh',
                         'deploy/aws-ec2/server/build-and-activate.sh', 'deploy/aws-ec2/server/repair-bootstrap.sh',
                         'agent_go/Dockerfile']:
            self.assertIn('install-slack-cli.sh', (REPO / filename).read_text(), filename)

if __name__ == '__main__':
    unittest.main()
