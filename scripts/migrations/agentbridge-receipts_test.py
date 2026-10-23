"""Exercise the real stopped-writer converter, including its backup."""
import copy
import json
import pathlib
import subprocess
import sys
import tempfile
import unittest


class MailboxPositionConversion(unittest.TestCase):
    def test_zero_positions_preserve_history_and_backup(self):
        original = {
            'next_agent_sequence': 40,
            'conversations': [{
                'inbox_id': 'inbox-fixture',
                'endpoints': [{'named_agent_id': 'agent-fixture'}, {}],
                'messages': [
                    {'message_id': 'old-stored', 'from': 1, 'message': 'keep pending', 'delivery': 'stored'},
                    {'message_id': 'old-delivered', 'from': 1, 'message': 'keep history',
                     'delivery': 'delivered', 'delivered_at': '2026-10-01T00:00:00Z', 'agent_sequence': 0},
                    {'message_id': 'current', 'from': 1, 'message': 'keep position', 'delivery': 'stored',
                     'agent_sequence': 45},
                    {'message_id': 'internal', 'from': 0, 'message': 'internal conversation', 'delivery': 'stored'},
                ],
            }],
        }
        script = pathlib.Path(__file__).with_name('agentbridge-receipts.py')
        with tempfile.TemporaryDirectory() as folder:
            path = pathlib.Path(folder) / 'conversations.json'
            saved = json.dumps(original).encode()
            path.write_bytes(saved)
            run = subprocess.run([sys.executable, str(script), str(path)], check=True,
                                 capture_output=True, text=True)
            result = json.loads(run.stdout)
            self.assertEqual(result['assigned_mailbox_positions'], 2)
            self.assertEqual(pathlib.Path(result['backup']).read_bytes(), saved)
            converted = json.loads(path.read_text())
            expected = copy.deepcopy(original)
            expected['next_agent_sequence'] = 47
            expected['conversations'][0]['messages'][0]['agent_sequence'] = 46
            expected['conversations'][0]['messages'][1]['agent_sequence'] = 47
            self.assertEqual(converted, expected)
            first = path.read_bytes()
            rerun = subprocess.run([sys.executable, str(script), str(path)], check=True,
                                   capture_output=True, text=True)
            self.assertIn('already current; no change', rerun.stdout)
            self.assertEqual(path.read_bytes(), first)
            self.assertEqual(len(list(path.parent.glob('*.before-receipts-*'))), 1)


if __name__ == '__main__':
    unittest.main()
