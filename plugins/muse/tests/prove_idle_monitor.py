"""Real Muse TUI/cache proof with an isolated profile and local fake model.

Usage: python3 prove_idle_monitor.py /absolute/path/to/muse-binary
The person's profile, credentials, approval and workspace remain untouched.
This pins native model visibility and idle wake, not real-account admission.
"""

import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shlex
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

PLUGIN = Path(__file__).resolve().parents[1]
HINT = "idle-monitor-fixture"
PROMPT = "CACHED_PROMPT_BODY_FIXTURE"
STOP = "CACHED_STOP_BODY_FIXTURE"


class Fixture:
    def __init__(self, mode, command):
        self.mode, self.command = mode, command
        self.calls = []
        self.visible = {"prompt": False, "stop": False}
        self.idle, self.done = threading.Event(), threading.Event()

    def respond(self, request):
        tools = [tool for namespace in request.get("tools", [])
                 for tool in namespace.get("tools", [])]
        main = any(tool.get("name") == "monitor" for tool in tools)
        number = len(self.calls)
        if main:
            self.calls.append(request)
            source = json.dumps(request.get("input", [])) + str(request.get("instructions", ""))
            self.visible["prompt"] |= PROMPT in source
            self.visible["stop"] |= STOP in source
            if self.mode == "hooks" and all(self.visible.values()):
                self.done.set()
            if self.mode == "monitor" and number > 1 and HINT in source:
                self.done.set()
        if main and self.mode == "monitor" and number == 0:
            item = {"id": "fixture-tool", "type": "function_call", "name": "monitor",
                    "namespace": "muse", "call_id": "fixture-monitor-call", "status": "completed",
                    "arguments": json.dumps({"command": self.command, "description": "Fixture inbox",
                                             "persistent": True, "wake_delay_ms": 0, "show_lines": True})}
            added = dict(item, arguments="", status="in_progress")
            middle = [{"type": "response.function_call_arguments.delta", "delta": item["arguments"]},
                      {"type": "response.function_call_arguments.done", "arguments": item["arguments"]}]
        else:
            content = "fixture idle" if main else "{}"
            item = {"id": "fixture-message-" + str(number), "type": "message", "role": "assistant",
                    "status": "completed", "content": [{"type": "output_text", "text": content, "annotations": []}]}
            added = dict(item, status="in_progress", content=[])
            middle = [{"type": "response.content_part.added", "content_index": 0,
                       "part": {"type": "output_text", "text": "", "annotations": []}},
                      {"type": "response.output_text.delta", "content_index": 0, "delta": content},
                      {"type": "response.output_text.done", "content_index": 0, "text": content}]
        for event in middle:
            event.update(item_id=item["id"], output_index=0)
        response = {"id": "fixture-response-" + str(number), "object": "response", "created_at": 1,
                    "model": "fixture", "status": "completed", "output": [item],
                    "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
        events = [{"type": "response.created", "response": dict(response, status="in_progress", output=[])},
                  {"type": "response.output_item.added", "output_index": 0, "item": added}, *middle,
                  {"type": "response.output_item.done", "output_index": 0, "item": item},
                  {"type": "response.completed", "response": response}]
        if main and number == 1:
            self.idle.set()
        return events


def provider(fixture):
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            row = {"id": "fixture", "model_id": "fixture", "context_window": 131072, "max_output_tokens": 4096}
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"data": [row], "models": [row]}).encode())

        def do_POST(self):
            request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
            events = fixture.respond(request)
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            for number, event in enumerate(events):
                event["sequence_number"] = number
                self.wfile.write(("event: " + event["type"] + "\ndata: " + json.dumps(event) + "\n\n").encode())
            self.wfile.flush()
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


