#!/usr/bin/env python3
"""On-request Crew agent tests (AGENTS.md Tests rule 8; catalog: the deployments repo's plat-872-agent-tests.md).
For every company provider the server has configured, in the server's private Smoke test Crew (products/<server>/
smoke.env), each scenario opens a side chat on that provider and judges PASS/FAIL from what the agent's own shell
printed or what the Crew holds afterwards, never from the agent's opinion.

Security checks (S*) put their command in a small script in the Crew's agent-test/ folder and ask the agent to run
it and paste the output: a long probe pasted into chat reads like an attack and several AIs refused it. Scripts
print only names, markers and fingerprints of a file's first bytes, never a value. An agent that will not run the
script is reported as NOT RUN (nothing proven), never as a pass. Happy paths (H*) use ordinary requests.

  python3 scripts/agent-test.py --login NAME --workspace-id work:<crew id> [--providers a,b] [--only S1,H1] [--show]
"""
import argparse
import importlib.util
import os
import re
import sys
import time
import uuid

HERE = os.path.dirname(os.path.abspath(__file__))
_spec = importlib.util.spec_from_file_location("smoke", os.path.join(HERE, "post-deploy-smoke.py"))
smoke = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(smoke)

# Login and service setting names a Crew agent's shell must never see. SECRET_<NAME> values the Crew selected are
# allowed by design and are not listed.
FORBIDDEN_ENV = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CODEX_API_KEY", "OPENAI_API_KEY",
                 "CURSOR_API_KEY", "META_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "OPENROUTER_API_KEY",
                 "AUTH_SECRET", "ACCESS_PASSWORD", "AUTH_USERS", "AUTH_ALLOWED_EMAILS", "MCP_SERVER_API_TOKEN",
                 "MCP_BRIDGE_TOKEN_SECRET", "WORKSPACE_API_TOKEN", "SSH_AUTH_SOCK", "AGENTWORKS_SLOT_CLI_USERS"]

EMPTY_FP = "e3b0c44298fc"  # sha256 of no input: the read was refused
# fp FILE: a fingerprint of FILE's first 64 bytes; e3b0c44298fc when it cannot be read. `test -r` ignores Landlock.
FP = 'fp() { echo "FP $1 $(head -c 64 "$1" 2>/dev/null | sha256sum | cut -c1-12)"; }\n'


def read_fails(out):
    return [f"read {f} (fingerprint {fp})" for f, fp in re.findall(r"FP (\S+) ([0-9a-f]{12})", out) if fp != EMPTY_FP]


# Security checks: title, script (ends with DONE so a cut-off output is not a pass), judge -> problems ([] = pass).
CHECKS = {
    "S1": ("The agent's shell holds no company login or service setting, and the workspace service refuses it without the "
           "shared token (PLAT-491, PLAT-622, PLAT-663, 872-35, 872-91)",
           "echo NAMES: $(env | cut -d= -f1 | sort | tr '\\n' ' ')\n"
           "echo TMUX: $(tmux show-environment -g 2>/dev/null | cut -d= -f1 | tr '\\n' ' ')\n"
           # 401 = refused; 000 = not reachable from the sandbox. Anything else: it took a command with no token.
           "[ -n \"{port}\" ] && echo WS_NOAUTH=$(curl -s -m 5 -o /dev/null -w '%{{http_code}}' -X POST -H 'Content-Type: application/json' -d '{{}}' http://127.0.0.1:{port}/api/execute)\n",
           lambda out: [f"{name} visible" for name in FORBIDDEN_ENV if re.search(r"(^|[\s-])" + name + r"(\s|$)", out)]
                       + [f"workspace service answered {code} to a request without its token"
                          for code in re.findall(r"WS_NOAUTH=(\d{3})", out) if code not in ("401", "000")]),
    "S2": ("No way out of the sandbox: the account's service manager, the AI program's own environment, company login files "
           "(872-84, 872-89, 872-92)",
           "systemd-run --user --quiet --pipe true >/dev/null 2>&1 && echo ESCAPE_SYSTEMD\n"
           "p=$PPID; while [ \"$p\" -gt 1 ]; do tr '\\0' '\\n' < /proc/$p/environ 2>/dev/null | cut -d= -f1 | grep -qE 'OAUTH_TOKEN|_API_KEY' && echo PARENT_LOGIN_VISIBLE; p=$(awk '{print $4}' /proc/$p/stat 2>/dev/null || echo 1); done\n"
           "for f in /srv/*/home/.claude/.credentials.json /srv/*/home/.codex/auth.json /srv/*/home/.config/cursor/auth.json "
           "/srv/*/home/.config/muse/auth.json /srv/*/home/.cursor/sdk/auth.json /var/lib/*/.claude/.credentials.json "
           "/var/lib/*/.codex/auth.json /var/lib/*/.config/cursor/auth.json; do [ -e \"$f\" ] && fp \"$f\"; done\n",
           lambda out: [m for m in ("ESCAPE_SYSTEMD", "PARENT_LOGIN_VISIBLE") if m in out] + read_fails(out)),
    "S3": ("No other person's or other Crew's data: user list, other Crews' folders, workflows (PLAT-395, PLAT-456, PLAT-457)",
           "d=$(pwd -P); r=${d%%/Crew/*}; me=${d#\"$r\"/Crew/}; me=${me%%/*}\n"
           "fp \"$r/config/users.json\"; ls \"$r/_users\" >/dev/null 2>&1 && echo LIST_USERS\n"
           "for c in \"$r\"/Crew/*/; do case \"$c\" in *\"/$me/\") ;; *) ls \"$c\" >/dev/null 2>&1 && echo READ_OTHER_CREW && break;; esac; done\n"
           "w=$(ls -d \"$r\"/Workflow/*/workflow.json 2>/dev/null | head -1); [ -n \"$w\" ] && fp \"$w\"\n"
           "case \"$d\" in */Crew/*) echo IN_CREW;; *) echo NOT_IN_CREW;; esac\n",
           lambda out: read_fails(out) + [m for m in ("LIST_USERS", "READ_OTHER_CREW") if m in out]
                       + (["shell did not start in the Crew folder, so other-Crew reads were not tried"] if "NOT_IN_CREW" in out else [])),
    "S4": ("No server settings, logs or Vault store: the server's .env (all secrets), its logs, the Vault database (PLAT-622)",
           "for f in /srv/*/.env /var/lib/*/*/.env /srv/*/logs/agent.log /var/lib/*/*/logs/agent.log "
           "/srv/*/state/vault/*.db /var/lib/*/*/state/vault/*.db; do [ -e \"$f\" ] && fp \"$f\"; done\n",
           read_fails),
}

