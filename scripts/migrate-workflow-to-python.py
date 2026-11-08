#!/usr/bin/env python3
"""One-off same-identity plan-to-Python conversion; remove after the last workflow moves.

Inspect by default. Stop this installation's writers before --apply. Initialize
runtime references before starting a binary that requires them. No history moves,
DB table merge, timer activation or startup compatibility path is performed.
Original model, browser, MCP and selected secret names are copied from the
workflow manifest. Secret values and account logins are never copied.
--entry-inputs FILE supplies explicit maps keyed by existing schedules/webhooks/
functions IDs. Entries specify input_preset and inputs; raw hooks may specify
allowed_variables and payload_mappings.inputs. Optional input_presets add named
presets without replacing the carried ones. Use --agent-binary to validate the
candidate with the same server contract. No route-to-input choice is inferred;
unmapped old routes/steps are listed. Message reminders stay unchanged. Omitting
--source applies only that mapping to an already converted workflow.
Existing outbound Crew links are carried to the owning Crew before its scripts
start calling as that Crew; inspect lists the exact targets and refuses missing ones.

The new --source folder holds flat Python files, script.json and optional
agent_mocks.json (agent test fixtures) and requirements.txt. Requirements are
carried unchanged. Inspect reports their hashes and Python import names for
the author to check; it never guesses package names/versions or installs them. Its
script.json must NOT carry capabilities or input_presets: both are copied from
the saved workflow. The owning Crew database must have no business rows before converting; platform
run-log rows are appended to the adopted database after the plain backup. --adopt-into-crew needs the helper scripts that ship beside this
one (listed at start when any is missing) and moves a shared workflow only
when the Crew owner plus accepted Crew Team members cover everyone with access.
"""
import argparse
import ast
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import tempfile


def encode(value):
    return (json.dumps(value, indent=2, ensure_ascii=False) + '\n').encode()


def safe_id(value):
    if not isinstance(value, str) or not value or value in ('.', '..') or any(c in value for c in '/\\'):
        raise ValueError('Choose an exact saved identity, without a path.')
    return value


def carried_crew_links(manifest, projects, private, document):
    """A converted script calls as its Crew; retain only its existing links."""
    def references(value):
        capabilities = value.get('capabilities') or {}
        if not isinstance(capabilities, dict):
            raise ValueError('Repair this item\'s Linked projects before converting; its settings cannot be read.')
        refs = value.get('workflow_context_paths') or capabilities.get('workflow_context_paths') or []
        if not isinstance(refs, list) or any(not isinstance(ref, str) or not ref.strip() for ref in refs):
            raise ValueError('Repair this workflow\'s Linked projects before converting; its saved links cannot be read.')
        return list(dict.fromkeys(ref.strip().rstrip('/') for ref in refs))

    outgoing = references(manifest)
    if not outgoing:
        return [], None, None
    for ref in outgoing:
        matches = [project for project in projects.values()
                   if project.get('product') in ('work', 'code') and 'Crew/' + project.get('folder', '') == ref]
        if len(matches) != 1:
            raise ValueError('Restore or update this existing Linked project before converting: ' + ref + '. Nothing was changed.')
    target = private / 'workflow.json'
    if not target.exists():
        target = private / 'product.json'
    contained(private, str(target.relative_to(private)))
    try:
        saved = document(target)
    except (OSError, ValueError):
        raise ValueError('Open the destination Crew first; its Linked projects settings cannot be read. Nothing was changed.') from None
    if not isinstance(saved, dict):
        raise ValueError('Open the destination Crew first; its Linked projects settings are unavailable. Nothing was changed.')
    # Same target paths; no function definition, scope, sharing or account edits.
    saved['workflow_context_paths'] = list(dict.fromkeys(references(saved) + outgoing))
    return outgoing, target, encode(saved)


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


def output_check_steps(definitions):
    """Old agent steps whose output was validated and repaired automatically. The
    hand-written source must check the output itself and give the agent one
    repair turn; the converter only lists these steps."""
    steps = set()
    for name in ('planning/plan.json', 'planning/step_config.json'):
        file = definitions / name
        if file.is_file():
            for step in json.loads(file.read_text()).get('steps', []):
                if isinstance(step, dict) and step.get('validation_schema'):
                    steps.add(step.get('id'))
    if not steps:
        return {}
    return {'output_check_steps': sorted(s for s in steps if s),
            'output_check_note': 'These old steps had their output checked and repaired automatically. In the Python source, check each matching agent reply; on a failed check send the error once to the same agent (conversation_id=reply.conversation_id), check again, and fail plainly if it still fails.'}


