#!/usr/bin/env python3
"""Post-deploy coding-agent smoke check over a deployed server's public MCP (PLAT-424).

For every company provider account the server has configured, open a side chat in a dedicated smoke Crew, choose
that provider for the chat only (models action=set scope=tab), and ask for one AgentWorks tool call: the Crew's
owners list. Then, in the same chat (the CLI's next launch, as a person's next message), a native shell command that
reports the account it runs as and writes, reads and removes a file in the Crew (PLAT-872-90: a CLI that worked once
failed on its next launch, and one turn per provider never saw it). A provider passes when each turn was accepted
once and answered once with the expected reply, within the time limit. The side chat is closed afterwards; the Crew's own model is never changed.

It reports and never gates a deploy: the exit code is 0 unless the check itself could not run.

  python3 scripts/post-deploy-smoke.py --login NAME --workspace-id work:<crew id> [--providers codex-cli,muse-cli]
"""
import argparse
import json
import os
import platform
import subprocess
import sys
import tempfile
import time
import urllib.request
import uuid
from concurrent.futures import ThreadPoolExecutor, as_completed


PROMPT = ("Post-deploy smoke check. Call workspace action=owners once for this Crew to list its owners, "
          "then reply with exactly OWNERS=<number of owners, counting the main owner and co-owners> and nothing else.")


# The second turn: the CLI's own shell, in the Crew's folder, as whatever account the platform launched it as.
SHELL_PROMPT = ("Post-deploy smoke check, part 2. Use your shell tool to run exactly this one command in the Crew folder: "
                "f=.smoke-$$; echo ok > $f && cat $f && rm $f; id -un\n"
                "Then reply with exactly RUNAS=<the account name it printed> FILE=<ok if it printed ok, else fail> and nothing else.")


# The third turn (with --app-host): start a tiny app the way a person's app is started from chat, detached, on this
# program's own port, so it can be opened from outside and must keep running through the next deploy (PLAT-856-506).
APP_PROMPT = ("Post-deploy smoke check, part 3. Use your shell tool to run exactly this one command in the Crew folder:\n"
              "{command}\n"
              "Then reply with exactly APP=<the text it printed last> and nothing else.")


def app_command(provider, port, started):
    script = f".smoke-app-{provider}.py"
    body = (f"import http.server,socketserver\n"
            f"class H(http.server.BaseHTTPRequestHandler):\n"
            f"    def do_GET(self):\n"
            f"        self.send_response(200); self.end_headers(); self.wfile.write(b'smoke-app {provider} {started}')\n"
            f"    def log_message(self, *a): pass\n"
            f"socketserver.TCPServer.allow_reuse_address = True\n"
            f"socketserver.TCPServer(('0.0.0.0', {port}), H).serve_forever()\n")
    # An app still running from an earlier run is kept (that is the point: it must outlive restarts); a sandboxed
    # shell cannot see other processes to stop it anyway.
    return (f"curl -s -m 3 http://127.0.0.1:{port}/ | grep -q 'smoke-app {provider} ' && {{ curl -s http://127.0.0.1:{port}/; exit 0; }}; printf '%s' \"{body}\" > {script} && "
            f"(setsid nohup python3 {script} > /dev/null 2>&1 < /dev/null &) ; sleep 2; curl -s http://127.0.0.1:{port}/")


def app_port(provider, base):
    # A fixed port per program, so the next deploy knows where to look.
    order = ["agy-cli", "claude-code", "codex-cli", "cursor-cli", "muse-cli", "pi-cli"]
    return base + (order.index(provider) if provider in order else len(order) + sum(map(ord, provider)) % 10)


def fetch(host, port):
    try:
        with urllib.request.urlopen(f"http://{host}:{port}/", timeout=5) as reply:
            return reply.read().decode("utf-8", "replace").strip()
    except Exception as error:
        return f"unreachable ({first_line(str(error))})"


def login_path(name):
    # The same place the CLI saves `agentworks login --name NAME` (os.UserConfigDir()/agentworks/logins).
    if platform.system() == "Darwin":
        base = os.path.expanduser("~/Library/Application Support")
    elif platform.system() == "Windows":
        base = os.environ.get("APPDATA", "")
    else:
        base = os.environ.get("XDG_CONFIG_HOME") or os.path.expanduser("~/.config")
    return os.path.join(base, "agentworks", "logins", name + ".json")


def call(config, tool, args, timeout=60):
    with tempfile.NamedTemporaryFile("w", suffix=".json") as handle:
        json.dump(args, handle)
        handle.flush()
        done = subprocess.run(["agentworks", "--config", config, "--json", "tools", "call", tool, "--input", handle.name],
                              capture_output=True, text=True, timeout=timeout)
    try:
        body = json.loads(done.stdout or "{}")
    except json.JSONDecodeError:
        body = {}
    if done.returncode != 0 or (isinstance(body, dict) and body.get("error")):
        error = body.get("error") if isinstance(body, dict) else None
        raise RuntimeError(first_line(error.get("message") if isinstance(error, dict) else done.stderr or done.stdout))
    return body