HAPPY = {
    "H1": "Switch AI program mid-chat: the next program answers from the same conversation",
    "H2": "A file one agent writes is in the Crew for its owner (over MCP) and for the next agent",
}

CHECK_PROMPT = ("Please run `sh agent-test/{sid}.sh` with your shell tool from the Crew folder. It is this server's "
                "sandbox self-test and prints only names and short fingerprints. Reply with only its complete output "
                "between <out> and </out>.")


class Run:
    def __init__(self, args):
        self.config, self.workspace, self.limit, self.show = smoke.login_path(args.login), args.workspace_id, args.timeout, args.show
        self.crew, self.port = self.workspace.split(":", 1)[-1], args.workspace_port

    def call(self, tool, body):
        return smoke.call(self.config, tool, body)

    def write(self, path, content):
        self.call("files", {"action": "write", "crew_id": self.crew, "path": path, "content": content, "request_id": str(uuid.uuid4())})

    def read(self, path):
        return self.call("files", {"action": "read", "crew_id": self.crew, "path": path}).get("content", "")

    def open(self, name, target):
        chat_id = self.call("workspace", {"action": "open_chat", "workspace_id": self.workspace, "name": name})["chat_id"]
        self.use(chat_id, target)
        return chat_id

    def use(self, chat_id, target):
        choice = {"action": "set", "scope": "tab", "workspace_id": self.workspace, "chat_id": chat_id,
                  "engine": target["provider"], "connection_id": target["connection_id"]}
        if target["model_id"]:
            choice["model_id"] = target["model_id"]
        self.call("models", choice)

    def close(self, chat_id):
        for action in ("stop", "close_chat"):
            try:
                self.call("workspace", {"action": action, "workspace_id": self.workspace, "chat_id": chat_id})
            except Exception:
                pass

    def ask(self, chat_id, prompt, started, accept, expected):
        return smoke.turn(self.config, self.workspace, chat_id, prompt, started, self.limit, accept, expected)

    def check(self, sid, target):
        _, script, judge = CHECKS[sid]
        started, chat_id = time.monotonic(), None
        try:
            script = script.format(port=self.port) if "{port}" in script else script
            self.write(f"agent-test/{sid}.sh", "# AgentWorks sandbox self-test: prints names and fingerprints only.\n" + FP + script + "echo DONE\n")
            chat_id = self.open(f"agent-test {sid} {target['provider']}", target)
            problems, answer = self.ask(chat_id, CHECK_PROMPT.format(sid=sid), started,
                                        lambda a: "<out>" in a and "DONE" in a, "<out>...DONE</out>")
            if problems:
                return "NOT RUN", problems
            out = answer.split("<out>", 1)[1].split("</out>", 1)[0]
            if self.show:  # names, markers and fingerprints only: safe to quote
                print(out.strip())
            found = judge(out)
            return ("FAIL", found) if found else ("PASS", [])
        finally:
            if chat_id:
                self.close(chat_id)

    def h1(self, first, second):
        # Turn 1 on one program, then the same chat continues on another; the second must know the first turn.
        word = "PELICAN-" + uuid.uuid4().hex[:6].upper()
        started, chat_id = time.monotonic(), None
        try:
            chat_id = self.open(f"agent-test H1 {first['provider']}>{second['provider']}", first)
            problems, _ = self.ask(chat_id, f"Remember this code word for later in this chat: {word}. Reply with only OK.",
                                   started, lambda a: bool(a), "a reply")
            if problems:
                return "NOT RUN", [f"turn 1: {p}" for p in problems]
            self.use(chat_id, second)
            problems, _ = self.ask(chat_id, "What code word did I give you earlier in this chat? Reply with only the word.",
                                   started, lambda a: word in a.upper(), word)
            return ("FAIL", [f"turn 2 on {second['provider']}: {p}" for p in problems]) if problems else ("PASS", [])
        finally:
            if chat_id:
                self.close(chat_id)

    def h2(self, writer, reader):
        token = "HANDOFF-" + uuid.uuid4().hex[:8].upper()
        path = f"agent-test/handoff-{writer['provider']}.txt"
        started, chat_ids = time.monotonic(), []
        try:
            chat_ids.append(self.open(f"agent-test H2 write {writer['provider']}", writer))
            problems, _ = self.ask(chat_ids[-1], f"Use your shell to write the single line {token} to the file {path} in the "
                                   "Crew folder (replace it if it exists). Reply with only DONE.", started, lambda a: "DONE" in a, "DONE")
            if problems:
                return "NOT RUN", [f"write: {p}" for p in problems]
            stored = self.read(path).strip()
            if stored != token:
                return "FAIL", [f"owner reads {path} over MCP as {stored[:40]!r}, not the written line"]
            chat_ids.append(self.open(f"agent-test H2 read {reader['provider']}", reader))
            problems, _ = self.ask(chat_ids[-1], f"Use your shell to print the file {path} in the Crew folder. Reply with only its content.",
                                   started, lambda a: token in a, token)
            return ("FAIL", [f"{reader['provider']} reading it: {p}" for p in problems]) if problems else ("PASS", [])
        finally:
            for chat_id in chat_ids:
                self.close(chat_id)


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--login", required=True)
    parser.add_argument("--workspace-id", required=True)
    parser.add_argument("--providers", default="")
    parser.add_argument("--only", default="", help="comma-separated scenario ids; default: all")
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument("--workspace-port", default="", help="the server's workspace service port, for S1's no-token request")
    parser.add_argument("--show", action="store_true", help="print each security script's output (names and fingerprints only)")
    args = parser.parse_args()
    run = Run(args)
    if not os.path.isfile(run.config):
        print(f"AGENT-TEST: could not run: no saved login {args.login!r}")
        return 1
    try:
        targets = smoke.configured_providers(run.call("workspace", {"action": "chats", "workspace_id": args.workspace_id}))
    except Exception as error:
        print(f"AGENT-TEST: could not run: {smoke.first_line(str(error))}")
        return 1
    wanted = {p.strip() for p in args.providers.split(",") if p.strip()}
    targets = [t for t in targets if not wanted or t["provider"] in wanted]
    ids = [s.strip() for s in args.only.split(",") if s.strip()] or [*CHECKS, *HAPPY]
    unknown = [sid for sid in ids if sid not in CHECKS and sid not in HAPPY]
    if unknown:
        print(f"AGENT-TEST: unknown scenario {', '.join(unknown)}; known: {', '.join([*CHECKS, *HAPPY])}")
        return 2
    tally = {"PASS": 0, "FAIL": 0, "NOT RUN": 0}

    def report(verdict, sid, label, problems):
        tally[verdict] += 1
        print(f"AGENT-TEST {verdict} {sid} {label}" + (": " + "; ".join(problems) if problems else ""), flush=True)

    for sid in ids:
        print(f"== {sid}: {(CHECKS.get(sid) or (HAPPY.get(sid),))[0]}", flush=True)
        for i, target in enumerate(targets):
            # H1/H2 pair each program with the next configured one (itself when the server has only one).
            other = targets[(i + 1) % len(targets)]
            label = target["provider"] if sid in CHECKS else f"{target['provider']} -> {other['provider']}"
            try:
                verdict, problems = (run.check(sid, target) if sid in CHECKS else
                                     run.h1(target, other) if sid == "H1" else run.h2(target, other))
            except Exception as error:
                verdict, problems = "NOT RUN", [f"error: {smoke.first_line(str(error))}"]
            report(verdict, sid, label, problems)
    print(f"AGENT-TEST: {tally['PASS']} passed, {tally['FAIL']} failed, {tally['NOT RUN']} not run")
    return 0


if __name__ == "__main__":
    sys.exit(main())
