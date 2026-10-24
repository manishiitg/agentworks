"""Retiring relay state must preserve normal OAuth and native inboxes."""
import json
import pathlib
import sqlite3
import subprocess
import sys
import tempfile
import unittest


class RelayRetirement(unittest.TestCase):
    def test_real_cleanup_backup_and_second_run(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            (root / 'auth').mkdir()
            path = root / 'auth/fixture.mcp-oauth.sqlite'
            with sqlite3.connect(path) as db:
                for table, column in [('clients', 'id'), ('requests', 'client_id'), ('codes', 'client_id')]:
                    db.execute(f'CREATE TABLE {table} ({column} TEXT)')
                    db.executemany(f'INSERT INTO {table} VALUES (?)', [('agent_link_fixture',), ('agentworks-cli',), ('agentXlink_not_a_relay',)])
                db.execute('CREATE TABLE tokens (hash TEXT, client_id TEXT, family_id TEXT)')
                db.executemany('INSERT INTO tokens VALUES (?,?,?)', [('relay', 'agent_link_fixture', 'relay-family'), ('native', 'agentworks-cli', 'native-family')])
                db.execute('CREATE TABLE agent_connections (family_id TEXT, parent_family TEXT)')
                db.executemany('INSERT INTO agent_connections VALUES (?,?)', [('relay-family', 'parent'), ('native-family', 'native-parent')])
                for table in ('agent_server_links', 'agent_server_link_requests'):
                    db.execute(f'CREATE TABLE {table} (fixture TEXT)')
                    db.execute(f'INSERT INTO {table} VALUES (?)', ('encrypted-fixture',))
            native = {'inbox_id': 'native', 'endpoints': [{'named_agent_id': 'native'}, {}],
                      'messages': [{'message_id': 'keep', 'message': 'keep history', 'agent_sequence': 42,
                                    'delivery': 'delivered', 'delivered_at': '2026-10-01T00:00:00Z'}]}
            original = {'next_agent_sequence': 99, 'agents': [{'agent_id': 'native', 'name': 'worker'},
                        {'agent_id': 'relay', 'relay': {'origin': 'https://example.com'}}],
                        'conversations': [native, {'inbox_id': 'relay', 'endpoints': [{'named_agent_id': 'relay'}, {}]}]}
            mailbox = root / 'conversations.json'
            saved = json.dumps(original).encode()
            mailbox.write_bytes(saved)
            script = pathlib.Path(__file__).with_name('retire-agent-server-relay.py')
            command = [sys.executable, str(script), '--state-root', str(root), '--mailbox', str(mailbox)]
            first = json.loads(subprocess.check_output(command, text=True))
            self.assertTrue(first['mailbox_changed'])
            self.assertTrue(all(count == 0 for count in first['server_relay_retirement'][0]['relay_rows_after'].values()))
            self.assertEqual(json.loads(mailbox.read_text()), {'next_agent_sequence': 99,
                             'agents': [{'agent_id': 'native', 'name': 'worker'}], 'conversations': [native]})
            self.assertEqual(next(root.glob('conversations.json.before-*')).read_bytes(), saved)
            with sqlite3.connect(path) as db:
                self.assertEqual(db.execute('SELECT * FROM tokens').fetchall(), [('native', 'agentworks-cli', 'native-family')])
                self.assertEqual(db.execute('SELECT * FROM clients').fetchall(), [('agentworks-cli',), ('agentXlink_not_a_relay',)])
                for table in ('requests', 'codes'):
                    self.assertEqual(db.execute(f'SELECT * FROM {table}').fetchall(), [('agentworks-cli',), ('agentXlink_not_a_relay',)])
                self.assertEqual(db.execute('SELECT * FROM agent_connections').fetchall(), [('native-family', 'native-parent')])
                self.assertFalse(db.execute("SELECT name FROM sqlite_master WHERE name LIKE 'agent_server_%'").fetchall())
            backup = root / 'auth' / first['server_relay_retirement'][0]['backup']
            with sqlite3.connect(backup) as db:
                self.assertEqual(db.execute('SELECT count(*) FROM tokens').fetchone()[0], 2)
            before = mailbox.read_bytes()
            second = json.loads(subprocess.check_output(command, text=True))
            self.assertFalse(second['mailbox_changed'])
            self.assertFalse(second['server_relay_retirement'][0]['changed'])
            self.assertEqual(mailbox.read_bytes(), before)
            self.assertEqual(len(list(root.rglob('*.before-server-relay-*'))), 2)


if __name__ == '__main__':
    unittest.main()