def reasoning_steps(definitions):
    """Report explicit effort choices; hand-written calls must preserve their
    matching old step, rather than silently inheriting a different tier."""
    choices = []
    def visit(value, source):
        if isinstance(value, list):
            for item in value:
                visit(item, source)
        elif isinstance(value, dict):
            config = value.get('agent_configs') or value
            model = config.get('execution_llm') or {}
            effort = (model.get('options') or {}).get('reasoning_effort')
            if value.get('id') and effort:
                choices.append({'step': value['id'], 'reasoning': effort,
                                'model': model.get('provider', '') + '/' + model.get('model_id', ''),
                                'source': source})
            for key, item in value.items():
                if key != 'agent_configs':
                    visit(item, source)
    for name in ('planning/plan.json', 'planning/step_config.json'):
        file = definitions / name
        if file.is_file():
            visit(json.loads(file.read_text()), name)
    if not choices:
        return {}
    return {'reasoning_steps': choices,
            'reasoning_note': 'Keep each listed effort on its matching agent() call with reasoning="low", "medium" or "high" and the same model. Unsupported levels/models refuse by name; choose an offered level explicitly instead of dropping the old choice.'}


def dependency_inventory(code, payload):
    """Names and hashes only; never execute source or read linked files."""
    existing, requirements = {}, []
    for directory, folders, names in os.walk(code, followlinks=False):
        if Path(directory).is_symlink():
            folders.clear()
            continue
        folders[:] = sorted(name for name in folders if not (Path(directory) / name).is_symlink())
        for name in sorted(names):
            file = Path(directory) / name
            if file.is_symlink() or not file.is_file():
                continue
            relative = file.relative_to(code).as_posix()
            if file.suffix == '.py':
                existing[relative] = file.read_bytes()
            elif name == 'requirements.txt':
                requirements.append({'path': relative, 'sha256': hashlib.sha256(file.read_bytes()).hexdigest()})

    def imports(files):
        roots, unparsed = set(), 0
        paths = [Path(name) for name in files]
        top_modules = {p.parts[0].removesuffix('.py') for p in paths}
        for name, raw in files.items():
            local = top_modules | {p.stem for p in paths if p.parent == Path(name).parent}
            try:
                tree = ast.parse(raw)
            except (SyntaxError, ValueError):
                unparsed += 1
                continue
            imported = {alias.name.split('.')[0] for node in ast.walk(tree)
                        if isinstance(node, ast.Import) for alias in node.names}
            imported.update(node.module.split('.')[0] for node in ast.walk(tree)
                            if isinstance(node, ast.ImportFrom) and not node.level and node.module)
            roots.update(imported - local - sys.stdlib_module_names - {'agentworks', 'agentworks_db'})
        return sorted(roots), unparsed

    authored, authored_unparsed = imports({name: raw for name, raw in payload.items() if name.endswith('.py')})
    old, existing_unparsed = imports(existing)
    authored_requirements = {'present': 'requirements.txt' in payload}
    if authored_requirements['present']:
        authored_requirements['sha256'] = hashlib.sha256(payload['requirements.txt']).hexdigest()
    return {'authored_requirements': authored_requirements, 'existing_requirements': sorted(requirements, key=lambda item: item['path']),
            'authored_import_roots': authored, 'existing_import_roots': old,
            'unparsed_python_files': authored_unparsed + existing_unparsed,
            'note': 'Import names are an inventory, not package names. Confirm the packages and versions in the authored requirements.txt. Existing requirements are not copied automatically; the runtime validates and installs the declaration.'}


