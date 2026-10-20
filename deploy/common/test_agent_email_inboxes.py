#!/usr/bin/env python3
"""The release retires personal intake with backups, keeping Google connections intact."""
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
class InboxMigration(unittest.TestCase):
    def test_retirement_and_repeat(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            dbfile = base / 'state' / 'gmail-inbound' / 'email.db'
            dbfile.parent.mkdir(parents=True)
            with sqlite3.connect(dbfile) as db:
                db.execute('CREATE TABLE inboxes(id TEXT)')
                db.execute("INSERT INTO inboxes VALUES('old')")
                db.execute('CREATE TABLE sender_consents(id TEXT)')
            workspace = base / 'docs'
            workflow = workspace / 'Workflow' / 'example' / 'workflow.json'
            workflow.parent.mkdir(parents=True)
            workflow.write_text(json.dumps({'id': 'agent', 'schedules': [{'id': 'old', 'kind': 'gmail'}, {'id': 'normal', 'kind': 'internal'}]}))
            credentials = workspace / 'config' / 'gmail-config.json'
            credentials.parent.mkdir()
            credentials.write_text('{"connections":[{"id":"personal"}]}')
            command = ['python3', str(ROOT / 'scripts/migrations/agent-email-inboxes.py'), str(dbfile), '--workspace-root', str(workspace)]
            subprocess.run(command, check=True)
            self.assertEqual([item['id'] for item in json.loads(workflow.read_text())['schedules']], ['normal'])
            self.assertEqual(credentials.read_text(), '{"connections":[{"id":"personal"}]}')
            backups = list(dbfile.parent.glob('email.db.before-agent-inboxes-*'))
            self.assertEqual(len(backups), 1)
            with sqlite3.connect(backups[0]) as backup:
                self.assertEqual(backup.execute('SELECT id FROM inboxes').fetchone(), ('old',))
            subprocess.run(command, check=True)
            self.assertEqual(len(list(dbfile.parent.glob('email.db.before-agent-inboxes-*'))), 1)
            for file in ['deploy/rootless-linux/build-and-activate.sh', 'deploy/aws-ec2/server/build-and-activate.sh']:
                source = (ROOT / file).read_text()
                self.assertIn('agent-email-inboxes.py', source)
                self.assertNotIn('gmail-personal-inbox.py', source)
if __name__ == '__main__':
    unittest.main()
