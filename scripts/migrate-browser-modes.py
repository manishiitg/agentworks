#!/usr/bin/env python3
"""PLAT-788: consolidate browser choices while project writers are stopped.

Back up every changed manifest and retired sidecar. Keep until all installations
have moved; this is not a runtime compatibility reader.
"""
import argparse
import json
from pathlib import Path
import shutil


def projects(root):
    bases = [root] + [p for p in (root / '_users').glob('*') if p.is_dir() and not p.is_symlink()]
    for base in bases:
        for folder in (base / 'Workflow', base / 'Crew', base / 'Chats/Work/projects'):
            if folder.is_symlink():
                continue
            for project in sorted(folder.glob('*')):
                if project.is_dir() and not project.is_symlink():
                    yield project


def convert(mode, deployment):
    if mode in ('', 'auto', None):
        return 'extension'
    if mode == 'headless' and deployment == 'local':
        return 'extension'
    if mode == 'cdp' and deployment == 'server':
        return 'headless'
    if mode in ('extension', 'none', 'headless', 'cdp'):
        return mode
    raise ValueError('Unknown browser mode; inspect project settings before continuing')


def migrate(root, backup, deployment, state):
    selection_file = state / 'chrome-extension/selected.json'
    selected = json.loads(selection_file.read_text()) if selection_file.is_file() else {}
    selected_paths = set()
    for key, label in selected.items():
        user = key.split('\x00', 1)[0]
        selected_paths.add(label.strip('/'))
        selected_paths.add(f'_users/{user}/{label.strip("/")}')
    changes = []
    for project in projects(root):
        manifest_path = project / 'workflow.json'
        sidecar = project / '.browser-settings.json'
        if not manifest_path.is_file() or manifest_path.is_symlink() or sidecar.is_symlink():
            continue
        before = json.loads(manifest_path.read_text())
        after = json.loads(json.dumps(before))
        caps = after.setdefault('capabilities', {})
        saved = json.loads(sidecar.read_text()) if sidecar.is_file() else {}
        # An explicit off state must never become enabled during conversion.
        mode = 'none' if caps.get('browser_mode') == 'none' else saved.get('mode', caps.get('browser_mode'))
        if mode != 'none' and project.relative_to(root).as_posix() in selected_paths:
            mode = 'extension'
        caps['browser_mode'] = convert(mode, deployment)
        port = saved.get('port', caps.get('browser_cdp_port'))
        if isinstance(port, int) and 1 <= port <= 65535:
            caps['browser_cdp_port'] = port
        if after != before or sidecar.is_file():
            changes.append((manifest_path, sidecar, after))
    # Validate all source records before writing; backups contain no secret stores.
    for manifest_path, sidecar, after in changes:
        for source in (manifest_path, sidecar):
            if source.is_file():
                target = backup / source.relative_to(root)
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source, target)
        manifest_path.write_text(json.dumps(after, indent=2, ensure_ascii=False) + '\n')
        if sidecar.is_file():
            sidecar.unlink()
        print(json.dumps({'event': 'browser_mode_converted', 'manifest': str(manifest_path.relative_to(root)), 'mode': after['capabilities']['browser_mode']}))
    print(json.dumps({'event': 'browser_modes_conversion', 'deployment_mode': deployment, 'changed_files': len(changes)}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-dir', type=Path, required=True)
    parser.add_argument('--backup-dir', type=Path, required=True)
    parser.add_argument('--state-root', type=Path, required=True)
    parser.add_argument('--deployment-mode', choices=('local', 'server'), required=True)
    args = parser.parse_args()
    migrate(args.workspace_dir, args.backup_dir, args.deployment_mode, args.state_root)