def python_payload(source, workflow_id, original_capabilities, original_inputs):
    source = source.resolve()
    payload, removed_tools = {}, []
    for file in source.iterdir():
        if not file.is_file() or file.is_symlink() or (file.suffix != '.py' and file.name not in ('script.json', 'agent_mocks.json', 'requirements.txt')):
            raise ValueError('The new source must contain only flat Python files, script.json and optional agent_mocks.json and requirements.txt.')
        raw = file.read_bytes()
        if file.suffix == '.py':
            raw, removed = clean_agent_selections(raw)
            removed_tools.extend(removed)
        payload[file.name] = raw
    definition = json.loads(payload['script.json'])
    if not isinstance(original_capabilities, dict):
        raise ValueError('The saved workflow settings must be an object.')
    original_capabilities = dict(original_capabilities)
    retired_notifications = dict(original_capabilities.pop('notifications', {}) or {})
    carried_mail_binding = {}
    for old, current in (('gmail_connection_id', 'gmail_connection_id'), ('block_recipients', 'gmail_blocked_recipients')):
        if old in retired_notifications:
            value = retired_notifications.pop(old)
            if (old == 'gmail_connection_id' and not isinstance(value, str)) or (old == 'block_recipients' and (not isinstance(value, list) or any(not isinstance(item, str) for item in value))):
                raise ValueError('The saved mail sender and blocked recipients must be account references and address lists, without credentials.')
            if current in original_capabilities and original_capabilities[current] != value:
                raise ValueError('The saved mail sender or blocked recipients conflict; choose the intended binding before converting.')
            original_capabilities[current] = value
            carried_mail_binding[current] = value
    for name in ('capabilities', 'input_presets'):
        if name in definition:
            raise ValueError(f'Remove {name} from the source script.json: the converter copies it from the saved workflow.')
    removed_selections = drop_retired_selections(original_capabilities)
    definition['capabilities'] = original_capabilities
    selected_secrets = original_capabilities.get('selected_global_secret_names') or []
    declared = definition.get('secrets', [])
    if not isinstance(selected_secrets, list) or not isinstance(declared, list) or any(not isinstance(name, str) for name in [*selected_secrets, *declared]):
        raise ValueError('Script secrets and saved Vault selections must be lists of names, without values.')
    definition['secrets'] = sorted(set(declared + [name.split('__', 1)[1] if name.startswith('VLT_') and '__' in name else name for name in selected_secrets]))
    names = {item['name'] for item in definition.get('inputs', [])}
    definition.setdefault('inputs', []).extend(item for item in original_inputs.get('variables', []) if item['name'] not in names)
    definition['input_presets'] = original_inputs.get('groups', [])
    payload['script.json'] = encode(definition)
    if definition.get('id') != workflow_id or 'main.py' not in payload:
        raise ValueError('The Python definition must retain the existing workflow ID and include main.py.')
    return payload, definition, {'removed_tools': sorted(set(removed_tools) | set(removed_selections)), 'removed_notification_fields': sorted(retired_notifications), 'carried_mail_binding': carried_mail_binding}


