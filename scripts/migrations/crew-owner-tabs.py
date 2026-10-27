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
    backup = state / 'migrations' / ('crew-owner-tabs-backup-' + stamp)
    changed = 0
    named = 0
    moved = {}
    pending = {}
    laptop_changed = 0
    for project in projects.values():
        if project.get('product') != 'work':
            continue
        owner = project['owner_id']
        root = project.get('canonical_root') or 'Crew/' + project['folder']
        key = hashlib.sha256((owner + '\0' + root).encode()).hexdigest()
        path = state / 'crew-laptop' / (key + '.json')
        if not path.exists():
            continue
        data = json.loads(path.read_text())
        if not data.get('device_id') or data.get('device_owner_id'):
            continue
        data['device_owner_id'] = owner
        pending[path] = ('laptop-' + key + '.json', (json.dumps(data, indent=2) + '\n').encode())
        laptop_changed += 1
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
            if (workspace / '_system/crew-data').is_dir():
                root = workspace / '_system/crew-data' / project_id
            manifest_path = root / 'product.json'
            if not manifest_path.exists():
                continue
            manifest = json.loads(manifest_path.read_text())
            project_id = project_id or manifest.get('id')
            old = 'work/' + project_id
            index_path = root / 'builder/conversation/chat-index.json'
            titles = {}
            if index_path.exists():
                index = json.loads(index_path.read_text())
                for entry in index.get('entries', {}).values():
                    session = entry.get('session', {})
                    if session.get('session_id') and session.get('title'):
                        titles[session['session_id']] = session['title'].strip()
            for entry_key, chat in entries.items():
                if entry_key != old and not entry_key.startswith(old + ':chat:'):
                    continue
                identity = data.setdefault('agents', {}).get(entry_key, {})
                title = titles.get(chat.get('session_id'), '')
                if not identity.get('name', '').strip() and title:
                    identity['name'] = title
                    data['agents'][entry_key] = identity
                    named += 1
                    touched = True
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
                # A co-owner may have opened their new default before this
                # migration. Keep it and retain the older conversation as a tab.
                suffix = 'root-' + hashlib.sha256(record['session_id'].encode()).hexdigest()
                key = project_id + ':chat:' + suffix
                new = 'work/' + key
            if new in entries and entries[new]['session_id'] != record['session_id']:
                raise ValueError('Crew tab key collision; no existing chat was replaced')
            moved[(user, record['session_id'], project_id)] = key
            entries[new] = entries.pop(old)
            entries[new]['conversation_key'] = key
            for section in ('agents', 'previous'):
                if old in data.get(section, {}):
                    data[section][new] = data[section].pop(old)
            for previous in data.get('previous', {}).get(new, []):
                previous['conversation_key'] = key
            if user == project['owner_id'] and manifest.get('main_chat_key') in (None, '', project_id):
                manifest['main_chat_key'] = key
                manifest['session_id'] = record['session_id']
                name = 'manifest-' + hashlib.sha256(str(manifest_path).encode()).hexdigest()[:16] + '.json'
                pending[manifest_path] = (name, (json.dumps(manifest, indent=2) + '\n').encode())
            touched = True
            changed += 1
        if touched:
            pending[path] = (user + '-conversations.json', (json.dumps(data, indent=2) + '\n').encode())
    messages = workspace / '_system/agent_messages/conversations.json'
    if moved and messages.exists():
        original = messages.read_bytes()
        data = json.loads(original)
        touched = False
        for conversation in data.get('conversations', []):
            for endpoint in conversation.get('endpoints', []):
                key = moved.get((endpoint.get('user_id'), endpoint.get('session'), endpoint.get('chat_key')))
                if key:
                    endpoint['chat_key'] = key
                    touched = True
        if touched:
            pending[messages] = ('agent-messages.json', (json.dumps(data, indent=2) + '\n').encode())
    # Parse and convert every document before changing any files.
    if pending:
        backup.mkdir(parents=True, exist_ok=True, mode=0o700)
        for path, (name, content) in pending.items():
            dest = backup / name
            shutil.copy2(path, dest)
            os.chmod(dest, 0o600)
            path.write_bytes(content)
    if laptop_changed:
        print(f'Crew shared settings: retained {laptop_changed} laptop device owners; backup {backup}')
    print(f'Crew owner tabs: migrated {changed} default chats, preserved {named} saved tab names; sessions and transcripts retained.')
    if changed or named:
        print(f'Crew owner tabs backup: {backup}')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-root', required=True, type=Path)
    parser.add_argument('--state-root', required=True, type=Path)
    args = parser.parse_args()
    migrate(args.workspace_root, args.state_root)
