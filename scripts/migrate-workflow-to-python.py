#!/usr/bin/env python3
"""One-off same-identity plan-to-Python conversion; remove after the last workflow moves.

Inspect by default. Stop this installation's writers before --apply. Initialize
runtime references before starting a binary that requires them. No history moves,
DB table merge, timer activation or startup compatibility path is performed.
Original model, browser, MCP and selected secret names are copied from the
workflow manifest. Secret values and account logins are never copied.
"""
import argparse
import ast
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import shutil
import sqlite3
import tempfile


def encode(value):
    return (json.dumps(value, indent=2, ensure_ascii=False) + '\n').encode()


def safe_id(value):
    if not isinstance(value, str) or not value or value in ('.', '..') or any(c in value for c in '/\\'):
        raise ValueError('Choose an exact saved identity, without a path.')
    return value


def runtime_root(value, identity):
    if value not in [f'{kind}/{safe_id(identity)}' for kind in ('workflows', 'scripts', 'automations')]:
        raise ValueError('Invalid private automation runtime root.')
    return value


def contained(root, relative):
    target = root / relative
    if not target.resolve().is_relative_to(root.resolve()):
        raise ValueError('The saved path leaves its owning Crew.')
    if any(p.is_symlink() for p in [target, *target.parents]):
        raise ValueError('Resolve the linked storage path before converting.')
    return target


