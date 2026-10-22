"""Muse lifecycle adapter. Authentication and delivery belong to the shared CLI."""

import json
import os
from pathlib import Path
import subprocess
import sys
import time
import uuid
from datetime import datetime, timezone


# Leave time for a quiet return before Muse's 1.5 s outer hook deadline.
CLI_BUDGET_SECONDS = 1.25


def record_outcome(event, session, started, outcome, returncode=None):
    """Local timing metadata only; never prompt, output or stderr content."""
    if sys.platform == "darwin":
        cache = Path.home() / "Library/Caches"
    elif sys.platform == "win32":
        if not os.environ.get("LOCALAPPDATA"):
            return
        cache = Path(os.environ["LOCALAPPDATA"])
    else:
        cache = Path(os.environ.get("XDG_CACHE_HOME", str(Path.home() / ".cache")))
    # Do not create a helper directory for someone who never connected.
    try:
        fd = os.open(cache / "agentworks/bridge/bridge.log", os.O_APPEND | os.O_CREAT | os.O_WRONLY, 0o600)
        with os.fdopen(fd, "a", encoding="utf-8") as log:
            at = datetime.now(timezone.utc).isoformat(timespec="milliseconds")
            detail = "" if returncode is None else f" returncode={returncode}"
            log.write(f"{at} [HOOK] event={event} session={session} transport=muse_hook "
                      f"outcome={outcome} elapsed_ms={(time.monotonic()-started)*1000:.0f}"
                      f" budget_ms=1250{detail}\n")
    except OSError:
        pass


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
        session = str(uuid.UUID(payload.get("session_id", "")))
    except (ValueError, TypeError, AttributeError):
        return
    if event == "Stop" and payload.get("stop_hook_active"):
        return
    prompt = payload.get("prompt")
    control = event == "UserPromptSubmit" and isinstance(prompt, str) and any(
        prompt == command or prompt.startswith(command + " ")
        for command in ("/agentworks", "/agentworks-muse:agentworks")
    )
    started = time.monotonic()
    record_outcome(event, session, started, "cli_started")
    try:
        result = subprocess.run(
            ["agentworks", "plugin", "hook", "muse"],
            input=raw,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            timeout=CLI_BUDGET_SECONDS,
            check=False,
        )
    except subprocess.TimeoutExpired:
        record_outcome(event, session, started, "cli_timeout")
        # No receipt is sent: a message stays pending for a later successful hook.
        if control:
            block("AgentWorks is still connecting. Try again in a moment.")
        return
    except OSError:
        record_outcome(event, session, started, "cli_unavailable")
        if control:
            unavailable()
        return
    # Offline/unattached hooks are silent. SessionEnd is observational in Muse.
    if result.returncode or event == "SessionEnd" or not result.stdout.strip():
        record_outcome(event, session, started, "cli_failed" if result.returncode else "quiet", result.returncode)
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
            record_outcome(event, session, started, "invalid_output")
            if control:
                unavailable()
            return
    except (ValueError, TypeError):
        record_outcome(event, session, started, "invalid_output")
        if control:
            unavailable()
        return
    sys.stdout.write(json.dumps(output, ensure_ascii=False) + "\n")
    record_outcome(event, session, started, "output_forwarded")


def block(reason):
    # Explicit person controls must not silently fall through to the model.
    print(json.dumps({"decision": "block", "reason": reason}))


def unavailable():
    block("AgentWorks Muse bridge support is unavailable. Install the bridge version "
          "that supports 'agentworks plugin hook muse', then retry this command. "
          "No attachment change was confirmed.")
