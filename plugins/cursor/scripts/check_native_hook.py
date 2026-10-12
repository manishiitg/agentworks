#!/usr/bin/env python3
"""Check real Cursor stop-hook delivery; does not attach or grant bridge authority.

Inherits the existing Cursor login/API-key environment. No trust bypass, account
registration, messaging receipt or production configuration change is performed.
"""
import argparse
from contextlib import nullcontext
import json
import os
import re
from pathlib import Path
import shlex
import signal
import subprocess
import sys
import tempfile
import time
import uuid


def hook(directory):
    root = Path(directory)
    settings = json.loads((root / "probe.json").read_text())
    event = json.load(sys.stdin)
    record = {key: event.get(key) for key in
              ("hook_event_name", "conversation_id", "session_id", "status", "loop_count")}
    record["probe_prompt"] = next((name for name in ("baseline", "followup")
                                  if settings[name] in event.get("prompt", "")), None)
    transcript = event.get("transcript_path")
    if isinstance(transcript, str) and Path(transcript).resolve().is_relative_to(root.resolve()):
        record["transcript_path"] = transcript
    record["ancestors"] = []
    pid = os.getppid()
    for _ in range(8):
        if pid <= 1:
            break
        result = subprocess.run(["ps", "-p", str(pid), "-o", "ppid=", "-o", "comm=",
                                 "-o", "lstart="], capture_output=True, text=True, timeout=2)
        fields = result.stdout.split()
        if result.returncode or len(fields) < 7:
            break
        ancestor = {"pid": pid, "command": fields[1], "start": " ".join(fields[-5:])}
        # Linux Node launchers may retain node as comm but set a provider argv0.
        # Read only argv0, never prompt arguments, environment or credentials.
        try:
            ancestor["argv0"] = Path(f"/proc/{pid}/cmdline").read_bytes().split(b"\0", 1)[0].decode()
        except (OSError, UnicodeError):
            pass
        record["ancestors"].append(ancestor)
        pid = int(fields[0])
    with (root / "hooks.jsonl").open("a") as stream:
        stream.write(json.dumps(record) + "\n")
    if (event.get("hook_event_name") == "stop" and event.get("status") == "completed"
            and type(event.get("loop_count")) is int and event["loop_count"] == 0):
        print(json.dumps({"followup_message": "Diagnostic hook message. Reply exactly " +
                          settings["followup"] + ". Do not run tools."}))


def assistant_text(raw):
    text = []
    for line in raw.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        message = event.get("message", {}) if isinstance(event, dict) else {}
        if not isinstance(message, dict) or message.get("role") != "assistant":
            continue
        content = message.get("content", [])
        if isinstance(content, str):
            text.append(content)
        elif isinstance(content, list):
            text.extend(item["text"] for item in content if isinstance(item, dict)
                        and item.get("type") == "text" and isinstance(item.get("text"), str))
    if not text:
        text = re.findall(r"(?ims)^assistant:\s*\n(.*?)(?=^(?:user|assistant|tool):|\Z)", raw)
    return "\n".join(text)


def read_events(root):
    path = root / "hooks.jsonl"
    events = []
    if path.exists():
        for line in path.read_text().splitlines():
            try:
                events.append(json.loads(line))
            except ValueError:
                pass  # A native hook may still be appending this record.
    return events


def transcript_text(root, events):
    text = []
    for path in {event.get("transcript_path") for event in events} - {None}:
        candidate = Path(path).resolve()
        if candidate.is_relative_to(root.resolve()) and candidate.is_file():
            with candidate.open() as stream:
                text.append(assistant_text(stream.read(512 << 10)))
    return "\n".join(text)


def interactive_run(cli_args, root, env, followup):
    # This drives only a fresh diagnostic CLI, never an existing chat or app UI.
    import fcntl
    import pty
    import select
    import struct
    import termios

    master, slave = pty.openpty()
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 30, 120, 0, 0))
    process = subprocess.Popen(cli_args, stdin=slave, stdout=slave, stderr=slave,
                               cwd=root, env=dict(env, TERM="xterm-256color"), start_new_session=True)
    os.close(slave)
    captured = bytearray()
    deadline, verified = time.monotonic() + 180, False
    try:
        while process.poll() is None and time.monotonic() < deadline:
            readable, _, _ = select.select([master], [], [], 0.25)
            if readable:
                try:
                    chunk = os.read(master, 65536)
                except OSError:
                    break
                if len(captured) < 1 << 20:
                    captured.extend(chunk)
                if b"\x1b[6n" in chunk:
                    os.write(master, b"\x1b[1;1R")
            events = read_events(root)
            second_done = any(event.get("hook_event_name") == "stop" and
                              event.get("status") == "completed" and
                              event.get("loop_count") == 1 for event in events)
            if second_done and followup in transcript_text(root, events):
                verified = True
                break
        timed_out = time.monotonic() >= deadline and not verified
        if process.poll() is None:
            process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()
        return process.returncode, captured.decode(errors="replace"), timed_out
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        os.close(master)


