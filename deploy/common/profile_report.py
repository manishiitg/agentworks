#!/usr/bin/env python3
"""Report how a running server differs from the standard runtime profile (deploy/common/runtime_profile.json).

Runs on the server, as root or as the product's account. Reads the environment of the running agent and workspace processes, plus a few
facts (private /tmp in the sandbox health, kept releases, release source), and prints what differs. Changes nothing. With --enforce it
exits 1 when a same_everywhere setting differs (not used yet: servers are aligned first, docs/design/deploy_unification.md).

  python3 profile_report.py --profile runtime_profile.json --name excellence --account agents --app /srv/agents \
      --data /srv/agents/state --workspace-port 24001 [--enforce]
"""
import argparse
import json
import os
import sys
import urllib.request

SECRET_MARKERS = ("KEY", "TOKEN", "SECRET", "PASSWORD", "WEBHOOK", "AUTH", "CREDENTIAL", "COOKIE", "PRIVATE")


def processes(account, role):
    """PIDs owned by account whose command line is '<something>-<role> server'."""
    import pwd
    try:
        uid = pwd.getpwnam(account).pw_uid
    except KeyError:
        return []
    found = []
    for pid in os.listdir("/proc"):
        if not pid.isdigit():
            continue
        try:
            if os.stat(f"/proc/{pid}").st_uid != uid:
                continue
            argv = open(f"/proc/{pid}/cmdline", "rb").read().split(b"\0")
        except OSError:
            continue
        if len(argv) > 1 and argv[0].decode(errors="replace").endswith(f"-{role}") and argv[1] == b"server":
            found.append(int(pid))
    return found


def environ(pid):
    try:
        raw = open(f"/proc/{pid}/environ", "rb").read().split(b"\0")
    except OSError:
        return None
    env = {}
    for item in raw:
        if b"=" in item:
            k, v = item.split(b"=", 1)
            env[k.decode(errors="replace")] = v.decode(errors="replace")
    return env


def shown(key, value):
    if value is None:
        return "(unset)"
    if any(m in key.upper() for m in SECRET_MARKERS):
        return "<set>"
    return value


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--profile", help="path to runtime_profile.json")
    ap.add_argument("--profile-json", help="the profile itself (when the script is piped to a server)")
    ap.add_argument("--name", required=True)
    ap.add_argument("--account", required=True)
    ap.add_argument("--app", required=True)
    ap.add_argument("--data", required=True)
    ap.add_argument("--workspace-port", type=int, default=0)
    ap.add_argument("--enforce", action="store_true")
    a = ap.parse_args()
    profile = json.loads(a.profile_json) if a.profile_json else json.load(open(a.profile))
    expect = {k: v.replace("{app}", a.app).replace("{data}", a.data) for k, v in profile["same_everywhere"].items()}

    envs = {}
    for role in ("agent", "workspace"):
        pids = processes(a.account, role)
        envs[role] = environ(pids[0]) if pids else None

    rows, diffs = [], 0
    for key, want in expect.items():
        for role, env in envs.items():
            if env is None:
                rows.append(("MISSING", role, key, want, "(process not found)"))
                diffs += 1
                continue
            got = env.get(key)
            ok = got == want
            diffs += 0 if ok else 1
            rows.append(("ok" if ok else "DIFF", role, key, want, shown(key, got)))
    per_server = []
    env = envs.get("agent") or envs.get("workspace") or {}
    for key in profile.get("per_server", []):
        per_server.append((key, shown(key, env.get(key))))

    facts = []
    if a.workspace_port:
        try:
            health = json.load(urllib.request.urlopen(f"http://127.0.0.1:{a.workspace_port}/health", timeout=5))
            detail = (health.get("shell_sandbox") or {}).get("detail", "")
            facts.append(("private /tmp in sandboxes", "private /tmp" in detail, detail))
        except Exception as error:  # noqa: BLE001 - a report, never a failure
            facts.append(("private /tmp in sandboxes", False, f"health unreadable: {error}"))
    releases = os.path.join(a.app, "releases")
    try:
        kept = sorted(d for d in os.listdir(releases) if os.path.isdir(os.path.join(releases, d)))
    except OSError:
        kept = []
    facts.append(("previous release kept (rollback)", len(kept) >= 2, f"{len(kept)} release(s)"))
    current = os.path.realpath(os.path.join(a.app, "current"))
    has_source = os.path.isdir(os.path.join(current, "source")) or os.path.isfile(os.path.join(current, "SOURCE_REVISIONS"))
    facts.append(("release records its source", has_source, os.path.basename(current)))
    path = (envs.get("agent") or {}).get("PATH", "")
    slots_bin = os.path.join(a.app, "slots", "bin")
    if os.path.isdir(slots_bin):
        facts.append(("slots/bin first on PATH", path.split(":")[0] == slots_bin, path.split(":")[0] if path else "(no PATH)"))

    print(f"== {a.name}: {diffs} difference(s) from the standard profile")
    for status, role, key, want, got in rows:
        if status != "ok":
            print(f"  {status:7} {role:9} {key:30} want {want!r:40} got {got!r}")
    for label, ok, note in facts:
        print(f"  {'ok' if ok else 'DIFF':7} {'fact':9} {label:30} {note}")
        diffs += 0 if ok else 1
    print("  per-server settings: " + ", ".join(f"{k}={v}" for k, v in per_server if v != "(unset)"))
    return 1 if (a.enforce and diffs) else 0


if __name__ == "__main__":
    sys.exit(main())
