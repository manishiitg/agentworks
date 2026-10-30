#!/usr/bin/env python3
"""Split mailbox metadata and history once. Stop the agent writer before applying."""
import argparse
import datetime
import json
import os
import pathlib
import shutil
import tempfile


def atomic(path, raw, mode):
    fd, name = tempfile.mkstemp(prefix='.mailbox-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(name, mode)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('store', type=pathlib.Path)
    parser.add_argument('--restore-backup', type=pathlib.Path)
    args = parser.parse_args()
    path = args.store
    if args.restore_backup:
        backup = args.restore_backup
        if backup.parent.resolve() != path.parent.resolve() or not backup.name.startswith(path.name + '.before-split-'):
            parser.error('Expected this mailbox original backup')
        original = backup.read_bytes()
        restored = json.loads(original)
        if 'mailbox_storage' in restored or not isinstance(restored.get('conversations'), (list, type(None))):
            parser.error('Expected an original unsplit mailbox backup')
        atomic(path, original, backup.stat().st_mode & 0o777)
        path.with_name('conversations.history.json').unlink(missing_ok=True)
        print(json.dumps({'restored':True, 'backup':str(backup)}))
        return
    if not path.exists():
        print(json.dumps({'converted':False, 'reason':'no mailbox'}))
        return
    original = path.read_bytes()
    store = json.loads(original)
    if store.get('mailbox_storage') == 'history-v1':
        json.loads(path.with_name('conversations.history.json').read_bytes())
        print(json.dumps({'converted':False, 'reason':'already split'}))
        return
    history = store.pop('conversations')
    history_path = path.with_name('conversations.history.json')
    if history_path.exists():
        existing = json.loads(history_path.read_bytes())
        if existing and existing != history:
            parser.error('Refusing to overwrite existing mailbox history; restore the .before-split backup before converting')
    if history is not None and not isinstance(history, list):
        parser.error('Expected the full mailbox conversation list')
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ')
    backup = path.with_name(path.name + '.before-split-' + stamp)
    shutil.copy2(path, backup)
    mode = path.stat().st_mode & 0o777
    # History first, metadata last. No writer may run between these operations.
    atomic(path.with_name('conversations.history.json'), json.dumps(history, separators=(',', ':')).encode(), mode)
    store['mailbox_storage'] = 'history-v1'
    atomic(path, json.dumps(store, separators=(',', ':')).encode(), mode)
    print(json.dumps({'converted':True, 'backup':str(backup), 'conversations':len(history or []), 'history_preserved':True}))


if __name__ == '__main__':
    main()
