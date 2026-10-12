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
import subprocess
import sys
import tempfile
import uuid


def hook(directory):
    root = Path(directory)
    settings = json.loads((root / "probe.json").read_text())
    event = json.load(sys.stdin)
    record = {key: event.get(key) for key in
              ("hook_event_name", "conversation_id", "session_id", "status", "loop_count")}
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
    return "\n".join(text)


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


def check(cli, workspace=None, trust_workspace=False):
    context = nullcontext(workspace) if workspace else tempfile.TemporaryDirectory(prefix="cursor-native-hook-")
    with context as directory:
        root = Path(directory)
        if workspace is None:
            prepare(root / "workspace")
            root = root / "workspace"
        settings = json.loads((root / "probe.json").read_text())
        baseline, followup = settings["baseline"], settings["followup"]
        cli_args = [cli, "--print", "--mode", "ask", "--workspace", str(root),
                    "--output-format", "stream-json"]
        if trust_workspace:
            # An operator must explicitly approve this prepared workspace and
            # its hooks. Never apply trust to an automatically created project.
            cli_args.append("--trust")
        cli_args.append("Reply exactly " + baseline + ". Do not run tools.")
        env = dict(os.environ, XDG_CACHE_HOME=str(root / "cache"),
                   XDG_CONFIG_HOME=str(root / "config"), NODE_COMPILE_CACHE=str(root / "cache/node"))
        timed_out = False
        try:
            result = subprocess.run(cli_args, capture_output=True, text=True,
                                    timeout=180, cwd=root, env=env)
            stdout, stderr, code = result.stdout, result.stderr, result.returncode
        except subprocess.TimeoutExpired:
            stdout, stderr, code, timed_out = "", "", 124, True
        path = root / "hooks.jsonl"
        events = [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []
        rendered = assistant_text(stdout)
        baseline_seen, followup_seen = baseline in rendered, followup in rendered
        completed = any(event.get("hook_event_name") == "stop" and
                        event.get("status") == "completed" for event in events)
        passed = code == 0 and baseline_seen and followup_seen and completed
        # Diagnostics are limited to this controlled probe; redact the inherited
        # key, URL query data and token-shaped values before returning a hint.
        hint = re.sub(r"\x1b\[[0-9;]*[A-Za-z]", "", stderr).strip()
        if os.environ.get("CURSOR_API_KEY"):
            hint = hint.replace(os.environ["CURSOR_API_KEY"], "[redacted]")
        hint = re.sub(r"https?://\S+|[A-Za-z0-9_+/=-]{40,}", "[redacted]", hint)[:400]
        report = {"passed": passed, "exit_code": code, "timeout": timed_out,
                  "failure_hint": hint if code else "",
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
    return check(args.cli, args.workspace, args.trust_workspace)


if __name__ == "__main__":
    raise SystemExit(main())
