"""Platform runner for relay.py. Protocol files belong to this invocation only."""
import asyncio
import contextvars
import importlib.util
import inspect
import json
import os
from pathlib import Path
import sys
import time
import typing


def _json_write(path, value):
    encoded = json.dumps(value, ensure_ascii=False, allow_nan=False)
    temporary = path.with_suffix(".tmp")
    temporary.write_text(encoded, encoding="utf-8")
    temporary.replace(path)


class ToolReconciliationError(RuntimeError):
    """An uncertain tool outcome must stop the agent, not become model input."""


def tool(function=None, *, description=None, schema=None):
    """Expose a Python callable to an agent; provide schema for complex inputs."""
    def decorate(fn):
        parameters = schema
        if parameters is None:
            properties, required = {}, []
            types = {str: "string", int: "integer", float: "number", bool: "boolean", dict: "object", list: "array"}
            annotations = typing.get_type_hints(fn)
            for name, parameter in inspect.signature(fn).parameters.items():
                if parameter.kind in (parameter.VAR_POSITIONAL, parameter.VAR_KEYWORD):
                    raise TypeError("Tool variadic arguments require an explicit schema")
                annotation = annotations.get(name, parameter.annotation)
                if annotation not in types:
                    raise TypeError("Tool arguments need basic type annotations or an explicit schema")
                properties[name] = {"type": types[annotation]}
                if parameter.default is parameter.empty:
                    required.append(name)
            parameters = {"type": "object", "properties": properties, "required": required, "additionalProperties": False}
        fn.__relay_tool__ = {"name": fn.__name__, "description": description or inspect.getdoc(fn) or fn.__name__, "schema": parameters}
        return fn
    return decorate(function) if function is not None else decorate


