#!/usr/bin/env python3
"""PLAT-856-418: the stopped-writer split preserves all records and a plain backup."""
import json
import pathlib
import subprocess
import sys
import tempfile

script = pathlib.Path(__file__).resolve().parents[2] / 'scripts/migrations/split-agent-mailbox.py'
with tempfile.TemporaryDirectory() as directory:
    store = pathlib.Path(directory) / 'conversations.json'
    original = {'agents':[{'agent_id':'agent-a'}], 'next_agent_sequence':8,
                'conversations':[{'inbox_id':'inbox-a','messages':[{'message':'hello ☀','delivery':'delivered','agent_sequence':7}]}],
                'shares':[{'id':'share-a'}], 'expired':[{'inbox_id':'old'}]}
    raw = json.dumps(original).encode()
    store.write_bytes(raw)
    subprocess.run([sys.executable, str(script), str(store)], check=True)
    metadata = json.loads(store.read_bytes())
    assert metadata.pop('mailbox_storage') == 'history-v1'
    metadata['conversations'] = json.loads(store.with_name('conversations.history.json').read_bytes())
    assert metadata == original
    assert next(store.parent.glob('*.before-split-*')).read_bytes() == raw
    before = store.stat().st_mtime_ns
    subprocess.run([sys.executable, str(script), str(store)], check=True)
    assert store.stat().st_mtime_ns == before
print('Mailbox split preservation passed')
