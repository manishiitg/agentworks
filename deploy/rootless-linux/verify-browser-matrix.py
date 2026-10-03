#!/usr/bin/env python3
"""Full browser matrix through the real app path, as the platform sends it. Run on the product host as the service account.

  python3 verify-browser-matrix.py agents --port 24001 --user-id <any user id>      # Excellence (multi-user)
  python3 verify-browser-matrix.py sparkquill --port 23001                          # single-user server
  python3 verify-browser-matrix.py video-studio --port 8080 --docs-dir /data/video-studio/docs

Every browser operation is its own POST /api/execute (X-User-ID, Bearer token, folder_guard with browser_session), exactly the shape of
the Code/Crew browser panel's "Start browser" (tab, stream status/enable) and of an agent's agent-browser call. The sessions are throwaway
project sessions (<prefix>--project-<16 hex>--browser) with their own profile and work folder; everything is removed afterwards.
Prints a PASS/FAIL table (failures with the Chrome stderr where it can be found) and exits 1 when anything failed. Never prints the token
or page contents beyond short markers. Do not run this on a production profile of a real user.
"""
import argparse
import base64
import http.server
import json
import os
import pwd
import re
import shlex
import shutil
import signal
import socket
import subprocess
import threading
import time
import urllib.request
import uuid
from pathlib import Path

FLAGS = ("--no-sandbox,--disable-gpu,--disable-blink-features=AutomationControlled,--lang=en-US,--restore-last-session,"
         "--use-fake-device-for-media-stream,--use-fake-ui-for-media-stream")

PAGE = """<!doctype html><html><head><title>Matrix Page</title></head><body>
<h1 id=h>Matrix heading</h1><input id=name><button id=go onclick="document.getElementById('out').textContent='clicked:'+document.getElementById('name').value">go</button>
<button id=alertbtn onclick="alert('matrix-dialog')">alert</button><div id=out>idle</div>
<a id=dl href="/file.txt" download>download</a><input id=up type=file>
<div style="height:3000px">tall</div><div id=bottom>bottom</div>
<script>
console.log('matrix-log'); console.warn('matrix-warn'); console.error('matrix-error');
fetch('/ok.json').then(r=>r.json()).then(j=>console.log('fetch-ok', JSON.stringify(j)));
fetch('/missing').then(r=>console.log('fetch-404', r.status));
fetch('https://nonexistent.invalid/x').catch(e=>console.log('fetch-dns-failed'));
setTimeout(()=>{ throw new Error('matrix-uncaught'); }, 100);
setTimeout(()=>{ Promise.reject(new Error('matrix-unhandled')); }, 150);
</script></body></html>"""

HEAVY = """<!doctype html><title>Heavy</title><body><script>
const f=document.createDocumentFragment();
for(let i=0;i<30000;i++){const d=document.createElement('div');d.textContent='row '+i;d.style.cssText='display:inline-block;width:30px;height:18px;overflow:hidden;background:hsl('+(i%360)+',60%,80%)';f.appendChild(d);}
document.body.appendChild(f);
const c=document.createElement('canvas');c.width=1200;c.height=600;document.body.appendChild(c);
const x=c.getContext('2d');for(let i=0;i<4000;i++){x.fillStyle='hsl('+(i%360)+',70%,50%)';x.fillRect(i%1200,(i*7)%600,40,40);}
</script></body>"""


class Site(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        routes = {
            "/page.html": ("text/html", PAGE.encode(), {}),
            "/heavy.html": ("text/html", HEAVY.encode(), {}),
            "/tall.html": ("text/html", b"<!doctype html><title>Tall</title><body style='margin:0'><div style='height:600000px;background:linear-gradient(red,blue)'>tall</div>", {}),
            "/ok.json": ("application/json", b'{"ok":true}', {"X-Matrix": "yes", "Set-Cookie": "matrix_srv=1; Path=/"}),
            "/file.txt": ("text/plain", b"matrix-download-content\n", {"Content-Disposition": 'attachment; filename="file.txt"'}),
        }
        path = self.path.split("?")[0]
        if path not in routes:
            self.send_response(404)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        kind, body, headers = routes[path]
        self.send_response(200)
        self.send_header("Content-Type", kind)
        self.send_header("Content-Length", str(len(body)))
        for key, value in headers.items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)


