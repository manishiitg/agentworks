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

# A failed activation selecting the old release restores the original before
# that writer restarts. A later conversion must still preserve all history.
with tempfile.TemporaryDirectory() as directory:
    root = pathlib.Path(directory)
    docs = root / 'docs'
    store = docs / '_system/agent_messages/conversations.json'
    store.parent.mkdir(parents=True)
    store.write_bytes(raw)
    converted = json.loads(subprocess.check_output([sys.executable,str(script),str(store)],text=True))
    backup = pathlib.Path(converted['backup'])
    saved_history = store.with_name('conversations.history.json').read_bytes()
    # A stale writer must not cause the next converter to erase retained history.
    store.write_text(json.dumps({'agents':[], 'conversations':[]}))
    refused = subprocess.run([sys.executable,str(script),str(store)],capture_output=True)
    assert refused.returncode != 0 and b'Refusing to overwrite existing mailbox history' in refused.stderr
    assert store.with_name('conversations.history.json').read_bytes() == saved_history
    app = root / 'app'
    old = root / 'old'
    new = root / 'new'
    app.mkdir(); old.mkdir(); new.mkdir()
    (app / 'current').symlink_to(old, target_is_directory=True)
    activation = script.parents[2] / 'deploy/common/activate-release.sh'
    # No live service is touched: only the actual recovery function runs.
    command = 'source "$1"; systemctl() { :; }; ACTIVATE_STOPPED=1; ACTIVATE_MAILBOX_BACKUP="$2"; APP_ROOT="$3"; RELEASE_DIR="$4"; DOCS_ROOT="$5"; REPO_ROOT="$6"; UNIT_PREFIX=test; activate_recover'
    subprocess.run(['bash','-c',command,'recovery-test',str(activation),str(backup),str(app),str(new),str(docs),str(script.parents[2])],check=True)
    assert json.loads(store.read_bytes()) == original
    assert not store.with_name('conversations.history.json').exists()
    # Simulate the old release reading and saving its restored full store.
    store.write_text(json.dumps(json.loads(store.read_bytes())))
    subprocess.run([sys.executable,str(script),str(store)],check=True)
    rebuilt=json.loads(store.read_bytes()); rebuilt.pop('mailbox_storage')
    rebuilt['conversations']=json.loads(store.with_name('conversations.history.json').read_bytes())
    assert rebuilt == original
print('Mailbox activation failure preservation passed')
