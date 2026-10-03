#!/usr/bin/env python3
"""PLAT-377: repair blank actors using exact saved chat/execution identities.

Dry run by default. Never infers actors from workflow owners, dates, or prompts.
Only user_id changes; both SQLite ledger copies are backed up and audited.
"""
import argparse
import hashlib
import json
import os
import sqlite3
from collections import defaultdict
from contextlib import closing
from datetime import datetime, timezone
from pathlib import Path


def connect(path, write=False):
    db = sqlite3.connect(path.as_uri() + ("?mode=rw" if write else "?mode=ro"), uri=True, timeout=60)
    db.row_factory = sqlite3.Row
    return db


def evidence(root, rows):
    identities = defaultdict(lambda: defaultdict(set))

    def add(kind, workspace, identity, actor, source):
        if isinstance(identity, str) and identity and isinstance(actor, str) and actor:
            identities[(kind, workspace, identity)][actor].add(source)

    for row in rows:
        for kind, field in [('session', 'session_id'), ('execution', 'execution_id')]:
            add(kind, row['workflow_id'], row[field], row['user_id'], 'ledger:' + row['event_id'])
    parents = defaultdict(list)
    # Only server-owned conversation files, not arbitrary user JSON documents.
    bases = list((root / 'Workflow').glob('*/builder/conversation'))
    bases += list((root / '_users').glob('*/Chats/*/projects/*/builder/conversation'))
    for base in bases:
        workspace = str(base.parent.parent.relative_to(root))
        for path in base.rglob('*-conversation.json'):
            data = json.loads(path.read_text())
            actor, session = data.get('user_id', ''), data.get('session_id', '')
            source = str(path.relative_to(root))
            parents[(workspace, session)].append((actor, source))
            add('session', workspace, session, actor, source)
            for event in data.get('ui_events', []):
                for field in ('execution_id', 'parent_execution_id'):
                    add('execution', workspace, event.get(field, ''), actor, source)
        for path in (base / 'background').glob('*/*.json'):
            data = json.loads(path.read_text())
            # Verify directory and file identities agree with server metadata.
            if data.get('session_id') != path.parent.name or data.get('agent_id') != path.stem:
                continue
            for actor, parent_source in parents[(workspace, data['session_id'])]:
                add('execution', workspace, data['agent_id'], actor,
                    str(path.relative_to(root)) + ' -> ' + parent_source)
    return identities


def plan(rows, identities):
    proposals, unresolved = {}, []
    for row in rows:
        if row['user_id']:
            continue
        matches = defaultdict(set)
        references = [('session', row['session_id']), ('execution', row['execution_id'])]
        # This historical correlation format is the full-workflow execution
        # ID prefixed with 'workshop-' (planning_exports.go). No fuzzy match.
        if row['correlation_id'].startswith('workshop-workflow-full-'):
            references.append(('execution', row['correlation_id'][len('workshop-'):]))
        for kind, identity in references:
            if not identity:
                continue
            if row['workflow_id']:
                keys = [(kind, row['workflow_id'], identity)]
            else:
                # Tool rows sometimes lack a workspace. Require a globally
                # unambiguous exact session/execution identity in that case.
                keys = [key for key in identities if key[0] == kind and key[2] == identity]
            for key in keys:
                for actor, sources in identities.get(key, {}).items():
                    matches[actor].update(sources)
        if len(matches) == 1 and 'default' not in matches:
            actor = next(iter(matches))
            proposals[row['event_id']] = {'user_id': actor, 'evidence': sorted(matches[actor])}
        else:
            unresolved.append({'event_id': row['event_id'], 'reason': ('legacy default actor' if set(matches) == {'default'} else 'conflict') if matches else 'no exact evidence',
                               'workflow_id': row['workflow_id'], 'cost': row['total_cost_usd']})
    return proposals, unresolved


def digest(rows):
    """All ledger columns except user_id, including IDs and accounting fields."""
    return hashlib.sha256(json.dumps([{k: row[k] for k in row.keys() if k != 'user_id'}
                                     for row in rows], sort_keys=True).encode()).hexdigest()


def pending_rows(path, proposals):
    with closing(connect(path)) as db:
        rows = db.execute('SELECT event_id, user_id FROM cost_events NOT INDEXED').fetchall()
    pending = []
    for row in rows:
        proposal = proposals.get(row['event_id'])
        if not proposal:
            continue
        if row['user_id'] and row['user_id'] != proposal['user_id']:
            raise RuntimeError('Existing actor conflicts in ' + str(path) + ': ' + row['event_id'])
        if not row['user_id']:
            pending.append(row['event_id'])
    return pending


