"""#2803: root-run account repair must not depend on optional host setup."""
import re
import subprocess
import unittest
from pathlib import Path

SCRIPT = (Path(__file__).resolve().parents[2] / 'deploy.sh').read_text()

class TeamAccountSlotsTest(unittest.TestCase):
    def test_repair_uses_existing_operator_path_and_runs_after_activation(self):
        body = re.search(r'(ensure_account_slots\(\) \{.*?\n\})', SCRIPT, re.S).group(1)
        # Exercise the actual function with SSH stubbed: no root or server effects.
        harness = '''set -eu
PRODUCT=example HOST_IP=example.invalid HOST_SETUP_USER=operator SLOTS_ENABLED=true
LOCAL_SCRIPT_DIR="%s/deploy/rootless-linux"
SSH_OPTS=(-o BatchMode=yes)
ssh() { printf 'root-command %%s\\n' "$*"; }
%s
ensure_account_slots test
''' % (Path(__file__).resolve().parents[2], body)
        run = subprocess.run(['bash', '-c', harness], capture_output=True, text=True)
        self.assertEqual(run.returncode, 0, run.stderr)
        self.assertIn('sudo -n env PRODUCT=example', run.stdout)
        self.assertIn('bash -s -- ensure', run.stdout)
        self.assertIn('phase=test outcome=ok', run.stdout)
        fail = subprocess.run(['bash', '-c', harness.replace("printf 'root-command %s\\n' \"$*\"", 'return 1')], capture_output=True, text=True)
        self.assertNotEqual(fail.returncode, 0)
        self.assertIn('outcome=failed', fail.stderr)
        self.assertLess(SCRIPT.index('ensure_account_slots after_activation'), SCRIPT.index('python3 - running'))
        self.assertGreater(SCRIPT.index('ensure_account_slots after_activation'), SCRIPT.index('systemd-run --user --quiet --wait --pipe'))

    def test_timer_repairs_only_missing_canonical_accounts_without_init(self):
        import json
        import tempfile
        source = (Path(__file__).parent / 'provision-slots.sh').read_text()
        body = source[source.index('cmd_repair_missing() {'):source.index('cmd_install_timer() {')]
        with tempfile.TemporaryDirectory() as root:
            docs = Path(root)
            (docs / 'config').mkdir()
            (docs / 'config/users.json').write_text(json.dumps({'users': [
                {'id': 'existing'}, {'id': 'team-new'}, {'id': 'disabled', 'disabled': True}]}))
            table = docs / 'slots.json'
            table.write_text(json.dumps({'slots': {'slot01': 'existing'}}))
            harness = 'set -eu\nDOCS="%s" TABLE="%s"\n%s\ncmd_assign() { echo "assigned=$1"; }\ncmd_repair_missing\n' % (docs, table, body)
            run = subprocess.run(['bash', '-c', harness], capture_output=True, text=True)
            self.assertEqual(run.returncode, 0, run.stderr)
            self.assertIn('assigned=team-new', run.stdout)
            self.assertNotIn('assigned=existing', run.stdout)
            self.assertNotIn('assigned=disabled', run.stdout)
            self.assertIn('event=repair account=team-new outcome=ok', run.stdout)
            failed = subprocess.run(['bash', '-c', harness.replace('echo "assigned=$1"', 'return 1')], capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertIn('outcome=failed', failed.stderr)
            table.write_text(json.dumps({'slots': {'slot01': 'existing', 'slot02': 'team-new'}}))
            again = subprocess.run(['bash', '-c', harness], capture_output=True, text=True)
            self.assertEqual(again.returncode, 0)
            self.assertEqual(again.stdout, '')

if __name__ == '__main__':
    unittest.main()
