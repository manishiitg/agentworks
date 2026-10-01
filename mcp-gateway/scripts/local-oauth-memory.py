#!/usr/bin/env python3
"""Loopback OAuth test wrapper for the official local memory MCP (no real accounts).

Writes a local product/catalog config with one synthetic OAuth provider. Implements
DCR, authorization code + PKCE and short-lived rotating tokens for smoke tests.
Never deploy this fixture or point it at private/production MCP data.
"""
import argparse
import base64
import hashlib
import html
import json
import secrets
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import parse_qs, urlencode, urlparse
from urllib.request import Request, urlopen
from urllib.error import HTTPError

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--port', type=int, default=18165)
parser.add_argument('--memory-port', type=int, default=18164)
parser.add_argument('--source-config', required=True)
parser.add_argument('--output-config', required=True)
args = parser.parse_args()
origin = 'http://127.0.0.1:' + str(args.port)
config = json.loads(Path(args.source_config).read_text())
config['mcpServers']['Local OAuth Memory'] = {
    'url': origin + '/mcp',
    'oauth': {'auth_url': origin + '/authorize', 'token_url': origin + '/token',
              'registration_endpoint': origin + '/register', 'scopes': ['memory']},
}
Path(args.output_config).write_text(json.dumps(config, indent=2))
Path(args.output_config).chmod(0o600)
clients, codes, refresh_tokens, access_tokens = {}, {}, {}, {}
counts = {'authorizations': 0, 'refreshes': 0, 'authenticated_mcp_requests': 0}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass  # Authorization codes/tokens must not appear in HTTP request logs.

    def send(self, status, body, content_type='application/json'):
        data = json.dumps(body).encode() if isinstance(body, dict) else body
        self.send_response(status)
        self.send_header('Content-Type', content_type)
        self.send_header('Cache-Control', 'no-store')
        self.send_header('Content-Length', str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        path = urlparse(self.path)
        if path.path == '/health':
            return self.send(200, {'local_test_only': True, **counts})
        if path.path == '/authorize':
            q = parse_qs(path.query)
            client = q.get('client_id', [''])[0]
            redirect = q.get('redirect_uri', [''])[0]
            if client not in clients or redirect not in clients[client]:
                return self.send(400, {'error': 'invalid_request'})
            if q.get('approve', [''])[0] != 'yes':
                target = self.path + '&approve=yes'
                page = ('<h1>Local OAuth Memory</h1><p>Local test only. No real account or company data.</p>'
                        '<p>Authorize the shared platform to use the synthetic memory MCP.</p>'
                        '<a href="' + html.escape(target, quote=True) + '">Authorize local test</a>')
                return self.send(200, page.encode(), 'text/html; charset=utf-8')
            code = secrets.token_urlsafe(24)
            codes[code] = {'client': client, 'redirect': redirect, 'challenge': q.get('code_challenge', [''])[0], 'method': q.get('code_challenge_method', [''])[0]}
            counts['authorizations'] += 1
            self.send_response(302)
            self.send_header('Location', redirect + '?' + urlencode({'code': code, 'state': q.get('state', [''])[0]}))
            self.end_headers()
            return
        return self.send(404, {'error': 'not_found'})

    def do_POST(self):
        raw = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        if self.path == '/register':
            doc = json.loads(raw)
            if not all(urlparse(u).hostname in ('localhost', '127.0.0.1') for u in doc['redirect_uris']):
                return self.send(400, {'error': 'invalid_redirect_uri'})
            client = secrets.token_urlsafe(12)
            clients[client] = doc['redirect_uris']
            return self.send(201, {'client_id': client, 'redirect_uris': doc['redirect_uris'], 'token_endpoint_auth_method': 'none'})
        if self.path == '/token':
            q = parse_qs(raw.decode())
            basic = self.headers.get('Authorization', '')
            if basic.startswith('Basic '):
                client_id = base64.b64decode(basic[6:]).decode().split(':', 1)[0]
                q.setdefault('client_id', [client_id])
            if q.get('grant_type', [''])[0] == 'refresh_token':
                old = q.get('refresh_token', [''])[0]
                if old not in refresh_tokens:
                    return self.send(400, {'error': 'invalid_grant'})
                refresh_tokens.pop(old)
                counts['refreshes'] += 1
            else:
                code = codes.get(q.get('code', [''])[0])
                verifier = q.get('code_verifier', [''])[0]
                challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).rstrip(b'=').decode()
                if not code or code['method'] != 'S256' or code['challenge'] != challenge or code['client'] != q.get('client_id', [''])[0] or code['redirect'] != q.get('redirect_uri', [''])[0]:
                    return self.send(400, {'error': 'invalid_grant'})
                codes.pop(q['code'][0])
            token, refresh = secrets.token_urlsafe(24), secrets.token_urlsafe(24)
            access_tokens[token] = time.time() + 20
            refresh_tokens[refresh] = True
            return self.send(200, {'access_token': token, 'refresh_token': refresh, 'token_type': 'Bearer', 'expires_in': 20})
        if self.path == '/mcp':
            token = self.headers.get('Authorization', '').removeprefix('Bearer ')
            if access_tokens.get(token, 0) <= time.time():
                return self.send(401, {'error': 'invalid_token'})
            counts['authenticated_mcp_requests'] += 1
            req = Request('http://127.0.0.1:' + str(args.memory_port) + '/memory/mcp', data=raw, headers={k: v for k, v in self.headers.items() if k.lower() in ('content-type', 'accept', 'mcp-session-id', 'mcp-protocol-version')})
            try:
                response = urlopen(req, timeout=30)
            except HTTPError as error:
                response = error
            with response:
                return self.send(response.status, response.read(), response.headers.get('Content-Type', 'application/json'))
        return self.send(404, {'error': 'not_found'})


print('Local OAuth memory fixture on ' + origin, flush=True)
ThreadingHTTPServer(('127.0.0.1', args.port), Handler).serve_forever()
