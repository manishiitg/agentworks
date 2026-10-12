"""Muse lifecycle adapter. Authentication and delivery belong to the shared CLI."""

import json
import subprocess
import sys
import uuid


def main(event):
    # Reject malformed/sessionless input before starting even a local client.
    try:
        raw = sys.stdin.buffer.read(65537)
        if len(raw) > 65536:
            return
        payload = json.loads(raw)
        if not isinstance(payload, dict):
            return
        if payload.get("hook_event_name") != event:
            return
        uuid.UUID(payload.get("session_id", ""))
    except (ValueError, TypeError, AttributeError):
        return
    if event == "Stop" and payload.get("stop_hook_active"):
        return
    prompt = payload.get("prompt")
    control = event == "UserPromptSubmit" and isinstance(prompt, str) and any(
        prompt == command or prompt.startswith(command + " ")
        for command in ("/agentworks", "/agentworks-muse:agentworks")
    )
    try:
        result = subprocess.run(
            ["agentworks", "plugin", "hook", "muse"],
            input=raw,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=8,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired):
        if control:
            unavailable()
        return
    # Offline/unattached hooks are silent. SessionEnd is observational in Muse.
    if result.returncode or event == "SessionEnd" or not result.stdout.strip():
        if control:
            unavailable()
        return
    try:
        output = json.loads(result.stdout)
        if not isinstance(output, dict):
            if control:
                unavailable()
            return
    except (ValueError, TypeError):
        if control:
            unavailable()
        return
    sys.stdout.write(json.dumps(output, ensure_ascii=False) + "\n")


def unavailable():
    # Explicit person controls must not silently fall through to the model.
    print(json.dumps({"decision": "block", "reason":
        "AgentWorks Muse bridge support is unavailable. Install the bridge version "
        "that supports 'agentworks plugin hook muse', then retry this command. "
        "No attachment change was confirmed."}))
