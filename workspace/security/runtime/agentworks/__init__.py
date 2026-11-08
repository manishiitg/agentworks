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

from contextlib import contextmanager
from dataclasses import dataclass, field
from functools import wraps
import inspect
import json
import os
from pathlib import Path
import threading
from types import MappingProxyType
from typing import Any, Mapping, Sequence, TypeAlias, Union
import urllib.error
import urllib.request

__all__ = ["AutomationError", "Reply", "agent", "call_function", "function_status", "FunctionResult", "check", "inputs", "notify", "run_folder", "run_script", "ScriptResult", "JSONValue", "step"]



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
    answer_file: Path | None = None


JSONValue: TypeAlias = Union[None, bool, int, float, str, list["JSONValue"], dict[str, "JSONValue"]]


@dataclass(frozen=True)
class ScriptResult(Reply):
    """Saved-script outputs; output_dir is None if no invocation folder was prepared."""
    output_dir: Path | None = None
    outputs: list[Path] = field(default_factory=list)


@dataclass(frozen=True)
class FunctionResult(Reply):
    """A published function receipt. Inspect status before using value.

    ok means the call was accepted without an error, not that it has finished.
    function_status(call_id) refreshes queued/running calls without starting work.
    receipt contains the existing service's authorized result and pending questions.
    """
    call_id: str = ""
    status: str = ""
    value: JSONValue = None
    receipt: Mapping[str, JSONValue] = field(default_factory=dict)


_order = 0
_order_lock = threading.Lock()
_run_dir = os.environ.get("WORKFLOW_RUN_DIR", "")
if not _run_dir:
    raise AutomationError("Run this Python from a Crew automation to use AgentWorks helpers.")
run_folder: Path = Path(_run_dir)

# Decode a fresh object: nested JSON values cannot alias the parent's inputs.
try:
    _inputs = json.loads(os.environ.get("STEP_PARAMS_JSON", "").strip() or "{}")
except ValueError as error:
    raise AutomationError("Script inputs must be a JSON object.") from error
if not isinstance(_inputs, dict):
    raise AutomationError("Script inputs must be a JSON object.")
inputs: Mapping[str, JSONValue] = MappingProxyType(_inputs)
del _inputs


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
        with urllib.request.urlopen(request, timeout=3 if action == "step" else None) as response:
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
                 log_kind=result.get("log_kind", "activity"),
                 answer_file=Path(result["answer_file"]) if result.get("answer_file") else None)
    if action in ("call_function", "function_status"):
        receipt = result.get("value") or {}
        return FunctionResult(**vars(reply), call_id=receipt.get("call_id", ""),
                              status=receipt.get("status", ""), value=receipt.get("result"),
                              receipt=receipt)
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

    You may pass the caller's full input object. Only the child's declared names
    are forwarded; unused caller inputs are dropped. Names declared by neither
    script are refused, and the child's types/defaults/required fields still apply.
    Output contents stay ordinary files. This helper never parses them.
    """
    try:
        reply = _call("run_script", {"id": script_id, "inputs": dict(inputs)}, key, redo)
        if isinstance(reply, ScriptResult):
            return reply
        return ScriptResult(ok=False, error="The saved script returned an unreadable receipt.")
    except AutomationError as error:
        return ScriptResult(ok=False, error=str(error))


def _source_site(frame) -> str:
    return f"{Path(frame.f_code.co_filename).name}:{frame.f_lineno}" if frame else "unknown:0"


@contextmanager
def _step_activity(title: str, function, site: str):
    identity = f"{Path(function.__code__.co_filename).name}:{function.__name__}@{site}"
    try:
        started = _call("step", {"phase": "start", "title": title, "source_call": identity}, None, True)
    except Exception:
        started = Reply(ok=False)
    failure = ""
    try:
        yield
    except BaseException as error:
        failure = f"{type(error).__name__}: {error}"
        raise
    finally:
        if started.ok:
            try:
                _call("step", {"phase": "finish", "execution_id": started.conversation_id,
                               "ok": not failure, "error": failure}, None, True)
            except Exception:
                pass  # Activity delivery never changes the function's result.


def step(title: str):
    """Label an ordinary Python function in Plan and Runs; never cache or retry it.

    Every call runs normally, including repeated calls. Arguments and returned
    values are not recorded. Existing run activity receives completion/errors;
    an exception still reaches the calling Python unchanged.
    """
    if not isinstance(title, str) or not title.strip():
        raise ValueError("step needs a non-empty label")
    def decorate(function):
        if inspect.iscoroutinefunction(function):
            @wraps(function)
            async def asynchronous(*args, **kwargs):
                frame = inspect.currentframe()
                site = _source_site(frame.f_back if frame else None)
                del frame
                with _step_activity(title, function, site):
                    return await function(*args, **kwargs)
            return asynchronous
        @wraps(function)
        def synchronous(*args, **kwargs):
            frame = inspect.currentframe()
            site = _source_site(frame.f_back if frame else None)
            del frame
            with _step_activity(title, function, site):
                return function(*args, **kwargs)
        return synchronous
    return decorate


def call_function(crew: str, name: str, inputs: Mapping[str, JSONValue], *,
                  wait_seconds: float = 25, timeout_minutes: float = 60,
                  key: str | None = None, redo: bool = False) -> FunctionResult:
    """Call a linked Crew's published function with its existing typed inputs.

    The normal function service validates inputs/results, access and timeouts.
    A queued or running receipt is not a final result; refresh with function_status.
    No separate notification watcher is created. Deliberate calls in a loop are
    distinct; delivery retries reuse the saved receipt in this run.
    """
    try:
        reply = _call("call_function", {"crew": crew, "name": name, "inputs": dict(inputs),
                      "wait_seconds": wait_seconds, "timeout_minutes": timeout_minutes}, key, redo)
        if isinstance(reply, FunctionResult):
            return reply
        return FunctionResult(ok=False, error="The function returned an unreadable receipt.")
    except AutomationError as error:
        return FunctionResult(ok=False, error=str(error))


def function_status(call_id: str) -> FunctionResult:
    """Read a function call from this script's run, rechecking current access."""
    try:
        reply = _call("function_status", {"call_id": call_id}, None, False)
        if isinstance(reply, FunctionResult):
            return reply
        return FunctionResult(ok=False, error="The function returned an unreadable receipt.")
    except AutomationError as error:
        return FunctionResult(ok=False, error=str(error))


