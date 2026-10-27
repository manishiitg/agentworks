#!/usr/bin/env python3
"""PLAT-872: real app model selection -> reply -> idle follow-up regression.

Run against an already running test server and an owned test Crew. Requires
AGENTWORKS_TEST_TOKEN (a normal authorized bearer token, never logged). Creates
and closes its own side chat; history remains available for diagnosis. This is
an HTTP acceptance check, not a browser test or a full provider certification.
"""
import argparse
import json
import os
import time
import urllib.error
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server-url', default='http://127.0.0.1:18743')
    parser.add_argument('--project-id', required=True)
    parser.add_argument('--provider', default='cursor-cli')
    parser.add_argument('--model', default='', help='Omit to exercise the shipped product default')
    args = parser.parse_args()
    token = os.environ.get('AGENTWORKS_TEST_TOKEN')
    if not token:
        parser.error('AGENTWORKS_TEST_TOKEN is required')

    def request(path, payload=None):
        headers = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}
        if payload is not None:
            headers['Idempotency-Key'] = str(uuid.uuid4())
        req = urllib.request.Request(args.server_url.rstrip('/') + path,
                                     data=None if payload is None else json.dumps(payload).encode(),
                                     headers=headers)
        try:
            with urllib.request.urlopen(req, timeout=30) as response:
                body = response.read()
                return json.loads(body) if body else {}
        except urllib.error.HTTPError as error:
            # Do not echo an arbitrary error body, which could contain chat data.
            raise RuntimeError(f'{req.method} {path} returned HTTP {error.code}') from None

    key = args.project_id + ':chat:' + str(uuid.uuid4())
    chat = request('/api/agent-profiles/work/conversation',
                   {'conversation_key': key, 'name': 'Coding chat HTTP regression'})
    session = chat['session_id']
    print('Created test chat', session, flush=True)
    try:
        request('/api/agent-profiles/work/chat-models',
                {'project_id': args.project_id, 'conversation_key': key,
                 'engine': args.provider, 'model_id': args.model})
        marker = 'CHAT_P0_' + uuid.uuid4().hex[:12]
        sequence = 0
        native = None
        for turn_index, message in enumerate([f'Remember {marker}. Reply only with that token. Do not use tools.',
                        'Reply only with the token I asked you to remember. Do not use tools.']):
            started = time.monotonic()
            admitted = request('/api/agent-profiles/work/query',
                               {'conversation_key': key, 'message': message})
            if admitted.get('session_id') != session:
                raise AssertionError('Submission changed the durable chat identity')
            deadline = time.monotonic() + 180
            completion = None
            restore_notice = False
            while time.monotonic() < deadline:
                page = request(f'/api/sessions/{session}/events?limit=1000')
                events = [e for e in page['events'] if e.get('sequence', 0) > sequence]
                for event in events:
                    # PLAT-872-32: recreating a per-turn Agent wrapper must not
                    # replay the archive notice into the continuing native chat.
                    if '[AGENTWORKS CONVERSATION CONTINUITY]' in json.dumps(event.get('data', {})):
                        restore_notice = True
                    if event.get('type') == 'conversation_error':
                        raise AssertionError('Chat emitted conversation_error; inspect this test chat')
                    if event.get('type') == 'unified_completion':
                        completion = event['data']['data']
                if completion:
                    sequence = page['latest_sequence']
                    break
                time.sleep(1)
            if completion is None:
                raise AssertionError('No completed response within 180 seconds')
            if completion.get('final_result', '').strip() != marker:
                raise AssertionError('Reply did not contain the requested/remembered token')
            if turn_index > 0 and restore_notice:
                raise AssertionError('Ordinary follow-up repeated the conversation restore notice')
            metadata = completion.get('metadata', {})
            if metadata.get('provider') != args.provider:
                raise AssertionError('The app used a different provider')
            current_native = metadata.get('native_session_id')
            if not current_native or (native and current_native != native):
                raise AssertionError('Follow-up did not retain the native session')
            native = current_native
            print('PASS reply', metadata.get('model_id'),
                  f'{time.monotonic() - started:.1f}s', flush=True)
        print('PASS product default, actual replies, idle follow-up and native continuity', flush=True)
    finally:
        try:
            request('/api/agent-profiles/work/conversation/close', {'conversation_key': key})
        except Exception:
            print('Test chat retained for inspection:', session, flush=True)


if __name__ == '__main__':
    main()
