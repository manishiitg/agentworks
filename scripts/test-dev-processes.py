#!/usr/bin/env python3
"""PLAT-856-439: repeating a launch cleans its owner, leaving other instances alive."""
from pathlib import Path
import os
import subprocess
import tempfile
import time
import unittest

HELPER = Path(__file__).with_name('dev-processes.sh').resolve()


class DevelopmentProcesses(unittest.TestCase):
    def test_repeat_waits_for_cleanup_and_preserves_other_instance(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            launcher = root / 'launcher.sh'
            launcher.write_text('''source "$HELPER"
dev_instance_claim "$0" || exit 1
cleanup() { sleep 0.2; touch "$CLEANED"; dev_instance_release; }
trap cleanup EXIT
trap 'exit 0' TERM INT
touch "$READY"
while :; do sleep 0.1; done
''')
            processes = []

            def launch(name, ready, relative=False):
                state = root / name
                state.mkdir(exist_ok=True)
                env = {**os.environ, 'HELPER': str(HELPER),
                       'DEV_STARTUP_LOG': str(state / 'startup'),
                       'READY': str(root / ready), 'CLEANED': str(root / (ready + '-cleaned'))}
                # The documented `./run_server_with_logging.sh` start runs the launcher by a relative path.
                process = subprocess.Popen(['/bin/bash', './launcher.sh' if relative else str(launcher)], env=env, cwd=root,
                                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
                processes.append(process)
                deadline = time.monotonic() + 10
                while not (root / ready).exists() and time.monotonic() < deadline:
                    if process.poll() is not None:
                        self.fail(process.stderr.read().decode())
                    time.sleep(0.05)
                self.assertTrue((root / ready).exists())
                return process

            try:
                first = launch('normal', 'first', relative=True)
                isolated = launch('isolated', 'isolated')
                second = launch('normal', 'second')
                self.assertEqual(first.wait(timeout=3), 0)
                self.assertTrue((root / 'first-cleaned').exists())
                self.assertIsNone(isolated.poll())
                self.assertEqual(int((root / 'normal/full-dev.pid').read_text().splitlines()[0]), second.pid)
                second.terminate()
                second.wait(timeout=3)
                self.assertFalse((root / 'normal/full-dev.pid').exists())
            finally:
                for process in processes:
                    if process.poll() is None:
                        process.terminate()
                    process.wait(timeout=5)
                    process.stderr.close()

    def test_stale_identity_or_wrong_command_never_signals_unrelated_process(self):
        with tempfile.TemporaryDirectory() as root:
            sleeper = subprocess.Popen(['sleep', '30'])
            try:
                started = subprocess.check_output(['ps', '-p', str(sleeper.pid), '-o', 'lstart='], text=True).rstrip('\n')
                record = Path(root) / 'full-dev.pid'
                env = {**os.environ, 'HELPER': str(HELPER), 'DEV_STARTUP_LOG': root + '/startup'}
                for birth, expected in [(started, 1), ('stale birth time', 0)]:
                    record.write_text(f'{sleeper.pid}\n{birth}\n')
                    result = subprocess.run(['/bin/bash', '-c', 'source "$HELPER"; dev_instance_claim /abs/launcher.sh'],
                                            env=env, capture_output=True, timeout=5)
                    self.assertEqual(result.returncode, expected)
                    self.assertIsNone(sleeper.poll())
            finally:
                sleeper.terminate()
                sleeper.wait(timeout=5)


if __name__ == '__main__':
    unittest.main()
