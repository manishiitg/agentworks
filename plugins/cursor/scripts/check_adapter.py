#!/usr/bin/env python3
"""Exercise the real adapter process against a local socket fixture, not a server."""
import http.server
import json
import os
from pathlib import Path
import socketserver
import subprocess
import tempfile
import threading
import time

SCRIPT = Path(__file__).with_name("cursor_hook.py")
ATTACHMENT = {"attachment_id": "attachment-a", "native_session_id": "session-a",
              "provider": "cursor", "agent_name": "reviewer"}
MESSAGE = {"attachment_id": "attachment-a", "message_id": "message-1",
           "inbox_id": "inbox-1", "sender": "agent:worker", "message": "Review the change.",
           "sequence": 1}


class UnixServer(socketserver.UnixStreamServer):
    pass


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
        self.server.requests.append(request)
        if self.server.refuse:
            response = {"error": "refused"}
        elif request["session_id"] != "session-a" or (
                request.get("attachment_id") is not None and
                request["attachment_id"] != ATTACHMENT["attachment_id"]):
            response = {"attached": False}
        else:
            response = {"attached": True, "attachment": ATTACHMENT}
            if request["action"] == "drain":
                response["messages"] = self.server.pending
                self.server.mailbox.extend(self.server.pending)
                self.server.pending = []
                time.sleep(self.server.delay)
            if request["action"] in ("ack", "reply"):
                self.server.mutations.append(request)
                response["result"] = {"delivery": request.get("delivery", "stored")}
            if request["action"] == "read":
                response["result"] = {"messages": self.server.mailbox, "next_cursor": 1}
            if request["action"] == "detach":
                response = {"attached": False}
        raw = json.dumps(response).encode()
        try:
            self.send_response(200)
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)
        except (BrokenPipeError, ConnectionResetError):
            pass  # Client timeout must preserve the fixture's stored mailbox.


def run(root, event=None, args=()):
    env = dict(os.environ, AGENTWORKS_BRIDGE_ROOT=str(root), PYTHONDONTWRITEBYTECODE="1")
    result = subprocess.run(["python3", str(SCRIPT), *args],
                            input=json.dumps(event) if event is not None else "",
                            capture_output=True, text=True, env=env, timeout=20)
    assert not result.stderr, result.stderr
    return result


