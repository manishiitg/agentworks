#!/usr/bin/env python3
"""Fold each Crew's role into its purpose, once, with a backup of every changed product.json.

A Crew used to carry a role (identity.role) and a purpose (the project description). It now has one
statement of what it is for. The role is joined in front of the purpose, then removed. Run with the
agent stopped; running it again changes nothing.
"""
import argparse
import json
import os
from pathlib import Path
import shutil
from datetime import datetime, timezone

PURPOSE_LIMIT = 1000


def folded(role, purpose):
    role, purpose = role.strip(), purpose.strip()
    if not role:
        return purpose
    if not purpose:
        return role
    if role.lower() in purpose.lower():
        return purpose
    joined = role.rstrip('.') + '. ' + purpose
    return joined if len(joined) <= PURPOSE_LIMIT else purpose


def migrate(workspace, state):
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = state / 'migrations' / ('fold-crew-role-backup-' + stamp)
    checked = changed = 0
    crew = workspace / '_system/crew-data'
    if not crew.is_dir():
        crew = workspace / 'Crew'
    for path in sorted(crew.glob('*/product.json')):
        checked += 1
        original = path.read_bytes()
        try:
            manifest = json.loads(original)
        except ValueError:
            print('Crew role: skipped unreadable', path.parent.name)
            continue
        identity = manifest.get('identity')
        if not isinstance(identity, dict) or 'role' not in identity:
            continue
        role = identity.pop('role')
        purpose = manifest.get('description')
        merged = folded(role if isinstance(role, str) else '', purpose if isinstance(purpose, str) else '')
        if merged:
            manifest['description'] = merged
        if not identity:
            del manifest['identity']
        target = backup / (path.parent.name + '.product.json')
        target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        target.write_bytes(original)
        temporary = path.with_name(path.name + '.fold-role-tmp')
        temporary.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + '\n')
        shutil.copymode(path, temporary)
        os.replace(temporary, path)
        changed += 1
    print('Crew role: checked %d Crews, folded the role of %d into the purpose%s.' % (
        checked, changed, ' (backup ' + str(backup) + ')' if changed else ''))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--workspace-root', required=True)
    parser.add_argument('--state-root', required=True)
    args = parser.parse_args()
    migrate(Path(args.workspace_root), Path(args.state_root))


if __name__ == '__main__':
    main()
