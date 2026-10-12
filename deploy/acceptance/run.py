#!/usr/bin/env python3
"""Acceptance run: send real messages to a server as the signed-in test user and check the answers.

Run it by hand against one server (it is not part of a deploy):

    agentworks --server https://<server> login          # once, as the test user
    deploy/acceptance/run.py --server https://<server> [--area sandbox] [--slots]

It uses the `agentworks` CLI's tool calls (the same API as MCP), so it needs only a signed-in test account. It creates one scratch
Crew called "QA acceptance" the first time and reuses it. Each case prints PASS/FAIL/SKIP with the reason; the exit code is 1 when
any case fails. See catalog.json for the messages and what each must (or must not) say.
"""
import argparse
import concurrent.futures
import json
import re
import subprocess
import sys
import time
import uuid
from pathlib import Path


CODE = {"on": False, "title": None}  # --code: message cases go to a Code project through the `code` tool instead of a Crew
CONFIG = []  # ["--config", path] when the run uses a saved login other than the default


def call(server, tool, args, timeout=120):
    process = subprocess.run(
        ["agentworks", *CONFIG, "--json", "--server", server, "tools", "call", tool, "--input", "-"],
        input=json.dumps(args), capture_output=True, text=True, timeout=timeout,
    )
    text = (process.stdout or process.stderr or "").strip()
    try:
        return process.returncode, json.loads(text) if text else {}, text
    except ValueError:
        return process.returncode, {}, text


def fill(value, crew, variables):
    """Put the scratch Crew's id and values captured by earlier steps into a case's arguments."""
    if isinstance(value, str):
        value = value.replace("{crew}", crew or "")
        for name, found in variables.items():
            value = value.replace("{v.%s}" % name, found)
        return value
    if isinstance(value, dict):
        return {k: fill(v, crew, variables) for k, v in value.items()}
    if isinstance(value, list):
        return [fill(v, crew, variables) for v in value]
    return value


def find_text(value):
    """All of a result's text, for matching: the answer's shape differs per tool."""
    return json.dumps(value, ensure_ascii=False)


def code_project_id(server):
    """The test account's own Code project to talk to: the one named by --code, else the first Dev/Cowork project (Local ones are refused)."""
    code, result, text = call(server, "code", {"action": "projects"})
    for project in result.get("projects") or []:
        if not CODE["title"] or project.get("title") == CODE["title"]:
            return project["project_id"]
    raise SystemExit("no Code project to test with (create one, or name it with --code TITLE): " + text[:300])


def crew_id(server, spec):
    if CODE["on"]:
        return code_project_id(server)
    code, result, text = call(server, "crew", {"action": "list", "query": spec["name"]})
    for crew in (result.get("crews") or result.get("items") or result.get("data") or []):
        if isinstance(crew, dict) and (crew.get("name") == spec["name"] or crew.get("title") == spec["name"]):
            return crew.get("id") or crew.get("crew_id")
    match = re.search(r'"(?:id|crew_id)"\s*:\s*"([^"]+)"[^{}]*"name"\s*:\s*"%s"' % re.escape(spec["name"]), text)
    if match:
        return match.group(1)
    code, result, text = call(server, "crew", {"action": "create", **spec})
    created = result.get("crew_id") or result.get("id") or (result.get("crew") or {}).get("id")
    if not created:
        raise SystemExit("could not find or create the QA Crew: " + text[:300])
    return created


REPLY_NOTE = ("\n\nIMPORTANT: your normal chat answer is not delivered to the tester. Send your answer with the send_message tool, using the inbox_id "
              "from this message's header. Fastest: in the shell, curl --fail-with-body -sS --json '{\"inbox_id\":\"<that inbox_id>\",\"message\":\"<your answer>\"}' "
              "-H \"$MCP_AUTH\" \"$MCP_CUSTOM/send_message\" (do not search for other ways first).")


