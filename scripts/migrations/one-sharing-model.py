#!/usr/bin/env python3
"""PLAT-856-393: one sharing model. Goals and Flows keep Owners and the people who
can use them (readers); the editor tier is gone. Run while writers are stopped.

Each person keeps today's power: a Goal's editors could build and change it, so
they become owners; a Flow's editors could only run it, so they become people who
can use it. Every changed workflow.json is copied to the backup folder first.
Only records that still list editors change, so running it again is a no-op.
This tool is retired after installations migrate; the server has no editor tier.
"""
import argparse
import json
import os
import shutil
from pathlib import Path

SKIP_PARTS = {'runs', '.git', 'node_modules', 'backups', '_backup'}


def merged(first, extra, exclude=()):
    out = []
    for person in list(first or []) + list(extra or []):
        if isinstance(person, str) and person and person not in out and person not in exclude:
            out.append(person)
    return out


def migrate(manifest):
    access = manifest.get('access')
    if not isinstance(access, dict) or 'editors' not in access:
        return False
    editors = access.pop('editors') or []
    owners = access.get('owners') or []
    if manifest.get('kind') == 'relay':
        access['readers'] = merged(access.get('readers'), editors, exclude=owners)
    else:
        access['owners'] = merged(owners, editors)
        access['readers'] = merged(access.get('readers'), [], exclude=access['owners'])
    return True


def keep_owner_and_mode(source, tmp):
    # Records may be group-shared in a setgid folder: keep the original mode, owner and group.
    shutil.copymode(source, tmp)
    st = source.stat()
    try:
        os.chown(tmp, st.st_uid, st.st_gid)
    except PermissionError:
        try:
            os.chown(tmp, -1, st.st_gid)
        except PermissionError:
            pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-dir', required=True, help='Workspace documents root')
    parser.add_argument('--backup-dir', required=True, help='Where to copy each changed workflow.json first')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    root = Path(args.workspace_dir)
    if not root.is_dir():
        return
    changed = 0
    for path in sorted(root.rglob('workflow.json')):
        if SKIP_PARTS & set(path.relative_to(root).parts[:-1]):
            continue
        try:
            manifest = json.loads(path.read_text())
        except (OSError, ValueError):
            continue
        if not isinstance(manifest, dict) or not migrate(manifest):
            continue
        changed += 1
        rel = path.relative_to(root)
        print(f'one-sharing-model: {"would migrate" if args.dry_run else "migrated"} {rel}')
        if args.dry_run:
            continue
        backup = Path(args.backup_dir) / rel
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, backup)
        tmp = path.with_name(path.name + '.one-sharing.tmp')
        tmp.write_text(json.dumps(manifest, indent=2) + '\n')
        keep_owner_and_mode(path, tmp)
        tmp.replace(path)
    print(f'one-sharing-model: {changed} project(s) {"to migrate" if args.dry_run else "migrated"}')


if __name__ == '__main__':
    main()