def database_rows(path):
    if not path.exists():
        return {}
    # Inspect a private copy, including committed WAL data. Opening mode=ro on
    # the original can still write shared-memory bookkeeping beside that file.
    with tempfile.TemporaryDirectory() as directory:
        copy = Path(directory) / 'db.sqlite'
        shutil.copy2(path, copy)
        wal = path.with_name(path.name + '-wal')
        if wal.exists():
            shutil.copy2(wal, copy.with_name(copy.name + '-wal'))
        connection = sqlite3.connect(copy.as_uri() + '?mode=ro', uri=True)
        try:
            tables = [r[0] for r in connection.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")]
            return {name: connection.execute('SELECT count(*) FROM "' + name.replace('"', '""') + '"').fetchone()[0] for name in tables}
        finally:
            connection.close()


def plan(docs, state, initialize=False, workflow_id=None, source=None):
    documents, changes, roots = {}, {}, []
    projects_file = state / 'ownership/projects.json'
    projects = json.loads(projects_file.read_text()) if projects_file.exists() else {'projects': {}}
    bindings = []
    for file in sorted((state / 'ownership/crew-executables').glob('*.json')):
        binding = json.loads(file.read_text())
        if binding.get('kind') not in ('workflow', 'script', 'relay'):
            raise ValueError('Invalid saved automation kind.')
        if binding.get('kind') == 'relay':
            continue
        identity = safe_id(binding.get('id'))
        logical = binding.get('workspace_path', '')
        if len(logical.split('/')) != 2 or not logical.startswith('Workflow/') or logical.split('/')[1] in ('', '.', '..'):
            raise ValueError('Invalid saved automation identity.')
        if file.name != hashlib.sha256(logical.encode()).hexdigest() + '.json':
            raise ValueError('The automation registry filename does not match its identity.')
        current = binding.get('runtime_root')
        if not current:
            current = ('workflows' if binding['kind'] == 'workflow' else 'scripts') + '/' + identity
            if initialize:
                binding['runtime_root'] = current
                changes[file] = encode(binding)
        runtime_root(current, identity)
        roots.append({'id': identity, 'workspace_path': logical, 'runtime_root': current, 'initialize': not json.loads(file.read_text()).get('runtime_root')})
        bindings.append((file, binding))
    if not workflow_id:
        return changes, documents, {'binding_count': len(roots), 'bindings': roots, 'conversion': None}
    matches = [(f, b) for f, b in bindings if b['id'] == workflow_id]
    if len(matches) != 1:
        raise ValueError('Choose one registered workflow ID.')
    binding_file, binding = matches[0]
    if binding['kind'] != 'workflow':
        raise ValueError('This automation is already Python; its identity must not be recreated.')
    if not binding.get('runtime_root'):
        raise ValueError('Run --initialize-runtime-roots --apply before converting this workflow.')
    owners = [(key, rec) for key, rec in projects['projects'].items() if rec.get('project_id') == binding['crew_id']]
    if len(owners) != 1:
        raise ValueError('The owning Crew is unavailable.')
    _, crew = owners[0]
    if crew.get('product') != 'work' or not crew.get('owner_id') or binding['crew_root'] != 'Crew/' + crew.get('folder', ''):
        raise ValueError('The automation does not match its owning Crew.')
    private = contained(docs, '_system/crew-data/' + safe_id(binding['crew_id']))
    files = Path(crew['local_root']).resolve() if crew.get('local_root') else contained(docs, binding['crew_root'])
    if not files.is_dir() or not private.is_dir():
        raise ValueError('The owning Crew storage is unavailable.')
    definitions = contained(files, '.agentworks/workflows/' + workflow_id)
    code = contained(files, '.agentworks/code/' + workflow_id)
    runtime = contained(private, binding['runtime_root'])
    database_root = binding['runtime_root'] + '/db'
    existing = crew.get('database_root')
    if existing and existing != database_root:
        raise ValueError(f'This Crew already uses database_root={existing}. Choose that database or migrate its tables explicitly with a backup before converting another workflow.')
    rows = database_rows(contained(private, 'db/db.sqlite')) if not existing else {}
    if any(rows.values()):
        raise ValueError('The Crew database already contains rows. Choose the authoritative database before converting; both databases are unchanged.')
    source = source.resolve()
    payload = {}
    for file in source.iterdir():
        if not file.is_file() or file.is_symlink() or (file.suffix != '.py' and file.name != 'script.json'):
            raise ValueError('The new source must contain only flat Python files and script.json.')
        raw = file.read_bytes()
        if file.suffix == '.py':
            ast.parse(raw, filename=file.name)
        payload[file.name] = raw
    definition = json.loads(payload['script.json'])
    original_capabilities = json.loads((definitions / 'workflow.json').read_text()).get('capabilities', {})
    if not isinstance(original_capabilities, dict):
        raise ValueError('The saved workflow settings must be an object.')
    proposed = definition.get('capabilities', {})
    if not isinstance(proposed, dict):
        raise ValueError('The Python source settings must be an object.')
    for name, value in proposed.items():
        if value != original_capabilities.get(name):
            raise ValueError(f'The Python source changes the saved {name} selection. Keep the original settings during conversion.')
    definition['capabilities'] = original_capabilities
    variables_file = definitions / 'variables/variables.json'
    original_inputs = json.loads(variables_file.read_text()) if variables_file.exists() else {}
    names = {item['name'] for item in definition.get('inputs', [])}
    definition.setdefault('inputs', []).extend(item for item in original_inputs.get('variables', []) if item['name'] not in names)
    presets = original_inputs.get('groups', [])
    if definition.get('input_presets', presets) != presets:
        raise ValueError('Keep the original named input presets during conversion.')
    definition['input_presets'] = presets
    payload['script.json'] = encode(definition)
    if definition.get('id') != workflow_id or 'main.py' not in payload:
        raise ValueError('The Python definition must retain the existing workflow ID and include main.py.')
    for name, raw in payload.items():
        target = code / name
        if target.exists() and target.read_bytes() != raw:
            raise ValueError('A top-level source file already exists; choose the source explicitly before replacing it: ' + name)
        changes[target] = raw
    crew['database_root'] = database_root
    changes[projects_file] = encode(projects)
    binding['kind'] = 'script'
    changes[binding_file] = encode(binding)
    documents = {'definitions': definitions, 'code': code, 'runtime': runtime, 'crew-db': private / 'db', 'crew-db-source': files / 'db'}
    report = {'binding_count': len(roots), 'bindings': roots, 'conversion': {'id': workflow_id, 'workspace_path': binding['workspace_path'], 'crew_id': binding['crew_id'], 'runtime_root': binding['runtime_root'], 'database_root': database_root, 'database_rows': database_rows(runtime / 'db/db.sqlite'), 'crew_database_rows': rows, 'old_plan_retained': True, 'carried_capability_fields': sorted(original_capabilities), 'carried_input_presets': len(presets)}}
    return changes, documents, report


def apply(changes, documents, state, backup=None):
    if not changes:
        return None
    backup = backup or state / 'migrations' / ('workflow-python-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ'))
    backup.mkdir(parents=True, exist_ok=True, mode=0o700)
    copies = dict(documents, ownership=state / 'ownership')
    # One plain snapshot for this conversion, including the existing registry.
    # --backup can reuse the operator's already prepared snapshot folder.
    for label, path in copies.items():
        if path.exists():
            if path.is_dir():
                shutil.copytree(path, backup / label, symlinks=True, dirs_exist_ok=True)
            else:
                shutil.copy2(path, backup / label)
    for path, raw in changes.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
    return str(backup)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--initialize-runtime-roots', action='store_true')
    parser.add_argument('--workflow-id')
    parser.add_argument('--source', type=Path)
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--backup', type=Path, help='One plain snapshot folder; reuse an existing operator backup if provided.')
    args = parser.parse_args()
    if bool(args.workflow_id) != bool(args.source):
        parser.error('--workflow-id and --source are required together')
    changes, documents, report = plan(args.docs.resolve(), args.state.resolve(), args.initialize_runtime_roots, args.workflow_id, args.source)
    report['changed_files'] = len(changes)
    report['backup'] = apply(changes, documents, args.state.resolve(), args.backup) if args.apply else None
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
