#!/usr/bin/env python3
"""PLAT-693: migrate account audiences once, while writers are stopped.

Back up settings and supplied environment files before changing them. Never
print policy contents: environment files may contain credentials. This tool is
retired after installations migrate; the server has no product-grant fallback.
"""
import argparse
import json
import os
import re
import shlex
import shutil
from pathlib import Path


def audience(value, people):
    if value is None:
        return None
    if isinstance(value, str):
        word = value.strip().lower()
        if word in ('all', 'admins', 'everyone'):
            return 'all' if word == 'everyone' else word
        raise ValueError('Unknown account audience word.')
    if not isinstance(value, dict) or set(value) - {'admins', 'products', 'users'}:
        raise ValueError('Unknown account audience; inspect the source before migrating.')
    users = value.get('users') or []
    products = value.get('products') or []
    if not isinstance(users, list) or not all(isinstance(u, str) for u in users):
        raise ValueError('The account audience needs a list of people.')
    if not isinstance(products, list) or not all(isinstance(p, str) for p in products):
        raise ValueError('The old account audience has invalid product grants.')
    if any(p.strip() for p in products):
        # Owner decision: old product-wide grants include future company members.
        return 'all'
    identities = {}
    for person in people:
        for field in ('id', 'username', 'email'):
            if person.get(field):
                identities[person[field].strip().casefold()] = person['id']
    chosen = []
    for user in users:
        user = user.strip()
        if not user:
            continue
        canonical = identities.get(user.casefold(), user)
        if canonical.casefold() not in {u.casefold() for u in chosen}:
            chosen.append(canonical)
    return {'users': chosen} if chosen else 'admins'


def policy(value, people):
    if not isinstance(value, dict):
        raise ValueError('The provider policy must be an object.')
    result = {}
    for provider, entry in value.items():
        if not isinstance(entry, dict) or 'available_to' not in entry:
            raise ValueError('A provider policy entry is missing its audience.')
        result[provider] = dict(entry, available_to=audience(entry['available_to'], people))
    return result


def convert_env(text, people):
    assignment = re.compile(r'(?m)^[ \t]*(?:export[ \t]+)?AGENTWORKS_PROVIDER_POLICY[ \t]*=[ \t]*')
    changes = []
    for match in assignment.finditer(text):
        start, end = match.end(), match.end()
        quote = text[start:start + 1]
        if quote in ('"', "'"):
            end += 1
            while end < len(text):
                if quote == '"' and text[end] == '\\':
                    end += 2
                    continue
                if text[end] == quote:
                    end += 1
                    break
                end += 1
            else:
                raise ValueError('The provider policy environment value has an unclosed quote.')
            raw = shlex.split(text[start:end])[0]
        else:
            end = text.find('\n', start)
            if end == -1:
                end = len(text)
            raw = text[start:end].strip()
        if not raw:
            continue
        try:
            before = json.loads(raw)
        except json.JSONDecodeError:
            raise ValueError('The provider policy environment value is not JSON.') from None
        after = policy(before, people)
        if after != before:
            # Single quotes work in both shell and systemd EnvironmentFile.
            # Encode apostrophes inside JSON so the surrounding quotes stay safe.
            encoded = json.dumps(after, separators=(',', ':'), ensure_ascii=True).replace("'", '\\u0027')
            changes.append((start, end, "'" + encoded + "'"))
    for start, end, value in reversed(changes):
        text = text[:start] + value + text[end:]
    return text


def migrate(workspace, backup, env_files):
    directory = workspace / 'config/users.json'
    people = json.loads(directory.read_text()).get('users', []) if directory.is_file() else []
    settings = workspace / 'config/provider-account-settings.json'
    changes = []
    if settings.is_file():
        before = json.loads(settings.read_text())
        after = dict(before)
        if before.get('available_to') is not None:
            after['available_to'] = {key: audience(value, people) for key, value in before['available_to'].items()}
        if after != before:
            changes.append((settings, backup / 'provider-account-settings.json', json.dumps(after, indent=2) + '\n'))
    for index, path in enumerate(dict.fromkeys(env_files)):
        if not path.is_file():
            continue
        before = path.read_text()
        after = convert_env(before, people)
        if after != before:
            changes.append((path, backup / f'environment-{index}.env', after))
    # All inputs have been validated before any mutation.
    if changes:
        backup.mkdir(mode=0o700, parents=True, exist_ok=True)
        backup.chmod(0o700)
    for source, target, after in changes:
        if target.exists():
            raise ValueError('The backup already exists; use a new backup directory.')
        shutil.copy2(source, target)
        target.chmod(0o600)
        source.write_text(after)
    print(json.dumps({'event': 'account_audiences_migrated', 'changed_files': len(changes)}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--workspace-dir', required=True, type=Path)
    parser.add_argument('--backup-dir', required=True, type=Path)
    parser.add_argument('--env-file', action='append', default=[], type=Path)
    parser.add_argument('--export-current-policy', action='store_true', help='Return only the converted inherited policy for the local launcher; no credentials.')
    args = parser.parse_args()
    try:
        if args.export_current_policy:
            raw = os.environ.get('AGENTWORKS_PROVIDER_POLICY', '')
            before = json.loads(raw) if raw.strip() else {}
            directory = args.workspace_dir / 'config/users.json'
            people = json.loads(directory.read_text()).get('users', []) if directory.is_file() else []
            after = policy(before, people)
            if after != before:
                args.backup_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
                args.backup_dir.chmod(0o700)
                backup = args.backup_dir / 'inherited-policy.json'
                if not backup.exists():
                    backup.write_text(raw)
                    backup.chmod(0o600)
            print(json.dumps(after, separators=(',', ':')))
        else:
            migrate(args.workspace_dir, args.backup_dir, args.env_file)
    except (ValueError, OSError, TypeError, KeyError):
        # Do not echo an exception whose message could include source JSON.
        raise SystemExit('Account audience migration could not finish. Check the source format and migration options; no policy or credential values were logged.') from None