def remap_entry_inputs(definition, changes, runtime, document, mapping=None, agent_binary=None):
    """One-off explicit data conversion; never infer a route or enable an entry."""
    mapping = mapping or {}
    if not isinstance(mapping, dict) or set(mapping) - {'input_presets', 'schedules', 'webhooks', 'functions'}:
        raise ValueError('Entry inputs need input_presets and maps keyed by existing schedules, webhooks or functions IDs.')
    presets = definition.setdefault('input_presets', [])
    for preset in mapping.get('input_presets', []):
        if not isinstance(preset, dict) or not isinstance(preset.get('name'), str) or not isinstance(preset.get('values'), dict):
            raise ValueError('Each additional input preset needs a name and saved values.')
        previous = [p for p in presets if p['name'] == preset['name']]
        if previous and previous != [preset]:
            raise ValueError('An input preset already exists with different values: ' + preset['name'])
        if not previous:
            presets.append(preset)
    report, callers = {'mapped_entry_inputs': [], 'unmapped_entry_inputs': []}, []
    for kind in ('schedules', 'webhooks', 'functions'):
        file = runtime / (kind + '.json')
        doc = document(file, {kind: []})
        selected = mapping.get(kind, {})
        if not isinstance(selected, dict):
            raise ValueError(kind + ' mappings must be keyed by existing entry IDs.')
        ids = [entry.get('id') for entry in doc.get(kind, [])]
        if set(selected) - set(ids) or any(ids.count(identity) != 1 for identity in selected):
            raise ValueError(kind + ' mapping does not match one existing entry ID.')
        changed = False
        for entry in doc.get(kind, []):
            identity = entry.get('id')
            if identity in selected:
                update = selected[identity]
                if entry.get('action', {}).get('type') != 'run':
                    raise ValueError('Only run entries can map script inputs; keep message reminders unchanged.')
                if not isinstance(update, dict) or set(update) - {'input_preset', 'inputs', 'allowed_variables', 'payload_mappings'}:
                    raise ValueError('Entry mappings may change only input_preset, inputs and payload input configuration.')
                saved = dict(entry.get('input') or {})
                saved.pop('routes', None)
                saved.pop('groups', None)
                saved['input_preset'] = update.get('input_preset', saved.get('input_preset', ''))
                saved['inputs'] = update.get('inputs', saved.get('inputs', {}))
                entry['input'] = saved
                entry['action'].pop('step', None)
                config = dict(entry.get('input_mapping') or {})
                payload = dict(config.get('payload') or {})
                for retired in ('routes', 'step', 'group'):
                    payload.pop(retired, None)
                if payload:
                    config['payload'] = payload
                else:
                    config.pop('payload', None)
                if 'payload_mappings' in update:
                    payload = update['payload_mappings']
                    if not isinstance(payload, dict) or set(payload) - {'inputs'}:
                        raise ValueError('Converted payload mappings may fill declared inputs only; never choose routes, steps or presets.')
                    config.update(mode='raw', payload=payload)
                if 'allowed_variables' in update:
                    config['variables'] = update['allowed_variables']
                if config:
                    entry['input_mapping'] = config
                exposed = list(config.get('variables') or [])
                mapped = (config.get('payload') or {}).get('inputs') or {}
                if set(mapped) - set(exposed):
                    raise ValueError('Mapped raw inputs must be listed in allowed_variables.')
                callers.append({'name': kind + ':' + str(identity), 'action': {'type': 'run'}, 'input': saved,
                                'inputs': entry.get('inputs', []), **({'input_mapping': config} if config else {})})
                report['mapped_entry_inputs'].append({'kind': kind, 'id': identity})
                changed = True
            else:
                payload = (entry.get('input_mapping') or {}).get('payload') or {}
                if (entry.get('input') or {}).get('routes') or entry.get('action', {}).get('step') or any(payload.get(k) for k in ('routes', 'step', 'group')):
                    report['unmapped_entry_inputs'].append({'kind': kind, 'id': identity, 'enabled': entry.get('enabled', False)})
        if changed:
            changes[file] = encode(doc)
    if mapping:
        if not agent_binary:
            raise ValueError('Pass --agent-binary with the current build to validate mapped inputs before any write.')
        candidate = {'definitions': {'variables': definition.get('inputs', []), 'groups': presets}, 'functions': callers}
        checked = subprocess.run([str(agent_binary), 'server', 'validate-workflow-input-migration'],
                                 input=json.dumps(candidate), text=True, capture_output=True, timeout=30)
        if checked.returncode:
            diagnostic = next((line for line in checked.stderr.splitlines() if line.startswith('Error:')), 'Input validation failed; use the current server build.')
            raise ValueError(diagnostic)
    return report


def map_saved_entry_inputs(docs, state, workflow_id, mapping, agent_binary):
    matches = [json.loads(file.read_text()) for file in (state/'ownership/crew-executables').glob('*.json')]
    bindings = [b for b in matches if b.get('id') == workflow_id and b.get('kind') == 'script']
    if len(bindings) != 1:
        raise ValueError('Choose one already converted Python workflow for --entry-inputs without --source.')
    binding = bindings[0]
    projects = json.loads((state/'ownership/projects.json').read_text())['projects']
    crews = [p for p in projects.values() if p.get('project_id') == binding['crew_id'] and p.get('product') == 'work']
    if len(crews) != 1:
        raise ValueError('The owning Crew is unavailable.')
    crew = crews[0]
    files = Path(crew['local_root']).resolve() if crew.get('local_root') else contained(docs, binding['crew_root'])
    runtime_root(binding.get('runtime_root'), workflow_id)
    runtime = contained(docs, '_system/crew-data/' + safe_id(binding['crew_id']) + '/' + binding['runtime_root'])
    code = contained(files, '.agentworks/code/' + safe_id(workflow_id))
    definition = json.loads((code/'script.json').read_text())
    changes = {}
    report = remap_entry_inputs(definition, changes, runtime, lambda f, default: json.loads(f.read_text()) if f.exists() else default, mapping, agent_binary)
    changes[code/'script.json'] = encode(definition)
    return changes, {'code': code, 'runtime': runtime}, report


