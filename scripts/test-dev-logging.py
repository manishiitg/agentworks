#!/usr/bin/env python3
"""PLAT-856-438: quiet startup must retain failure evidence and exit status."""
from pathlib import Path
import os
import subprocess
import tempfile
import unittest


class DevelopmentLogging(unittest.TestCase):
    def test_quiet_setup_preserves_diagnostics_status_and_private_log(self):
        helper = Path(__file__).with_name('dev-logging.sh').resolve()
        with tempfile.TemporaryDirectory() as root:
            script = '''
source "$HELPER"
dev_detail "routine setup detail"
dev_setup bash -c 'echo setup output; echo actionable warning >&2'
dev_setup bash -c 'echo concrete failure detail; exit 7'
'''
            env = {**os.environ, 'HELPER': str(helper), 'SCRIPT_DIR': root,
                   'AGENTWORKS_LOG_DIR': 'logs', 'AGENTWORKS_DEV_VERBOSE': '0'}
            result = subprocess.run(['/bin/bash', '-c', script], env=env,
                                    text=True, capture_output=True, timeout=10)
            self.assertEqual(result.returncode, 7)
            self.assertEqual(result.stdout, '')
            self.assertIn('actionable warning', result.stderr)
            self.assertIn('concrete failure detail', result.stderr)
            logs = list((Path(root) / 'logs').glob('startup-*'))
            self.assertEqual(len(logs), 1)
            self.assertEqual(logs[0].stat().st_mode & 0o777, 0o600)
            self.assertIn('routine setup detail', logs[0].read_text())
            self.assertIn('setup output', logs[0].read_text())

            env['AGENTWORKS_DEV_VERBOSE'] = '1'
            verbose = subprocess.run(['/bin/bash', '-c', script], env=env,
                                     text=True, capture_output=True, timeout=10)
            self.assertEqual(verbose.returncode, 7)
            self.assertIn('routine setup detail', verbose.stdout)
            self.assertIn('setup output', verbose.stdout)
            self.assertIn('concrete failure detail', verbose.stdout)
            self.assertIn('actionable warning', verbose.stderr)


if __name__ == '__main__':
    unittest.main()