def main():
    package = SCRIPT.parent.parent
    manifest = json.loads((package / ".cursor-plugin/plugin.json").read_text())
    assert manifest["name"] == "agentworks"
    assert (package / manifest["hooks"]).is_file()
    assert (package / manifest["mcpServers"]).is_file()
    assert json.loads((package / manifest["mcpServers"]).read_text())["mcpServers"] == {}
    hooks = json.loads((package / manifest["hooks"]).read_text())["hooks"]
    assert set(hooks) == {"beforeSubmitPrompt", "stop", "sessionEnd"}
    assert hooks["stop"][0]["loop_limit"] == 5
    assert manifest["skills"] == ["./skills/agent-messages", "./skills/agentworks-messaging"]
    for skill in manifest["skills"]:
        assert (package / skill / "SKILL.md").is_file()
    source = package.parent / "shared/skills/agent-messages/SKILL.md"
    assert (package / "skills/agent-messages/SKILL.md").read_bytes() == source.read_bytes()
    with tempfile.TemporaryDirectory(prefix="cursor-adapter-") as directory:
        root = Path(directory)
        stop = {"hook_event_name": "stop", "conversation_id": "session-a",
                "generation_id": "generation-1", "status": "completed", "loop_count": 0}
        assert run(root, stop).stdout == ""  # Missing bridge stays silent.
        server = UnixServer(str(root / "bridge.sock"), Handler)
        os.chmod(root / "bridge.sock", 0o600)
        server.requests, server.pending, server.refuse = [], [MESSAGE], False
        server.mailbox, server.mutations, server.delay = [], [], 0
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            # No consumption on errors, aborts or invalid/exhausted loop counters.
            for event in [dict(stop, status="aborted"), dict(stop, status="error"),
                          *[dict(stop, loop_count=n) for n in [-1, 5, True, None, "0"]]]:
                assert run(root, event).stdout == ""
            assert server.requests == []
            assert run(root, dict(stop, conversation_id="session-b")).stdout == ""
            out = json.loads(run(root, stop).stdout)
            assert "Review the change." in out["followup_message"]
            assert "inbox-1" in out["followup_message"]
            assert server.requests[-1]["transport"] == "cursor_hook"
            assert all(r["action"] != "ack" for r in server.requests)
            assert run(root, stop).stdout == ""  # Fixture owns consumption, adapter does not.
            prompt = {"hook_event_name": "beforeSubmitPrompt", "conversation_id": "session-a",
                      "prompt": "Keep my ordinary prompt untouched."}
            before = len(server.requests)
            assert run(root, prompt).stdout == ""
            assert len(server.requests) == before
            attach = dict(prompt, prompt='/agentworks attach --name reviewer --purpose "Review work" '
                          '--config /private/connection.json --allow-sender worker')
            assert json.loads(run(root, attach).stdout)["continue"] is False
            assert server.requests[-1]["allowed_senders"] == ["worker"]
            for text in ["/agentworks", "/agentworks attach", "/agentworks detach extra",
                         "/agentworks attach --name reviewer --purpose work --config relative --allow-sender worker"]:
                assert json.loads(run(root, dict(prompt, prompt=text)).stdout)["continue"] is False
            ack = ["ack", "--session", "session-a", "--attachment-id", "attachment-a",
                   "--message-id", "message-1", "--delivery", "admitted"]
            assert json.loads(run(root, args=ack).stdout)["ok"] is True
            assert server.requests[-1]["attachment_id"] == "attachment-a"
            before = len(server.mutations)
            for action in ("ack", "reply"):
                extra = (["--delivery", "admitted"] if action == "ack" else
                         ["--message", "No side effect", "--submission-id", "stable-id"])
                for session, attachment in [("session-b", "attachment-a"),
                                            ("session-a", "old-attachment")]:
                    arguments = [action, "--session", session, "--attachment-id", attachment,
                                 "--message-id", "message-1", *extra]
                    assert json.loads(run(root, args=arguments).stdout)["ok"] is False
            assert len(server.mutations) == before
            assert run(root, args=["ack", "--session", "session-a", "--message-id", "message-1",
                                   "--delivery", "admitted"]).returncode == 1
            assert run(root, args=["attach"]).returncode == 1
            reply = ["reply", "--session", "session-a", "--attachment-id", "attachment-a",
                     "--message-id", "message-1",
                     "--message", "Reply text", "--submission-id", "stable-id"]
            assert json.loads(run(root, args=reply).stdout)["ok"] is True
            assert "inbox_id" not in server.requests[-1]  # Bridge resolves original inbox.
            server.pending = [MESSAGE, dict(MESSAGE, attachment_id="old-attachment", message="DO_NOT_RENDER"),
                              dict(MESSAGE, sequence=2, message_id="message-2", message="Second valid message")]
            text = json.loads(run(root, stop).stdout)["followup_message"]
            assert "Review the change." in text and "Second valid message" in text
            assert "DO_NOT_RENDER" not in text and "explicit mailbox recovery" in text
            server.pending, server.delay = [MESSAGE], 3.2
            assert "Review the change." in json.loads(run(root, stop).stdout)["followup_message"]
            # The bridge fixture journals before responding. An actual socket
            # timeout must produce receiver recovery rather than an empty hook.
            server.pending, server.delay = [MESSAGE], 10.2
            mutations_before_timeout = len(server.mutations)
            text = json.loads(run(root, stop).stdout)["followup_message"]
            assert "explicit mailbox recovery" in text and "attachment-a" in text
            assert server.pending == [] and len(server.mutations) == mutations_before_timeout
            server.delay = 0
            recover = ["recover", "--session", "session-a", "--attachment-id", "attachment-a"]
            recovered = json.loads(run(root, args=recover).stdout)
            assert recovered["ok"] and recovered["result"]["messages"]
            assert server.requests[-1]["action"] == "read"
            assert server.requests[-1]["attachment_id"] == "attachment-a"
            assert len(server.mutations) == mutations_before_timeout
            server.refuse = True
            assert run(root, stop).stdout == ""  # Status refused; no known attachment.
            assert json.loads(run(root, attach).stdout)["continue"] is False
            assert json.loads(run(root, args=ack).stdout)["ok"] is False
            server.refuse = False
            assert run(root, dict(stop, hook_event_name="sessionEnd")).stdout == ""
            assert server.requests[-1]["action"] == "detach"
            assert server.requests[-1]["attachment_id"] == "attachment-a"
            assert run(root, dict(stop, session_id="different")).stdout == ""
            os.chmod(root, 0o755)
            before = len(server.requests)
            assert run(root, stop).stdout == ""
            assert len(server.requests) == before
        finally:
            server.shutdown()
            server.server_close()
    print("PASSED: Cursor adapter process/socket regression checks (fixture only)")


if __name__ == "__main__":
    main()