def read_inbox(server, inbox, wait=20):
    """The messages an agent sent back to `inbox`. (The `messages` tool is read through its CLI subcommand: `tools call messages` would overwrite the action.)"""
    process = subprocess.run(["agentworks", *CONFIG, "--json", "--server", server, "messages", "read", "--inbox", inbox, "--wait", str(wait)],
                             capture_output=True, text=True, timeout=wait + 60)
    text = (process.stdout or process.stderr).strip()
    try:
        return json.loads(text) if text else {}, text
    except ValueError:
        return {}, text


def ask(server, crew, message, budget=180, chat_id=None, retry=True):
    """Ask the Crew (or, with --code, the Code project) and wait for the answer it sends back (up to `budget` seconds).

    An ask is asynchronous: it returns an inbox id, and the agent's chat answer is not forwarded, so the message tells the agent to
    answer with send_message into that inbox. `chat_id` is kept for old callers and ignored: each ask starts its own conversation."""
    submission = "qa-" + uuid.uuid4().hex[:12]
    if CODE["on"]:
        code, result, text = call(server, "code", {"action": "ask", "project_id": crew, "message": message + REPLY_NOTE, "submission_id": submission})
    else:
        code, result, text = call(server, "ask_crew", {"crew_id": crew, "message": message + REPLY_NOTE, "submission_id": submission})
    inbox = result.get("inbox_id") if isinstance(result, dict) else None
    if not inbox:
        return text
    deadline = time.time() + budget
    while time.time() < deadline:
        page, page_text = read_inbox(server, inbox, 20)
        answers = [str(m.get("message", "")) for m in page.get("messages") or []]
        if answers:
            return "\n".join(answers)
    if retry:
        # A weaker model sometimes loops looking for the tool; one fresh ask tells a loop from a refusal.
        return ask(server, crew, message, budget, retry=False)
    return f"no answer sent back within {budget} s (asked twice)"


def check(case, answer):
    expect, forbid = case.get("expect"), case.get("forbid")
    if forbid and re.search(forbid, answer, re.I | re.M):
        return "FAIL", f"the answer contains forbidden text /{forbid}/: {answer[:240]}"
    if expect and not re.search(expect, answer, re.I | re.M):
        return "FAIL", f"the answer lacks /{expect}/: {answer[:240]}"
    return "PASS", ""


