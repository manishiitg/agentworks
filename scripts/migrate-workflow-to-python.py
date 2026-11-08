#!/usr/bin/env python3
"""One-off same-identity plan-to-Python conversion; remove after the last workflow moves.

Inspect by default. Stop this installation's writers before --apply. Initialize
runtime references before starting a binary that requires them. No history moves,
DB table merge, timer activation or startup compatibility path is performed.
Original model, browser, MCP and selected secret names are copied from the
workflow manifest. Secret values and account logins are never copied.

The new --source folder holds flat Python files and script.json. Its
script.json must NOT carry capabilities or input_presets: both are copied from
the saved workflow. The owning Crew database must be empty (no rows) before
converting. --adopt-into-crew needs the helper scripts that ship beside this
one (listed at start when any is missing) and moves a shared workflow only
when the Crew owner plus accepted Crew Team members cover everyone with access.
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


# One-off source cleanup for removed optional platform tools. Native tool
# selections are already supplied by the coding agent, not copied as app tools.
RETIRED_TOOLS = {
    'notify_user', 'record_summary', 'get_notification_history', 'search_web_llm',
    'image_gen', 'media.generate', 'read_image', 'files.image', 'human_feedback',
    'needs_you.ask', 'needs_you.create', 'needs_you.get', 'needs_you.answer',
    'needs_you.withdraw', 'needs_you.dismiss_duplicate', 'needs_you.mark_consumed',
    'create_human_input_request', 'get_human_input_request', 'answer_human_input_request',
    'withdraw_human_input_request', 'dismiss_duplicate_human_input_request',
    'mark_human_input_consumed', 'messages.record_summary', 'messages.notification_history',
}


def clean_agent_selections(raw):
    tree = ast.parse(raw)
    agents = {a.asname or a.name for n in ast.walk(tree) if isinstance(n, ast.ImportFrom)
              and n.module == 'agentworks' for a in n.names if a.name == 'agent'}
    modules = {a.asname or a.name for n in ast.walk(tree) if isinstance(n, ast.Import)
               for a in n.names if a.name == 'agentworks'}
    lines = raw.splitlines(keepends=True)
    offsets = [0]
    for line in lines: offsets.append(offsets[-1] + len(line))
    def span(node):
        return offsets[node.lineno-1]+node.col_offset, offsets[node.end_lineno-1]+node.end_col_offset
    edits, removed = [], []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call): continue
        function = node.func
        helper = isinstance(function, ast.Name) and function.id in agents or (
            isinstance(function, ast.Attribute) and function.attr == 'agent' and
            isinstance(function.value, ast.Name) and function.value.id in modules)
        if not helper: continue
        for keyword in node.keywords:
            if keyword.arg == 'can_ask_person':
                a,b = span(keyword)
                _,call_end = span(node)
                comma = raw.find(b',', b, call_end)
                if comma >= 0: b = comma+1
                else: a = raw.rfind(b',', span(node)[0], a)
                if a < 0: raise ValueError('Cannot remove the retired question option from this agent call.')
                edits.append((a,b,b'')); removed.append('can_ask_person')
            elif keyword.arg == 'tools' and isinstance(keyword.value, (ast.List, ast.Tuple)):
                values = keyword.value.elts
                kept = [v for v in values if not (isinstance(v, ast.Constant) and isinstance(v.value,str)
                        and v.value.split(':',1)[-1] in RETIRED_TOOLS)]
                if len(kept) != len(values):
                    removed += [v.value for v in values if v not in kept]
                    a,b = span(keyword.value)
                    replacement = '['+', '.join(ast.get_source_segment(raw.decode(),v) for v in kept)+']'
                    edits.append((a,b,replacement.encode()))
    for a,b,replacement in sorted(edits,reverse=True): raw = raw[:a]+replacement+raw[b:]
    ast.parse(raw)
    return raw, removed


def drop_retired_selections(capabilities):
    """Remove retired tool names from carried selections; returns what was removed."""
    removed = []
    for key in ('selected_tools', 'enabled_custom_tools'):
        values = capabilities.get(key)
        if not isinstance(values, list):
            continue
        kept = [v for v in values if not (isinstance(v, str) and v.split(':', 1)[-1] in RETIRED_TOOLS)]
        removed += [v for v in values if v not in kept]
        capabilities[key] = kept
    return removed


def knowledge_write_steps(definitions):
    """Old steps that could write the knowledge base. The converter cannot tell
    which hand-written agent() call replaces a step, so it only lists them."""
    steps = set()
    plan_file, config_file = definitions / 'planning/plan.json', definitions / 'planning/step_config.json'
    if plan_file.is_file():
        for step in json.loads(plan_file.read_text()).get('steps', []):
            if isinstance(step, dict) and (step.get('write_access') or {}).get('knowledgebase'):
                steps.add(step.get('id'))
    if config_file.is_file():
        for step in json.loads(config_file.read_text()).get('steps', []):
            access = (step.get('agent_configs') or {}).get('knowledgebase_access', '') if isinstance(step, dict) else ''
            if access in ('write', 'read-write'):
                steps.add(step.get('id'))
    if not steps:
        return {}
    return {'knowledge_write_steps': sorted(s for s in steps if s),
            'knowledge_write_note': "These old steps could write the knowledge base. Agents now read it by default; add knowledge='write' only to the agent() calls that replace these steps."}


def python_payload(source, workflow_id, original_capabilities, original_inputs):
    source = source.resolve()
    payload, removed_tools = {}, []
    for file in source.iterdir():
        if not file.is_file() or file.is_symlink() or (file.suffix != '.py' and file.name != 'script.json'):
            raise ValueError('The new source must contain only flat Python files and script.json.')
        raw = file.read_bytes()
        if file.suffix == '.py':
            raw, removed = clean_agent_selections(raw)
            removed_tools.extend(removed)
        payload[file.name] = raw
    definition = json.loads(payload['script.json'])
    if not isinstance(original_capabilities, dict):
        raise ValueError('The saved workflow settings must be an object.')
    original_capabilities = dict(original_capabilities)
    retired_notifications = original_capabilities.pop('notifications', {})
    for name in ('capabilities', 'input_presets'):
        if name in definition:
            raise ValueError(f'Remove {name} from the source script.json: the converter copies it from the saved workflow.')
    removed_selections = drop_retired_selections(original_capabilities)
    definition['capabilities'] = original_capabilities
    names = {item['name'] for item in definition.get('inputs', [])}
    definition.setdefault('inputs', []).extend(item for item in original_inputs.get('variables', []) if item['name'] not in names)
    definition['input_presets'] = original_inputs.get('groups', [])
    payload['script.json'] = encode(definition)
    if definition.get('id') != workflow_id or 'main.py' not in payload:
        raise ValueError('The Python definition must retain the existing workflow ID and include main.py.')
    return payload, definition, {'removed_tools': sorted(set(removed_tools) | set(removed_selections)), 'removed_notification_fields': sorted(retired_notifications)}


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
    original_capabilities = json.loads((definitions / 'workflow.json').read_text()).get('capabilities', {})
    variables_file = definitions / 'variables/variables.json'
    original_inputs = json.loads(variables_file.read_text()) if variables_file.exists() else {}
    payload, definition, removed = python_payload(source, workflow_id, original_capabilities, original_inputs)
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
    report = {'binding_count': len(roots), 'bindings': roots, 'conversion': {'id': workflow_id, 'workspace_path': binding['workspace_path'], 'crew_id': binding['crew_id'], 'runtime_root': binding['runtime_root'], 'database_root': database_root, 'database_rows': database_rows(runtime / 'db/db.sqlite'), 'crew_database_rows': rows, 'old_plan_retained': True, 'carried_capability_fields': sorted(definition['capabilities']), **removed, **knowledge_write_steps(definitions), 'carried_input_presets': len(definition['input_presets'])}}
    return changes, documents, report


def apply(changes, documents, state, backup=None, identity='runtime-roots'):
    if not changes:
        return None
    backup = backup or state / 'migrations' / ('workflow-python-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ'))
    backup = backup / safe_id(identity)
    if backup.exists() and (not backup.is_dir() or any(backup.iterdir())):
        raise ValueError('This workflow backup already contains a snapshot. Keep it and choose a new backup folder.')
    copies = dict(documents, ownership=state / 'ownership')
    for source in copies.values():
        if backup == source or source in backup.parents or backup in source.parents:
            raise ValueError('Choose a backup outside the active automation storage.')
    backup.mkdir(parents=True, exist_ok=True, mode=0o700)
    # One plain snapshot for this conversion, including the existing registry.
    # --backup can reuse the operator's already prepared snapshot folder.
    for label, path in copies.items():
        if path.exists():
            if path.is_dir():
                shutil.copytree(path, backup / label, symlinks=True)
            else:
                shutil.copy2(path, backup / label)
    for path, raw in changes.items():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(raw)
    return str(backup)


# Standalone adoption reuses the Goal-to-Crew converter, which loads these
# sibling scripts. Copy them together; a missing one fails before any read.
ADOPTION_HELPERS = ('migrate-goal-to-crew', 'migrate-project-schedules', 'migrate-browser-modes',
                    'migrate-workflow-input-contracts', 'migrate-crew-product')


def missing_adoption_helpers():
    here = Path(__file__).resolve().parent
    return [name + '.py' for name in ADOPTION_HELPERS if not (here / (name + '.py')).is_file()]


def adopt_into_crew(docs, state, workflow_id, crew_id, source, agent_binary=None, deployment_mode=None):
    if deployment_mode not in ("local", "server"):
        raise ValueError("Choose --deployment-mode local or server for this installation.")
    import importlib.util
    spec = importlib.util.spec_from_file_location('goal_to_crew', Path(__file__).with_name('migrate-goal-to-crew.py'))
    migration = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(migration)
    matches = [p.parent for p in (docs/'Workflow').glob('*/workflow.json')
               if json.loads(p.read_text()).get('id') == workflow_id]
    if len(matches) != 1:
        raise ValueError('Choose one existing standalone workflow ID. Create or choose its same-owner destination Crew first.')
    conversion = migration.Conversion(docs, state, matches[0].name, crew_id, agent_binary, deployment_mode=deployment_mode)
    if conversion.done:
        raise ValueError('This standalone workflow has already moved. Inspect its current binding before converting it again.')
    manifest = conversion.document(conversion.definitions/'workflow.json', conversion.manifest)
    original_inputs = conversion.document(conversion.definitions/'variables/variables.json', migration.read(conversion.source/'variables/variables.json', {}))
    payload, definition, removed = python_payload(source, workflow_id, manifest.get('capabilities', {}), original_inputs)
    for name, raw in payload.items():
        target = conversion.source/'code'/name
        if target.exists() and target.read_bytes() != raw:
            raise ValueError('A top-level source file already exists; choose the source explicitly before replacing it: ' + name)
        conversion.writes[conversion.code/name] = raw
    projects_file = state/'ownership/projects.json'
    projects = conversion.document(projects_file)
    crew = next(p for p in projects['projects'].values() if p.get('project_id') == crew_id)
    database_root = 'workflows/' + workflow_id + '/db'
    if crew.get('database_root') not in (None, '', database_root):
        raise ValueError('The destination Crew already adopted another database. Choose the authoritative database explicitly.')
    if any(database_rows(conversion.private/'db/db.sqlite').values()):
        raise ValueError('The Crew database already contains rows. Merge its tables explicitly before adopting this workflow.')
    crew['database_root'] = database_root
    conversion.writes[projects_file] = encode(projects)
    binding = json.loads(conversion.writes[conversion.binding_file])
    binding.update(kind='script', runtime_root='workflows/' + workflow_id)
    conversion.writes[conversion.binding_file] = encode(binding)
    conversion.counts.update(carried_input_presets=len(definition['input_presets']), python_scripts=1, **removed, **knowledge_write_steps(conversion.definitions))
    conversion.python_source = True
    return conversion


def retire_old_plan(docs, state, workflow_id):
    """List the old plan-engine files a converted workflow no longer uses.

    Only after the conversion proved itself: the binding is kind=script and at
    least one converted run completed. Removes the old planning/ definitions and
    the old plan-edit changelog, and plan revisions no converted run refers to.
    Never workflow.json, variables, code/, runs, the database or timers."""
    matches = []
    for file in sorted((state / 'ownership/crew-executables').glob('*.json')):
        binding = json.loads(file.read_text())
        if binding.get('id') == workflow_id:
            matches.append(binding)
    if len(matches) != 1:
        raise ValueError('Choose one registered workflow ID.')
    binding = matches[0]
    if binding.get('kind') != 'script' or not binding.get('runtime_root'):
        raise ValueError('Convert this workflow first: its binding is not a converted script yet.')
    runtime_root(binding['runtime_root'], workflow_id)
    projects = json.loads((state / 'ownership/projects.json').read_text())['projects']
    owners = [rec for rec in projects.values() if rec.get('project_id') == binding['crew_id']]
    if len(owners) != 1:
        raise ValueError('The owning Crew is unavailable.')
    crew = owners[0]
    private = contained(docs, '_system/crew-data/' + safe_id(binding['crew_id']))
    files = Path(crew['local_root']).resolve() if crew.get('local_root') else contained(docs, binding['crew_root'])
    definitions = contained(files, '.agentworks/workflows/' + workflow_id)
    runtime = contained(private, binding['runtime_root'])
    completed, keep = [], set()
    for meta_file in sorted((runtime / 'runs').rglob('run_metadata.json')):
        meta = json.loads(meta_file.read_text())
        # Every run keeps the plan revision it recorded, whatever its status:
        # failed and stopped runs are inspected and resumed from it too.
        pinned = [meta.get('plan_revision')] + [r.get('plan_revision') for r in (meta.get('completion_receipts') or {}).values() if isinstance(r, dict)]
        keep.update(r + '.json' for r in pinned if isinstance(r, str) and r)
        # A converted run executes the one script step named after the workflow.
        if meta.get('status') == 'completed' and meta.get('required_step_ids') == [workflow_id]:
            completed.append(str(meta_file.parent.relative_to(runtime / 'runs')))
    if not completed:
        raise ValueError('Run the converted workflow successfully at least once before retiring its old plan.')
    removals = sorted(p for p in (definitions / 'planning').rglob('*') if p.is_file()) if (definitions / 'planning').is_dir() else []
    changelog, revisions = runtime / 'planning/changelog', runtime / 'planning/revisions'
    if changelog.is_dir():
        removals += sorted(p for p in changelog.rglob('*') if p.is_file())
    if revisions.is_dir():
        removals += sorted(p for p in revisions.iterdir() if p.is_file() and p.name not in keep)
    for path in removals:
        if path.is_symlink():
            raise ValueError('Resolve the linked plan file before retiring it: ' + str(path))
    documents = {'definitions-planning': definitions / 'planning', 'runtime-planning': runtime / 'planning'}
    report = {'workflow_id': workflow_id, 'completed_converted_runs': completed, 'kept_revisions': sorted(keep),
              'removed': [str(p.relative_to(definitions if p.is_relative_to(definitions) else runtime)) for p in removals]}
    return removals, documents, report


def apply_retirement(removals, documents, state, backup, workflow_id):
    if not removals:
        return None
    backup = (backup or state / 'migrations' / ('workflow-python-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ'))) / (safe_id(workflow_id) + '-retire-old-plan')
    if backup.exists() and (not backup.is_dir() or any(backup.iterdir())):
        raise ValueError('This retirement backup already contains a snapshot. Keep it and choose a new backup folder.')
    for source in documents.values():
        if backup == source or source in backup.parents or backup in source.parents:
            raise ValueError('Choose a backup outside the active automation storage.')
    backup.mkdir(parents=True, exist_ok=True, mode=0o700)
    # One plain snapshot of both planning folders before anything is removed.
    for label, path in documents.items():
        if path.is_dir():
            shutil.copytree(path, backup / label, symlinks=True)
    for path in removals:
        path.unlink()
    for path in documents.values():
        if not path.is_dir():
            continue
        for folder in sorted([p for p in path.rglob('*') if p.is_dir()] + [path], key=lambda p: -len(p.parts)):
            if not any(folder.iterdir()):
                folder.rmdir()
    return str(backup)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--initialize-runtime-roots', action='store_true')
    parser.add_argument('--workflow-id')
    parser.add_argument('--source', type=Path)
    parser.add_argument('--adopt-into-crew', help='Adopt one standalone workflow into an existing same-owner Crew, reusing the Goal-to-Crew converter.')
    parser.add_argument('--agent-binary', type=Path, help='Current server binary for importing older chat archives and validating inputs.')
    parser.add_argument('--deployment-mode', choices=('local', 'server'), help='Required for standalone adoption: this installation runs locally or on a server.')
    parser.add_argument('--retire-old-plan', action='store_true', help='After at least one completed converted run, remove the old plan-engine files (planning/, its changelog and unused plan revisions). Never workflow.json, variables, code/, runs, the database or timers.')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--backup', type=Path, help='Backup parent folder; each workflow gets its own new snapshot, never overwriting an earlier one.')
    args = parser.parse_args()
    if args.retire_old_plan:
        if not args.workflow_id or args.source or args.adopt_into_crew or args.initialize_runtime_roots:
            parser.error('--retire-old-plan takes only --workflow-id (with --backup and --apply)')
        removals, documents, report = retire_old_plan(args.docs.resolve(), args.state.resolve(), args.workflow_id)
        report['backup'] = apply_retirement(removals, documents, args.state.resolve(), args.backup, args.workflow_id) if args.apply else None
        print(json.dumps(report, indent=2))
        return
    if bool(args.workflow_id) != bool(args.source):
        parser.error('--workflow-id and --source are required together')
    if args.adopt_into_crew:
        missing = missing_adoption_helpers()
        if missing:
            parser.error('copy these helper scripts beside this one first: ' + ', '.join(missing))
        if not args.workflow_id or args.initialize_runtime_roots or not args.deployment_mode:
            parser.error('--adopt-into-crew needs --workflow-id, --source and --deployment-mode local|server; initialize existing bindings separately')
        conversion = adopt_into_crew(args.docs.resolve(), args.state.resolve(), args.workflow_id, args.adopt_into_crew, args.source, args.agent_binary, args.deployment_mode)
        report = conversion.apply(backup=args.backup, plain_backup=True) if args.apply else conversion.report()
        print(json.dumps(report, indent=2))
        return
    changes, documents, report = plan(args.docs.resolve(), args.state.resolve(), args.initialize_runtime_roots, args.workflow_id, args.source)
    report['changed_files'] = len(changes)
    report['backup'] = apply(changes, documents, args.state.resolve(), args.backup, args.workflow_id or 'runtime-roots') if args.apply else None
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
