#!/usr/bin/env python3
"""PLAT-856-394: replace the saved NO_SERVERS marker with the setting "Use all my
Vault apps automatically" (stored as capabilities.chosen_vault_apps_only).

Each Goal and Crew keeps today's behaviour: a list with NO_SERVERS, or with any
Vault app picked by hand, used only the chosen Vault apps, so it becomes
chosen_vault_apps_only=true without the marker; any other list used every Vault
app and stays as it is. Every changed workflow.json is copied to the backup
folder first. It runs once per installation (--done-file); afterwards people
change the setting themselves. Retired after installations migrate.
"""
import argparse
import json
import os
import shutil
from pathlib import Path

MARKER = 'NO_SERVERS'
SKIP_PARTS = {'runs', '.git', 'node_modules', 'backups', '_backup'}


def migrate(manifest):
    caps = manifest.get('capabilities')
    if not isinstance(caps, dict) or not isinstance(caps.get('selected_servers'), list):
        return False
    servers = caps['selected_servers']
    chosen_only = MARKER in servers or any(isinstance(s, str) and s.startswith('vault_') for s in servers)
    changed = False
    if MARKER in servers:
        caps['selected_servers'] = [s for s in servers if s != MARKER]
        changed = True
    if chosen_only and caps.get('chosen_vault_apps_only') is not True:
        caps['chosen_vault_apps_only'] = True
        changed = True
    return changed


def keep_owner_and_mode(source, tmp):
    # Crew records are group-shared in a setgid folder: keep the original mode, owner and group.
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
    parser.add_argument('--done-file', required=True, help='Written after a successful run; the tool then does nothing')
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    done = Path(args.done_file)
    root = Path(args.workspace_dir)
    if done.exists() or not root.is_dir():
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
        print(f'vault-apps-setting: {"would migrate" if args.dry_run else "migrated"} {rel}')
        if args.dry_run:
            continue
        backup = Path(args.backup_dir) / rel
        backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(path, backup)
        tmp = path.with_name(path.name + '.vault-apps.tmp')
        tmp.write_text(json.dumps(manifest, indent=2) + '\n')
        keep_owner_and_mode(path, tmp)
        tmp.replace(path)
    print(f'vault-apps-setting: {changed} project(s) {"to migrate" if args.dry_run else "migrated"}')
    if not args.dry_run:
        done.parent.mkdir(parents=True, exist_ok=True)
        done.write_text('done\n')


if __name__ == '__main__':
    main()