def run_case(server, crew, case):
    if case.get("steps"):
        # Steps run in order and stop at the first that fails; steps marked cleanup always run at the end (they remove what the case made).
        variables, verdict = {}, ("PASS", "")
        for step in [x for x in case["steps"] if not x.get("cleanup")]:
            if step.get("sleep"):
                time.sleep(step["sleep"])
            args = fill(step.get("args", {}), crew, variables)
            code, result, text = call(server, step["tool"], args)
            answer = find_text(result) or text
            label = f"{step['tool']} {args.get('action', args.get('operation', ''))}".strip()
            refused = code != 0 or (isinstance(result, dict) and bool(result.get("error")))
            if refused and step.get("optional"):
                verdict = ("SKIP", step.get("skip_reason") or "not available here: " + answer[:160])
                break
            if refused:
                verdict = ("FAIL", f"{label} refused: {answer[:240]}")
                break
            if step.get("expect") and not re.search(step["expect"], answer, re.I | re.S):
                verdict = ("FAIL", f"{label}: the answer lacks /{step['expect']}/: {answer[:240]}")
                break
            for name, pattern in (step.get("capture") or {}).items():
                found = re.search(pattern, answer)
                if found:
                    variables[name] = found.group(1)
        for step in [x for x in case["steps"] if x.get("cleanup")]:
            call(server, step["tool"], fill(step.get("args", {}), crew, variables))
        return verdict
    if case.get("tool") == "brain":
        title = "qa-" + uuid.uuid4().hex[:8]
        code, result, text = call(server, "brain_update", {"action": "create", "folder_path": "", "filename": title + ".md", "type": "note", "title": title, "content": "acceptance note " + title, "request_id": "qa-" + title})
        if code != 0 and re.search(r"folder|access|not.?found|denied", text, re.I):
            return "SKIP", "no Brain folder for this account: " + text[:120]
        code, result, text = call(server, "brain_read", {"action": "search", "query": title})
        return ("PASS", "") if title in text else ("FAIL", "saved note not found: " + text[:200])
    if case.get("tool"):
        code, result, text = call(server, case["tool"], fill(case.get("args", {}), crew, {}))
        if code != 0 or (isinstance(result, dict) and result.get("error")):
            if case.get("optional"):
                return "SKIP", "not available: " + text[:160]
            return "FAIL", text[:240]
        return check(case, find_text(result) or text)
    if case.get("burst"):
        # Several people asking at once: the load on the model. Each ask starts its own conversation.
        count = min(case["burst"], 4)
        with concurrent.futures.ThreadPoolExecutor(max_workers=count) as pool:
            answers = list(pool.map(lambda _: ask(server, crew, case["message"]), range(count)))
        failed = [(i, a) for i, a in enumerate(answers) if check(case, a)[0] != "PASS"]
        return ("PASS", "") if not failed else ("FAIL", f"{len(failed)} of {len(answers)} failed; first: {failed[0][1][:200]}")
    return check(case, ask(server, crew, case["message"]))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--server", required=True, help="https://<server>")
    parser.add_argument("--area", help="only cases of this area (model, sandbox, isolation, brain, limits, access)")
    parser.add_argument("--admin", action="store_true", help="the test user is an administrator: include the admin cases (users, Vault, Code review, schedules, Relays)")
    parser.add_argument("--slots", action="store_true", help="the server runs per-user Linux slots: include the cases that need them")
    parser.add_argument("--crew-name", help="name of the QA Crew to find or create (default: the catalog's). Use one per test account: a Crew's folder admits only its owner's slot, so another account's shell commands in it fail")
    parser.add_argument("--config", help="the agentworks connection file of the test account (agentworks --config PATH login), so an admin and a member can stay signed in side by side")
    parser.add_argument("--code", nargs="?", const="", metavar="TITLE", help="run the message cases through the account's own Code project (tool `code`, needs a login with --scopes code:run) instead of a Crew; optional project title")
    parser.add_argument("--catalog", default=str(Path(__file__).with_name("catalog.json")))
    options = parser.parse_args()
    if options.code is not None:
        CODE["on"], CODE["title"] = True, options.code or None
    if options.config:
        CONFIG[:] = ["--config", options.config]
    catalog = json.loads(Path(options.catalog).read_text())
    cases = [c for c in catalog["cases"] if (not options.area or c["area"] == options.area)]
    if CODE["on"]:
        # A Code project has no side chats and the API cases do not depend on where the chat runs: only the single-message cases apply.
        cases = [c for c in cases if "message" in c and not c.get("burst") and c.get("needs") != "admin"]
    crew = None
    failures = 0
    for case in cases:
        label = f"{case['id']:<16} {case['title']}"
        if case.get("needs") == "slots" and not options.slots:
            print(f"SKIP  {label}  (needs --slots)")
            continue
        if case.get("needs") == "admin" and not options.admin:
            print(f"SKIP  {label}  (needs --admin)")
            continue
        try:
            if crew is None and ("message" in case or "{crew}" in json.dumps(case)):
                crew = crew_id(options.server, {**catalog["crew"], **({"name": options.crew_name} if options.crew_name else {})})
            status, why = run_case(options.server, crew, case)
        except subprocess.TimeoutExpired:
            status, why = "FAIL", "timed out"
        print(f"{status:<5} {label}" + (f"\n        {why}" if why else ""))
        failures += status == "FAIL"
    print(f"\n{len(cases)} cases, {failures} failed")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