def agent(name: str, message: str | Sequence[str], *, model: str | None = None,
          tier: str | None = None, scripts: Sequence[str] = (),
          mcp: Sequence[str] = (), skills: Sequence[str] = (), tools: Sequence[str] = (),
          browser: bool = False, can_ask_person: bool = False,
          timeout_seconds: float | None = None, conversation_id: str | None = None,
          key: str | None = None, redo: bool = False) -> Reply:
    """Run ordinary agent turns in this run folder, with no external MCPs by default.

    A list sends follow-ups to the same agent. To continue later in this run,
    pass reply.conversation_id; omitted settings retain its original selections.
    browser uses this automation's connected browser. can_ask_person allows
    questions that wait for a person's answer; both are off by default.
    tools names existing category:tool selections, for example
    human_tools:integrations.google. It cannot add an unavailable app or login.
    An explicit can_ask_person also applies to scheduled runs; question waits
    stay bounded and do not silently cancel the agent.
    Browser calls share the workflow's existing browser session and profile,
    including its configured operator Chrome connection. timeout_seconds=None
    adds no helper timer beyond the parent deadline. Each person question uses
    the existing five-minute default, at most thirty minutes. Its timeout is
    returned to the agent; Python can decide on another call after the reply.
    ok means the provider turn completed, not that its task or files are valid.
    Python checks the answer and artifacts, including any failure stated in text.
    text is a bounded preview. For task/status checks, read the full last final
    answer with reply.answer_file.read_text() after checking ok. It contains the
    same redacted answer without transcript events or truncation; it is None
    when no successful final answer was saved. log_file contains the provider transcript the
    platform received, including available streamed text and tool records;
    log_complete does not claim uncaptured operating-system output.
    """
    frame = inspect.currentframe()
    source_call = "agent@" + _source_site(frame.f_back if frame else None)
    del frame
    messages = [message] if isinstance(message, str) else list(message)
    if not name.strip() or not messages or any(not isinstance(item, str) or not item.strip() for item in messages):
        raise ValueError("agent needs a name and at least one non-empty message")
    try:
        return _call("agent", {"name": name, "messages": messages, "model": model,
                     "tier": tier, "scripts": list(scripts), "mcp": list(mcp),
                     "skills": list(skills), "tools": list(tools), "browser": browser,
                     "can_ask_person": can_ask_person, "timeout_seconds": timeout_seconds,
                     "conversation_id": conversation_id, "source_call": source_call}, key, redo)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))