def first_line(text):
    return (text or "").strip().splitlines()[0][:200] if (text or "").strip() else "no output"


def configured_providers(chats):
    options = {item.get("provider"): item for item in chats.get("provider_options", [])}
    out = []
    for account in chats.get("provider_connections", []):
        if not account.get("configured") or not account.get("usable") or account.get("relation") != "server":
            continue
        provider = account.get("provider")
        models = account.get("allowed_models") or (options.get(provider) or {}).get("models") or []
        model = models[0] if models else (options.get(provider) or {}).get("model_id", "")
        out.append({"provider": provider, "model_id": model, "connection_id": account.get("id")})
    return out


def turn(config, workspace, chat_id, prompt, started, limit, accept, expected):
    """Send prompt in the chat and follow that one turn. Returns (problems, answer): no problems when it was accepted
    once and answered once with a reply accept() takes."""
    asked = call(config, "workspace", {"action": "ask", "workspace_id": workspace, "chat_id": chat_id, "submission_id": str(uuid.uuid4()),
                                       "message": prompt})
    query = asked.get("query_id", "")
    after, messages = 0, []

    def mine(message):
        # After a switch of AI program the platform stores its hand-over notice in front of the person's text,
        # which follows "[USER MESSAGE]" (shown as a notice on the website).
        text = message.get("text") or ""
        return message.get("role") == "you" and (text == prompt or text.endswith("\n[USER MESSAGE]\n" + prompt))

    while time.monotonic() - started < limit:
        page = call(config, "workspace", {"action": "read_chat", "workspace_id": workspace, "chat_id": chat_id, "query_id": query,
                                          "after": after, "wait_seconds": 25})
        messages += page.get("messages") or []
        after = page.get("next_after", after)
        # Only this prompt's own answer ends the wait: in a chat with history an earlier turn's answer is already there
        # before this turn starts, and stopping on it read the turn as never accepted.
        turns = {m.get("turn") for m in messages if mine(m)}
        if not page.get("working") and any(m.get("role") == "assistant" and m.get("turn") in turns for m in messages):
            break
    # Chat messages carry the chat's own turn id: follow the turn of the message this check sent.
    sent = [m for m in messages if mine(m)]
    turns = {m.get("turn") for m in sent}
    this = [m for m in messages if m.get("turn") in turns]
    answers = [m.get("text", "").strip() for m in this if m.get("role") == "assistant" and m.get("text", "").strip()]
    problems = []
    if len(sent) != 1:
        problems.append(f"accepted {len(sent)} times")
    if not answers:
        problems.append(f"no answer within {limit}s")
    elif len(answers) > 1:
        problems.append(f"{len(answers)} answers")
    elif not accept(answers[0]):
        problems.append(f"answer {answers[0][:80]!r} lacks {expected}")
    if problems:
        tools = [m.get("text", "") for m in this if m.get("role") == "tool"]
        problems.append("tools: " + (", ".join(tools[:6]) or "none"))
    return problems, answers[0] if len(answers) == 1 else ""


