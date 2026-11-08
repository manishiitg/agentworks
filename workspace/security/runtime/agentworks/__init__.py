"""Typed helpers for Python running inside a Crew automation.

Each call runs normally. Saved results are reused only when the platform
explicitly resumes a previous execution in the same run folder.

Typed saved-script example::

    result = run_script("report", {"count": 3})
    if result.ok and result.output_dir is not None:
        for output in result.outputs:
            print(output.read_text())
"""
from __future__ import annotations

from dataclasses import dataclass, field
import inspect
import json
import os
from pathlib import Path
import threading
from typing import Any, Mapping, Sequence, TypeAlias, Union
import urllib.error
import urllib.request

__all__ = ["AutomationError", "Reply", "agent", "check", "notify", "run_folder", "run_script", "ScriptResult", "JSONValue"]



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
    log_kind: str = "activity"


JSONValue: TypeAlias = Union[None, bool, int, float, str, list["JSONValue"], dict[str, "JSONValue"]]


@dataclass(frozen=True)
class ScriptResult(Reply):
    """Saved-script outputs; output_dir is None if no invocation folder was prepared."""
    output_dir: Path | None = None
    outputs: list[Path] = field(default_factory=list)


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
    reply = Reply(ok=bool(result.get("ok")), text=result.get("text", ""),
                 conversation_id=result.get("conversation_id", ""),
                 error=result.get("error", ""),
                 log_file=Path(result["log_file"]) if result.get("log_file") else None,
                 log_complete=bool(result.get("log_complete")),
                 log_error=result.get("log_error", ""),
                 log_kind=result.get("log_kind", "activity"))
    if action == "run_script":
        value = result.get("value") or {}
        directory = value.get("output_dir")
        return ScriptResult(ok=reply.ok, error=reply.error, text=reply.text,
                            conversation_id=reply.conversation_id, log_file=reply.log_file,
                            log_complete=reply.log_complete, log_error=reply.log_error,
                            log_kind=reply.log_kind,
                            output_dir=Path(directory) if directory else None,
                            outputs=[Path(path) for path in value.get("outputs", [])])
    return reply


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


def run_script(script_id: str, inputs: Mapping[str, JSONValue], *,
               key: str | None = None, redo: bool = False) -> ScriptResult:
    """Run this Crew's saved script; return typed output folder and file paths.

    Output contents stay ordinary files. This helper never parses them.
    """
    try:
        reply = _call("run_script", {"id": script_id, "inputs": dict(inputs)}, key, redo)
        if isinstance(reply, ScriptResult):
            return reply
        return ScriptResult(ok=False, error="The saved script returned an unreadable receipt.")
    except AutomationError as error:
        return ScriptResult(ok=False, error=str(error))


def agent(name: str, message: str | Sequence[str], *, model: str | None = None,
          tier: str | None = None, scripts: Sequence[str] = (),
          mcp: Sequence[str] = (), skills: Sequence[str] = (),
          browser: bool = False, can_ask_person: bool = False,
          timeout_seconds: float | None = None, conversation_id: str | None = None,
          key: str | None = None, redo: bool = False) -> Reply:
    """Run ordinary agent turns in this run folder, with no external MCPs by default.

    A list sends follow-ups to the same agent. To continue later in this run,
    pass reply.conversation_id; omitted settings retain its original selections.
    browser uses this automation's connected browser. can_ask_person allows
    questions that wait for a person's answer; both are off by default.
    Browser calls share the workflow's existing browser session and profile,
    including its configured operator Chrome connection. timeout_seconds=None
    adds no helper timer beyond the parent deadline. Each person question uses
    the existing five-minute default, at most thirty minutes. Its timeout is
    returned to the agent; Python can decide on another call after the reply.
    ok means the provider turn completed, not that its task or files are valid.
    Python checks the answer and artifacts, including any failure stated in text.
    text is a bounded preview. log_file contains the provider transcript the
    platform received, including available streamed text and tool records;
    log_complete does not claim uncaptured operating-system output.
    """
    messages = [message] if isinstance(message, str) else list(message)
    if not name.strip() or not messages or any(not isinstance(item, str) or not item.strip() for item in messages):
        raise ValueError("agent needs a name and at least one non-empty message")
    try:
        return _call("agent", {"name": name, "messages": messages, "model": model,
                     "tier": tier, "scripts": list(scripts), "mcp": list(mcp),
                     "skills": list(skills), "browser": browser,
                     "can_ask_person": can_ask_person, "timeout_seconds": timeout_seconds,
                     "conversation_id": conversation_id}, key, redo)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))
