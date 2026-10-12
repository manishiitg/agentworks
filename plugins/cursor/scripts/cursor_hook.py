#!/usr/bin/env python3
"""Thin Cursor entry point; process/session/lease authority stays in AgentWorks."""
import argparse
import json
import os
import subprocess
import sys

LIMIT = 4 << 20


class AdapterError(Exception):
    pass


class Parser(argparse.ArgumentParser):
    def error(self, message):
        raise AdapterError("invalid action")


def invoke(arguments, payload=None):
    result = subprocess.run([os.environ.get("AGENTWORKS_CLI", "agentworks"), *arguments],
                            input=payload, capture_output=True, text=True, timeout=18)
    if result.returncode or len(result.stdout.encode()) > LIMIT:
        raise AdapterError("shared adapter refused")
    if not result.stdout.strip():
        return None
    reply = json.loads(result.stdout)
    if not isinstance(reply, dict):
        raise AdapterError("invalid shared response")
    return reply


def agent_action(argv):
    parser = Parser(add_help=False)
    parser.add_argument("action", choices=["ack", "reply", "recover"])
    parser.add_argument("--session", required=True)
    parser.add_argument("--attachment-id", required=True)
    parser.add_argument("--message-id")
    parser.add_argument("--delivery", choices=["admitted", "processed"])
    parser.add_argument("--message")
    parser.add_argument("--submission-id")
    parser.add_argument("--after", type=int, default=0)
    args = parser.parse_args(argv)
    if not args.session or not args.attachment_id:
        raise AdapterError("original attachment required")
    action = "read" if args.action == "recover" else args.action
    command = ["bridge", "message", action, "--provider", "cursor", "--session", args.session,
               "--attachment-id", args.attachment_id, "--json"]
    if action == "ack":
        if not args.message_id or not args.delivery:
            raise AdapterError("explicit receipt required")
        command += ["--message-id", args.message_id, "--delivery", args.delivery]
    elif action == "reply":
        if not args.message_id or not args.message or not args.submission_id:
            raise AdapterError("reply context required")
        command += ["--message-id", args.message_id, "--message", args.message,
                    "--submission-id", args.submission_id]
    else:
        if args.after < 0:
            raise AdapterError("invalid cursor")
        command += ["--after", str(args.after)]
    # The shared child derives CURSOR_CONVERSATION_ID from the native executor,
    # verifies the exact process/observation and compares this optional pin.
    reply = invoke(command)
    attachment = reply.get("attachment", {}) if reply else {}
    if (not reply or reply.get("attached") is not True or reply.get("offline") or
            not isinstance(attachment, dict) or attachment.get("provider") != "cursor" or
            attachment.get("native_session_id") != args.session or
            attachment.get("attachment_id") != args.attachment_id or reply.get("result") is None):
        raise AdapterError("original attachment refused")
    return {"ok": True, "result": reply["result"]}


def main():
    if len(sys.argv) > 1:
        try:
            print(json.dumps(agent_action(sys.argv[1:])))
            return 0
        except (AdapterError, OSError, ValueError, subprocess.SubprocessError):
            print('{"ok":false,"error":"AgentWorks action refused or shared adapter unavailable"}')
            return 1
    control = False
    try:
        raw = sys.stdin.buffer.read(LIMIT + 1)
        if len(raw) > LIMIT:
            return 0
        event = json.loads(raw)
        if not isinstance(event, dict):
            return 0
        prompt = event.get("prompt", "")
        control = (event.get("hook_event_name") == "beforeSubmitPrompt" and isinstance(prompt, str)
                   and (prompt.strip() == "/agentworks" or prompt.strip().startswith("/agentworks ")))
        reply = invoke(["plugin", "hook", "cursor"], json.dumps(event))
        if reply is not None:
            print(json.dumps(reply))
    except (AdapterError, OSError, ValueError, subprocess.SubprocessError):
        if control:
            print(json.dumps({"continue": False, "user_message":
                              "AgentWorks control was not completed. Check the shared Cursor CLI adapter."}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
