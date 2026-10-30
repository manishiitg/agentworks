#!/usr/bin/env python3
"""One-time AgentBridge receipt and read-position conversion; stop the writer first."""
import argparse
import datetime
import json
import pathlib
import shutil
import os
import tempfile


def migrate(store):
    converted = retired = 0
    for conversation in store['conversations']:
        for message in conversation.get('messages', []):
            admitted = message.pop('admitted_at', None)
            processed = message.pop('processed_at', None)
            message.pop('notice_sent', None)
            if admitted or processed:
                message['delivered_at'] = admitted or processed
            if message.get('delivery') in ('admitted', 'processed'):
                message['delivery'] = 'delivered'
                converted += 1
            if message.get('kind') == 'status' and message.get('delivery') in ('stored', 'pending'):
                message['delivery'] = 'expired'
                message['delivery_error'] = 'Automatic waiting notice retired; history kept.'
                retired += 1
            if message.get('delivery') == 'typed':
                # Terminal input alone did not prove receipt by the chat.
                message['delivery'] = 'stored'
                message.pop('live_attachment', None)
                message.pop('live_transport', None)
                message.pop('live_session', None)
    position = max([store.get('next_agent_sequence', 0)] + [
        message.get('agent_sequence', 0)
        for conversation in store['conversations']
        for message in conversation.get('messages', [])
    ])
    assigned = 0
    for conversation in store['conversations']:
        for message in conversation.get('messages', []):
            destination = conversation['endpoints'][1 - message['from']]
            if destination.get('named_agent_id') and message.get('agent_sequence', 0) <= 0:
                position += 1
                message['agent_sequence'] = position
                assigned += 1
    if position > store.get('next_agent_sequence', 0):
        store['next_agent_sequence'] = position
    return converted, retired, assigned


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('store', type=pathlib.Path)
    args = parser.parse_args()
    if not args.store.exists():
        print(f'AgentBridge receipts: {args.store}: no saved mailbox; no change')
        return
    store = json.loads(args.store.read_text())
    if store.get('mailbox_storage') == 'history-v1':
        print('AgentBridge receipts: mailbox already converted; no change')
        return
    original = json.dumps(store, sort_keys=True)
    if not isinstance(store, dict) or not isinstance(store.get('conversations'), list):
        parser.error('Expected the AgentBridge conversation store')
    converted, retired, assigned = migrate(store)
    if json.dumps(store, sort_keys=True) == original:
        print(f'AgentBridge receipts: {args.store}: already current; no change')
        return
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ')
    backup = args.store.with_name(args.store.name + '.before-receipts-' + stamp)
    shutil.copy2(args.store, backup)
    fd, temporary = tempfile.mkstemp(prefix='.agentbridge-', dir=args.store.parent)
    try:
        with os.fdopen(fd, 'w') as stream:
            stream.write(json.dumps(store, separators=(',', ':')) + '\n')
        shutil.copymode(args.store, temporary)
        os.replace(temporary, args.store)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)
    print(json.dumps({'store': str(args.store), 'backup': str(backup), 'converted_receipts': converted,
                      'retired_pending_notices': retired, 'assigned_mailbox_positions': assigned, 'history_kept': True}))


if __name__ == '__main__':
    main()
