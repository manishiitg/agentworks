#!/usr/bin/env python3
"""Regression: owner-tab conversion preserves chats and exact message addresses."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('crew_owner_tabs', Path(__file__).resolve().parents[2] / 'scripts/migrations/crew-owner-tabs.py')
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class OwnerTabMigrationTest(unittest.TestCase):
    def test_preserves_identity_and_only_rewrites_exact_message_endpoints(self):
        with tempfile.TemporaryDirectory() as folder:
            base = Path(folder)
            workspace, state = base / 'docs', base / 'state'
            (state / 'ownership').mkdir(parents=True)
            (state / 'ownership/projects.json').write_text(json.dumps({'projects': {'work/demo': {
                'product': 'work', 'folder': 'demo', 'project_id': 'p', 'owner_id': 'owner', 'co_owners': ['co']
            }}}))
            project = workspace / 'Crew/demo'
            project.mkdir(parents=True)
            manifest = {'id': 'p', 'product': 'work', 'session_id': 'current-session', 'main_chat_key': 'p:chat:current'}
            (project / 'product.json').write_text(json.dumps(manifest))
            for user in ('owner', 'co'):
                path = workspace / '_users' / user / 'chat_history/product-conversations.json'
                path.parent.mkdir(parents=True)
                path.write_text(json.dumps({'version': 1, 'entries': {'work/p': {
                    'conversation_key': 'p', 'conversation_id': 'logical-' + user, 'session_id': 'session-' + user,
                    'resource_id': 'p', 'profile_id': 'work', 'workspace_path': 'Crew/demo'
                }}, 'agents': {'work/p': {'name': 'Keep name ' + user, 'engine': 'example-provider'}},
                    'previous': {'work/p': [{'session_id': 'earlier-' + user, 'conversation_key': 'p'}]}}))
            owner_registry = workspace / '_users/owner/chat_history/product-conversations.json'
            owner_data = json.loads(owner_registry.read_text())
            owner_data['entries']['work/p:chat:saved-name'] = {
                'conversation_key': 'p:chat:saved-name', 'session_id': 'named-session', 'resource_id': 'p', 'profile_id': 'work', 'workspace_path': 'Crew/demo'
            }
            owner_data['agents']['work/p:chat:saved-name'] = {'engine': 'example-provider'}
            owner_registry.write_text(json.dumps(owner_data))
            title_index = project / 'builder/conversation/chat-index.json'
            title_index.parent.mkdir(parents=True)
            title_index.write_text(json.dumps({'complete': True, 'entries': {
                'named-session': {'session': {'session_id': 'named-session', 'title': 'Existing coach name'}},
                'session-owner': {'session': {'session_id': 'session-owner', 'title': 'Do not replace explicit tab names'}}
            }}))
            messages = workspace / '_system/agent_messages/conversations.json'
            messages.parent.mkdir(parents=True)
            endpoints = [
                {'user_id': 'co', 'session': 'session-co', 'chat_key': 'p'},
                {'user_id': 'other', 'session': 'session-co', 'chat_key': 'p'},
                {'user_id': 'co', 'session': 'unrelated', 'chat_key': 'p'},
                {'user_id': 'co', 'session': 'session-co', 'chat_key': 'another-project'},
            ]
            messages.write_text(json.dumps({'conversations': [{'endpoints': endpoints}]}))
            migration.migrate(workspace, state)
            results = [json.loads((workspace / '_users' / user / 'chat_history/product-conversations.json').read_text()) for user in ('owner', 'co')]
            keys = [next(key for key in result['entries'] if key != 'work/p:chat:saved-name') for result in results]
            self.assertNotEqual(keys[0], keys[1])
            for user, result, key in zip(('owner', 'co'), results, keys):
                self.assertEqual(result['entries'][key]['session_id'], 'session-' + user)
                self.assertEqual(result['entries'][key]['conversation_id'], 'logical-' + user)
                self.assertEqual(result['agents'][key]['name'], 'Keep name ' + user)
                self.assertEqual(result['agents'][key]['engine'], 'example-provider')
                self.assertEqual(result['previous'][key][0]['session_id'], 'earlier-' + user)
            self.assertEqual(results[0]['agents']['work/p:chat:saved-name'], {'engine': 'example-provider', 'name': 'Existing coach name'})
            rewritten = json.loads(messages.read_text())['conversations'][0]['endpoints']
            self.assertEqual(rewritten[0]['chat_key'], keys[1].removeprefix('work/'))
            self.assertEqual(rewritten[1:], endpoints[1:])
            self.assertEqual(json.loads((project / 'product.json').read_text()), manifest)
            before = {path: path.read_bytes() for path in workspace.rglob('*.json')}
            migration.migrate(workspace, state)
            self.assertEqual(before, {path: path.read_bytes() for path in workspace.rglob('*.json')})
            self.assertEqual(len(list((state / 'migrations').rglob('*conversations.json'))), 2)
            self.assertEqual(len(list((state / 'migrations').rglob('agent-messages.json'))), 1)


if __name__ == '__main__':
    unittest.main()
