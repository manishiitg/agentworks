#!/usr/bin/env python3
"""One-off #2825 cleanup. Preview by default; explicit --apply backs up first.
Run as the server operator with the exact work-folder-access.json path. Never
removes folders, assignments, missing ordinary work folders or user files.
"""
import argparse
import datetime
import json
import os
from pathlib import Path
import shutil
import tempfile


def internal(path):
    for candidate in (Path(path), Path(path).resolve()):
        parts = candidate.parts
        if any(part in ('cli-runtimes', 'workflow-cli-runtimes', '.sandbox-cache') for part in parts):
            return True
        if any(parts[i] == 'slots' and parts[i+1] in ('state', 'run', 'home') for i in range(len(parts)-1)):
            return True
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('file', type=Path)
    parser.add_argument('--apply', action='store_true')
    args = parser.parse_args()
    source = args.file
    if source.is_symlink():
        raise SystemExit('Choose the real configuration file, not a symlink')
    original = source.read_bytes()
    doc = json.loads(original)
    removed = 0
    for user, grants in doc.get('grants', {}).items():
        keep = [grant for grant in grants if not internal(grant.get('path', ''))]
        removed += len(grants) - len(keep)
        doc['grants'][user] = keep
    print(f'internal_grants={removed} action={"apply" if args.apply else "preview"}')
    if not args.apply or not removed:
        return
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S.%fZ')
    backup = source.with_name(source.name + '.backup-' + stamp)
    shutil.copy2(source, backup)
    if source.read_bytes() != original:
        raise SystemExit('Configuration changed; nothing replaced')
    info = source.stat()
    fd, temporary = tempfile.mkstemp(prefix=source.name + '.', dir=source.parent)
    try:
        os.fchmod(fd, info.st_mode & 0o777)
        os.fchown(fd, info.st_uid, info.st_gid)
        with os.fdopen(fd, 'w') as out:
            json.dump(doc, out, indent=2)
            out.write('\n')
            out.flush()
            os.fsync(out.fileno())
        os.replace(temporary, source)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print('backup_created=true outcome=ok')


if __name__ == '__main__':
    main()
