#!/usr/bin/env python3
"""Exercise the one-off migration and the pre-start deployment boundary."""
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
MIGRATE = ROOT / 'scripts/migrations/gmail-personal-inbox.py'
OLD = '''CREATE TABLE routes(id TEXT PRIMARY KEY,owner TEXT NOT NULL,workspace TEXT NOT NULL,connection TEXT NOT NULL,address TEXT NOT NULL UNIQUE,data TEXT NOT NULL,enabled INTEGER NOT NULL,UNIQUE(owner,workspace));
CREATE TABLE sender_consents(route TEXT PRIMARY KEY,owner TEXT,config_hash TEXT,approved_at INTEGER);
CREATE TABLE deliveries(id TEXT PRIMARY KEY,status TEXT,error TEXT);'''


def old_store(app, *, populated=True, invalid_manifest=False):
    docs = app / 'data/docs'
    workflow = docs / 'Workflow/mail'
    workflow.mkdir(parents=True)
    manifest = workflow / 'workflow.json'
    manifest.write_text('invalid' if invalid_manifest else json.dumps({'schedules': [
        {'id': 'route', 'kind': 'gmail', 'gmail': {'address': 'owner+old@example.com'}},
        {'id': 'timer', 'kind': 'schedule'}]}))
    dbfile = app / 'state/gmail-inbound/email.db'
    dbfile.parent.mkdir(parents=True)
    with sqlite3.connect(dbfile) as db:
        db.executescript(OLD)
        if populated:
            route = {'id': 'route', 'owner_id': 'owner', 'workspace_path': 'Workflow/mail', 'workflow_trigger': True, 'enabled': True}
            db.execute('INSERT INTO routes VALUES(?,?,?,?,?,?,?)', ('route', 'owner', 'Workflow/mail', 'saved', 'owner+old@example.com', json.dumps(route), 1))
            db.execute("INSERT INTO sender_consents VALUES('route','owner','old',1)")
            db.execute("INSERT INTO deliveries VALUES('delivery','pending','')")
    return dbfile, docs, manifest


class GmailMigration(unittest.TestCase):
    def migrate(self, db, docs):
        return subprocess.run(['python3', str(MIGRATE), str(db), '--workspace-root', str(docs)], capture_output=True, text=True)

    def test_backed_up_transition_and_repeated_deploy(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            dbfile, docs, manifest = old_store(app)
            original = manifest.read_bytes()
            self.assertEqual(self.migrate(dbfile, docs).returncode, 0)
            backups = list(dbfile.parent.glob('*.before-personal-inbox-*'))
            saved_db = next(p for p in backups if p.is_file())
            self.assertEqual(saved_db.stat().st_mode & 0o777, 0o600)
            self.assertEqual(Path(str(saved_db) + '.workflows/Workflow/mail/workflow.json').read_bytes(), original)
            self.assertEqual([s['id'] for s in json.loads(manifest.read_text())['schedules']], ['timer'])
            with sqlite3.connect(dbfile) as db:
                self.assertEqual(db.execute('SELECT enabled FROM routes').fetchone()[0], 0)
                self.assertEqual(db.execute('SELECT count(*) FROM sender_consents').fetchone()[0], 0)
                self.assertEqual(db.execute('SELECT status FROM deliveries').fetchone()[0], 'rejected')
                # Two targets may now bind the same personal address.
                db.execute("INSERT INTO routes VALUES('second','owner','Workflow/second','saved','owner+old@example.com','{}',0)")
            self.assertEqual(self.migrate(dbfile, docs).returncode, 0)
            self.assertEqual(list(dbfile.parent.glob('*.before-personal-inbox-*')), backups)

    def test_missing_and_empty_stores_need_no_operator_step(self):
        with tempfile.TemporaryDirectory() as tmp:
            app = Path(tmp)
            missing = app / 'absent/email.db'
            self.assertEqual(self.migrate(missing, app / 'missing-docs').returncode, 0)
            self.assertFalse(missing.exists())
            dbfile, docs, _ = old_store(app, populated=False)
            self.assertEqual(self.migrate(dbfile, docs).returncode, 0)
            with sqlite3.connect(dbfile) as db:
                self.assertNotIn('address TEXT NOT NULL UNIQUE', db.execute("SELECT sql FROM sqlite_master WHERE name='routes'").fetchone()[0])
            self.assertEqual(self.migrate(dbfile, docs).returncode, 0)

    def test_failed_migration_restores_old_backend_without_activating_candidate(self):
        rootless = (ROOT / 'deploy/rootless-linux/build-and-activate.sh').read_text()
        aws = (ROOT / 'deploy/aws-ec2/server/build-and-activate.sh').read_text()
        for label, source in [('rootless', rootless), ('aws', aws)]:
            with self.subTest(label=label), tempfile.TemporaryDirectory() as tmp:
                app = Path(tmp)
                dbfile, docs, manifest = old_store(app)
                # Fail inside the SQL transaction, after manifest writes.
                with sqlite3.connect(dbfile) as db:
                    db.execute("DROP TABLE deliveries")
                manifest_before = manifest.read_bytes()
                before = dbfile.read_bytes()
                old = app / 'old'
                old.mkdir()
                (app / 'current').symlink_to(old)
                release = app / 'candidate'
                (release / 'migrations').mkdir(parents=True)
                package_line = next(line for line in source.splitlines() if line.startswith('install -m 0755 "$REPO_ROOT/scripts/migrations/gmail-personal-inbox.py"'))
                subprocess.run(['bash', '-c', package_line], env={**os.environ, 'REPO_ROOT': str(ROOT), 'BUILD_DIR': str(release)}, check=True)
                bindir = app / 'tools'
                bindir.mkdir()
                systemctl = bindir / 'systemctl'
                systemctl.write_text('#!/bin/sh\necho "$*" >> "$CALL_LOG"\n')
                systemctl.chmod(0o755)
                if label == 'rootless':
                    cleanup = source[source.index('cleanup_build() {'):source.index('trap cleanup_build EXIT')]
                    block = source[source.index('# The personal inbox schema must be ready'):source.index('vault_install "$BUILD_DIR"')]
                    script = 'MIGRATION_STOPPED_AGENT=0\n' + cleanup + 'trap cleanup_build EXIT\n' + block
                else:
                    start = source.index('stopped=1; systemctl --user stop video-studio-agent; python3')
                    end = source.index("; migration_state=", start)
                    block = source[start:end].replace('$REMOTE_RELEASE', str(release)).replace('$REMOTE_APP', str(app)).replace('/data/video-studio/docs', str(docs))
                    script = "stopped=0\ntrap 'if [ \"$stopped\" = 1 ]; then systemctl --user restart video-studio-agent; fi' EXIT\n" + block
                env = {**os.environ, 'PATH': str(bindir) + ':' + os.environ['PATH'], 'CALL_LOG': str(app / 'calls'), 'PRODUCT': 'example', 'REMOTE_APP': str(app), 'BUILD_DIR': str(release)}
                result = subprocess.run(['bash', '-c', 'set -euo pipefail\n' + script], env=env, capture_output=True, text=True)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('stop ', (app / 'calls').read_text())
                self.assertIn('restart', (app / 'calls').read_text())
                self.assertEqual((app / 'current').resolve(), old.resolve())
                self.assertEqual(dbfile.read_bytes(), before)
                self.assertEqual(manifest.read_bytes(), manifest_before)


if __name__ == '__main__':
    unittest.main()