def plan(docs, state, initialize=False, workflow_id=None, source=None, entry_inputs=None, agent_binary=None):
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
    rows = crew_platform_rows(contained(private, 'db/db.sqlite'), runtime / 'db/db.sqlite')['tables'] if not existing else {}
    original_manifest = json.loads((definitions / 'workflow.json').read_text())
    original_capabilities = original_manifest.get('capabilities', {})
    variables_file = definitions / 'variables/variables.json'
    original_inputs = json.loads(variables_file.read_text()) if variables_file.exists() else {}
    payload, definition, removed = python_payload(source, workflow_id, original_capabilities, original_inputs)
    entry_report = remap_entry_inputs(definition, changes, runtime, lambda f, default: json.loads(f.read_text()) if f.exists() else default, entry_inputs, agent_binary)
    payload['script.json'] = encode(definition)
    links, links_file, links_data = carried_crew_links(original_manifest, projects['projects'], private,
                                                    lambda file: json.loads(file.read_text()) if file.exists() else None)
    if links_file is not None:
        changes[links_file] = links_data
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
    if links_file is not None:
        documents['crew-links'] = links_file
    report = {'binding_count': len(roots), 'bindings': roots, 'conversion': {'id': workflow_id, 'workspace_path': binding['workspace_path'], 'crew_id': binding['crew_id'], 'runtime_root': binding['runtime_root'], 'database_root': database_root, 'database_rows': database_rows(runtime / 'db/db.sqlite'), 'crew_database_rows': rows, 'appended_crew_platform_rows': sum(rows.values()), 'old_plan_retained': True, 'carried_capability_fields': sorted(definition['capabilities']), **removed, **knowledge_write_steps(definitions), **output_check_steps(definitions), **reasoning_steps(definitions), 'carried_input_presets': len(definition['input_presets'])}}
    report['conversion'].update(entry_report)
    report['conversion']['dependencies'] = dependency_inventory(code, payload)
    report['conversion']['carried_crew_links'] = links
    return changes, documents, report


def apply(changes, documents, state, backup=None, identity='runtime-roots', append_platform_rows=False):
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
    skipped = []
    for label, path in copies.items():
        if path.exists():
            if path.is_dir():
                def ignore_cache(directory, names):
                    # Same disposable paths as the standalone Goal converter.
                    ignored = [name for name in names if name in ('.tmp', '.cache', '.sandbox-cache') or name.startswith('.scratch-')]
                    skipped.extend(label + '/' + str((Path(directory) / name).relative_to(path)) for name in ignored)
                    return ignored
                shutil.copytree(path, backup / label, symlinks=True, ignore=ignore_cache)
            else:
                shutil.copy2(path, backup / label)
    (backup / 'skipped.json').write_bytes(encode(sorted(skipped)))
    if append_platform_rows:
        target = {'path': documents['runtime'] / 'db/db.sqlite'}
        source = crew_platform_rows(documents['crew-db'] / 'db.sqlite', target['path'])
        if any(source['tables'].values()):
            target['path'].parent.mkdir(parents=True, exist_ok=True)
            merge_database_tables(target, [source])
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


def adopt_into_crew(docs, state, workflow_id, crew_id, source, agent_binary=None, deployment_mode=None, entry_inputs=None):
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
    entry_report = remap_entry_inputs(definition, conversion.writes, conversion.runtime,
        lambda f, default: conversion.document(f, migration.read(conversion.source/f.relative_to(conversion.runtime), default)), entry_inputs, agent_binary)
    payload['script.json'] = encode(definition)
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
    conversion.counts.update(carried_input_presets=len(definition['input_presets']), python_scripts=1, **removed, **knowledge_write_steps(conversion.definitions), **output_check_steps(conversion.definitions), **reasoning_steps(conversion.definitions))
    links, links_file, links_data = carried_crew_links(manifest, projects['projects'], conversion.private, conversion.document)
    if links_file is not None:
        conversion.writes[links_file] = links_data
    conversion.carried_crew_links = links
    conversion.counts['carried_crew_links'] = len(links)
    conversion.counts.update(entry_report)
    conversion.counts['dependencies'] = dependency_inventory(conversion.source/'code', payload)
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


def table_rows(path):
    """Row counts per table, read from a private copy (see database_rows)."""
    return database_rows(path) if path.exists() else {}


