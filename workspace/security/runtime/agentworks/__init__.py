"""Typed helpers for Python running inside a Crew automation.

Each call runs normally. Saved results are reused only when the platform
explicitly resumes a previous execution in the same run folder.
"""
from __future__ import annotations

from dataclasses import dataclass
import inspect
import json
import os
from pathlib import Path
import threading
from typing import Any
import urllib.error
import urllib.request

__all__ = ["AutomationError", "Reply", "check", "notify", "run_folder"]



class AutomationError(RuntimeError):
    """The helper could not obtain a reliable result; inspect the saved run."""


@dataclass(frozen=True)
class Reply:
    ok: bool
    text: str = ""
    conversation_id: str = ""
    error: str = ""
    log_file: Path | None = None
    log_complete: bool = False
    log_error: str = ""


_order = 0
_order_lock = threading.Lock()
_run_dir = os.environ.get("WORKFLOW_RUN_DIR", "")
if not _run_dir:
    raise AutomationError("Run this Python from a Crew automation to use AgentWorks helpers.")
run_folder: Path = Path(_run_dir)


def _call(action: str, arguments: dict[str, Any], key: str | None, redo: bool) -> Reply:
    global _order
    base = os.environ.get("MCP_CUSTOM", "").rstrip("/")
    header, separator, credential = os.environ.get("MCP_AUTH", "").partition(":")
    if not base or not separator or not credential.strip():
        raise AutomationError("Run this Python from a Crew automation to use AgentWorks helpers.")
    with _order_lock:
        _order += 1
        order = _order
    caller = inspect.currentframe()
    caller = caller.f_back.f_back if caller and caller.f_back else None
    site = f"{Path(caller.f_code.co_filename).name}:{caller.f_lineno}" if caller else action
    label = key if key is not None else f"{action}:{site}:{order}"
    request = urllib.request.Request(
        base + "/python_automation",
        data=json.dumps({"action": action, "arguments": arguments, "key": label,
                         "order": order, "redo": redo}, allow_nan=False).encode(),
        headers={"Content-Type": "application/json", header.strip(): credential.strip()},
        method="POST",
    )
    try:
        with urllib.request.urlopen(request) as response:
            envelope = json.load(response)
    except (urllib.error.URLError, OSError, ValueError) as error:
        raise AutomationError("Helper delivery is uncertain. Check this run before trying again.") from error
    if not isinstance(envelope, dict):
        raise AutomationError("The helper returned an unreadable receipt. Check this run.")
    if not envelope.get("success"):
        raise AutomationError(str(envelope.get("error") or "Helper request failed; check this run."))
    result = envelope.get("result")
    if isinstance(result, str):
        try:
            result = json.loads(result)
        except ValueError as error:
            raise AutomationError("The helper returned an unreadable receipt. Check this run.") from error
    if not isinstance(result, dict):
        raise AutomationError("The helper returned an unreadable receipt. Check this run.")
    return Reply(ok=bool(result.get("ok")), text=result.get("text", ""),
                 conversation_id=result.get("conversation_id", ""),
                 error=result.get("error", ""),
                 log_file=Path(result["log_file"]) if result.get("log_file") else None,
                 log_complete=bool(result.get("log_complete")),
                 log_error=result.get("log_error", ""))


def notify(text: str, *, key: str | None = None, redo: bool = False) -> Reply:
    """Best-effort progress to the run's chat; never starts an agent turn."""
    try:
        return _call("notify", {"text": text}, key, redo)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))


def check(name: str, ok: bool, detail: str = "", *, key: str | None = None,
          redo: bool = False) -> Reply:
    """Record a check. A failure stays visible; Python decides what to do next."""
    if not isinstance(ok, bool):
        raise TypeError("check's ok argument must be True or False")
    try:
        return _call("check", {"name": name, "ok": ok, "detail": detail}, key, redo)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))
