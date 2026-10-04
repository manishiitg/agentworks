#!/usr/bin/env python3
"""Run on the product host/account; use an isolated profile, never a user's login.

Example: python3 verify-managed-chrome.py sparkquill --port 23001 --cycles 20
RTS:     python3 verify-managed-chrome.py video-studio --port 8080 --docs-dir /data/video-studio/docs
Each browser operation is a separate sandbox request so command scratch cleanup
is exercised. Print only test status, never service tokens or page contents.
"""
import argparse
import json
import os
from pathlib import Path
import pwd
import re
import shlex
import shutil
import subprocess
import time
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("product")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--cycles", type=int, default=20)
    parser.add_argument("--docs-dir", help="workspace docs root (default /srv/<product>/data/docs)")
    parser.add_argument("--user-id", help="X-User-ID to send, as the app does on multi-user servers (Excellence: any user id)")
    args = parser.parse_args()
    if not re.fullmatch(r"[a-z][a-z0-9-]*", args.product):
        parser.error("invalid product")
    if pwd.getpwuid(os.getuid()).pw_name != args.product:
        parser.error("run as the product service account")
    if not 1 <= args.cycles <= 30 or not 1 <= args.port <= 65535:
        parser.error("cycles must be 1–30 and port must be valid")
    pid = subprocess.check_output(
        ["systemctl", "--user", "show", args.product + "-workspace", "-p", "MainPID", "--value"],
        text=True, timeout=10,
    ).strip()
    env = dict(item.split("=", 1) for item in Path(f"/proc/{pid}/environ").read_text().split("\0") if "=" in item)
    # A project browser, like a Crew's: exercises the -projects profile grant.
    root = Path(env.get("AGENT_BROWSER_SHARED_PROFILE", f"/srv/{args.product}/state/browser-profile") + "-projects")
    session = "project-" + uuid.uuid4().hex[:16] + "--browser"
    profile = root / session
    profile.mkdir(mode=0o700, parents=True)
    relative_work = "tmp/browser-smoke-" + session
    work = Path(args.docs_dir or f"/srv/{args.product}/data/docs") / relative_work
    work.mkdir(mode=0o700, parents=True)
    flags = "--no-sandbox,--disable-gpu,--disable-blink-features=AutomationControlled,--lang=en-US,--restore-last-session,--use-fake-device-for-media-stream,--use-fake-ui-for-media-stream"
    prefix = ["agent-browser", "--session", session, "--profile", str(profile), "--idle-timeout", "0", "--args", flags, "--json"]

    token = env.get("WORKSPACE_API_TOKEN", "")
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + token, "X-Workspace-Token": token}
    if args.user_id:
        headers["X-User-ID"] = args.user_id

    def call(*command):
        body = {"command": shlex.join(prefix + list(command)), "working_directory": relative_work,
                "timeout": 40, "folder_guard": {"enabled": True, "read_paths": [relative_work], "write_paths": [relative_work], "browser_session": session}}
        request = urllib.request.Request(
            f"http://127.0.0.1:{args.port}/api/execute", data=json.dumps(body).encode(),
            headers=headers,
        )
        with urllib.request.urlopen(request, timeout=50) as response:
            result = json.load(response)
        data = result.get("data", {})
        if not result.get("success") or data.get("exit_code") != 0:
            raise RuntimeError(f"{command[0]} failed: {data.get('stdout', '')[:2000]} {data.get('stderr', '')[:1000]}")
        browser = json.loads(data["stdout"])
        if browser.get("success") is not True:
            raise RuntimeError(f"{command[0]} failed: {browser.get('error')}")
        return browser

    def chrome_pid():
        # Chrome records hostname-PID in this profile's singleton symlink.
        # Unlike enumerating /proc, this also works with procfs hidepid settings.
        browser_pid = int(os.readlink(profile / "SingletonLock").rsplit("-", 1)[1])
        os.kill(browser_pid, 0)
        return browser_pid

    passed = False
    try:
        initial_pid = None
        for cycle in range(args.cycles):
            url = "https://example.com" if cycle % 2 == 0 else "https://en.wikipedia.org/wiki/Browser"
            call("open", url)
            call("snapshot")
            call("tab", "list")
            actual_url = call("get", "url")["data"]["url"]
            if actual_url.rstrip("/") != url.rstrip("/"):
                raise RuntimeError("navigation reported success but the browser is on another URL")
            current_pid = chrome_pid()
            if initial_pid is None:
                initial_pid = current_pid
            if current_pid != initial_pid:
                raise RuntimeError("Chrome restarted during the test despite successful commands")
            print(f"PASS cycle {cycle + 1}/{args.cycles}: same Chrome PID {current_pid}", flush=True)
            time.sleep(1)
        call("open", "https://example.com")
        call("storage", "local", "set", "aw_smoke", "persistence_ok")
        screenshot = work / "smoke.png"
        call("screenshot", str(screenshot))
        if screenshot.read_bytes()[:8] != b"\x89PNG\r\n\x1a\n":
            raise RuntimeError("screenshot is not a PNG")
        call("close")
        call("open", "https://example.com")
        if "persistence_ok" not in json.dumps(call("storage", "local", "aw_smoke")["data"]):
            raise RuntimeError("storage was lost across close/reopen")
        print("PASS screenshot and persistent storage across close/reopen", flush=True)
        passed = True
    finally:
        call("close")
        if passed:
            shutil.rmtree(profile)
            shutil.rmtree(work)
        else:
            print(f"Preserved diagnostic profile: {profile}", flush=True)


if __name__ == "__main__":
    main()
