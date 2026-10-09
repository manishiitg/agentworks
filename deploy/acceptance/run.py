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


def call(server, tool, args, timeout=120):
    process = subprocess.run(
        ["agentworks", "--json", "--server", server, "tools", "call", tool, "--input", "-"],
        input=json.dumps(args), capture_output=True, text=True, timeout=timeout,
    )
    text = (process.stdout or process.stderr or "").strip()
    try:
        return process.returncode, json.loads(text) if text else {}, text
    except ValueError:
        return process.returncode, {}, text


def find_text(value):
    """All of a result's text, for matching: the answer's shape differs per tool."""
    return json.dumps(value, ensure_ascii=False)


def crew_id(server, spec):
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


def ask(server, crew, message, budget=150):
    """Ask the Crew and wait for its final reply (up to `budget` seconds). Returns the reply's text."""
    submission = "qa-" + uuid.uuid4().hex[:12]
    code, result, text = call(server, "ask_crew", {"crew_id": crew, "message": message, "wait_seconds": 25, "submission_id": submission})
    call_id = result.get("call_id")
    deadline = time.time() + budget
    while call_id and re.search(r'"status"\s*:\s*"(running|pending|queued)"', find_text(result)) and time.time() < deadline:
        time.sleep(4)
        code, result, text = call(server, "functions", {"action": "status", "crew_id": crew, "call_id": call_id, "wait_seconds": 20})
    return find_text(result) if result else text


def check(case, answer):
    expect, forbid = case.get("expect"), case.get("forbid")
    if forbid and re.search(forbid, answer, re.I | re.M):
        return "FAIL", f"the answer contains forbidden text /{forbid}/: {answer[:240]}"
    if expect and not re.search(expect, answer, re.I | re.M):
        return "FAIL", f"the answer lacks /{expect}/: {answer[:240]}"
    return "PASS", ""


def run_case(server, crew, case):
    if case.get("tool") == "brain":
        title = "qa-" + uuid.uuid4().hex[:8]
        code, result, text = call(server, "brain_update", {"action": "create", "folder_path": "", "filename": title + ".md", "type": "note", "title": title, "content": "acceptance note " + title, "request_id": "qa-" + title})
        if code != 0 and re.search(r"folder|access|not.?found|denied", text, re.I):
            return "SKIP", "no Brain folder for this account: " + text[:120]
        code, result, text = call(server, "brain_read", {"action": "search", "query": title})
        return ("PASS", "") if title in text else ("FAIL", "saved note not found: " + text[:200])
    if case.get("tool"):
        code, result, text = call(server, case["tool"], case.get("args", {}))
        if code != 0:
            return "FAIL", text[:240]
        return check(case, find_text(result) or text)
    if case.get("burst"):
        with concurrent.futures.ThreadPoolExecutor(max_workers=case["burst"]) as pool:
            answers = list(pool.map(lambda _: ask(server, crew, case["message"]), range(case["burst"])))
        failed = [(i, a) for i, a in enumerate(answers) if check(case, a)[0] != "PASS"]
        return ("PASS", "") if not failed else ("FAIL", f"{len(failed)} of {len(answers)} failed; first: {failed[0][1][:200]}")
    return check(case, ask(server, crew, case["message"]))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--server", required=True, help="https://<server>")
    parser.add_argument("--area", help="only cases of this area (model, sandbox, isolation, brain, limits, access)")
    parser.add_argument("--slots", action="store_true", help="the server runs per-user Linux slots: include the cases that need them")
    parser.add_argument("--catalog", default=str(Path(__file__).with_name("catalog.json")))
    options = parser.parse_args()
    catalog = json.loads(Path(options.catalog).read_text())
    cases = [c for c in catalog["cases"] if (not options.area or c["area"] == options.area)]
    crew = None
    failures = 0
    for case in cases:
        label = f"{case['id']:<16} {case['title']}"
        if case.get("needs") == "slots" and not options.slots:
            print(f"SKIP  {label}  (needs --slots)")
            continue
        try:
            if crew is None and "message" in case:
                crew = crew_id(options.server, catalog["crew"])
            status, why = run_case(options.server, crew, case)
        except subprocess.TimeoutExpired:
            status, why = "FAIL", "timed out"
        print(f"{status:<5} {label}" + (f"\n        {why}" if why else ""))
        failures += status == "FAIL"
    print(f"\n{len(cases)} cases, {failures} failed")
    sys.exit(1 if failures else 0)


if __name__ == "__main__":
    main()
