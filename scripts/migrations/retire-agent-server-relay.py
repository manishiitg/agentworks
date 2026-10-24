#!/usr/bin/env python3
"""Retire unused server-relay state once, with backups; stop the agent first."""
import argparse
import datetime
import json
import os
import pathlib
import shutil
import sqlite3
import tempfile


PREFIX = 'agent_link_'
LINK_TABLES = ('agent_server_links', 'agent_server_link_requests')


def backup_path(path):
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    return path.with_name(path.name + '.before-server-relay-' + stamp)


def retire_database(path):
    with sqlite3.connect(path) as db:
        tables = {row[0] for row in db.execute("SELECT name FROM sqlite_master WHERE type='table'")}
        columns = {'clients': 'id', 'requests': 'client_id', 'codes': 'client_id', 'tokens': 'client_id'}
        counts = {
            table: db.execute(f'SELECT count(*) FROM {table} WHERE substr({column},1,?)=?',
                              (len(PREFIX), PREFIX)).fetchone()[0]
            for table, column in columns.items() if table in tables
        }
        families = []
        if 'tokens' in tables:
            families = [row[0] for row in db.execute(
                'SELECT DISTINCT family_id FROM tokens WHERE substr(client_id,1,?)=?',
                (len(PREFIX), PREFIX))]
        if not any(counts.values()) and not any(table in tables for table in LINK_TABLES):
            return {'database': path.name, 'changed': False, 'relay_rows_after': counts}
        backup = backup_path(path)
        backup.touch(mode=0o600)
        with sqlite3.connect(backup) as saved:
            db.backup(saved)
        db.execute('BEGIN')
        connections = 0
        if 'agent_connections' in tables:
            for family in families:
                connections += db.execute('DELETE FROM agent_connections WHERE family_id=? OR parent_family=?',
                                          (family, family)).rowcount
        for table, column in columns.items():
            if table in tables:
                db.execute(f'DELETE FROM {table} WHERE substr({column},1,?)=?', (len(PREFIX), PREFIX))
        for table in LINK_TABLES:
            db.execute(f'DROP TABLE IF EXISTS {table}')
        db.commit()
        remaining = {
            table: db.execute(f'SELECT count(*) FROM {table} WHERE substr({column},1,?)=?',
                              (len(PREFIX), PREFIX)).fetchone()[0]
            for table, column in columns.items() if table in tables
        }
        if any(remaining.values()):
            raise RuntimeError('Server-relay credentials remain after retirement')
        return {'database': path.name, 'changed': True, 'backup': backup.name,
                'relay_rows_before': counts, 'relay_rows_after': remaining, 'removed_connections': connections,
                'dropped_tables': [table for table in LINK_TABLES if table in tables]}


def retire_mailbox(path):
    if not path.exists():
        return {'mailbox_changed': False}
    store = json.loads(path.read_text())
    retired = {agent['agent_id'] for agent in store.get('agents', []) if agent.get('relay')}

    def relay_conversation(conversation):
        return any(endpoint.get('remote') or endpoint.get('named_agent_id') in retired
                   for endpoint in conversation.get('endpoints', [])) or any(
                       message.get('relay') for message in conversation.get('messages', []))

    counts = {'agents': len(retired)}
    for key in ('conversations', 'expired'):
        counts[key] = sum(relay_conversation(conversation) for conversation in store.get(key, []))
    if not any(counts.values()):
        return {'mailbox_changed': False}
    backup = backup_path(path)
    shutil.copy2(path, backup)
    store['agents'] = [agent for agent in store.get('agents', []) if agent['agent_id'] not in retired]
    for key in ('conversations', 'expired'):
        if key in store:
            store[key] = [conversation for conversation in store[key] if not relay_conversation(conversation)]
    fd, temporary = tempfile.mkstemp(prefix='.agentbridge-', dir=path.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(json.dumps(store, separators=(',', ':')) + '\n')
        shutil.copymode(path, temporary)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    return {'mailbox_changed': True, 'backup': backup.name, 'removed_relay_records': counts}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state-root', required=True, type=pathlib.Path)
    parser.add_argument('--mailbox', required=True, type=pathlib.Path)
    args = parser.parse_args()
    results = [retire_database(path) for path in sorted((args.state_root / 'auth').glob('*.mcp-oauth.sqlite'))]
    print(json.dumps({'server_relay_retirement': results, **retire_mailbox(args.mailbox)}))


if __name__ == '__main__':
    main()
