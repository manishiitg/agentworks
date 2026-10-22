#!/usr/bin/env python3
"""Give existing Crew owner chats distinct tab keys, preserving sessions and history."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
from datetime import datetime, timezone


def migrate(workspace, state):
    ownership = state / 'ownership/projects.json'
    if not ownership.exists():
        print('Crew owner tabs: no owner registry; nothing to migrate.')
        return
    projects = json.loads(ownership.read_text()).get('projects', {})
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = state / 'backups' / ('crew-owner-tabs-' + stamp)
    changed = 0
    for path in (workspace / '_users').glob('*/chat_history/product-conversations.json'):
        user = path.parent.parent.name
        original = path.read_bytes()
        data = json.loads(original)
        entries = data.get('entries', {})
        touched = False
        for project in projects.values():
            if project.get('product') != 'work' or user not in [project.get('owner_id'), *project.get('co_owners', [])]:
                continue
            project_id = project.get('project_id')
            root = workspace / project.get('canonical_root', 'Crew/' + project['folder'])
            manifest_path = root / 'product.json'
            if not manifest_path.exists():
                continue
            manifest = json.loads(manifest_path.read_text())
            project_id = project_id or manifest.get('id')
            old = 'work/' + project_id
            if old not in entries:
                continue
            record = entries[old]
            if user == project['owner_id']:
                identity = record.get('conversation_id') or record['session_id']
                suffix = 'root-' + hashlib.sha256(identity.encode()).hexdigest()[:12]
            else:
                suffix = 'owner-' + hashlib.sha256(user.encode()).hexdigest()[:16]
            key = project_id + ':chat:' + suffix
            new = 'work/' + key
            if new in entries and entries[new]['session_id'] != record['session_id']:
                raise ValueError('Crew tab key collision; no existing chat was replaced')
            entries[new] = entries.pop(old)
            entries[new]['conversation_key'] = key
            for section in ('agents', 'previous'):
                if old in data.get(section, {}):
                    data[section][new] = data[section].pop(old)
            for previous in data.get('previous', {}).get(new, []):
                previous['conversation_key'] = key
            if user == project['owner_id']:
                if not backup.exists():
                    backup.mkdir(parents=True, mode=0o700)
                dest = backup / ('manifest-' + hashlib.sha256(str(manifest_path).encode()).hexdigest()[:16] + '.json')
                shutil.copyfile(manifest_path, dest)
                os.chmod(dest, 0o600)
                manifest['main_chat_key'] = key
                manifest['session_id'] = record['session_id']
                manifest_path.write_text(json.dumps(manifest, indent=2) + '\n')
            touched = True
            changed += 1
        if touched:
            backup.mkdir(parents=True, exist_ok=True, mode=0o700)
            dest = backup / (user + '-conversations.json')
            dest.write_bytes(original)
            os.chmod(dest, 0o600)
            path.write_text(json.dumps(data, indent=2) + '\n')
    print(f'Crew owner tabs: migrated {changed} default chats; sessions and transcripts retained.')
    if changed:
        print(f'Crew owner tabs backup: {backup}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-root', required=True, type=Path)
    parser.add_argument('--state-root', required=True, type=Path)
    args = parser.parse_args()
    migrate(args.workspace_root, args.state_root)