def repair_db(path, proposals, backup_dir):
    with closing(connect(path, True)) as db, db:
        # Backup API includes WAL and makes a consistent snapshot while live.
        backup_path = backup_dir / (hashlib.sha256(str(path).encode()).hexdigest()[:12] + '.sqlite')
        with closing(sqlite3.connect(backup_path)) as backup, backup:
            db.backup(backup)
            assert backup.execute('PRAGMA quick_check').fetchone()[0] == 'ok'
        os.chmod(backup_path, 0o600)
        db.execute('BEGIN IMMEDIATE')
        integrity = [r[0] for r in db.execute('PRAGMA integrity_check')]
        if integrity != ['ok']:
            raise RuntimeError('Ledger integrity failed; refusing attribution updates: ' + str(path) + ': ' + str(integrity[:3]))
        before = db.execute('SELECT * FROM cost_events NOT INDEXED ORDER BY event_id').fetchall()
        changed = []
        for row in before:
            proposal = proposals.get(row['event_id'])
            if not proposal:
                continue
            if row['user_id'] and row['user_id'] != proposal['user_id']:
                raise RuntimeError('Existing actor conflicts in ' + str(path) + ': ' + row['event_id'])
            if not row['user_id']:
                updated = db.execute("UPDATE cost_events SET user_id=? WHERE event_id=? AND user_id=''",
                           (proposal['user_id'], row['event_id']))
                if updated.rowcount != 1:
                    raise RuntimeError('Expected exactly one updated event: ' + row['event_id'])
                changed.append(row['event_id'])
        after = db.execute('SELECT * FROM cost_events NOT INDEXED ORDER BY event_id').fetchall()
        actors = {row['event_id']: row['user_id'] for row in after}
        if any(actors[event_id] != proposals[event_id]['user_id'] for event_id in changed):
            raise RuntimeError('Actor verification failed; rolling back')
        if digest(before) != digest(after):
            raise RuntimeError('Non-attribution data changed; rolling back')
        assert [r[0] for r in db.execute('PRAGMA integrity_check')] == ['ok']
        result = {'path': str(path), 'backup': str(backup_path), 'changed': changed,
                  'accounting_sha256': digest(after)}
        # Persist audit before committing; rerun reconciles partial multi-DB runs.
        (backup_dir / (backup_path.stem + '.audit.json')).write_text(json.dumps(result, indent=2))
        db.commit()
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs-root', type=Path, required=True)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    root = args.docs_root.resolve()
    global_path = root / '_system/costs.sqlite'
    with closing(connect(global_path)) as db:
        rows = db.execute('SELECT * FROM cost_events NOT INDEXED ORDER BY event_id').fetchall()
    proposals, unresolved = plan(rows, evidence(root, rows))
    selected = [row for row in rows if row['event_id'] in proposals]
    summary = {'mode': 'apply' if args.apply else 'dry-run', 'repair_rows': len(selected),
               'repair_cost_usd': sum(row['total_cost_usd'] for row in selected),
               'remaining_rows': len(unresolved), 'remaining_cost_usd': sum(row['cost'] for row in unresolved),
               'by_user': {}, 'unresolved_by_workflow': {}}
    for row in selected:
        user = proposals[row['event_id']]['user_id']
        item = summary['by_user'].setdefault(user, {'rows': 0, 'cost': 0})
        item['rows'] += 1
        item['cost'] += row['total_cost_usd']
    for row in unresolved:
        item = summary['unresolved_by_workflow'].setdefault(row['workflow_id'], {'rows': 0, 'cost': 0, 'reasons': {}})
        item['rows'] += 1
        item['cost'] += row['cost']
        item['reasons'][row['reason']] = item['reasons'].get(row['reason'], 0) + 1
    # Exact event IDs reconcile workflow copies with the authoritative global
    # ledger, including a rerun after a partial multi-database repair.
    copy_proposals = dict(proposals)
    for row in rows:
        if row['user_id'] and row['user_id'] != 'default':
            copy_proposals[row['event_id']] = {'user_id': row['user_id'], 'evidence': ['global-event:' + row['event_id']]}
    paths = [global_path] + sorted((root / 'Workflow').glob('*/costs/costs.sqlite'))
    pending = {path: pending_rows(path, copy_proposals) for path in paths}
    paths = [path for path in paths if pending[path]]
    summary['ledger_changes'] = {str(path): len(pending[path]) for path in paths}
    if args.apply and paths:
        os.umask(0o077)
        backup_dir = root / '_system' / ('cost-attribution-repair-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ'))
        backup_dir.mkdir(mode=0o700)
        (backup_dir / 'plan.json').write_text(json.dumps({'proposals': copy_proposals, 'unresolved': unresolved}, indent=2))
        summary['backup_dir'] = str(backup_dir)
        summary['ledgers'] = [repair_db(path, copy_proposals, backup_dir) for path in paths]
        summary['ledgers'] = [{**item, 'changed': len(item['changed'])} for item in summary['ledgers']]
        (backup_dir / 'result.json').write_text(json.dumps(summary, indent=2))
    print(json.dumps(summary, indent=2))


if __name__ == '__main__':
    main()
