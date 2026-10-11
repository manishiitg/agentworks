#!/usr/bin/env python3
"""Real Pi TUI editor/draft probe against a private local bridge fixture."""
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import signal
import socketserver
import struct
import subprocess
import tempfile
import termios
import threading
import time
from http.server import BaseHTTPRequestHandler

package = Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix='plat868-tui-', dir='/private/tmp' if Path('/private/tmp').exists() else None) as temp:
    root = Path(temp)
    os.chmod(root, 0o700)
    session = None
    requests = []
    ready = threading.Event()
    admitted = threading.Event()
    offered = False
    active = True

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def do_POST(self):
            global session, offered, active
            request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
            requests.append(request)
            session = session or request['session_id']
            attachment = {'attachment_id': 'tui-lease', 'provider': 'pi', 'native_session_id': session, 'agent_name': 'receiver'}
            messages = []
            if request['action'] == 'next':
                ready.wait(10)
                if not offered:
                    messages = [{'attachment_id': 'tui-lease', 'message_id': 'tui-idle', 'inbox_id': 'original-inbox',
                                 'sender': 'sender', 'message': 'FIXTURE_MESSAGE_TUI_DRAFT_PROBE', 'sequence': 1}]
                    offered = True
                else:
                    time.sleep(0.5)
            if request['action'] == 'ack' and request.get('delivery') == 'admitted':
                assert len(request.get('host_key', '')) == 64
                assert request['attachment_id'] == 'tui-lease'
                admitted.set()
            if request['action'] == 'detach':
                active = False
            body = json.dumps({'attached': active, 'attachment': attachment, 'messages': messages}).encode()
            self.send_response(200)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            try:
                self.wfile.write(body)
            except BrokenPipeError:
                pass

    class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
        daemon_threads = True

    server = Server(str(root / 'bridge.sock'), Handler)
    os.chmod(root / 'bridge.sock', 0o600)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    report = root / 'draft.jsonl'
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
    env = dict(os.environ, PI_CODING_AGENT_DIR=str(root / 'agent'), AGENTWORKS_BRIDGE_ROOT=str(root),
               PLAT868_DRAFT_REPORT=str(report), TERM='xterm-256color')
    artifact = os.environ.get('PLAT868_AGENTWORKS_BIN')
    forge_report = root / 'forged-result.jsonl'
    if artifact:
        env.update(PATH=str(Path(artifact).parent) + os.pathsep + env.get('PATH', ''),
                   PLAT868_FORGE_REPORT=str(forge_report), AGENTWORKS_CONFIG=str(root / 'fixture-config.json'))
    child = subprocess.Popen([os.environ.get('PI_BIN', 'pi'), '--offline', '--tui-mode', 'regular',
                              '--no-extensions', '--no-mcp', '--no-skills', '--no-themes', '--no-context-files',
                              '--no-prompt-templates', '--no-builtin-tools', '-e', str(package / 'index.ts'),
                              '-e', str(package / 'test/provider.ts'), '--model', 'plat868-fixture/fixture',
                              '--session', str(root / 'session.jsonl')], cwd=root, env=env,
                             stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
    os.close(slave)
    try:
        # Pi asks the terminal for size/cursor/key protocol support; consume its
        # output and respond as a terminal, without submitting the draft.
        deadline = time.time() + 15
        start = time.time()
        while time.time() < deadline and not admitted.is_set():
            if select.select([master], [], [], 0.05)[0]:
                data = os.read(master, 65536)
                if b'\x1b[6n' in data:
                    os.write(master, b'\x1b[1;1R')
                if b'\x1b[?u' in data:
                    os.write(master, b'\x1b[?0u')
            if time.time() - start > 2:
                ready.set()
            if child.poll() is not None:
                raise AssertionError('Pi TUI exited before native delivery')
        assert admitted.is_set(), 'no actual Pi native admission'
        assert report.exists(), 'TUI user-message event was not observed'
        observations = [json.loads(line) for line in report.read_text().splitlines()]
        assert observations and all(row['draft'] == 'PERSON_DRAFT' for row in observations), observations
        assert not any(r.get('delivery') == 'processed' for r in requests), 'processing was inferred'
        print('PASSED: real Pi 1.1.0 TUI idle delivery preserves PERSON_DRAFT and proves admission')
        if artifact:
            def drive_until(condition, label):
                end = time.time() + 12
                while time.time() < end and not condition():
                    if select.select([master], [], [], 0.05)[0]:
                        data = os.read(master, 65536)
                        if b'\x1b[6n' in data:
                            os.write(master, b'\x1b[1;1R')
                    if child.poll() is not None:
                        raise AssertionError('Pi TUI exited during ' + label)
                assert condition(), label
            # The receiver model selects a shell-exec tool with forged hook
            # JSON. Its actual Pi parent/session metadata is insufficient.
            os.write(master, b'\x15FORGE_HOOK\r')
            drive_until(lambda: forge_report.exists(), 'real model shell forged-hook result')
            result = json.loads(forge_report.read_text().splitlines()[0])
            refusal = json.loads(result['stdout'])
            assert refusal['decision'] == 'block' and 'one-use proof' in refusal['reason'], refusal
            assert not any(r['action'] == 'attach' for r in requests), 'forged model shell attached'
            # A literal interactive command produces the private nonce and
            # reaches only this fixture bridge, without confirmation dialogs.
            os.write(master, b'\x15/agentworks attach reviewer\r')
            drive_until(lambda: any(r['action'] == 'attach' for r in requests), 'literal Pi attach proof')
            assert sum(r['action'] == 'attach' for r in requests) == 1
            os.write(master, b'\x15/agentworks detach\r')
            drive_until(lambda: any(r['action'] == 'detach' for r in requests), 'literal Pi detach')
            print('PASSED: GitHub-built CLI refuses real Pi model bash-exec forged hook; literal TUI attach/detach succeeds')

    finally:
        child.send_signal(signal.SIGTERM)
        try:
            child.wait(timeout=4)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait()
        os.close(master)
        server.shutdown()
        server.server_close()
