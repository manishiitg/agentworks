#!/usr/bin/env python3
"""On-request Crew agent tests (AGENTS.md Tests rule 8; catalog: the deployments repo's plat-872-agent-tests.md).
For every company provider the server has configured, in the server's private Smoke test Crew (products/<server>/
smoke.env), each scenario opens a side chat on that provider, asks the agent to run one fixed command with its own
shell and paste the output verbatim, and judges PASS/FAIL from that output, never from the agent's own opinion.
Commands print only names and markers, never a value. An agent that will not run the command is reported as
NOT RUN (nothing proven), not as a pass.

  python3 scripts/agent-test.py --login NAME --workspace-id work:<crew id> [--providers a,b] [--only S1,S2]
"""
import argparse
import importlib.util
import os
import re
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("smoke", os.path.join(HERE, "post-deploy-smoke.py"))
smoke = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(smoke)

# Login and service setting names a Crew agent's shell must never see. SECRET_<NAME> values the Crew selected are
# allowed by design and are not listed.
FORBIDDEN_ENV = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY",
                 "CURSOR_API_KEY", "META_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY",
                 "AUTH_SECRET", "ACCESS_PASSWORD", "AUTH_USERS", "AUTH_ALLOWED_EMAILS", "MCP_SERVER_API_TOKEN",
                 "MCP_BRIDGE_TOKEN_SECRET", "SSH_AUTH_SOCK", "AGENTWORKS_SLOT_CLI_USERS"]

EMPTY_FP = "e3b0c44298fc"  # sha256 of no input: the read was refused

# Each scenario: one shell command (prints names/markers only) and a judge over its output. A judge returns the
# problems found; [] is a pass. Every command ends with DONE so a cut-off output is not taken as a pass.
SCENARIOS = {
    "S1": ("The agent's shell environment holds no login or service settings (PLAT-491, PLAT-622, PLAT-663, 872-35)",
           "echo NAMES: $(env | cut -d= -f1 | sort | tr '\\n' ' '); echo TMUX: $(tmux show-environment -g 2>/dev/null | cut -d= -f1 | tr '\\n' ' '); echo DONE",
           lambda out: [f"{name} visible" for name in FORBIDDEN_ENV if re.search(r"(^|[\s-])" + name + r"(\s|$)", out)]),
    "S2": ("No way out of the sandbox: the slot's systemd, the CLI's own process, the server's login files (872-89, 872-84)",
           "systemd-run --user --quiet --pipe true >/dev/null 2>&1 && echo ESCAPE_SYSTEMD; "
           "p=$PPID; while [ \"$p\" -gt 1 ]; do tr '\\0' '\\n' < /proc/$p/environ 2>/dev/null | cut -d= -f1 | grep -qE 'OAUTH_TOKEN|_API_KEY' && echo PARENT_LOGIN_VISIBLE; p=$(awk '{print $4}' /proc/$p/stat 2>/dev/null || echo 1); done; "
           "for f in /srv/*/home/.claude/.credentials.json /srv/*/home/.codex/auth.json /srv/*/home/.config/cursor/auth.json /var/lib/*/.claude/.credentials.json /var/lib/*/.codex/auth.json; do test -r \"$f\" && echo LOGIN_FILE_READABLE; done; echo DONE",
           lambda out: [m for m in ("ESCAPE_SYSTEMD", "PARENT_LOGIN_VISIBLE", "LOGIN_FILE_READABLE") if m in out]),
    "S3": ("No other people's or other Crews' data: users list, other users' folders, other Crews, workflows (PLAT-395, PLAT-456, PLAT-457)",
           "d=$(pwd -P); r=${d%%/Crew/*}; me=${d#\"$r\"/Crew/}; me=${me%%/*}; "
           "echo USERS_FP=$(head -c 64 \"$r/config/users.json\" 2>/dev/null | sha256sum | cut -c1-12); ls \"$r/_users\" >/dev/null 2>&1 && echo LIST_USERS; "
           "for c in \"$r\"/Crew/*/; do case \"$c\" in *\"/$me/\") ;; *) ls \"$c\" >/dev/null 2>&1 && echo READ_OTHER_CREW && break;; esac; done; "
           "w=$(ls -d \"$r\"/Workflow/*/workflow.json 2>/dev/null | head -1); echo WF_FP=$(head -c 64 \"$w\" 2>/dev/null | sha256sum | cut -c1-12); "
           "case \"$d\" in */Crew/*) echo IN_CREW;; *) echo NOT_IN_CREW;; esac; echo DONE",
           # A fingerprint of the file's first bytes (not the content) proves a read; e3b0c44298fc is empty input (refused).
           lambda out: [f"read {what} (fingerprint {fp})" for what, fp in re.findall(r"(USERS|WF)_FP=([0-9a-f]{12})", out) if fp != EMPTY_FP]
                       + [m for m in ("LIST_USERS", "READ_OTHER_CREW") if m in out]
                       + (["shell did not start in the Crew folder, so other-Crew reads were not tried"] if "NOT_IN_CREW" in out else [])),
}

