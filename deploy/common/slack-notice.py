#!/usr/bin/env python3
"""Post short deployment notices as a bot, retaining receipts for safe retries."""
import argparse
import json
import shlex
import sys
import urllib.parse
import urllib.request
import uuid
from pathlib import Path


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--channel', required=True)
    p.add_argument('--state', required=True)
    p.add_argument('--phase', choices=['start', 'finish'], required=True)
    p.add_argument('--text', required=True)
    p.add_argument('--token-file', default='~/.config/agentworks/agentworks-slack.env')
    a = p.parse_args()
    token = None
    for line in Path(a.token_file).expanduser().read_text().splitlines():
        line = line.strip().removeprefix('export ')
        if line.startswith('SLACK_BOT_TOKEN='):
            values = shlex.split(line.split('=', 1)[1])
            token = values[0] if values else None
    if not token or not token.startswith('xoxb-'):
        raise RuntimeError('A Slack bot token is required')

    def api(method, args, post=False):
        url = 'https://slack.com/api/' + method
        data = json.dumps(args).encode() if post else None
        if not post:
            url += '?' + urllib.parse.urlencode(args)
        req = urllib.request.Request(url, data=data, headers={
            'Authorization': 'Bearer ' + token,
            'Content-Type': 'application/json; charset=utf-8',
        })
        with urllib.request.urlopen(req, timeout=10) as response:
            result = json.load(response)
        if not result.get('ok'):
            raise RuntimeError('Slack rejected the deployment notice')
        return result

    state_file = Path(a.state)
    state = json.loads(state_file.read_text()) if state_file.exists() and state_file.stat().st_size else {
        'run_id': str(uuid.uuid4()), 'channel': a.channel, 'events': {},
    }
    if state['channel'] != a.channel:
        raise RuntimeError('Deployment notice channel changed')
    event = state['events'].setdefault(a.phase, {
        'client_msg_id': str(uuid.uuid5(uuid.UUID(state['run_id']), a.phase)),
        'text': a.text,
    })
    if event['text'] != a.text:
        raise RuntimeError('Deployment notice text changed during retry')
    def find_notice(client_id, parent=None):
        cursor = ''
        while True:
            result = api('conversations.replies' if parent else 'conversations.history', {
                'channel': a.channel, 'limit': 100,
                **({'ts': parent} if parent else {}), **({'cursor': cursor} if cursor else {}),
            })
            found = next((m for m in result['messages'] if m.get('client_msg_id') == client_id), None)
            if found:
                return found['ts']
            cursor = result.get('response_metadata', {}).get('next_cursor', '')
            if not cursor:
                if result.get('has_more'):
                    raise RuntimeError('Incomplete deployment notice reconciliation')
                break
        return None

    start = state['events'].get('start', {})
    if a.phase == 'finish' and start.get('attempted') and not start.get('ts'):
        start['ts'] = find_notice(start['client_msg_id'])
        state_file.write_text(json.dumps(state) + '\n')
    parent = start.get('ts') if a.phase == 'finish' else None
    if event.get('attempted'):
        found = find_notice(event['client_msg_id'], parent)
        if found:
            event['ts'] = found
            state_file.write_text(json.dumps(state) + '\n')
            return
        if event.get('ts'):
            raise RuntimeError('A recorded notice is missing; refusing to repost it')
    event['attempted'] = True
    state_file.write_text(json.dumps(state) + '\n')
    result = api('chat.postMessage', {
        'channel': a.channel, 'text': a.text, 'client_msg_id': event['client_msg_id'],
        **({'thread_ts': parent} if parent else {}),
    }, True)
    event['ts'] = result['ts']
    state_file.write_text(json.dumps(state) + '\n')


if __name__ == '__main__':
    try:
        main()
    except Exception:
        print('Deployment notice could not be confirmed; receipt retained for reconciliation.', file=sys.stderr)
        sys.exit(1)