def native_tui(binary, scratch, env, server, fixture):
    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 100, 0, 0))

    def controlling_terminal():
        os.setsid()
        fcntl.ioctl(0, termios.TIOCSCTTY, 0)

    # Trust applies only to this empty temporary workspace. Sandbox defaults stay.
    command = [str(binary), "--provider", "meta", "--model", "fixture", "--base-url",
               f"http://127.0.0.1:{server.server_port}", "--workspace", str(scratch / "workspace"),
               "--trust-workspace", "--approval-judge", "off"]
    process = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave, env=env,
                               cwd=scratch / "workspace", preexec_fn=controlling_terminal)
    os.close(slave)
    started, sent, submitted = time.monotonic(), False, False
    try:
        while time.monotonic() - started < 30 and not fixture.done.is_set():
            ready, _, _ = select.select([master], [], [], .2)
            if ready:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                for query, answer in ((b"\x1b[6n", b"\x1b[1;1R"), (b"\x1b[c", b"\x1b[?1;2c"),
                                      (b"\x1b[?u", b"\x1b[?0u"),
                                      (b"\x1b]10;?", b"\x1b]10;rgb:dddd/dddd/dddd\x1b\\"),
                                      (b"\x1b]11;?", b"\x1b]11;rgb:0000/0000/0000\x1b\\")):
                    if query in chunk:
                        os.write(master, answer)
                # Approves only the fixture's harmless native command in its disposable profile.
                if b"Allow once" in chunk:
                    os.write(master, b"\r")
            if process.poll() is not None:
                break
            if not sent and time.monotonic() - started > 2:
                os.write(master, b"\x1b[200~Start the native fixture.\x1b[201~")
                sent = True
            if sent and not submitted and time.monotonic() - started > 2.5:
                os.write(master, b"\r")
                submitted = True
    finally:
        os.close(master)
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                # Native terminal teardown can outlive its reaped helper.
                pass
    assert fixture.done.is_set(), f"{fixture.mode}: model-visible proof failed ({len(fixture.calls)} requests)"


