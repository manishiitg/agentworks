#!/usr/bin/env python3
"""Post one "what's new" message to the server's Slack channel after a deploy goes live.

The private products/<name>/deploy-notes.env names the channel (SLACK_DEPLOY_CHANNEL) and, optionally, how the
server is called in the message (SERVER_LABEL). The bot token comes from the person's own Slack env file
(SLACK_BOT_TOKEN in ~/.config/agentworks/agentworks-slack.env, or AGENTWORKS_SLACK_ENV); it is never passed on the
command line or printed. Each change is one line: the commit's "Release-Note:" trailer when it has one, otherwise its
subject without the ticket prefix.
"""
import argparse
import json
import os
import pathlib
import re
import subprocess
import sys
import urllib.request

MAX_LINES = 12


def read_env(path):
    values = {}
    for line in pathlib.Path(path).read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values[key.strip()] = value.strip().strip('"').strip("'")
    return values


def only_tests_or_docs(repo, commit):
    """A commit that changes only tests or docs is nothing people see."""
    files = subprocess.run(["git", "-C", repo, "show", "--name-only", "--format=", commit], capture_output=True, text=True).stdout.split()
    test = re.compile(r"(_test\.go|\.test\.tsx?|\.spec\.tsx?|/tests?/|/test_[^/]*\.py|\.md)$|/tests?/")
    return bool(files) and all(test.search(f) for f in files if f != "frontend/src/version.ts")


def changes(repo, old, new):
    if not old or not new or old == new:
        return []
    fmt = "%H%x1f%s%x1f%(trailers:key=Release-Note,valueonly,separator=%x1e)%x1d"
    try:
        out = subprocess.run(["git", "-C", repo, "log", "--no-merges", "--reverse", f"--format={fmt}", f"{old}..{new}"],
                             capture_output=True, text=True, check=True).stdout
    except subprocess.CalledProcessError:
        return []
    lines, seen = [], set()
    for record in out.split("\x1d"):
        if not record.strip():
            continue
        commit, subject, notes = (record.strip().split("\x1f") + ["", ""])[:3]
        if only_tests_or_docs(repo, commit):
            continue
        picked = [n.strip() for n in notes.split("\x1e") if n.strip()]
        if not picked:
            picked = [re.sub(r"^(PLAT-[0-9A-Za-z-]+(, ?PLAT-[0-9A-Za-z-]+)*|[A-Z]+-\d+)[:,]?\s*", "", subject.strip())]
        for line in picked:
            if line and line.lower() not in seen:
                seen.add(line.lower())
                lines.append(line)
    return lines


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--config", required=True)
    parser.add_argument("--repo", required=True)
    parser.add_argument("--from", dest="old", default="")
    parser.add_argument("--to", dest="new", default="")
    args = parser.parse_args()

    config = read_env(args.config)
    channel = config.get("SLACK_DEPLOY_CHANNEL", "")
    if not channel:
        sys.exit("deploy-notes.env needs SLACK_DEPLOY_CHANNEL")
    slack_env = os.environ.get("AGENTWORKS_SLACK_ENV") or os.path.expanduser("~/.config/agentworks/agentworks-slack.env")
    token = read_env(slack_env).get("SLACK_BOT_TOKEN", "") if os.path.exists(slack_env) else ""
    if not token:
        sys.exit("No SLACK_BOT_TOKEN in " + slack_env)

    label = config.get("SERVER_LABEL", "AgentWorks")
    lines = changes(args.repo, args.old, args.new)
    text = f"*{label} was updated.* Refresh the page to get it."
    if lines:
        shown = lines[:MAX_LINES]
        text += "\nWhat's new:\n" + "\n".join("• " + line for line in shown)
        if len(lines) > len(shown):
            text += f"\n…and {len(lines) - len(shown)} smaller changes."

    request = urllib.request.Request("https://slack.com/api/chat.postMessage",
                                     data=json.dumps({"channel": channel, "text": text, "unfurl_links": False}).encode(),
                                     headers={"Content-Type": "application/json; charset=utf-8", "Authorization": "Bearer " + token})
    with urllib.request.urlopen(request, timeout=30) as response:
        reply = json.load(response)
    if not reply.get("ok"):
        sys.exit("Slack refused the post: " + str(reply.get("error")))
    print(f"Posted the deploy notes ({len(lines)} changes) to Slack.")


if __name__ == "__main__":
    main()