# Tables the server creates itself. When the same one has rows in several
# databases with exactly the same columns, its rows are appended (new row ids)
# instead of refusing; a business table clash always refuses.
PLATFORM_TABLES = {'background_agent_log', 'run_concerns', 'org_dashboard_notifications', 'report_human_inputs',
                   'report_human_input_events', 'eval_results', 'schema_migration_log'}


def table_columns(path):
    if not path.exists():
        return {}
    with tempfile.TemporaryDirectory() as directory:
        copy = Path(directory) / 'db.sqlite'
        shutil.copy2(path, copy)
        wal = path.with_name(path.name + '-wal')
        if wal.exists():
            shutil.copy2(wal, copy.with_name(copy.name + '-wal'))
        connection = sqlite3.connect(copy.as_uri() + '?mode=ro', uri=True)
        try:
            names = [r[0] for r in connection.execute("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")]
            return {n: [tuple(c[1:]) for c in connection.execute('PRAGMA table_info("' + n.replace('"', '""') + '")')] for n in names}
        finally:
            connection.close()


def single_db_plan(docs, state):
    """Plan one database per Crew (owner decision, 2026-11-08).

    Per Crew: when only one of the Crew database and its workflow databases has
    rows, point database_root at it (empty means the Crew's own database). When
    several have rows, copy the other tables into the largest one; refuse that
    Crew, naming the tables, when a table with rows exists in both. Nothing is
    deleted; the old files stay as history."""
    projects = json.loads((state / 'ownership/projects.json').read_text())['projects']
    receipt_file = state / 'migrations/single-db-receipt.json'
    receipts = json.loads(receipt_file.read_text()) if receipt_file.exists() else {}
    bindings = {}
    for file in sorted((state / 'ownership/crew-executables').glob('*.json')):
        binding = json.loads(file.read_text())
        if binding.get('kind') not in ('workflow', 'script'):
            continue
        root = binding.get('runtime_root') or (('workflows' if binding['kind'] == 'workflow' else 'scripts') + '/' + safe_id(binding['id']))
        runtime_root(root, binding['id'])
        bindings.setdefault(binding.get('crew_id'), []).append((binding['id'], root + '/db'))
    crews, merges = [], []
    for key, crew in sorted(projects.items()):
        if crew.get('product') != 'work' or not crew.get('project_id'):
            continue
        private = docs / '_system/crew-data' / safe_id(crew['project_id'])
        if not private.is_dir():
            continue
        dbs = [{'name': 'crew', 'database_root': '', 'path': private / 'db/db.sqlite'}]
        for workflow_id, root in sorted(bindings.get(crew['project_id'], [])):
            dbs.append({'name': workflow_id, 'database_root': root, 'path': contained(private, root + '/db.sqlite')})
        for db in dbs:
            db['tables'] = table_rows(db['path'])
            db['columns'] = table_columns(db['path'])
            db['rows'] = sum(db['tables'].values())
        with_data = [db for db in dbs if db['rows']]
        current = crew.get('database_root') or ''
        entry = {'crew': key, 'crew_id': crew['project_id'], 'current_database_root': current,
                 'databases': [{'name': d['name'], 'database_root': d['database_root'], 'exists': d['path'].exists(), 'rows': d['rows'], 'tables': d['tables']} for d in dbs]}
        selected = next((d for d in dbs if d['database_root'] == current), None)
        # All readers and writers now use this choice. Other DBs are frozen
        # history, never fresh input to another append. An unset Crew default
        # needs the apply receipt to distinguish it from a first merge.
        completed = bool(current) or key in receipts and receipts[key]['database_root'] == current
        if completed:
            if selected is None:
                entry["action"] = "refused: selected database is not one of this Crew's databases"
            else:
                entry['action'] = 'nothing: already uses ' + selected['name']
            crews.append(entry)
            continue
        if not with_data:
            entry['action'] = 'nothing: no database has rows'
        else:
            target = max(with_data, key=lambda d: (d['rows'], d['database_root'] == current, d['name'] == 'crew'))
            sources = [d for d in with_data if d is not target]
            clashes, appended, seen = set(), {}, {t: target for t in target['tables']}
            for d in sources:
                for t, n in d['tables'].items():
                    if not n:
                        continue
                    if t in seen:
                        if t in PLATFORM_TABLES and d['columns'].get(t) == seen[t]['columns'].get(t):
                            appended[t] = appended.get(t, 0) + n
                        else:
                            clashes.add(t + (' (platform table columns differ)' if t in PLATFORM_TABLES else ''))
                    else:
                        seen[t] = d
            clashes = sorted(clashes)
            if appended:
                entry['append_platform_rows'] = appended
            if clashes:
                entry['action'] = 'refused: tables with rows exist in more than one database: ' + ', '.join(clashes)
            else:
                entry['target'] = target['name']
                entry['target_database_root'] = target['database_root']
                entry['copy_tables'] = {d['name']: sorted(t for t, n in d['tables'].items() if n) for d in sources}
                if sources:
                    entry['action'] = f"copy {sum(len(v) for v in entry['copy_tables'].values())} table(s) into {target['name']} and use it"
                elif target['database_root'] == current:
                    entry['action'] = 'nothing: already uses the only database with rows'
                else:
                    entry['action'] = 'use ' + target['name']
                if sources or target['database_root'] != current:
                    merges.append((key, crew, target, sources))
        crews.append(entry)
    return merges, crews


