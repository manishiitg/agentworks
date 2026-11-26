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

    def test_timer_continues_after_failure_and_suppresses_same_reason(self):
        import json
        import tempfile
        source = (Path(__file__).parent / 'provision-slots.sh').read_text()
        body = source.split("<<'PY_MISSING'\n", 1)[1].split('\nPY_MISSING', 1)[0]
        with tempfile.TemporaryDirectory() as root:
            path = Path(root)
            users, table, state, script = [path / n for n in ('users', 'slots', 'failures', 'assign.sh')]
            users.write_text(json.dumps({'users': [{'id': 'bad'}, {'id': 'good'}, {'id': 'existing'}, {'id': 'disabled', 'disabled': True}]}))
            table.write_text(json.dumps({'slots': {'slot01': 'existing'}}))
            script.write_text('if [ "$2" = bad ]; then echo "no free slot" >&2; exit 1; fi\necho "$2" >> "'+root+'/assigned"\n')
            args = ['python3', '-c', body, str(users), str(table), str(state), str(script)]
            first = subprocess.run(args, capture_output=True, text=True)
            self.assertEqual(first.returncode, 0, first.stderr)
            self.assertIn('account=bad outcome=failed reason=slots_full', first.stdout)
            self.assertIn('account=good outcome=ok', first.stdout)
            self.assertEqual((path / 'assigned').read_text(), 'good\n')
            second = subprocess.run(args, capture_output=True, text=True)
            self.assertEqual(second.returncode, 0, second.stderr)
            self.assertNotIn('account=bad', second.stdout)
            script.write_text('if [ "$2" = bad ]; then exit 2; fi\n')
            third = subprocess.run(args, capture_output=True, text=True)
            self.assertIn('account=bad outcome=failed reason=assignment_failed', third.stdout)

    def test_timer_script_requires_root_and_no_shared_write(self):
        import tempfile
        source = (Path(__file__).parent / 'provision-slots.sh').read_text()
        guard = source[source.index('trusted_timer_script() {'):source.index('cmd_repair_missing() {')]
        with tempfile.NamedTemporaryFile() as script:
            for metadata, expected in [('0 700', 0), ('0 755', 0), ('0 775', 1), ('0 707', 1), ('1000 700', 1)]:
                harness = guard + '\nstat() { echo "'+metadata+'"; }\ntrusted_timer_script "'+script.name+'"'
                result = subprocess.run(['bash', '-c', harness], capture_output=True, text=True)
                self.assertEqual(result.returncode, expected, result.stderr)

if __name__ == '__main__':
    unittest.main()
