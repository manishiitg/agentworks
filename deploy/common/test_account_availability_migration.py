#!/usr/bin/env python3
"""Stopped migration fixtures; synthetic files only, no running app."""
import contextlib
import importlib.util
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

source = Path(__file__).resolve().parents[2] / 'scripts/migrations/account-availability.py'
spec = importlib.util.spec_from_file_location('account_availability', source)
migration = importlib.util.module_from_spec(spec)
spec.loader.exec_module(migration)


class AccountAudienceMigration(unittest.TestCase):
    def test_packaged_desktop_prestart(self):
        subprocess.run(['node', '--test', str(source.parents[2] / 'desktop/lib/accountAvailabilityMigration.test.js')], check=True, capture_output=True, text=True)

    def test_product_grants_people_and_environment_are_backed_up_once(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            config = root / 'docs/config'
            config.mkdir(parents=True)
            people = [{'id': 'person-a', 'username': 'alpha', 'email': 'alpha@example.com'}, {'id': 'person-b', 'username': 'beta'}]
            (config / 'users.json').write_text(json.dumps({'users': people}))
            before = {'available_to': {'codex-cli': {'products': ['work'], 'users': []}, 'claude-code': {'admins': True, 'users': ['ALPHA@example.com', 'person-a']}, 'cursor-cli': 'admins'}, 'token_limits': {'codex-cli': {'daily': 1234}}, 'product_defaults': {'work': {'provider': 'codex-cli', 'model': 'example-model'}}}
            settings = config / 'provider-account-settings.json'
            settings.write_text(json.dumps(before))
            original = settings.read_bytes()
            env = root / 'app.env'
            original_env = 'UNRELATED_VALUE=synthetic-secret-marker\nAGENTWORKS_PROVIDER_POLICY=\'{\n "muse-cli":{"available_to":{"products":["agentworks"]},"pinned":true}}\'\nOTHER=value\n'
            env.write_text(original_env)
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                migration.migrate(root / 'docs', root / 'backup', [env])
                migration.migrate(root / 'docs', root / 'unused-backup', [env])
            after = json.loads(settings.read_text())
            self.assertEqual(after['available_to'], {'codex-cli': 'all', 'claude-code': {'users': ['person-a']}, 'cursor-cli': 'admins'})
            self.assertEqual(after['token_limits'], before['token_limits'])
            self.assertEqual(after['product_defaults'], before['product_defaults'])
            self.assertEqual((root / 'backup/provider-account-settings.json').read_bytes(), original)
            self.assertEqual((root / 'backup/environment-0.env').read_text(), original_env)
            self.assertEqual((root / 'backup/environment-0.env').stat().st_mode & 0o777, 0o600)
            self.assertIn('UNRELATED_VALUE=synthetic-secret-marker\n', env.read_text())
            self.assertIn('"available_to":"all","pinned":true', env.read_text())
            self.assertFalse((root / 'unused-backup').exists())
            self.assertNotIn('synthetic-secret-marker', output.getvalue())
            self.assertNotIn('alpha@example.com', output.getvalue())
            self.assertEqual(migration.audience(' Everyone ', people), 'all')
            self.assertEqual(migration.audience('ADMINS', people), 'admins')
            self.assertEqual(migration.audience({'products': ['work'], 'users': ['external@example.com']}, people), 'all')
            # The launcher updates just its inherited policy, preserving all
            # other resolved environment settings without re-sourcing .env.
            inherited = '{"codex-cli":{"available_to":{"products":["work"]},"pinned":true}}'
            result = subprocess.run([sys.executable, str(source), '--workspace-dir', str(root / 'docs'), '--backup-dir', str(root / 'inherited-backup'), '--export-current-policy'], env={**os.environ, 'AGENTWORKS_PROVIDER_POLICY': inherited}, text=True, capture_output=True, check=True)
            self.assertEqual(json.loads(result.stdout), {'codex-cli': {'available_to': 'all', 'pinned': True}})
            self.assertEqual((root / 'inherited-backup/inherited-policy.json').read_text(), inherited)
            self.assertEqual((root / 'inherited-backup/inherited-policy.json').stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