def smoke(config, workspace, target, owners, limit, app=None):
    started = time.monotonic()
    chat_id = None
    try:
        try:
            opened = call(config, "workspace", {"action": "open_chat", "workspace_id": workspace, "name": f"smoke {target['provider']}"})
        except Exception:  # one retry for a first call that hung on a cold server
            time.sleep(5)
            opened = call(config, "workspace", {"action": "open_chat", "workspace_id": workspace, "name": f"smoke {target['provider']}"})
        chat_id = opened["chat_id"]
        choice = {"action": "set", "scope": "tab", "workspace_id": workspace, "chat_id": chat_id, "engine": target["provider"],
                  "connection_id": target["connection_id"]}
        if target["model_id"]:
            choice["model_id"] = target["model_id"]
        call(config, "models", choice)
        problems, _ = turn(config, workspace, chat_id, PROMPT, started, limit,
                           lambda answer: f"OWNERS={owners}" in answer.replace(" ", ""), f"OWNERS={owners}")
        if not problems:
            problems, answer = turn(config, workspace, chat_id, SHELL_PROMPT, started, limit,
                                    lambda answer: "FILE=ok" in answer.replace(" ", ""), "FILE=ok")
            problems = [f"turn 2: {p}" for p in problems]
            target["runas"] = next((w[len("RUNAS="):] for w in answer.replace("RUNAS=", " RUNAS=").split() if w.startswith("RUNAS=")), "")
        if not problems and app:
            host, port, stamp = app
            expected = f"smoke-app {target['provider']} "
            app_problems, _ = turn(config, workspace, chat_id, APP_PROMPT.format(command=app_command(target["provider"], port, stamp)),
                                   started=time.monotonic(), limit=limit, accept=lambda answer: expected in answer, expected=expected)
            seen = fetch(host, port)
            target["app"] = (f"app on :{port} reachable from outside" if seen.startswith(expected)
                             else f"app on :{port} not reachable from outside: {seen[:80]}")
            problems = [f"turn 3: {p}" for p in app_problems] + ([] if seen.startswith(expected) else [target["app"]])
        return problems, time.monotonic() - started
    except Exception as error:  # a failed call is a failed smoke result, not a crashed check
        return [f"error: {first_line(str(error))}"], time.monotonic() - started
    finally:
        if chat_id:
            try:
                # A chat that is still working cannot be closed; stop it first (nothing to stop is fine).
                try:
                    call(config, "workspace", {"action": "stop", "workspace_id": workspace, "chat_id": chat_id})
                except Exception:
                    pass
                call(config, "workspace", {"action": "close_chat", "workspace_id": workspace, "chat_id": chat_id})
            except Exception as error:
                print(f"  note: could not close smoke chat {chat_id}: {first_line(str(error))}")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--login", required=True, help="saved login name (agentworks login --name NAME) of the smoke connection")
    parser.add_argument("--workspace-id", required=True, help="the smoke Crew, work:<crew id>")
    parser.add_argument("--providers", default="", help="comma-separated subset; default: every configured company account")
    parser.add_argument("--timeout", type=int, default=420, help="seconds per provider (both turns)")
    parser.add_argument("--app-host", default="",
                        help="public address: each program starts a small app on its own port there, and the next run checks it survived")
    parser.add_argument("--app-port-base", type=int, default=20310, help="first of the app ports, one per program")
    parser.add_argument("--require-slot", default="",
                        help="comma-separated programs whose shell must run as the Crew owner's slot, or 1 for every program "
                             "(servers with slots, PLAT-872-86)")
    args = parser.parse_args()

    config = login_path(args.login)
    if not os.path.isfile(config):
        print(f"SMOKE: could not run: no saved login {args.login!r} on this computer (agentworks login --name {args.login})")
        return 1
    # Right after activation the first calls can hang until the gateway gives up (502 after ~58 s on four parallel
    # calls, 2 Nov). Wait until a light call answers quickly before starting the providers at the same time.
    deadline = time.monotonic() + 180
    while True:
        started = time.monotonic()
        try:
            call(config, "workspace", {"action": "chats", "workspace_id": args.workspace_id}, timeout=10)
            if time.monotonic() - started < 5:
                break
        except Exception:
            pass
        if time.monotonic() > deadline:
            break
        time.sleep(5)
    try:
        chats = call(config, "workspace", {"action": "chats", "workspace_id": args.workspace_id})
        state = call(config, "workspace", {"action": "owners", "workspace_id": args.workspace_id})
    except Exception as error:
        print(f"SMOKE: could not run: {first_line(str(error))}")
        return 1
    owners = 1 + len(state.get("co_owners") or [])
    targets = configured_providers(chats)
    wanted = {name.strip() for name in args.providers.split(",") if name.strip()}
    if wanted:
        targets = [target for target in targets if target["provider"] in wanted]
    if not targets:
        print("SMOKE: no configured company provider account to check")
        return 0
    failed = 0
    started = time.strftime("%Y%m%dT%H%M%S")
    if args.app_host:
        # The apps the last run started: still answering means they kept running through this deploy's restart.
        for target in targets:
            port = app_port(target["provider"], args.app_port_base)
            seen = fetch(args.app_host, port)
            if seen.startswith(f"smoke-app {target['provider']} "):
                print(f"APP KEPT {target['provider']} :{port} (started {seen.split()[-1]})", flush=True)
            else:
                print(f"APP LOST {target['provider']} :{port}: {seen[:80]}", flush=True)
    # Each provider has its own side chat and its own file name, so they run at the same time: the check takes as long
    # as the slowest provider, not the sum (four providers took ~3 minutes one after another). Results print as they finish.
    with ThreadPoolExecutor(max_workers=len(targets)) as pool:
        running = {pool.submit(smoke, config, args.workspace_id, target, owners, args.timeout,
                               (args.app_host, app_port(target["provider"], args.app_port_base), started) if args.app_host else None): target
                   for target in targets}
        results = ((running[done], *done.result()) for done in as_completed(running))
        for target, problems, elapsed in results:
            # On a server with slots every program runs as the Crew owner's slot; the app account there is a failure.
            must = {name.strip() for name in args.require_slot.split(",") if name.strip()}
            if (must == {"1"} or target["provider"] in must) and not problems and not target.get("runas", "").startswith("slot"):
                problems = [f"runs as {target.get('runas') or 'unknown'}, not the Crew owner's slot"]
            failed += bool(problems)
            verdict = "PASS" if not problems else "FAIL"
            runas = f" runs as {target['runas']}" if target.get("runas") else ""
            runas += f", {target['app']}" if target.get("app") else ""
            print(f"SMOKE {verdict} {target['provider']} ({target['model_id'] or 'default'}){runas} {elapsed:.0f}s" + (": " + "; ".join(problems) if problems else ""), flush=True)
    print(f"SMOKE: {len(targets) - failed}/{len(targets)} providers passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