def crew_platform_rows(source, target):
    rows = database_rows(source)
    if any(n for name, n in rows.items() if name not in PLATFORM_TABLES):
        raise ValueError('The Crew database already contains rows. Choose the authoritative database before converting; both databases are unchanged.')
    columns, target_columns = table_columns(source), table_columns(target)
    for table, count in rows.items():
        if count and table in target_columns and columns[table] != target_columns[table]:
            raise ValueError('Platform table columns differ: ' + table + '; both databases are unchanged.')
    return {'path': source, 'tables': rows, 'columns': columns}


def merge_database_tables(target, sources):
    connection = sqlite3.connect(target['path'])
    try:
        for index, source in enumerate(sources):
            alias = f'src{index}'
            connection.execute(f"ATTACH DATABASE ? AS {alias}", (str(source['path']),))
            for table in sorted(t for t, n in source['tables'].items() if n):
                quoted = '"' + table.replace('"', '""') + '"'
                exists = connection.execute("SELECT 1 FROM main.sqlite_master WHERE type='table' AND name=?", (table,)).fetchone()
                if exists:
                    # A platform table with identical columns (checked at plan time):
                    # append its rows, letting the target assign new row ids.
                    columns = source['columns'][table]
                    keep = [c[0] for c in columns if not (c[4] == 1 and c[1].upper() == 'INTEGER')]
                    names = ', '.join('"' + c.replace('"', '""') + '"' for c in keep)
                    connection.execute(f'INSERT INTO main.{quoted} ({names}) SELECT {names} FROM {alias}.{quoted}')
                    continue
                create = connection.execute(f"SELECT sql FROM {alias}.sqlite_master WHERE type='table' AND name=?", (table,)).fetchone()[0]
                connection.execute(create)
                connection.execute(f'INSERT INTO main.{quoted} SELECT * FROM {alias}.{quoted}')
                for (sql,) in connection.execute(f"SELECT sql FROM {alias}.sqlite_master WHERE type='index' AND tbl_name=? AND sql IS NOT NULL", (table,)).fetchall():
                    connection.execute(sql)
            connection.commit()
            connection.execute(f'DETACH DATABASE {alias}')
    finally:
        connection.close()