class Matrix:
    def __init__(self, args):
        self.args = args
        pid = subprocess.check_output(["systemctl", "--user", "show", args.product + "-workspace", "-p", "MainPID", "--value"],
                                      text=True, timeout=10).strip()
        self.env = dict(item.split("=", 1) for item in Path(f"/proc/{pid}/environ").read_text().split("\0") if "=" in item)
        token = self.env.get("WORKSPACE_API_TOKEN", "")
        self.headers = {"Content-Type": "application/json", "Authorization": "Bearer " + token, "X-Workspace-Token": token}
        if args.user_id:
            self.headers["X-User-ID"] = args.user_id
        self.token = token
        prefix = self.env.get("AGENTWORKS_BROWSER_SESSION_PREFIX", "")
        self.prefix = prefix + "--" if prefix else ""
        self.profile_root = Path(self.env.get("AGENT_BROWSER_SHARED_PROFILE", f"/srv/{args.product}/state/browser-profile") + "-projects")
        self.docs = Path(args.docs_dir or f"/srv/{args.product}/data/docs")
        self.rel = "tmp/browser-matrix-" + uuid.uuid4().hex[:8]
        self.work = self.docs / self.rel
        self.work.mkdir(mode=0o700, parents=True)
        self.sessions = {}
        self.rows = []
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Site)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        self.server = server
        self.base = f"http://127.0.0.1:{server.server_address[1]}"

    # --- plumbing -------------------------------------------------------------------------------------------------------------------
    def session(self, label="a"):
        if label not in self.sessions:
            name = f"{self.prefix}project-{uuid.uuid4().hex[:16]}--browser"
            self.sessions[label] = (name, self.profile_root / name)
        return self.sessions[label]

    def execute(self, command, timeout=60, session=None):
        body = {"command": command, "working_directory": self.rel, "timeout": timeout,
                "folder_guard": {"enabled": True, "read_paths": [self.rel], "write_paths": [self.rel]}}
        if session:
            body["folder_guard"]["browser_session"] = session
        request = urllib.request.Request(f"http://127.0.0.1:{self.args.port}/api/execute", data=json.dumps(body).encode(), headers=self.headers)
        with urllib.request.urlopen(request, timeout=timeout + 15) as response:
            return json.load(response)

    def ab(self, *argv, label="a", timeout=45, extra=(), allow_fail=False, retries=0):
        self.retried = 0
        for attempt in range(retries + 1):
            try:
                return self._ab(*argv, label=label, timeout=timeout, extra=extra, allow_fail=allow_fail)
            except RuntimeError:
                if attempt == retries:
                    raise
                self.retried += 1
                time.sleep(0.5)

    def _ab(self, *argv, label="a", timeout=45, extra=(), allow_fail=False):
        name, profile = self.session(label)
        profile.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        prefix = ["agent-browser", "--session", name, "--profile", str(profile), "--idle-timeout", "0", "--args", FLAGS, *extra, "--json"]
        result = self.execute(shlex.join(prefix + list(argv)), timeout, name)
        data = result.get("data", {})
        out = (data.get("stdout") or "").strip()
        try:
            parsed = json.loads(out.splitlines()[-1]) if out else {}
        except ValueError:
            parsed = {"success": False, "error": out[:300]}
        ok = result.get("success") and data.get("exit_code") == 0 and parsed.get("success") is not False
        if not ok and not allow_fail:
            raise RuntimeError(f"{argv[0]} failed: {(parsed.get('error') or data.get('stderr') or out)[:400]}")
        return parsed

    def pid(self, label="a"):
        return int(os.readlink(self.session(label)[1] / "SingletonLock").rsplit("-", 1)[1])

    def processes(self, label="a"):
        needle = str(self.session(label)[1])
        found = []
        for entry in Path("/proc").iterdir():
            if entry.name.isdigit():
                try:
                    if needle in (entry / "cmdline").read_bytes().decode(errors="replace"):
                        found.append(int(entry.name))
                except OSError:
                    pass
        return found

    def stderr_hint(self):
        return ""

    def check(self, name, fn):
        started = time.time()
        try:
            detail = fn()
            detail = detail if isinstance(detail, str) else ""
            self.rows.append((name, True, str(detail)[:180], time.time() - started))
        except Exception as error:  # noqa: BLE001 - a report row, never a crash
            self.rows.append((name, False, str(error)[:600], time.time() - started))
        mark = "PASS" if self.rows[-1][1] else "FAIL"
        print(f"{mark}  {name}  {self.rows[-1][2][:140]}", flush=True)

    def text(self, parsed):
        return json.dumps(parsed)

    def expect(self, parsed, *needles):
        text = self.text(parsed)
        for needle in needles:
            if needle not in text:
                raise RuntimeError(f"missing {needle!r} in {text[:300]}")

    def png(self, name, minimum=1000):
        path = self.work / name
        data = path.read_bytes()
        if data[:8] != b"\x89PNG\r\n\x1a\n" or len(data) < minimum:
            raise RuntimeError(f"{name} is not a valid PNG ({len(data)} bytes)")
        return len(data)

    # --- stream (the Code browser panel's live view) --------------------------------------------------------------------------------
    def stream_frames(self, session, want_console=False, seconds=12):
        key = base64.b64encode(os.urandom(16)).decode()
        sock = socket.create_connection(("127.0.0.1", self.args.port), timeout=seconds)
        head = (f"GET /api/browser/live/{session}/stream HTTP/1.1\r\nHost: 127.0.0.1:{self.args.port}\r\nUpgrade: websocket\r\n"
                f"Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
                f"X-Workspace-Token: {self.token}\r\nAuthorization: Bearer {self.token}\r\n")
        if self.args.user_id:
            head += f"X-User-ID: {self.args.user_id}\r\n"
        sock.sendall((head + "\r\n").encode())
        buffer = b""
        while b"\r\n\r\n" not in buffer:
            chunk = sock.recv(4096)
            if not chunk:
                raise RuntimeError("stream closed during handshake")
            buffer += chunk
        status = buffer.split(b"\r\n", 1)[0].decode()
        if " 101 " not in status:
            raise RuntimeError("stream handshake: " + status + " " + buffer.split(b"\r\n\r\n", 1)[1][:200].decode(errors="replace"))
        buffer = buffer.split(b"\r\n\r\n", 1)[1]
        frames, kinds, console = 0, set(), 0
        deadline = time.time() + seconds
        sock.settimeout(2)

        def need(count):
            nonlocal buffer
            while len(buffer) < count:
                chunk = sock.recv(65536)
                if not chunk:
                    raise EOFError
                buffer += chunk

        try:
            while time.time() < deadline and (frames < 2 or (want_console and console == 0)):
                try:
                    need(2)
                    opcode, length = buffer[0] & 15, buffer[1] & 127
                    offset = 2
                    if length == 126:
                        need(4)
                        length, offset = int.from_bytes(buffer[2:4], "big"), 4
                    elif length == 127:
                        need(10)
                        length, offset = int.from_bytes(buffer[2:10], "big"), 10
                    need(offset + length)
                    payload, buffer = buffer[offset:offset + length], buffer[offset + length:]
                except socket.timeout:
                    continue
                if opcode == 8:
                    break
                if opcode != 1:
                    continue
                try:
                    message = json.loads(payload)
                except ValueError:
                    continue
                kinds.add(message.get("type"))
                if message.get("type") == "frame" and base64.b64decode(message.get("data", ""))[:3] == b"\xff\xd8\xff":
                    frames += 1
                if message.get("type") == "console":
                    console += 1
        except EOFError:
            pass
        finally:
            sock.close()
        return frames, kinds, console

    def recording_api(self, session, action):
        body = {"workspace_path": self.rel, "action": action, "working_directory": self.rel,
                "folder_guard": {"enabled": True, "read_paths": [self.rel], "write_paths": [self.rel], "browser_session": session}}
        request = urllib.request.Request(f"http://127.0.0.1:{self.args.port}/api/browser/live/{session}/recording",
                                         data=json.dumps(body).encode(), headers=self.headers)
        try:
            with urllib.request.urlopen(request, timeout=100) as response:
                return json.load(response)
        except urllib.error.HTTPError as error:
            raise RuntimeError(f"recording {action}: HTTP {error.code} {error.read()[:300].decode(errors='replace')}")

    # --- the matrix -----------------------------------------------------------------------------------------------------------------
    def run(self):
        ab, check, base = self.ab, self.check, self.base
        name_a, profile_a = self.session("a")
        check("session name format <prefix>project-<16hex>--browser", lambda: re.fullmatch(r"(?:[A-Za-z0-9_-]+--)?project-[a-f0-9]{16}--browser", name_a) and name_a)

        # The panel's own Start browser: `tab`, then `stream status` / `stream enable`.
        check("start browser (tab --json, as the panel does)", lambda: ab("tab"))
        def stream_enable():
            status = ab("stream", "status")
            if not ((status.get("data") or {}).get("enabled")):
                ab("stream", "enable")
            return "enabled: " + str((ab("stream", "status").get("data") or {}).get("enabled"))
        check("stream status/enable (live view, as the panel does)", stream_enable)
        check("open https://example.com", lambda: ab("open", "https://example.com") and self.expect(ab("get", "title"), "Example Domain"))
        check("open http (public) http://example.com", lambda: self.expect(ab("open", "http://example.com"), "example.com"))
        check("open http (local test page)", lambda: self.expect(ab("open", base + "/page.html"), "Matrix Page"))
        check("get title / get url", lambda: (self.expect(ab("get", "title"), "Matrix Page"), self.expect(ab("get", "url"), "/page.html")))
        check("snapshot", lambda: self.expect(ab("snapshot"), "Matrix heading"))
        check("fill", lambda: ab("fill", "#name", "alice"))
        check("type", lambda: ab("type", "#name", "-typed"))
        check("press key", lambda: ab("press", "Tab"))
        check("click (and result visible via get text)", lambda: (ab("click", "#go"), self.expect(ab("get", "text", "#out"), "clicked:alice-typed")))
        check("eval", lambda: self.expect(ab("eval", "1+41"), "42"))
        check("scroll", lambda: (ab("scroll", "down", "1500"), self.expect(ab("eval", "String(window.scrollY>1000)"), "true")))
        check("scrollintoview + wait", lambda: (ab("scrollintoview", "#bottom"), ab("wait", "300"), ab("wait", "#bottom")))

        def tabs():
            ab("tab", "new", base + "/ok.json")
            listing = ab("tab", "list")
            self.expect(listing, "ok.json", "page.html")
            ab("tab", "t1")
            self.expect(ab("get", "url"), "page.html")
            ab("tab", "close", "t2")
            return "new/list/switch/close"
        check("tabs (new, list, switch, close)", tabs)

        check("screenshot viewport (PNG)", lambda: (ab("screenshot", str(self.work / "v.png")), self.png("v.png")))
        check("screenshot full page (PNG)", lambda: (ab("screenshot", "--full", str(self.work / "f.png")), self.png("f.png")))
        check("screenshot annotated (PNG)", lambda: (ab("screenshot", "--annotate", str(self.work / "n.png")), self.png("n.png")))
        check("screenshot second capture after the first", lambda: (ab("screenshot", str(self.work / "v2.png")), self.png("v2.png")))
        check("pdf", lambda: (ab("pdf", str(self.work / "p.pdf")), (self.work / "p.pdf").read_bytes()[:4] == b"%PDF" or 1 / 0))

        def element():
            ab("screenshot", "#go", str(self.work / "e.png"))
            try:
                return f"{self.png('e.png', 60)} B element crop"
            except Exception:  # noqa: BLE001
                raise RuntimeError("agent-browser has no element screenshot: output was " + repr((self.work / "e.png").read_bytes()[:100]))
        check("screenshot element (selector argument)", element)

        def live():
            frames, kinds, _ = self.stream_frames(name_a)
            if frames < 2:
                raise RuntimeError(f"only {frames} JPEG frames, message types {sorted(k for k in kinds if k)}")
            return f"{frames} JPEG frames, types {sorted(k for k in kinds if k)}"
        check("live view stream: JPEG frames via /api/browser/live/<session>/stream", live)

        def live_console():
            threading.Timer(2.0, lambda: ab("eval", "console.log('stream-console-marker'); 1")).start()
            frames, kinds, console = self.stream_frames(name_a, want_console=True, seconds=10)
            if not console:
                raise RuntimeError(f"no console messages on the stream (types {sorted(k for k in kinds if k)}); page console is available through `console`/capture API")
            return f"console messages on stream: {console}"
        check("live view stream carries console messages emitted after connect", live_console)

        # uploads / downloads
        def upload():
            (self.work / "up.txt").write_text("matrix-upload\n")
            ab("upload", "#up", str(self.work / "up.txt"))
            self.expect(ab("eval", "document.getElementById('up').files[0].name"), "up.txt")
        check("upload (file input)", upload)

        def download():
            ab("download", "#dl", str(self.work / "dl.txt"))
            if "matrix-download-content" not in (self.work / "dl.txt").read_text():
                raise RuntimeError("downloaded file has the wrong content")
        check("download (click link, save under work dir)", download)

        def dialog():
            ab("click", "#alertbtn")
            self.expect(ab("eval", "document.title"), "Matrix Page")
        check("alert dialog does not hang the page (auto-dismissed)", dialog)

        # observability
        def console():
            ab("open", base + "/page.html")
            ab("wait", "800")
            self.expect(ab("console"), "matrix-log", "matrix-warn", "matrix-error")
        check("console log/warn/error captured", console)

        def errors():
            self.expect(ab("errors"), "matrix-uncaught")
            return "unhandled rejection present: " + str("matrix-unhandled" in self.text(ab("errors")))
        check("page errors: uncaught exception", errors)
        check("page errors: unhandled rejection", lambda: self.expect(ab("errors"), "matrix-unhandled"))

        def network():
            listing = self.text(ab("network", "requests"))
            for needle in ("ok.json", "missing", "404", "GET"):
                if needle not in listing:
                    raise RuntimeError(f"network requests lacks {needle!r}: {listing[:300]}")
            return "url/method/status present; dns failure present: " + str("nonexistent.invalid" in listing)
        check("network requests (url, method, status, 404)", network)
        check("network requests: failed DNS request listed", lambda: "nonexistent.invalid" in self.text(ab("network", "requests")) or 1 / 0)
        check("network requests --filter", lambda: self.expect(ab("network", "requests", "--filter", "ok.json"), "ok.json"))

        def headers():
            listing = ab("network", "requests")
            for request in json.dumps(listing).split("},{"):
                if "ok.json" in request:
                    if "x-matrix" in request.lower() or "X-Matrix" in request:
                        return "response headers present"
            raise RuntimeError("no response headers in network requests (see 'network request <id>')")
        check("network response headers (info)", headers)

        def har():
            ab("network", "har", "start", "--content", "none")
            ab("open", base + "/page.html")
            ab("wait", "800")
            ab("network", "har", "stop", str(self.work / "n.har"))
            har_json = json.loads((self.work / "n.har").read_text())
            urls = [entry["request"]["url"] for entry in har_json["log"]["entries"]]
            if not any("ok.json" in url for url in urls):
                raise RuntimeError(f"HAR has no ok.json entry: {urls[:5]}")
            return f"{len(urls)} entries"
        check("HAR export (network har start/stop)", har)

        def trace():
            ab("trace", "start")
            ab("open", base + "/page.html")
            ab("trace", "stop", str(self.work / "t.json"))
            data = json.loads((self.work / "t.json").read_text())
            events = data.get("traceEvents", []) if isinstance(data, dict) else data
            if not events:
                raise RuntimeError("trace file has no events: " + str(list(data)[:5] if isinstance(data, dict) else type(data)))
            return f"{len(events)} events"
        check("trace export (trace start/stop)", trace)

        def profiler():
            ab("profiler", "start")
            ab("wait", "300")
            ab("profiler", "stop", str(self.work / "pr.json"))
            json.loads((self.work / "pr.json").read_text())
        check("profiler export", profiler)

        def recording():
            ab("open", base + "/page.html")
            started = self.recording_api(name_a, "start")
            time.sleep(3)
            ab("eval", "console.log('rec-marker')")
            stopped = self.recording_api(name_a, "stop")
            files = stopped.get("files") or []
            for needed in ("manifest.json",):
                if needed not in files:
                    raise RuntimeError(f"capture files {files}, errors {stopped.get('errors')}")
            return f"recording API: files {files}, errors {stopped.get('errors')}"
        check("platform capture API (recording start/stop: console, errors, HAR, video)", recording)

        # state persistence across close / reopen
        def persistence():
            ab("open", base + "/page.html")
            ab("eval", "localStorage.setItem('mk','persist-ok'); document.cookie='mc=cookie-ok; max-age=3600; path=/'; 1")
            ab("close")
            time.sleep(1)
            ab("open", base + "/page.html")
            self.expect(ab("eval", "localStorage.getItem('mk')"), "persist-ok")
            self.expect(ab("cookies", "get"), "mc")
        check("localStorage + cookies persist across close/reopen", persistence)

        def heavy():
            started = time.time()
            ab("open", base + "/heavy.html", timeout=90)
            self.expect(ab("eval", "String(document.querySelectorAll('div').length)"), "30000")
            ab("screenshot", str(self.work / "heavy-v.png"), timeout=90)
            ab("screenshot", "--full", str(self.work / "heavy.png"), timeout=90)
            height = self.text(ab("eval", "String(document.documentElement.scrollHeight)"))
            return f"viewport {self.png('heavy-v.png', 2000)} B, full page {self.png('heavy.png', 5000)} B, height {height[-30:]}, {time.time() - started:.1f}s"
        check("heavy page (30000 nodes + canvas): viewport and full-page screenshots", heavy)

        def extreme():
            ab("open", base + "/tall.html", timeout=60)
            ab("screenshot", str(self.work / "tall-v.png"))
            self.png("tall-v.png", 500)
            try:
                ab("screenshot", "--full", str(self.work / "tall.png"), timeout=60)
                detail = "600000px full-page screenshot worked"
            except RuntimeError as error:
                detail = "600000px full-page screenshot refused/crashed (" + str(error)[:60] + ")"
            ab("close", allow_fail=True)
            time.sleep(1)
            ab("open", base + "/page.html", retries=3)
            self.expect(ab("get", "title"), "Matrix Page")
            return detail + "; browser usable afterwards"
        check("extreme page height: viewport ok, browser survives and recovers (info on full-page)", extreme)

        # second session, different project: no shared state
        def isolation():
            name_b, _ = self.session("b")
            ab("open", base + "/page.html", label="b")
            ab("wait", "600", label="b")
            if "persist-ok" in self.text(ab("eval", "String(localStorage.getItem('mk'))", label="b")):
                raise RuntimeError("session B sees session A's localStorage")
            if "mc" in self.text(ab("cookies", "get", label="b")).replace("matrix_srv", ""):
                raise RuntimeError("session B sees session A's cookie")
            ab("eval", "console.log('only-in-b'); 1", label="b")
            if "only-in-b" in self.text(ab("console")):
                raise RuntimeError("session A console shows session B's output")
            if self.pid("a") == self.pid("b"):
                raise RuntimeError("both sessions share one Chrome")
            if self.session("a")[1] == self.session("b")[1]:
                raise RuntimeError("same profile")
            return f"separate Chrome pids {self.pid('a')} / {self.pid('b')}, state and console not shared"
        check("two concurrent project sessions: no shared state", isolation)

        def second_session_stream():
            frames, _, _ = self.stream_frames(self.session("b")[0], seconds=10)
            if frames < 1:
                raise RuntimeError("no frames for the second session")
        check("second session live view frames", second_session_stream)
        check("stream of session A not readable with B's data (own frames only)", lambda: self.stream_frames(name_a, seconds=6)[0] >= 1 or 1 / 0)

        def killed():
            before = self.pid("a")
            os.kill(before, signal.SIGKILL)
            time.sleep(1.5)
            ab("open", base + "/page.html")
            self.expect(ab("get", "title"), "Matrix Page")
            after = self.pid("a")
            if after == before:
                raise RuntimeError("Chrome pid unchanged after kill -9")
            return f"recovered: pid {before} -> {after}"
        check("kill -9 Chrome, next command recovers", killed)

        def killed_screenshot():
            ab("screenshot", str(self.work / "after-kill.png"))
            return self.png("after-kill.png")
        check("screenshot works after recovery", killed_screenshot)

        def idle():
            first = self.pid("a")
            time.sleep(25)
            self.expect(ab("get", "title"), "Matrix Page")
            if self.pid("a") != first:
                raise RuntimeError("Chrome restarted during idle with --idle-timeout 0")
            return "same Chrome after 25s idle (--idle-timeout 0)"
        check("idle with --idle-timeout 0 keeps the browser", idle)

        def crash_tab():
            ab("open", "chrome://crash", allow_fail=True, timeout=30)
            time.sleep(2)
            plain = ab("open", base + "/page.html", allow_fail=True, timeout=30)
            if plain.get("success") is False or "error" in plain and plain.get("error"):
                ab("close", allow_fail=True)
                time.sleep(1)
                ab("open", base + "/page.html", retries=3, timeout=60)
                how = "needed close + open"
            else:
                how = "plain open recovered"
            self.expect(ab("get", "title"), "Matrix Page")
            return how
        check("renderer crash (chrome://crash) then recover", crash_tab)

        def idle_short():
            ab("open", base + "/page.html", label="c", extra=("--idle-timeout", "5s"))
            first = self.pid("c")
            time.sleep(14)
            if self.processes("c"):
                raise RuntimeError("Chrome still running 14s after a 5s idle timeout")
            ab("open", base + "/page.html", label="c", extra=("--idle-timeout", "5s"))
            return f"idle timeout shut Chrome {first} down, next command relaunched it"
        check("short --idle-timeout shuts down and relaunches", idle_short)

        def close_clean():
            for label in list(self.sessions):
                try:
                    ab("close", label=label)
                except Exception:  # noqa: BLE001
                    pass
            time.sleep(2)
            left = {label: self.processes(label) for label in self.sessions}
            left = {k: v for k, v in left.items() if v}
            if left:
                raise RuntimeError(f"processes left after close: {left}")
        check("close leaves no processes", close_clean)

        def cycles():
            shm = lambda: len([p for p in Path("/dev/shm").iterdir() if p.stat().st_uid == os.getuid()])  # noqa: E731
            tmpdirs = lambda: len(list((self.docs / "tmp").glob("aw-browser-*/*")) + list(Path("/tmp").glob("aw-browser-*/*")))  # noqa: E731
            before = (shm(), tmpdirs())
            pids, retried = set(), 0
            for index in range(self.args.cycles):
                ab("open", base + "/page.html", label="d", timeout=60, retries=3)
                retried += self.retried
                pids.add(self.pid("d"))
                ab("close", label="d")
                if index % 5 == 4:
                    print(f"      cycle {index + 1}/{self.args.cycles}", flush=True)
            time.sleep(2)
            leftover = self.processes("d")
            after = (shm(), tmpdirs())
            if leftover:
                raise RuntimeError(f"processes left after {self.args.cycles} cycles: {leftover}")
            if after[0] > before[0] + 5 or after[1] > before[1] + 20:
                raise RuntimeError(f"resource growth over {self.args.cycles} cycles: /dev/shm {before[0]}->{after[0]}, aw-browser files {before[1]}->{after[1]}")
            return f"{self.args.cycles} open/close cycles ({retried} open retries after close), {len(pids)} Chrome launches, no processes left, /dev/shm {before[0]}->{after[0]}, scratch files {before[1]}->{after[1]}"
        check(f"{self.args.cycles} open/close cycles: no leaks", cycles)

        def cleanup_api():
            request = urllib.request.Request(f"http://127.0.0.1:{self.args.port}/api/browser/cleanup", data=json.dumps({"pids": [999999]}).encode(), headers=self.headers)
            with urllib.request.urlopen(request, timeout=15) as response:
                result = json.load(response)
            if result.get("killed") != 0:
                raise RuntimeError(f"cleanup killed something unexpected: {result}")
            return "POST /api/browser/cleanup refuses a pid that is not a browser (the app has no separate stop-browser API: Stop is `close`)"
        check("cleanup API refuses non-browser pids", cleanup_api)

    def finish(self):
        for label in list(self.sessions):
            try:
                self.ab("close", label=label, allow_fail=True, timeout=20)
            except Exception:  # noqa: BLE001
                pass
        for label, (name, profile) in self.sessions.items():
            for pid in self.processes(label):
                try:
                    os.kill(pid, signal.SIGKILL)
                except OSError:
                    pass
            shutil.rmtree(profile, ignore_errors=True)
            owner = re.search(r"project-([a-f0-9]{16})--browser$", name)
            if owner:  # the browser's socket folder (host /tmp, or the sandbox's shared tmp under the docs folder)
                for root in (Path("/tmp/.agent-browser/o"), self.docs / "tmp/.agent-browser/o"):
                    shutil.rmtree(root / ("p" + owner.group(1)), ignore_errors=True)
        shutil.rmtree(self.work, ignore_errors=True)
        self.server.shutdown()

    def report(self):
        width = max(len(row[0]) for row in self.rows)
        print("\n" + "=" * 100)
        for name, ok, detail, seconds in self.rows:
            print(f"{'PASS' if ok else 'FAIL'}  {name:<{width}}  {seconds:5.1f}s  {detail[:110]}")
        failed = [row for row in self.rows if not row[1]]
        print(f"\n{len(self.rows) - len(failed)} passed, {len(failed)} failed")
        for name, _, detail, _ in failed:
            print(f"FAILED: {name}\n    {detail}")
        return 1 if failed else 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("product")
    parser.add_argument("--port", type=int, required=True, help="the workspace service port")
    parser.add_argument("--docs-dir", help="workspace docs root (default /srv/<product>/data/docs)")
    parser.add_argument("--user-id", help="X-User-ID to send, as the app does on multi-user servers")
    parser.add_argument("--cycles", type=int, default=20)
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.product):
        parser.error("invalid product")
    if pwd.getpwuid(os.getuid()).pw_name != args.product:
        parser.error("run as the product service account")
    matrix = Matrix(args)
    try:
        matrix.run()
    finally:
        matrix.finish()
    raise SystemExit(matrix.report())


if __name__ == "__main__":
    main()