class Context:
    def __init__(self, run_dir, variables):
        self.run_dir = run_dir
        self.variables = variables
        self._ipc = run_dir / ".relay_ipc"
        self._sequence = 0
        self._lock = asyncio.Lock()
        self._in_tool = contextvars.ContextVar("relay_tool_callback", default=False)
        self._trace = {"version": 1, "status": "running", "calls": []}
        self._save_trace()

    def vault(self, name):
        """Read a secret already admitted to this Relay by the platform."""
        return os.environ["SECRET_" + name]

    def _save_trace(self):
        _json_write(self.run_dir / "relay_trace.json", self._trace)

    async def call_mcp(self, *, server, tool, arguments):
        """Call a live authorized connection through the platform MCP executor."""
        if self._in_tool.get():
            raise RuntimeError("Nested agent/MCP calls inside Python tools are unsupported; call them from run(INPUT, ctx)")
        async with self._lock:
            self._sequence += 1
            call_id = "call-" + str(self._sequence)
            call = {"id": call_id, "name": server + ":" + tool, "status": "running",
                    "started_at": time.time(), "tools": []}
            self._trace["calls"].append(call)
            self._save_trace()
            _json_write(self._ipc / (call_id + ".request.json"), {
                "id": call_id, "kind": "mcp", "server": server,
                "tool": tool, "arguments": arguments,
            })
            try:
                response = self._ipc / (call_id + ".response.json")
                while not response.exists():
                    await asyncio.sleep(0.1)
                while True:
                    try:
                        result = json.loads(response.read_text(encoding="utf-8"))
                        break
                    except json.JSONDecodeError:
                        await asyncio.sleep(0.1)
                if result.get("error"):
                    raise RuntimeError(result["error"])
                call.update(status="completed", output=result.get("output"))
                return call["output"]
            except BaseException as exc:
                call.update(status="failed", error=str(exc))
                raise
            finally:
                call["completed_at"] = time.time()
                self._save_trace()

    async def call_agent(self, *, system_prompt, user_message=None, messages=None,
                         model=None, name=None, tools=(), skills=(), mcp=(),
                         output_schema=None, max_turns=20, recovery=None):
        if recovery not in (None, "restart"):
            raise ValueError("Unknown agent recovery mode")
        if messages is None:
            messages = [user_message] if user_message is not None else []
        elif user_message is not None:
            raise ValueError("Use messages or user_message, not both")
        if not isinstance(system_prompt, str) or not messages or not all(isinstance(v, str) for v in messages):
            raise ValueError("system_prompt must be text and messages must be a nonempty list of text")
        handlers, definitions = {}, []
        for fn in tools:
            if not hasattr(fn, "__relay_tool__"):
                fn = tool(fn)
            definition = fn.__relay_tool__
            if definition["name"] in handlers:
                raise ValueError("Duplicate tool name: " + definition["name"])
            handlers[definition["name"]] = fn
            definitions.append(definition)
        # One active agent at a time in the MVP; ordinary Python can branch/loop.
        if self._in_tool.get():
            raise RuntimeError("Nested agent/MCP calls inside Python tools are unsupported; call them from run(INPUT, ctx)")
        async with self._lock:
            self._sequence += 1
            call_id = "call-" + str(self._sequence)
            call = {"id": call_id, "name": name or call_id, "model": model,
                    "status": "running", "started_at": time.time(), "tools": []}
            self._trace["calls"].append(call)
            self._save_trace()
            _json_write(self._ipc / (call_id + ".request.json"), {
                "id": call_id, "name": call["name"], "system_prompt": system_prompt,
                "messages": messages, "model": model, "tools": definitions,
                "skills": list(skills), "mcp": list(mcp), "output_schema": output_schema,
                "max_turns": max_turns,
                "recovery": recovery,
            })
            handled = set()
            try:
                while True:
                    response = self._ipc / (call_id + ".response.json")
                    if response.exists():
                        try:
                            result = json.loads(response.read_text(encoding="utf-8"))
                        except json.JSONDecodeError:
                            await asyncio.sleep(0.1)
                            continue
                        call["tools"].extend(result.get("tools") or [])
                        if result.get("provider"):
                            call["provider"], call["model"] = result["provider"], result["model"]
                        if result.get("error"):
                            raise RuntimeError(result["error"])
                        call["output"] = result.get("output")
                        call["status"] = "completed"
                        return call["output"]
                    for request in self._ipc.glob(call_id + ".tool-*.request.json"):
                        if request.name in handled:
                            continue
                        try:
                            payload = json.loads(request.read_text(encoding="utf-8"))
                        except json.JSONDecodeError:
                            continue
                        handled.add(request.name)
                        receipt = {"name": payload["name"], "args": payload["args"]}
                        token = self._in_tool.set(True)
                        try:
                            value = handlers[payload["name"]](**payload["args"])
                            if inspect.isawaitable(value):
                                value = await value
                            # Reject unserializable values in the same tool boundary.
                            json.dumps(value, allow_nan=False)
                            receipt["result"] = value
                            reply = {"output": value}
                        except ToolReconciliationError:
                            raise
                        except Exception as exc:
                            receipt["error"] = str(exc)
                            reply = {"error": str(exc)}
                        finally:
                            self._in_tool.reset(token)
                        call["tools"].append(receipt)
                        self._save_trace()
                        _json_write(request.with_name(request.name.replace(".request.json", ".response.json")), reply)
                    await asyncio.sleep(0.1)
            except BaseException as exc:
                call.update(status="failed", error=str(exc))
                raise
            finally:
                call["completed_at"] = time.time()
                self._save_trace()


async def _main():
    source = Path(sys.argv[1]).resolve()
    run_dir = Path(sys.argv[2]).resolve()
    data = json.loads((run_dir / ".relay_ipc" / "input.json").read_text(encoding="utf-8"))
    ctx = Context(run_dir, data["variables"])
    # relay.py and helpers can import the public decorator without installing a package.
    sys.modules["relay_sdk"] = sys.modules[__name__]
    sys.path.insert(0, str(source.parent))
    try:
        spec = importlib.util.spec_from_file_location("relay_program", source)
        program = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(program)
        result = await program.run(data["input"], ctx)
        encoded = json.dumps(result, ensure_ascii=False, allow_nan=False)
        if len(encoded.encode("utf-8")) > 128 * 1024:
            raise ValueError("Relay output exceeds 128 KiB")
        _json_write(run_dir / "relay_result.json", result)
        ctx._trace["status"] = "completed"
    except BaseException as exc:
        ctx._trace.update(status="failed", error=str(exc))
        raise
    finally:
        ctx._save_trace()


if __name__ == "__main__":
    asyncio.run(_main())
