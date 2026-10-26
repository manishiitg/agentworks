#!/usr/bin/env python3
"""PLAT-746-149: remove execution refs from Builder tabs; retain all history."""
import argparse
import json
from pathlib import Path
import shutil


def is_run(session):
    # Repair only the external execution identity observed in the incident.
    return session.startswith('pat-') and '-run-' in session


def repair(root, backup=None, check=False):
    root = Path(root).resolve()
    paths = [root / 'chat_history/product-conversations.json']
    paths += sorted((root / '_users').glob('*/chat_history/product-conversations.json'))
    counts = {'event': 'workflow_builder_tab_repair', 'registries_changed': 0,
              'run_tab_refs_removed': 0, 'check': check}
    for path in paths:
        if not path.is_file() or path.is_symlink():
            continue
        doc = json.loads(path.read_text())
        entries = doc.get('entries') or {}
        if not isinstance(entries, dict):
            raise ValueError('Invalid conversation registry entries')
        removed = [key for key, row in entries.items()
                   if row.get('profile_id') == 'agentworks'
                   and str(row.get('workspace_path', '')).startswith('Workflow/')
                   and is_run(str(row.get('session_id', '')))
                   and row.get('title') == 'Builder'
                   and row.get('conversation_key', '').startswith(str(row.get('resource_id', '')) + ':chat:')
                   and key == 'agentworks/' + row['conversation_key']]
        if not removed:
            continue
        counts['registries_changed'] += 1
        counts['run_tab_refs_removed'] += len(removed)
        if check:
            continue
        if backup is None:
            raise ValueError('A backup directory is required')
        saved = Path(backup) / path.relative_to(root)
        saved.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        shutil.copy2(path, saved)
        for key in removed:
            del entries[key]
        # Agents/Previous metadata and every session/transcript file stay intact.
        path.write_text(json.dumps(doc, indent=2, ensure_ascii=False) + '\n')
    return counts


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-dir', type=Path, required=True)
    parser.add_argument('--backup-dir', type=Path)
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    if not args.check and args.backup_dir is None:
        parser.error('--backup-dir is required when applying the repair')
    print(json.dumps(repair(args.workspace_dir, args.backup_dir, args.check)))