PROMPT = ("AgentWorks platform self-test {id}, run by the platform's operators to check this server's sandbox. "
          "Use your shell tool to run exactly this one command in the Crew folder, unchanged:\n{command}\n"
          "Then reply with only its complete output between <out> and </out>, nothing else.")


def run(config, workspace, target, sid, limit):
    title, command, judge = SCENARIOS[sid]
    started, chat_id = time.monotonic(), None
    try:
        chat_id = smoke.call(config, "workspace", {"action": "open_chat", "workspace_id": workspace, "name": f"agent-test {sid} {target['provider']}"})["chat_id"]
        choice = {"action": "set", "scope": "tab", "workspace_id": workspace, "chat_id": chat_id, "engine": target["provider"], "connection_id": target["connection_id"]}
        if target["model_id"]:
            choice["model_id"] = target["model_id"]
        smoke.call(config, "models", choice)
        problems, answer = smoke.turn(config, workspace, chat_id, PROMPT.format(id=sid, command=command), started, limit,
                                      lambda a: "<out>" in a and "DONE" in a, "<out>...DONE</out>")
        if problems:
            return "NOT RUN", problems
        out = answer.split("<out>", 1)[1].split("</out>", 1)[0]
        found = judge(out)
        return ("FAIL", found) if found else ("PASS", [])
    except Exception as error:
        return "NOT RUN", [f"error: {smoke.first_line(str(error))}"]
    finally:
        if chat_id:
            for action in ("stop", "close_chat"):
                try:
                    smoke.call(config, "workspace", {"action": action, "workspace_id": workspace, "chat_id": chat_id})
                except Exception:
                    pass


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--login", required=True)
    parser.add_argument("--workspace-id", required=True)
    parser.add_argument("--providers", default="")
    parser.add_argument("--only", default="", help="comma-separated scenario ids; default: all")
    parser.add_argument("--timeout", type=int, default=300)
    args = parser.parse_args()
    config = smoke.login_path(args.login)
    if not os.path.isfile(config):
        print(f"AGENT-TEST: could not run: no saved login {args.login!r}")
        return 1
    try:
        chats = smoke.call(config, "workspace", {"action": "chats", "workspace_id": args.workspace_id})
    except Exception as error:
        print(f"AGENT-TEST: could not run: {smoke.first_line(str(error))}")
        return 1
    targets = smoke.configured_providers(chats)
    wanted = {p.strip() for p in args.providers.split(",") if p.strip()}
    targets = [t for t in targets if not wanted or t["provider"] in wanted]
    ids = [s.strip() for s in args.only.split(",") if s.strip()] or list(SCENARIOS)
    tally = {"PASS": 0, "FAIL": 0, "NOT RUN": 0}
    for sid in ids:
        print(f"== {sid}: {SCENARIOS[sid][0]}", flush=True)
        for target in targets:
            verdict, problems = run(config, args.workspace_id, target, sid, args.timeout)
            tally[verdict] += 1
            print(f"AGENT-TEST {verdict} {sid} {target['provider']}" + (": " + "; ".join(problems) if problems else ""), flush=True)
    print(f"AGENT-TEST: {tally['PASS']} passed, {tally['FAIL']} failed, {tally['NOT RUN']} not run")
    return 0


if __name__ == "__main__":
    sys.exit(main())
