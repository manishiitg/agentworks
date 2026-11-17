#!/usr/bin/env python3
"""#2980: audit-only flag backfill preserves names, data and original backups."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('migration', Path(__file__).with_name('migrate-generated-agent-names.py'))
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class GeneratedNamesTest(unittest.TestCase):
    def test_audit_rechecks_name_backs_up_and_only_sets_flag(self):
        with tempfile.TemporaryDirectory() as root:
            root = Path(root)
            docs, backups = root / 'docs', root / 'backup'
            relative = '_users/owner/chat_history/product-conversations.json'
            path = docs / relative
            path.parent.mkdir(parents=True)
            data = {'agents': {'work/project:chat:one': {'name': 'Claude Code (example)', 'updated_by': 'tab name'}, 'work/project:chat:two': {'name': 'Planner', 'updated_by': 'owner'}}, 'entries': {'saved': 'unchanged'}}
            path.write_text(json.dumps(data))
            original = path.read_bytes()
            audit = root / 'audit.json'
            item = {'registry': relative, 'entry': 'work/project:chat:one', 'expected_name': 'Claude Code (example)', 'expected_updated_by': 'tab name'}
            audit.write_text(json.dumps({'entries': [item]}))
            self.assertEqual(migration.migrate(docs, audit, backups, True), 1)
            self.assertEqual(path.read_bytes(), original)
            self.assertFalse(backups.exists())
            self.assertEqual(migration.migrate(docs, audit, backups), 1)
            expected = json.loads(original)
            expected['agents'][item['entry']]['generated_name'] = True
            self.assertEqual(json.loads(path.read_bytes()), expected)
            backup, = backups.glob('*/*.json')
            self.assertEqual(backup.read_bytes(), original)
            self.assertEqual(backup.stat().st_mode & 0o777, 0o600)
            self.assertEqual(migration.migrate(docs, audit, backups), 0)
            expected['agents'][item['entry']]['name'] = 'Person renamed it'
            path.write_text(json.dumps(expected))
            with self.assertRaises(ValueError):
                migration.migrate(docs, audit, backups)
            with self.assertRaises(ValueError):
                migration.plain_path(docs, '../outside')
            path.unlink()
            path.symlink_to(backup)
            with self.assertRaises(ValueError):
                migration.migrate(docs, audit, backups)


if __name__ == '__main__':
    unittest.main()