def prepare(root):
    root.mkdir(mode=0o700, exist_ok=False)
    settings = {"baseline": "CURSOR_BASE_" + uuid.uuid4().hex,
                "followup": "CURSOR_HOOK_" + uuid.uuid4().hex}
    (root / "probe.json").write_text(json.dumps(settings))
    (root / ".cursor").mkdir()
    command = shlex.join([sys.executable, str(Path(__file__).resolve()), "--hook", str(root)])
    hooks = {name: [{"command": command, "timeout": 20, **(
             {"loop_limit": 2} if name == "stop" else {})}]
             for name in ("beforeSubmitPrompt", "stop", "sessionEnd")}
    (root / ".cursor/hooks.json").write_text(json.dumps({"version": 1, "hooks": hooks}))


def check(cli, workspace=None, trust_workspace=False, interactive=False):
    context = nullcontext(workspace) if workspace else tempfile.TemporaryDirectory(prefix="cursor-native-hook-")
    with context as directory:
        root = Path(directory)
        if workspace is None:
            prepare(root / "workspace")
            root = root / "workspace"
        settings = json.loads((root / "probe.json").read_text())
        baseline, followup = settings["baseline"], settings["followup"]
        cli_args = [cli, "--mode", "ask", "--workspace", str(root)]
        if not interactive:
            cli_args.extend(["--print", "--output-format", "stream-json"])
        if trust_workspace:
            # An operator must explicitly approve this prepared workspace and
            # its hooks. Never apply trust to an automatically created project.
            cli_args.append("--trust")
        cli_args.append("Reply exactly " + baseline + ". Do not run tools.")
        env = dict(os.environ, XDG_CACHE_HOME=str(root / "cache"),
                   XDG_CONFIG_HOME=str(root / "config"), NODE_COMPILE_CACHE=str(root / "cache/node"))
        timed_out = False
        try:
            if interactive:
                code, stderr, timed_out = interactive_run(cli_args, root, env, followup)
                stdout = ""
            else:
                result = subprocess.run(cli_args, capture_output=True, text=True,
                                        timeout=180, cwd=root, env=env)
                stdout, stderr, code = result.stdout, result.stderr, result.returncode
        except subprocess.TimeoutExpired:
            stdout, stderr, code, timed_out = "", "", 124, True
        events = read_events(root)
        rendered = transcript_text(root, events) if interactive else assistant_text(stdout)
        baseline_seen, followup_seen = baseline in rendered, followup in rendered
        completed = any(event.get("hook_event_name") == "stop" and
                        event.get("status") == "completed" for event in events)
        second_done = any(event.get("hook_event_name") == "stop" and
                          event.get("status") == "completed" and event.get("loop_count") == 1
                          for event in events)
        passed = baseline_seen and followup_seen and completed and not timed_out and (
                 second_done if interactive else code == 0)
        # Diagnostics are limited to this controlled probe; redact the inherited
        # key, URL query data and token-shaped values before returning a hint.
        hint = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", stderr).strip()
        if os.environ.get("CURSOR_API_KEY"):
            hint = hint.replace(os.environ["CURSOR_API_KEY"], "[redacted]")
        hint = re.sub(r"https?://\S+|[A-Za-z0-9_+/=-]{40,}", "[redacted]", hint)[:400]
        if interactive:
            hint = ("Authentication required" if "authentication required" in stderr.lower() else
                    "Workspace trust required" if "workspace trust required" in stderr.lower() else
                    "Native TUI did not verify both assistant markers; see hook metadata")
        report = {"passed": passed, "exit_code": code, "timeout": timed_out,
                  "interactive": interactive, "followup_turn_completed": second_done,
                  "failure_hint": hint if not passed else "",
                  "baseline_assistant_response": baseline_seen,
                  "followup_assistant_response": followup_seen, "hook_events": events,
                  "authentication_required": "authentication required" in (stdout + stderr).lower(),
                  "evidence": "Real Cursor hook/model path only; no AgentWorks lease or receipts."}
        print(json.dumps(report))
        return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--cli", default="cursor-agent")
    parser.add_argument("--hook", help=argparse.SUPPRESS)
    parser.add_argument("--prepare", help="Create a new fixed workspace for operator review; does not run Cursor")
    parser.add_argument("--workspace", help="Run in the exact prepared workspace")
    parser.add_argument("--interactive", action="store_true", help="Use a fresh diagnostic CLI TUI instead of print mode")
    parser.add_argument("--trust-workspace", action="store_true",
                        help="Only after the person approves this prepared workspace and hooks")
    args = parser.parse_args()
    if args.hook:
        hook(args.hook)
        return 0
    if args.prepare:
        prepare(Path(args.prepare))
        print(json.dumps({"prepared": True, "workspace": args.prepare,
                          "hook_script": str(Path(__file__).resolve())}))
        return 0
    if args.trust_workspace and not args.workspace:
        parser.error("trust requires a fixed prepared --workspace and explicit person approval")
    return check(args.cli, args.workspace, args.trust_workspace, args.interactive)


if __name__ == "__main__":
    raise SystemExit(main())
