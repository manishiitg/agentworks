#!/usr/bin/env python3
"""PLAT-384: back up and rebuild damaged cost_events indexes, preserving rows.

Dry run by default. Run only on a confirmed cost ledger. Refuses table damage
and duplicate keys. Attribution repair is a separate operation (PLAT-377).
"""
import argparse
import hashlib
import json
import os
import sqlite3
from contextlib import closing
from datetime import datetime, timezone
from pathlib import Path


def fingerprint(db):
    # NOT INDEXED is essential: a damaged index can hide actual table rows.
    rows = db.execute('SELECT * FROM cost_events NOT INDEXED ORDER BY rowid').fetchall()
    return hashlib.sha256(json.dumps(rows).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ledger', type=Path, required=True)
    parser.add_argument('--backup-root', type=Path)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    path = args.ledger.resolve()
    uri = path.as_uri() + ('?mode=rw' if args.apply else '?mode=ro')
    with closing(sqlite3.connect(uri, uri=True, timeout=60)) as db, db:
        if args.apply:
            if not args.backup_root:
                parser.error('--backup-root is required with --apply')
            os.umask(0o077)
            backup_dir = args.backup_root.resolve() / ('cost-index-repair-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ'))
            backup_dir.mkdir(mode=0o700)
            with closing(sqlite3.connect(backup_dir / 'before.sqlite')) as backup:
                db.backup(backup)
            db.execute('BEGIN IMMEDIATE')
        errors = [row[0] for row in db.execute('PRAGMA integrity_check')]
        result = {'path': str(path), 'mode': 'apply' if args.apply else 'dry-run', 'integrity': errors}
        if errors != ['ok']:
            if not all(any(message in error for message in ('missing from index', 'wrong # of entries in index', 'non-unique entry in index')) for error in errors):
                raise RuntimeError('Non-index damage; refusing automatic rebuild')
            for field in ('event_id', 'idempotency_key'):
                duplicates = db.execute('SELECT ' + field + ',count(*) FROM cost_events NOT INDEXED GROUP BY ' + field + ' HAVING count(*)>1').fetchall()
                if duplicates:
                    raise RuntimeError('Duplicate ' + field + '; refusing rebuild')
            if args.apply:
                before = fingerprint(db)
                db.execute('REINDEX cost_events')
                if fingerprint(db) != before:
                    raise RuntimeError('Row data changed; rolling back')
                if db.execute('PRAGMA integrity_check').fetchall() != [('ok',)]:
                    raise RuntimeError('Integrity still bad; rolling back')
                result.update(backup_dir=str(backup_dir), integrity=['ok'], rows_sha256=before)
        if args.apply:
            (backup_dir / 'audit.json').write_text(json.dumps(result, indent=2))
            db.commit()
        print(json.dumps(result, indent=2))


if __name__ == '__main__':
    main()