def apply_single_db(merges, docs, state, backup):
    if not merges:
        return None
    backup = (backup or state / 'migrations' / ('single-db-' + datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')))
    if backup.exists() and (not backup.is_dir() or any(backup.iterdir())):
        raise ValueError('This backup folder already contains a snapshot. Keep it and choose a new backup folder.')
    backup.mkdir(parents=True, exist_ok=True, mode=0o700)
    projects_file = state / 'ownership/projects.json'
    shutil.copy2(projects_file, backup / 'projects.json')
    projects = json.loads(projects_file.read_text())
    receipt_file = state / 'migrations/single-db-receipt.json'
    receipts = json.loads(receipt_file.read_text()) if receipt_file.exists() else {}
    for key, crew, target, sources in merges:
        if sources:
            # One plain copy of the target database (and its WAL) before writing to it.
            target_backup = backup / safe_id(crew['project_id'])
            target_backup.mkdir(mode=0o700)
            for suffix in ('', '-wal', '-shm'):
                file = target['path'].with_name(target['path'].name + suffix)
                if file.exists():
                    shutil.copy2(file, target_backup / file.name)
            merge_database_tables(target, sources)
        record = projects['projects'][key]
        if target['database_root']:
            record['database_root'] = target['database_root']
        else:
            record.pop('database_root', None)
        receipts[key] = {'database_root': target['database_root'], 'backup': str(backup)}
    projects_file.write_bytes(encode(projects))
    receipt_file.parent.mkdir(parents=True, exist_ok=True)
    receipt_file.write_bytes(encode(receipts))
    return str(backup)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--initialize-runtime-roots', action='store_true')
    parser.add_argument('--workflow-id')
    parser.add_argument('--source', type=Path)
    parser.add_argument('--entry-inputs', type=Path, help='Explicit JSON maps keyed by existing entry IDs; optional additional presets. Preserves entry identity, state, timing, auth and caller. Without --source, maps one already converted workflow.')
    parser.add_argument('--adopt-into-crew', help='Adopt one standalone workflow into an existing same-owner Crew, reusing the Goal-to-Crew converter.')
    parser.add_argument('--agent-binary', type=Path, help='Current server binary for importing older chat archives and validating inputs.')
    parser.add_argument('--deployment-mode', choices=('local', 'server'), help='Required for standalone adoption: this installation runs locally or on a server.')
    parser.add_argument('--retire-old-plan', action='store_true', help='After at least one completed converted run, remove the old plan-engine files (planning/, its changelog and unused plan revisions). Never workflow.json, variables, code/, runs, the database or timers.')
    parser.add_argument('--single-db', action='store_true', help='One database per Crew: point each Crew at its only database with rows, or copy the other tables into the largest one (refusing on a table-name clash). Inspect by default; nothing is deleted.')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--backup', type=Path, help='Backup parent folder; each workflow gets its own new snapshot, never overwriting an earlier one.')
    args = parser.parse_args()
    if args.entry_inputs and (args.single_db or args.retire_old_plan or not args.workflow_id):
        parser.error('--entry-inputs needs --workflow-id, separately from database or plan retirement')
    if args.single_db:
        if args.workflow_id or args.source or args.adopt_into_crew or args.initialize_runtime_roots or args.retire_old_plan:
            parser.error('--single-db takes no other step (only --backup and --apply)')
        merges, crews = single_db_plan(args.docs.resolve(), args.state.resolve())
        report = {'crews': crews, 'backup': apply_single_db(merges, args.docs.resolve(), args.state.resolve(), args.backup) if args.apply else None}
        print(json.dumps(report, indent=2, default=str))
        return
    if args.retire_old_plan:
        if not args.workflow_id or args.source or args.adopt_into_crew or args.initialize_runtime_roots:
            parser.error('--retire-old-plan takes only --workflow-id (with --backup and --apply)')
        removals, documents, report = retire_old_plan(args.docs.resolve(), args.state.resolve(), args.workflow_id)
        report['backup'] = apply_retirement(removals, documents, args.state.resolve(), args.backup, args.workflow_id) if args.apply else None
        print(json.dumps(report, indent=2))
        return
    entry_inputs = json.loads(args.entry_inputs.read_text()) if args.entry_inputs else None
    if entry_inputs is not None and args.workflow_id and not args.source and not args.adopt_into_crew:
        changes, documents, report = map_saved_entry_inputs(args.docs.resolve(), args.state.resolve(), args.workflow_id, entry_inputs, args.agent_binary)
        report['backup'] = apply(changes, documents, args.state.resolve(), args.backup, args.workflow_id + '-entry-inputs') if args.apply else None
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
        conversion = adopt_into_crew(args.docs.resolve(), args.state.resolve(), args.workflow_id, args.adopt_into_crew, args.source, args.agent_binary, args.deployment_mode, entry_inputs)
        report = conversion.apply(backup=args.backup, plain_backup=True) if args.apply else conversion.report()
        print(json.dumps(report, indent=2))
        return
    changes, documents, report = plan(args.docs.resolve(), args.state.resolve(), args.initialize_runtime_roots, args.workflow_id, args.source, entry_inputs, args.agent_binary)
    report['changed_files'] = len(changes)
    report['backup'] = apply(changes, documents, args.state.resolve(), args.backup, args.workflow_id or 'runtime-roots', append_platform_rows=bool((report.get('conversion') or {}).get('appended_crew_platform_rows'))) if args.apply else None
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