def run(binary, mode):
    with tempfile.TemporaryDirectory(prefix="agentworks-muse-idle-", dir="/tmp") as directory:
        scratch = Path(directory).resolve()
        (scratch / "workspace").mkdir()
        (scratch / "bin").mkdir()
        env = dict(os.environ, XDG_CONFIG_HOME=str(scratch / "config"), XDG_DATA_HOME=str(scratch / "data"),
                   XDG_CACHE_HOME=str(scratch / "cache"), TERM="xterm-256color",
                   PATH=str(scratch / "bin") + os.pathsep + os.environ["PATH"])
        subprocess.run([str(binary), "auth", "set", "--provider", "meta", "--api-key-stdin"],
                       input=b"fixture-key\n", env=env, capture_output=True, timeout=15, check=True)
        command = ""
        connected = threading.Event()
        if mode == "monitor":
            directory = scratch / "cache/agentworks/bridge/muse-monitor"
            directory.mkdir(parents=True, mode=0o700)
            fifo, lock = directory / "fixture.fifo", directory / "fixture.lock"
            os.mkfifo(fifo, 0o600)
            lock.touch(mode=0o600)
            script = scratch / "workspace/watch.py"
            script.write_text(
                "import os,json,pathlib,fcntl\n"
                "lock=open(" + repr(str(lock)) + ", 'rb')\n"
                "fcntl.flock(lock.fileno(),fcntl.LOCK_EX|fcntl.LOCK_NB)\n"
                "stream=open(" + repr(str(fifo)) + ", 'rb',buffering=0)\n"
                "call=os.environ.get('MUSE_TOOL_USE_ID','')\n"
                "root=pathlib.Path(os.environ['XDG_DATA_HOME'])/'muse/sessions'\n"
                "spools=list(root.glob('**/'+call+'-monitor-stdout.txt*'))\n"
                "pathlib.Path(" + repr(str(scratch / "workspace/binding.json")) + ").write_text("
                "json.dumps({'call_id':call,'spool':bool(spools)}))\n"
                "for line in stream:print(line.decode().strip(),flush=True)\n")
            command = shlex.quote(sys.executable) + " " + shlex.quote(str(script)) + " 2>&1"
        else:
            # The approved MCP sidecar must own the Unix connection which the
            # native command sandbox denies. Exercise that actual permission split.
            bridge = scratch / "cache/agentworks/bridge"
            bridge.mkdir(parents=True, mode=0o700)
            unix = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            unix.bind(str(bridge / "bridge.sock"))
            (bridge / "bridge.sock").chmod(0o600)
            unix.listen(1)
            unix.settimeout(15)
            def accept_mcp():
                try:
                    connection, _ = unix.accept()
                    connection.close()
                except OSError:
                    pass
            threading.Thread(target=accept_mcp, daemon=True).start()
            cli = scratch / "bin/agentworks"
            cli.write_text("#!" + sys.executable + "\n" +
                "import sys,json,pathlib,socket\n"
                "if sys.argv[1:]==['plugin','mcp','muse']:\n"
                " c=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM);c.connect(" + repr(str(bridge / "bridge.sock")) + ");c.close()\n"
                " pathlib.Path(" + repr(str(scratch / "workspace/mcp-socket-access")) + ").write_text('connected')\n"
                " for line in sys.stdin:\n"
                "  r=json.loads(line)\n"
                "  if 'id' not in r:continue\n"
                "  m=r.get('method')\n"
                "  out={'protocolVersion':'2024-11-05','capabilities':{'tools':{}},'serverInfo':{'name':'fixture','version':'1'}} if m=='initialize' else {'tools':[]} if m=='tools/list' else {}\n"
                "  print(json.dumps({'jsonrpc':'2.0','id':r['id'],'result':out}),flush=True)\n"
                "else:\n"
                " p=json.load(sys.stdin)\n"
                " if p['hook_event_name']=='UserPromptSubmit':print(json.dumps({'hookSpecificOutput':{'hookEventName':'UserPromptSubmit','additionalContext':" + repr(PROMPT) + "}}))\n"
                " if p['hook_event_name']=='Stop' and not p.get('stop_hook_active'):\n"
                "  marker=pathlib.Path(" + repr(str(scratch / "workspace/stop-once")) + ")\n"
                "  if not marker.exists():marker.write_text('1');print(json.dumps({'decision':'block','reason':" + repr(STOP) + "}))\n")
            cli.chmod(0o700)
            install = subprocess.run([str(binary), "plugins", "install", str(PLUGIN), "--scope", "user", "--json"],
                                     env=env, cwd=scratch / "workspace", capture_output=True, check=True, timeout=15)
            cached = Path(json.loads(install.stdout)["installed"]["cache_path"]).resolve()
            assert cached.is_relative_to(scratch), "installation escaped the disposable profile"
            subprocess.run([str(binary), "plugins", "approve", "agentworks-muse", "--json"],
                           env=env, cwd=scratch / "workspace", capture_output=True, check=True, timeout=15)
        fixture = Fixture(mode, command)
        if mode == "monitor":
            def emit():
                with fifo.open("wb", buffering=0) as writer:
                    connected.set()
                    if fixture.idle.wait(15):
                        # Let the assistant finish; only then wake the idle TUI.
                        time.sleep(.3)
                        writer.write(json.dumps({"kind": "offer", "source": "agentworks-muse-watch",
                                                 "message_id": HINT}).encode() + b"\n")
                        fixture.done.wait(15)
            threading.Thread(target=emit, daemon=True).start()
        server = provider(fixture)
        try:
            native_tui(binary, scratch, env, server, fixture)
        finally:
            server.shutdown()
            server.server_close()
            if mode == "hooks":
                unix.close()
        if mode == "monitor":
            binding = json.loads((scratch / "workspace/binding.json").read_text())
            assert binding == {"call_id": "fixture-monitor-call", "spool": True}, binding
            assert connected.is_set() and fixture.idle.is_set() and len(fixture.calls) >= 3
        else:
            assert all(fixture.visible.values()) and len(fixture.calls) >= 2
            assert (scratch / "workspace/mcp-socket-access").read_text() == "connected"
        return {"mode": mode, "model_requests": len(fixture.calls), "passed": True}


if __name__ == "__main__":
    binary = Path(sys.argv[1]).resolve(strict=True)
    print(json.dumps([run(binary, "hooks"), run(binary, "monitor")]))
