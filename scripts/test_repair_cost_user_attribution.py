import json
import sqlite3
import tempfile
import unittest
from collections import defaultdict
from pathlib import Path

import repair_cost_user_attribution as repair


def event(event_id='e1', actor='', session='child', execution='exec1', correlation='', workspace='Workflow/w'):
    return dict(event_id=event_id, user_id=actor, session_id=session, execution_id=execution,
                correlation_id=correlation, workflow_id=workspace, prompt_tokens=100,
                cache_read_tokens=90, completion_tokens=2, total_cost_usd=0.125)


class RepairTests(unittest.TestCase):
    def test_exact_child_parent_and_legacy_correlation(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); base = root / 'Workflow/w/builder/conversation'
            (base / 'background/parent').mkdir(parents=True)
            (base / 'session-parent-conversation.json').write_text(json.dumps(
                {'session_id': 'parent', 'user_id': 'user-a', 'ui_events': []}))
            (base / 'background/parent/exec1.json').write_text(json.dumps(
                {'session_id': 'parent', 'agent_id': 'exec1'}))
            (base / 'background/parent/workflow-full-token.json').write_text(json.dumps(
                {'session_id': 'parent', 'agent_id': 'workflow-full-token'}))
            rows = [event(), event('e2', execution='missing', correlation='workshop-workflow-full-token')]
            proposals, unresolved = repair.plan(rows, repair.evidence(root, rows))
            self.assertEqual(set(proposals), {'e1', 'e2'})
            self.assertEqual(proposals['e1']['user_id'], 'user-a')
            self.assertEqual(unresolved, [])

    def test_conflicting_users_legacy_default_and_unknown_stay_unresolved(self):
        identities = defaultdict(dict)
        identities[('execution', 'Workflow/w', 'exec1')] = {'a': {'source1'}, 'b': {'source2'}}
        identities[('session', 'Workflow/w', 'legacy')] = {'default': {'legacy-file'}}
        rows = [event(), event('e2', session='legacy', execution=''), event('e3', execution='unknown')]
        proposals, unresolved = repair.plan(rows, identities)
        self.assertEqual(proposals, {})
        self.assertEqual([r['reason'] for r in unresolved], ['conflict', 'legacy default actor', 'no exact evidence'])

    def test_mismatched_background_metadata_not_accepted(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp); base = root / 'Workflow/w/builder/conversation'
            (base / 'background/parent').mkdir(parents=True)
            (base / 'session-parent-conversation.json').write_text(json.dumps(
                {'session_id': 'parent', 'user_id': 'a'}))
            (base / 'background/parent/exec1.json').write_text(json.dumps(
                {'session_id': 'other-parent', 'agent_id': 'exec1'}))
            self.assertEqual(repair.plan([event()], repair.evidence(root, []))[0], {})

    def test_atomic_financial_preservation_backup_and_idempotence(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / 'costs.sqlite'; backup = Path(tmp) / 'backups'; backup.mkdir()
            row = event()
            with sqlite3.connect(path) as db:
                db.execute('CREATE TABLE cost_events (' + ','.join(k + (' REAL' if isinstance(v, (int, float)) else ' TEXT') for k,v in row.items()) + ')')
                db.execute('INSERT INTO cost_events VALUES (' + ','.join('?' for _ in row) + ')', list(row.values()))
            proposals = {'e1': {'user_id': 'a', 'evidence': ['saved-parent']}}
            result = repair.repair_db(path, proposals, backup)
            self.assertEqual(result['changed'], ['e1'])
            with repair.connect(path) as db:
                current = dict(db.execute('SELECT * FROM cost_events').fetchone())
            self.assertEqual(current, {**row, 'user_id': 'a'})
            with repair.connect(Path(result['backup'])) as db:
                self.assertEqual(db.execute('SELECT user_id FROM cost_events').fetchone()[0], '')
            self.assertEqual(repair.pending_rows(path, proposals), [])
            with self.assertRaises(RuntimeError):
                repair.pending_rows(path, {'e1': {'user_id': 'b'}})
            modified = dict(current, cache_read_tokens=0)
            self.assertNotEqual(repair.digest([current]), repair.digest([modified]))

    def test_workspace_less_tool_identity_requires_global_agreement(self):
        identities = {('session', 'Workflow/a', 'tool-session'): {'a': {'a'}},
                      ('session', 'Workflow/b', 'tool-session'): {'b': {'b'}}}
        proposals, unresolved = repair.plan([event(workspace='', session='tool-session', execution='')], identities)
        self.assertEqual(proposals, {})
        self.assertEqual(unresolved[0]['reason'], 'conflict')


if __name__ == '__main__':
    unittest.main()
