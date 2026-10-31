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
import uuid


PROMPT = ("Post-deploy smoke check. Call workspace action=owners once for this Crew to list its owners, "
          "then reply with exactly OWNERS=<number of owners, counting the main owner and co-owners> and nothing else.")


# The second turn: the CLI's own shell, in the Crew's folder, as whatever account the platform launched it as.
SHELL_PROMPT = ("Post-deploy smoke check, part 2. Use your shell tool to run exactly this one command in the Crew folder: "
                "f=.smoke-$$; echo ok > $f && cat $f && rm $f; id -un\n"
                "Then reply with exactly RUNAS=<the account name it printed> FILE=<ok if it printed ok, else fail> and nothing else.")


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
    while time.monotonic() - started < limit:
        page = call(config, "workspace", {"action": "read_chat", "workspace_id": workspace, "chat_id": chat_id, "query_id": query,
                                          "after": after, "wait_seconds": 25})
        messages += page.get("messages") or []
        after = page.get("next_after", after)
        if not page.get("working") and any(m.get("role") == "assistant" for m in messages):
            break
    # Chat messages carry the chat's own turn id: follow the turn of the message this check sent.
    sent = [m for m in messages if m.get("role") == "you" and m.get("text") == prompt]
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


def smoke(config, workspace, target, owners, limit):
    started = time.monotonic()
    chat_id = None
    try:
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
    args = parser.parse_args()

    config = login_path(args.login)
    if not os.path.isfile(config):
        print(f"SMOKE: could not run: no saved login {args.login!r} on this computer (agentworks login --name {args.login})")
        return 1
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
    for target in targets:
        problems, elapsed = smoke(config, args.workspace_id, target, owners, args.timeout)
        failed += bool(problems)
        verdict = "PASS" if not problems else "FAIL"
        runas = f" runs as {target['runas']}" if target.get("runas") else ""
        print(f"SMOKE {verdict} {target['provider']} ({target['model_id'] or 'default'}){runas} {elapsed:.0f}s" + (": " + "; ".join(problems) if problems else ""), flush=True)
    print(f"SMOKE: {len(targets) - failed}/{len(targets)} providers passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
