#!/usr/bin/env python3
"""One-off #2980: mark an operator-reviewed list, never infer from label text.

Audit JSON: {"entries":[{"registry":"_users/owner/chat_history/product-conversations.json",
"entry":"work/project:chat:tab", "expected_name":"Claude Code (example)",
"expected_updated_by":"tab name"}]}. Run with the agent stopped. Remove after rollout.
"""
import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import tempfile


def plain_path(root, relative):
    parts = Path(relative).parts
    if not parts or Path(relative).is_absolute() or '..' in parts:
        raise ValueError('audit registry must be relative to docs')
    if len(parts) != 4 or parts[0] != '_users' or parts[2:] != ('chat_history', 'product-conversations.json'):
        raise ValueError('audit must name an identity store')
    path = root
    for part in parts:
        path = path / part
        if path.is_symlink():
            raise ValueError('linked identity store refused')
    if not path.is_file():
        raise ValueError('audited identity store missing')
    return path


def migrate(docs, audit_path, backup_root, dry_run=False):
    docs, backup_root = Path(docs), Path(backup_root)
    if docs.is_symlink() or backup_root.is_symlink() or Path(audit_path).is_symlink():
        raise ValueError('linked migration paths refused')
    audit = json.loads(Path(audit_path).read_text())
    documents, pending, seen = {}, [], set()
    for item in audit['entries']:
        relative, entry = item['registry'], item['entry']
        if (relative, entry) in seen:
            raise ValueError('duplicate audited identity')
        seen.add((relative, entry))
        path = plain_path(docs, relative)
        if path not in documents:
            raw = path.read_bytes()
            documents[path] = (raw, json.loads(raw))
        identity = documents[path][1].get('agents', {}).get(entry)
        # Recheck the exact approved snapshot. A rename since audit is never overwritten.
        if not isinstance(identity, dict) or identity.get('name') != item['expected_name'] or identity.get('updated_by') != item['expected_updated_by']:
            raise ValueError('audited identity changed; re-audit before deployment')
        if identity.get('generated_name'):
            continue
        pending.append((path, entry, item['expected_name']))
    print('[AGENTBRIDGE] event=name_migration_audit ' + json.dumps({
        'count': len(pending), 'identities': [
            {'registry': str(path.relative_to(docs)), 'entry': entry, 'name': name}
            for path, entry, name in pending]}, ensure_ascii=False), flush=True)
    if dry_run or not pending:
        print('[AGENTBRIDGE] event=name_migration result=' + ('dry_run' if dry_run else 'unchanged'))
        return len(pending)
    stamp = datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = backup_root / ('generated-agent-names-' + stamp)
    backup.mkdir(parents=True, mode=0o700)
    changed = sorted({path for path, _, _ in pending})
    # All backups complete before the first mutation; original bytes are preserved.
    for index, path in enumerate(changed):
        target = backup / (str(index) + '.json')
        fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as stream:
            stream.write(documents[path][0]); stream.flush(); os.fsync(stream.fileno())
    for path, entry, _ in pending:
        documents[path][1]['agents'][entry]['generated_name'] = True
    for path in changed:
        fd, temporary = tempfile.mkstemp(prefix='.generated-name-', dir=path.parent)
        try:
            with os.fdopen(fd, 'w') as stream:
                json.dump(documents[path][1], stream, ensure_ascii=False, indent=2)
                stream.write('\n'); stream.flush(); os.fsync(stream.fileno())
            os.replace(temporary, path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)
    print('[AGENTBRIDGE] event=name_migration result=done count=%d backup=%s' % (len(pending), backup))
    return len(pending)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--docs-root', required=True)
    parser.add_argument('--audit', required=True)
    parser.add_argument('--backup-root', required=True)
    parser.add_argument('--dry-run', action='store_true')
    args = parser.parse_args()
    migrate(args.docs_root, args.audit, args.backup_root, args.dry_run)
