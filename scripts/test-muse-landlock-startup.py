#!/usr/bin/env python3
"""Live Linux regression probe using Muse's echo provider and a disposable home.

Build workspace/cmd/landlock-runner, then pass --runner and --muse absolute
paths. No account credentials or model requests are needed. Nothing changes
in the installed Muse configuration or a real user's workspace.
"""

import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import sys
import tempfile
import time


def check_tui(prepare, base, env):
    """Use a separate tmux socket; exercise fresh startup and native resume."""
    env = {**env, "TMUX_TMPDIR": str(base / "tmp"), "TERM": "xterm-256color"}

    def tmux(*command, check=True):
        return subprocess.run(["tmux", "-L", "muse-regression", *command],
                              env=env, capture_output=True, text=True, check=check)

    def launch(command):
        tmux("new-session", "-d", "-s", "repro", "-x", "200", "-y", "50",
             "-c", str(base / "work"), shlex.join(prepare(command)))

    def wait_reply(token):
        until = time.monotonic() + 20
        while time.monotonic() < until:
            for log in (base / "data").rglob("session.jsonl"):
                if token in log.read_text(errors="replace"):
                    return log
            if tmux("has-session", "-t", "repro", check=False).returncode:
                raise RuntimeError("Muse TUI exited before replying")
            time.sleep(0.2)
        raise RuntimeError("TUI timeout: " + tmux("capture-pane", "-p", "-t", "repro").stdout[-1800:])

    try:
        muse = env["CODING_PROBE_MUSE"]
        flags = ["--provider", "echo", "--trust-workspace", "--disable-approval",
                 "--approval-mode", "never"]
        launch([muse, *flags, "Reply TUI_STARTUP_OK"])
        session = wait_reply("echo: Reply TUI_STARTUP_OK").parent.name
        print("fresh TUI startup: PASS", flush=True)
        tmux("kill-server")
        time.sleep(0.4)
        launch([muse, "resume", session, *flags])
        time.sleep(3)
        tmux("send-keys", "-t", "repro", "-l", "Reply RESUME_STARTUP_OK")
        time.sleep(0.7)
        tmux("send-keys", "-t", "repro", "Enter")
        time.sleep(0.7)
        tmux("send-keys", "-t", "repro", "Enter")
        wait_reply("echo: Reply RESUME_STARTUP_OK")
        print("resumed TUI startup: PASS", flush=True)
    finally:
        tmux("kill-server", check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runner", required=True, type=Path)
    parser.add_argument("--muse", required=True, type=Path)
    parser.add_argument("--tui", action="store_true", help="also exercise tmux startup and resume")
    args = parser.parse_args()
    runner, muse = args.runner.resolve(), args.muse.resolve()
    if sys.platform != "linux":
        parser.error("Landlock requires Linux")

    with tempfile.TemporaryDirectory(prefix="muse-landlock-regression-") as tmp:
        root = Path(tmp)
        other = root / "other"
        other.mkdir()
        secret = other / "secret.txt"
        secret.write_text("outside-canary")
        results = {}
        for label in ("unconfined", "confined", "directory_only"):
            base = root / label
            for name in ("work", "home", "config/muse", "data", "tmp"):
                (base / name).mkdir(parents=True, exist_ok=True)
            env = {
                "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
                "HOME": str(base / "home"),
                "XDG_CONFIG_HOME": str(base / "config"),
                "XDG_DATA_HOME": str(base / "data"),
                "TMPDIR": str(base / "tmp"),
                "MUSE_NO_AUTO_UPDATE": "1",
                "MUSE_LOGIN": "0",
                "CODING_PROBE_MUSE": str(muse),
            }

            def prepare(command, confine=True):
                if confine:
                    policy = {
                        "read_paths": [str(muse.parent)],
                        "write_paths": [str(base)],
                        "work_dir": str(base / "work"),
                    }
                    if label == "directory_only":
                        policy["list_paths"] = ["/"]
                    path = base / "policy.json"
                    path.write_text(json.dumps(policy))
                    command = [str(runner), "--config", str(path), "--"] + command
                return command

            def run(command, confine=True):
                command = prepare(command, confine)
                return subprocess.run(command, env=env, cwd=base / "work",
                                      capture_output=True, text=True, timeout=30)

            result = run([str(muse), "exec", "--provider", "echo",
                          "--trust-workspace", "--json", "Reply STARTUP_OK"],
                         confine=label != "unconfined")
            completed = "echo: Reply STARTUP_OK" in result.stdout
            if label == "confined":
                passed = result.returncode != 0 and "Agent Definition filesystem source failed: IoError" in result.stderr
            else:
                passed = result.returncode == 0 and completed
            results[label] = {"exit_code": result.returncode,
                              "completed": completed, "expected_behavior": passed}
            print(label, json.dumps(results[label]), flush=True)
            if not passed:
                raise RuntimeError(f"Unexpected {label} result: {result.stderr[-1500:]}")

            if label == "directory_only":
                probe = """import os,sys
p=sys.argv[1]
assert os.path.basename(p) in os.listdir(os.path.dirname(p))
for mode in ('r','w'):
    try:
        with open(p,mode): pass
    except PermissionError:
        continue
    raise RuntimeError('outside file access allowed: '+mode)
with open('local-canary','w') as f: f.write('allowed')
with open('local-canary') as f: assert f.read()=='allowed'
print('outside reads/writes denied; workspace read/write allowed')
"""
                boundary = run([sys.executable, "-c", probe, str(secret)])
                if boundary.returncode != 0:
                    raise RuntimeError(boundary.stderr)
                print(boundary.stdout.strip(), flush=True)
                if args.tui:
                    check_tui(prepare, base, env)
        assert secret.read_text() == "outside-canary"
        print("PASS: startup regression reproduced and directory-only workaround verified")


if __name__ == "__main__":
    main()
