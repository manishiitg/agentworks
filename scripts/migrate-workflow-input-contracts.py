#!/usr/bin/env python3
"""One-off conversion to workflow-owned input definitions. Review first; --apply backs up changed files."""
import argparse
import copy
import json
from pathlib import Path
import shutil
import subprocess
import tempfile


def has_schema_reference(value):
    # Extracting a property changes its resource scope. Do not guess how an old
    # reference should resolve in the new independent input definition.
    if isinstance(value, dict):
        return any(key in {'$ref', '$dynamicRef', '$recursiveRef'} or has_schema_reference(child)
                   for key, child in value.items())
    return isinstance(value, list) and any(has_schema_reference(child) for child in value)


def convert(root, agent_binary=None):
    changes, errors = {}, []
    for file in sorted(root.rglob('functions.json')):
        if any(part in {'runs', '.relay_releases', '.git'} for part in file.relative_to(root).parts):
            continue
        document = json.loads(file.read_text())
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
    return changes, errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True, help='Workspace docs directory to convert')
    parser.add_argument('--agent-binary', type=Path, required=True, help='Current refactor agent binary for offline validation')
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    root = args.root.resolve(strict=True)
    changes, errors = convert(root, args.agent_binary.resolve())
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
