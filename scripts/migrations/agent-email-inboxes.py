#!/usr/bin/env python3
"""Back up and retire personal Gmail intake. New agent inbox storage is independent."""
import argparse
import json
import os
from pathlib import Path
import sqlite3
from datetime import datetime, timezone

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('database', type=Path)
parser.add_argument('--workspace-root', required=True, type=Path)
args = parser.parse_args()
stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
legacy = {'routes', 'inboxes', 'sender_consents', 'deliveries', 'mailboxes'}
if args.database.exists():
    with sqlite3.connect(args.database) as db:
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        retired = tables & legacy
        if retired:
            backup = args.database.with_name(args.database.name + '.before-agent-inboxes-' + stamp)
            with sqlite3.connect(backup) as copy:
                db.backup(copy)
            os.chmod(backup, 0o600)
            with db:
                for table in sorted(retired):
                    db.execute(f'DROP TABLE "{table}"')
            print(f'Personal Gmail intake retired; backed up {len(retired)} tables.')
changed = 0
backups = args.database.parent / 'retired-trigger-manifests' / stamp
if args.workspace_root.exists():
    for manifest in args.workspace_root.rglob('workflow.json'):
        if not manifest.is_file() or manifest.is_symlink():
            continue
        raw = manifest.read_bytes()
        data = json.loads(raw)
        schedules = data.get('schedules')
        if not isinstance(schedules, list):
            continue
        kept = [item for item in schedules if item.get('kind') != 'gmail']
        if len(kept) == len(schedules):
            continue
        backups.mkdir(parents=True, exist_ok=True, mode=0o700)
        backup = backups / f'{changed}.json'
        backup.write_bytes(raw)
        os.chmod(backup, 0o600)
        data['schedules'] = kept
        manifest.write_text(json.dumps(data, indent=2) + '\n')
        changed += 1
print(f'Retired Gmail-specific schedules in {changed} manifests. Personal Google connections are unchanged.')
