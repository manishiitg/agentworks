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
            stderr=subprocess.PIPE,
            timeout=8,
            check=False,
        )
    except subprocess.TimeoutExpired:
        # The bridge is busy; the work usually finishes by itself, so say that instead of "unavailable".
        if control:
            block("AgentWorks is still connecting. Try again in a moment.")
        return
    except OSError:
        if control:
            unavailable()
        return
    # Offline/unattached hooks are silent. SessionEnd is observational in Muse.
    if result.returncode or event == "SessionEnd" or not result.stdout.strip():
        if control:
            # The CLI's own one-line reason, when it gave one, beats a guess about the install.
            reason = (result.stderr or b"").decode("utf-8", "replace").strip().splitlines()
            reason = "".join(ch for ch in (reason[0] if reason else "") if ch.isprintable())[:300]
            if reason and event != "SessionEnd":
                block(reason)
            else:
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


def block(reason):
    # Explicit person controls must not silently fall through to the model.
    print(json.dumps({"decision": "block", "reason": reason}))


def unavailable():
    block("AgentWorks Muse bridge support is unavailable. Install the bridge version "
          "that supports 'agentworks plugin hook muse', then retry this command. "
          "No attachment change was confirmed.")
