#!/usr/bin/env python3
"""One-off conversion to workflow-owned input definitions. Review first; --apply backs up changed files."""
import argparse
import copy
import json
from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid


def has_schema_reference(value):
    # Extracting a property changes its resource scope. Do not guess how an old
    # reference should resolve in the new independent input definition.
    if isinstance(value, dict):
        return any(key in {'$ref', '$dynamicRef', '$recursiveRef'} or has_schema_reference(child)
                   for key, child in value.items())
    return isinstance(value, list) and any(has_schema_reference(child) for child in value)


def convert_saved_groups(root, split_schedule_groups=False):
    """PLAT-746-362: migrate saved callers, without a legacy runtime reader."""
    changes, errors = {}, []
    for filename, key in [('schedules.json', 'schedules'), ('functions.json', 'functions'), ('webhooks.json', 'webhooks')]:
        for file in sorted(root.rglob(filename)):
            if any(part in {'runs', '.relay_releases', '.git', 'migration-backups'} for part in file.relative_to(root).parts):
                continue
            manifest = file.parent / 'workflow.json'
            if not manifest.is_file() or json.loads(manifest.read_text()).get('kind') == 'relay':
                continue
            document = json.loads(file.read_text())
            definitions = file.parent / 'variables/variables.json'
            presets = json.loads(definitions.read_text()).get('groups', []) if definitions.exists() else []
            preset_names = [p['name'] for p in presets]
            entries, changed = [], False
            for record in document.get(key, []):
                values = record.get('input') or {}
                if 'groups' not in values:
                    entries.append(record)
                    continue
                changed = True
                groups = values.pop('groups')
                action = record.get('action', {}).get('type')
                if action == 'message':
                    # Message executors consume messages, never preset batches.
                    entries.append(record)
                    continue
                if action != 'run' or not isinstance(groups, list) or not groups or not all(isinstance(g, str) and g for g in groups):
                    errors.append(f'{file.relative_to(root)}: saved groups need an explicit run/preset decision')
                    continue
                if values.get('input_preset') or any(preset_names.count(g) != 1 for g in groups) or len(set(groups)) != len(groups):
                    errors.append(f'{file.relative_to(root)}: conflicting or unavailable saved presets')
                    continue
                if len(groups) > 1 and (key != 'schedules' or not split_schedule_groups):
                    errors.append(f'{file.relative_to(root)}: batch needs an owner decision; --split-schedule-groups preserves one timer per preset')
                    continue
                if len(groups) > 1 and (not record.get('id') or record.get('when', {}).get('dates')):
                    errors.append(f'{file.relative_to(root)}: split requires a timer identity and no dated input overrides')
                    continue
                for index, group in enumerate(groups):
                    entry = copy.deepcopy(record)
                    entry.setdefault('input', {})['input_preset'] = group
                    if index:
                        entry['id'] = str(uuid.uuid5(uuid.NAMESPACE_URL, f'agentworks:input-preset:{record["id"]}:{group}'))
                        entry['name'] = f'{record.get("name", "Scheduled run")} · {group}'
                    entries.append(entry)
            if changed:
                ids = [e.get('id') for e in entries if e.get('id')]
                if len(ids) != len(set(ids)):
                    errors.append(f'{file.relative_to(root)}: split timer identity already exists')
                document[key] = entries
                changes[file] = document
    return changes, errors


