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
from contextvars import ContextVar
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

__all__ = ["AutomationError", "Reply", "agent", "call_function", "function_status", "FunctionResult", "check", "inputs", "notify", "run_folder", "run_script", "ScriptResult", "JSONValue", "step", "send_email", "send_slack", "google", "slack", "github"]
__all__.append("secret")



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
    reused: bool = False


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


_active_step: ContextVar[str] = ContextVar("agentworks_step", default="")
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

_declared_secrets = frozenset(json.loads(os.environ.get("SCRIPT_REQUIRED_SECRETS_JSON", "[]") or "[]") or [])


def secret(name: str) -> str:
    """Read a declared Vault value from the existing environment, without caching it."""
    if name not in _declared_secrets:
        raise AutomationError(f"Declare {name} in script.json secrets and select it in Vault for this workflow.")
    key = name.split("__", 1)[1] if name.startswith("VLT_") and "__" in name else name
    value = os.environ.get("SECRET_" + key, "")
    if not value:
        raise AutomationError(f"Select or add {name} in Vault for this workflow. Ask this Crew's chat to help.")
    return value


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
    if action in ("check", "notify") and _active_step.get():
        arguments = {**arguments, "source_call": _active_step.get()}
    request = urllib.request.Request(
        base + "/python_automation",
        data=json.dumps({"action": action, "arguments": arguments, "key": label,
                         "order": order, "redo": redo, "explicit_key": key is not None}, allow_nan=False).encode(),
        headers={"Content-Type": "application/json", header.strip(): credential.strip()},
        method="POST",
    )
    try:
        # The owning script deadline and Stop bound helper calls. A shorter
        # activity-only timeout can lose an accepted start ID and leave a
        # successful Python function recorded as failed at process exit.
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
                 log_kind=result.get("log_kind", "activity"),
                 answer_file=Path(result["answer_file"]) if result.get("answer_file") else None,
                 reused=bool(result.get("reused")))
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
                            log_kind=reply.log_kind, reused=reply.reused,
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


def _source_name(filename) -> str:
    source = Path(filename)
    root = os.environ.get("SCRIPT_SOURCE_DIR")
    if root:
        try:
            return source.resolve().relative_to(Path(root).resolve()).as_posix()
        except ValueError:
            pass
    return source.name


def _source_site(frame) -> str:
    return f"{_source_name(frame.f_code.co_filename)}:{frame.f_lineno}" if frame else "unknown:0"


@contextmanager
def _step_activity(title: str, function, site: str):
    identity = f"{_source_name(function.__code__.co_filename)}:{function.__name__}@{site}"
    try:
        started = _call("step", {"phase": "start", "title": title, "source_call": identity}, None, True)
    except Exception:
        started = Reply(ok=False)
    token = _active_step.set(identity)
    failure = ""
    try:
        yield
    except BaseException as error:
        failure = f"{type(error).__name__}: {error}"
        raise
    finally:
        _active_step.reset(token)
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


def send_email(to: str | Sequence[str], subject: str, body: str,
               attachments: Sequence[str | Path] = (), *,
               cc: str | Sequence[str] = (), html: str | None = None) -> Reply:
    """Send explicitly through the existing mail account and recipient checks."""
    recipients = [to] if isinstance(to, str) else list(to)
    arguments = {"to": recipients, "subject": subject, "body": body,
                 "attachments": [str(p) for p in attachments]}
    if cc:
        arguments["cc"] = [cc] if isinstance(cc, str) else list(cc)
    if html is not None:
        arguments["html"] = html
    try:
        return _call("send_email", arguments, None, False)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))


def send_slack(channel: str, text: str) -> Reply:
    """Send to an existing configured Slack route; arbitrary channels are refused."""
    try:
        return _call("send_slack", {"route_id": channel, "message": text}, None, False)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))


def agent(name: str, message: str | Sequence[str], *, model: str | None = None,
          tier: str | None = None, reasoning: str | None = None, scripts: Sequence[str] = (),
          mcp: Sequence[str] = (), skills: Sequence[str] = (), tools: Sequence[str] = (), apps: Sequence[str] = (),
          browser: bool = False, knowledge: str | None = None,
          timeout_seconds: float | None = None, conversation_id: str | None = None,
          key: str | None = None, redo: bool = False) -> Reply:
    """Run ordinary agent turns in this run folder, with no external MCPs by default.

    A list sends follow-ups to the same agent. To continue later in this run,
    pass reply.conversation_id; omitted settings retain its original selections.
    reasoning optionally selects "low", "medium" or "high" where the selected
    model supports it; unsupported choices are refused, never silently ignored.
    knowledge defaults to "read" for the owning workflow; "write" also allows
    its normal knowledge-note writes. Continued conversations keep their setting.
    browser uses this automation's connected browser and defaults to False.
    tools names existing category:tool selections, for example
    human_tools:integrations.google. It cannot add an unavailable app or login.
    apps=("google", "slack", "github") is a shorthand for those existing app
    tools. Choose only what this agent needs; continuing it keeps that choice.
    Workflow agents never wait for a person. Browser calls share the workflow's
    existing browser session and profile. timeout_seconds=None adds no helper
    timer beyond the parent deadline.
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
                     "tier": tier, "reasoning": reasoning, "scripts": list(scripts), "mcp": list(mcp),
                     "skills": list(skills), "tools": list(tools), "apps": list(apps), "browser": browser,
                     "knowledge": knowledge, "timeout_seconds": timeout_seconds,
                     "conversation_id": conversation_id, "source_call": source_call}, key, redo)
    except AutomationError as error:
        return Reply(ok=False, error=str(error))


from . import google, slack, github
