"""DBOS execution adapter. One supervised process and SQLite database per run.

This is not a sandbox or a distributed recovery supervisor. The Go caller owns
admission and release verification. Authored Python must obey DBOS determinism.
"""
import asyncio
import ast
import copy
import fcntl
import hashlib
import importlib.util
from importlib.metadata import version
import inspect
import json
import os
from pathlib import Path
import sys
import threading
import time

from dbos import DBOS, SetWorkflowID


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(",", ":"),
                                     allow_nan=False).encode()).hexdigest()


def load_module(name, file):
    spec = importlib.util.spec_from_file_location(name, file)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def durable_json_write(sdk, file, value):
    # The intent must reach disk before an external effect can occur. Atomic
    # rename alone protects readers, but does not survive a machine failure.
    sdk._json_write(file, value)
    with file.open("rb") as handle:
        os.fsync(handle.fileno())
    directory = os.open(file.parent, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(directory)
    finally:
        os.close(directory)


async def main():
    if version("dbos") != "3.2.0":
        raise RuntimeError("DBOS prototype requires dbos==3.2.0")
    source, run_dir = (Path(arg).resolve() for arg in sys.argv[1:3])
    attempt = Path(__file__).resolve().parent
    data = json.loads((attempt / "input.json").read_text(encoding="utf-8"))
    config = data["dbos"]
    if hashlib.sha256(source.read_bytes()).hexdigest() != config["source_sha256"]:
        raise RuntimeError("Relay source changed after admission")
    state = run_dir / ".relay_dbos"
    state.mkdir(mode=0o700, exist_ok=True)
    # Prevent two launchers from treating a live executor as a crashed one.
    lock = (state / "executor.lock").open("a")
    try:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        raise RuntimeError("DBOS prototype run already has a live executor") from None
    sdk = load_module("relay_sdk", attempt / "sdk.py")
    identity = digest({"config": config, "input": data["input"],
                       "variables": data["variables"], "source": str(source),
                       "python": sys.version, "dbos": version("dbos"),
                       "sqlalchemy": version("sqlalchemy"),
                       "sdk": hashlib.sha256((attempt / "sdk.py").read_bytes()).hexdigest(),
                       "native": hashlib.sha256((attempt / "native.py").read_bytes()).hexdigest(),
                       "runner": hashlib.sha256(Path(__file__).read_bytes()).hexdigest()})
    binding = state / "binding.json"
    if binding.exists():
        if json.loads(binding.read_text(encoding="utf-8"))["identity"] != identity:
            raise RuntimeError("DBOS recovery input, release or runtime identity changed")
    else:
        durable_json_write(sdk, binding, {"identity": identity})

    def save_status(status, error=None):
        durable_json_write(sdk, run_dir / "relay_durability.json", {
            "engine": "dbos", "status": status, "attempt_id": attempt.name,
            "run_id": config["run_id"], "error": error,
        })

    save_status("running")
    lease_stop = threading.Event()

    def watch_coordinator():
        # File timestamps use the workspace machine's clock, so backend/workspace
        # clock differences cannot accidentally expire a healthy executor.
        while not lease_stop.wait(1):
            try:
                stale = time.time() - (attempt / "lease.json").stat().st_mtime > 15
            except OSError:
                stale = True
            if stale:
                os._exit(98)

    threading.Thread(target=watch_coordinator, daemon=True).start()

    entry = next(node for node in ast.parse(source.read_text()).body
                 if isinstance(node, ast.AsyncFunctionDef) and node.name == "run")
    if len(entry.args.posonlyargs + entry.args.args) == 1:
        # Ordinary authored DBOS functions register before launch. The platform
        # does not wrap their workflow or implement their checkpoint machinery.
        native = load_module("relay_native_adapter", attempt / "native.py")
        try:
            await native.execute(sdk, load_module, source, run_dir, attempt, data,
                                 state, identity, durable_json_write, save_status)
        except BaseException as exc:
            save_status("reconciliation" if "requires reconciliation" in str(exc) else "failed", str(exc))
            raise
        finally:
            lease_stop.set()
            DBOS.destroy()
            lock.close()
        return

    pending = {}
    executed = set()
    durable_context = None

    @DBOS.step(name="relay.prototype.operation", retries_allowed=False)
    async def operation(index, signature, replay_safe):
        executed.add(index)
        fn, describe = pending[index]
        marker = state / f"operation-{index}.json"
        if marker.exists():
            previous = json.loads(marker.read_text(encoding="utf-8"))
            if previous["signature"] != signature:
                raise RuntimeError("Relay operation changed during recovery")
            if not replay_safe:
                raise RuntimeError("Uncertain Relay operation requires reconciliation; "
                                   "automatic retry is disabled")
        durable_json_write(sdk, marker, {"signature": signature})
        before = len(durable_context._trace["calls"])
        value = await fn()
        # A JSON round trip prevents callbacks, closures or live credentials in
        # the Context object itself from being serialized into DBOS history.
        record = (durable_context._trace["calls"][-1] if
                  len(durable_context._trace["calls"]) > before else describe)
        record = copy.deepcopy(record)
        record.update(id=f"call-{index}", status="completed", output=value)
        record.setdefault("completed_at", time.time())
        result = {"signature": signature, "value": value, "record": record}
        return json.loads(json.dumps(result, allow_nan=False))

    class DurableContext(sdk.Context):
        def __init__(self):
            super().__init__(run_dir, data["variables"])
            self._ipc = attempt
            self._operation_index = 0
            self._durable_lock = asyncio.Lock()
            self._trace["durability"] = "dbos"
            self._trace["attempt_id"] = attempt.name
            self._trace["attempt_number"] = data.get("attempt_number") or 1

        async def _invoke(self, descriptor, fn, replay_safe):
            if not isinstance(replay_safe, bool):
                raise TypeError("replay_safe must be a boolean")
            if self._in_tool.get():
                raise RuntimeError("Nested agent/MCP calls inside Python tools are unsupported")
            async with self._durable_lock:
                self._operation_index += 1
                index = self._operation_index
                signature = digest({"operation": descriptor, "replay_safe": replay_safe})
                describe = {"name": descriptor["name"], "started_at": time.time(), "tools": []}
                pending[index] = (fn, describe)
                before = len(self._trace["calls"])
                try:
                    result = await operation(index, signature, replay_safe)
                    if result["signature"] != signature:
                        raise RuntimeError("Relay operation changed during recovery")
                    record = result["record"]
                    record["checkpoint_reused"] = index not in executed
                    # Transport IDs start at one in each fresh mailbox; logical
                    # IDs remain stable across checkpoint reuse and attempts.
                    self._trace["calls"][before:] = [record]
                    self._save_trace()
                    return result["value"]
                finally:
                    pending.pop(index, None)

        async def call_agent(self, *, replay_safe=False, **kwargs):
            tools = tuple(fn if hasattr(fn, "__relay_tool__") else sdk.tool(fn)
                          for fn in kwargs.get("tools", ()))
            kwargs["tools"] = tools
            descriptor = dict(kwargs)
            descriptor["tools"] = [fn.__relay_tool__ for fn in tools]
            descriptor.update(kind="agent", name=kwargs.get("name", "agent"))
            return await self._invoke(descriptor,
                lambda: super(DurableContext, self).call_agent(**kwargs), replay_safe)

        async def call_mcp(self, *, server, tool, arguments, replay_safe=False):
            descriptor = {"kind": "mcp", "name": server + ":" + tool,
                          "server": server, "tool": tool, "arguments": arguments}
            return await self._invoke(descriptor,
                lambda: super(DurableContext, self).call_mcp(
                    server=server, tool=tool, arguments=arguments), replay_safe)

        async def step(self, name, function, *, arguments=None, replay_safe=False):
            """Checkpoint a Python service operation; credentials stay in closures.

            replay_safe=True is only valid for reads or operations whose service
            implements idempotency. Arguments must include the idempotency key.
            """
            arguments = {} if arguments is None else arguments

            async def invoke():
                # Live platform admission also precedes Python service effects.
                # This receipt is transport-only and is not a workflow step.
                async with self._lock:
                    self._sequence += 1
                    call_id = "call-" + str(self._sequence)
                    sdk._json_write(self._ipc / (call_id + ".request.json"), {
                        "id": call_id, "kind": "admission", "name": name,
                    })
                    response = self._ipc / (call_id + ".response.json")
                    while not response.exists():
                        await asyncio.sleep(0.1)
                    reply = json.loads(response.read_text(encoding="utf-8"))
                    if reply.get("error"):
                        raise RuntimeError(reply["error"])
                value = function(**arguments)
                return await value if inspect.isawaitable(value) else value

            return await self._invoke({"kind": "script", "name": name,
                                       "arguments": arguments}, invoke, replay_safe)

    @DBOS.workflow(name="relay.prototype.run", max_recovery_attempts=3)
    async def relay_workflow(input_value):
        nonlocal durable_context
        durable_context = DurableContext()
        # Loading source happens after binding verification. Helpers are covered
        # by the full release checksum verified by the Go admission callback.
        sys.path.insert(0, str(source.parent))
        program = load_module("relay_program", source)
        result = await program.run(input_value, durable_context)
        if len(json.dumps(result, ensure_ascii=False, allow_nan=False).encode()) > 128 * 1024:
            raise ValueError("Relay output exceeds 128 KiB")
        return {"output": result, "trace": copy.deepcopy(durable_context._trace)}

    DBOS(config={"name": "relay-prototype", "application_version": identity,
                 "executor_id": "relay-prototype", "enable_otlp": False,
                 "console_log_level": "WARNING",
                 "system_database_url": "sqlite:///" + str(state / "checkpoints.sqlite")})
    try:
        DBOS.launch()
        run_id = config["run_id"]
        if await DBOS.get_workflow_status_async(run_id) is None:
            with SetWorkflowID(run_id):
                handle = await DBOS.start_workflow_async(relay_workflow, data["input"])
        else:
            # Launch recovers pending work. ERROR/CANCELLED are never silently
            # reset to PENDING, and SUCCESS simply returns its stored result.
            handle = await DBOS.retrieve_workflow_async(run_id)
        result = await handle.get_result()
        sdk._json_write(run_dir / "relay_result.json", result["output"])
        trace = result["trace"]
        if durable_context is None:
            for record in trace["calls"]:
                record["checkpoint_reused"] = True
        trace.update(status="completed", attempt_id=attempt.name,
                     attempt_number=data.get("attempt_number") or 1,
                     result_reused=durable_context is None)
        sdk._json_write(run_dir / "relay_trace.json", trace)
        save_status("completed")
    except BaseException as exc:
        if durable_context is not None:
            durable_context._trace.update(status="failed", error=str(exc))
            durable_context._save_trace()
        save_status("reconciliation" if "requires reconciliation" in str(exc) else "failed", str(exc))
        raise
    finally:
        lease_stop.set()
        DBOS.destroy()
        lock.close()


if __name__ == "__main__":
    asyncio.run(main())