def convert(root, agent_binary=None, split_schedule_groups=False):
    changes, errors = convert_saved_groups(root, split_schedule_groups)
    for file in sorted(root.rglob('functions.json')):
        if any(part in {'runs', '.relay_releases', '.git'} for part in file.relative_to(root).parts):
            continue
        document = changes.get(file, json.loads(file.read_text()))
        definitions_file = file.parent / 'variables' / 'variables.json'
        workflow_file = file.parent / 'workflow.json'
        if not workflow_file.exists() or json.loads(workflow_file.read_text()).get('kind') == 'relay':
            continue
        functions = document.get('functions', [])
        old = [fn for fn in functions if fn.get('action', {}).get('type') == 'run' and fn.get('input_schema') is not None and not fn.get('input_mapping')]
        if not old:
            continue
        if not definitions_file.exists():
            errors.append(f'{file.relative_to(root)}: no workflow Inputs definition')
            continue
        definitions = json.loads(definitions_file.read_text())
        declared = {v['name']: v for v in definitions.get('variables', [])}
        # Conflicting contracts cannot be guessed: stop without writing anything.
        proposed = {}
        for fn in old:
            schema = fn['input_schema']
            properties = schema.get('properties', {})
            if schema.get('type') != 'object' or any(key not in {'type', 'properties', 'required', 'additionalProperties', 'description', 'title', '$schema'} for key in schema):
                errors.append(f'{file.relative_to(root)} / {fn["name"]}: root constraints need an author decision')
                continue
            for name, property_schema in properties.items():
                if name not in declared or not isinstance(property_schema, dict):
                    errors.append(f'{file.relative_to(root)} / {fn["name"]}: cannot map input {name}')
                    continue
                if has_schema_reference(property_schema):
                    errors.append(f'{file.relative_to(root)} / {fn["name"]}: references in {name} need an author decision before extraction')
                    continue
                field = copy.deepcopy(declared[name])
                field['type'] = property_schema.get('type', 'string')
                field['required'] = name in schema.get('required', [])
                field['schema'] = copy.deepcopy(property_schema)
                if 'default' in property_schema:
                    default = property_schema['default']
                    value = default if isinstance(default, str) else json.dumps(default, separators=(',', ':'))
                    if field.get('value', '') not in ('', value):
                        errors.append(f'{file.relative_to(root)} / {fn["name"]}: default conflicts for {name}')
                    field['value'] = value
                if name in proposed and proposed[name] != field or declared[name].get('schema') and declared[name]['schema'] != field['schema']:
                    errors.append(f'{file.relative_to(root)} / {fn["name"]}: conflicting definition for {name}')
                proposed[name] = field
            fn['inputs'] = list(properties)
            del fn['input_schema']
        definitions['variables'] = [proposed.get(v['name'], v) for v in definitions.get('variables', [])]
        # Validate the full merged result, including existing presets and every
        # saved function, with the same backend contract used when loading it.
        if not agent_binary:
            errors.append(f'{file.relative_to(root)}: pass --agent-binary with this refactor build to validate the proposed inputs')
        else:
            try:
                checked = subprocess.run(
                    [str(agent_binary), 'server', 'validate-workflow-input-migration'],
                    input=json.dumps({'definitions': definitions, 'functions': functions}),
                    text=True, capture_output=True, timeout=30)
                if checked.returncode:
                    # Cobra may print usage after the diagnostic. Do not echo
                    # input JSON (which is private saved data).
                    diagnostic = next((line for line in checked.stderr.splitlines() if line.startswith('Error:')), '')
                    errors.append(f'{file.relative_to(root)}: {diagnostic or "input validation failed; use the current refactor build"}')
            except (OSError, subprocess.TimeoutExpired):
                errors.append(f'{file.relative_to(root)}: could not run input validation; use --agent-binary with the current refactor build')
        changes[file] = document
        changes[definitions_file] = definitions
    # Every converted run caller must pass the actual input contract before
    # --apply can restore an enabled timer. Message-only records do not consume
    # workflow inputs; leave unrelated definitions (including credentials) alone.
    for folder in sorted({f.parent for f in changes if f.name in {'schedules.json', 'functions.json', 'webhooks.json'}}):
        callers = []
        for filename, key in [('schedules.json', 'schedules'), ('functions.json', 'functions'), ('webhooks.json', 'webhooks')]:
            file = folder / filename
            if file not in changes:
                continue
            for index, record in enumerate(changes[file].get(key, [])):
                if record.get('action', {}).get('type') == 'run':
                    callers.append(record if key == 'functions' else
                                   {'name': f'migration-{key}-{index}', 'action': {'type': 'run'}, 'input': record.get('input', {})})
        if not callers:
            continue
        definitions_file = folder / 'variables/variables.json'
        if not agent_binary or not definitions_file.is_file():
            errors.append(f'{folder.relative_to(root)}: converted run callers need definitions and --agent-binary validation')
            continue
        definitions = changes.get(definitions_file, json.loads(definitions_file.read_text()))
        try:
            checked = subprocess.run([str(agent_binary), 'server', 'validate-workflow-input-migration'],
                                     input=json.dumps({'definitions': definitions, 'functions': callers}),
                                     text=True, capture_output=True, timeout=30)
            if checked.returncode:
                diagnostic = next((line for line in checked.stderr.splitlines() if line.startswith('Error:')), 'input validation failed')
                errors.append(f'{folder.relative_to(root)}: {diagnostic}')
        except (OSError, subprocess.TimeoutExpired):
            errors.append(f'{folder.relative_to(root)}: could not validate converted run callers')
    return changes, errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True, help='Workspace docs directory to convert')
    parser.add_argument('--agent-binary', type=Path, required=True, help='Current refactor agent binary for offline validation')
    parser.add_argument('--apply', action='store_true')
    parser.add_argument('--split-schedule-groups', action='store_true',
                        help='After the owner chooses this, keep one timer per saved preset, preserving timing, caller and enabled state')
    args = parser.parse_args()
    root = args.root.resolve(strict=True)
    changes, errors = convert(root, args.agent_binary.resolve(), args.split_schedule_groups)
    if errors:
        for error in errors:
            print(error)
        raise SystemExit('No changes written. Resolve the listed definitions first.')
    print(f'{len(changes)} files ready to convert')
    if args.apply and changes:
        backup = Path(tempfile.mkdtemp(prefix='workflow-input-contracts-backup-'))
        for file, document in changes.items():
            destination = backup / file.relative_to(root)
            destination.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(file, destination)
            file.write_text(json.dumps(document, indent=2) + '\n')
        print(f'Backup: {backup}')


if __name__ == '__main__':
    main()
