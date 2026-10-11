"""Thin platform bridge for ordinary DBOS workflows; DBOS owns step history."""
import asyncio
import copy
import hashlib
import functools
import inspect
import json
import logging

from dbos import DBOS, DBOSClient, SetWorkflowID


def error_message(error, depth=0):
    # DBOS retry exhaustion keeps the underlying exceptions in errors. Preserve
    # their actionable reasons in the UI and platform reconciliation status.
    nested = getattr(error, "errors", []) if depth < 4 else []
    return "\n".join([str(error)] + [error_message(item, depth + 1) for item in nested])


async def execute(sdk, load_module, source, run_dir, attempt, data, state, identity,
                  durable_write, save_status):
    run_id = data["dbos"]["run_id"]
    database = state / "checkpoints.sqlite"
    url = "sqlite:///" + str(database)
    previous = set()
    if database.exists():
        client = DBOSClient(system_database_url=url)
        try:
            previous = {step["function_id"] for step in client.list_workflow_steps(run_id)
                        if step.get("error") is None}
        finally:
            client.destroy()

    context = sdk.Context(run_dir, data["variables"])
    context._ipc = attempt
    raw_calls = context._trace["calls"]
    receipts = state / "receipts"
    receipts.mkdir(exist_ok=True)
    sequence = {}
    attempt_number = data.get("attempt_number") or 1
    # Transport receipts are separate from the DBOS-backed execution timeline.
    context._save_trace = lambda: None

    async def admit():
        async with context._lock:
            context._sequence += 1
            call_id = "call-" + str(context._sequence)
            sdk._json_write(attempt / (call_id + ".request.json"), {
                "id": call_id, "kind": "admission", "name": "DBOS step",
            })
            response = attempt / (call_id + ".response.json")
            while not response.exists():
                await asyncio.sleep(0.1)
            reply = json.loads(response.read_text())
            if reply.get("error"):
                raise RuntimeError(reply["error"])

    async def bridge(kind, kwargs, replay_safe):
        step_id = DBOS.step_id
        if step_id is None or DBOS.workflow_id != run_id:
            raise RuntimeError("Platform agent/MCP calls must run inside an @DBOS.step of run(INPUT)")
        if context._in_tool.get():
            raise RuntimeError("Nested agent/MCP calls inside Python tools are unsupported")
        if not isinstance(replay_safe, bool):
            raise TypeError("replay_safe must be a boolean")
        restart = kind == "agent" and kwargs.get("recovery") == "restart"
        if restart and (replay_safe or kwargs.get("mcp") or kwargs.get("skills")):
            raise ValueError("Restart recovery requires journaled Python tools only, without skills, MCP, or replay_safe")
        retry = DBOS.step_status.current_attempt
        if step_id not in sequence or sequence[step_id][0] != retry:
            sequence[step_id] = [retry, 0]
        sequence[step_id][1] += 1
        key = f"{step_id}-{sequence[step_id][1]}"
        descriptor = dict(kwargs, kind=kind, replay_safe=replay_safe)
        if kind == "agent":
            tools = tuple(fn if hasattr(fn, "__relay_tool__") else sdk.tool(fn)
                          for fn in kwargs.get("tools", ()))
            kwargs["tools"] = tools
            descriptor["tools"] = [fn.__relay_tool__ for fn in tools]
        signature = hashlib.sha256(json.dumps(descriptor, sort_keys=True,
                                  allow_nan=False).encode()).hexdigest()
        marker = state / ("native-call-" + key + ".json")
        result_file = state / ("native-result-" + key + ".json")
        recovering = marker.exists()
        if marker.exists():
            if json.loads(marker.read_text())["signature"] != signature:
                raise RuntimeError("Platform call changed during DBOS recovery")
            if not replay_safe and not restart:
                raise RuntimeError("Uncertain platform call requires reconciliation; automatic retry is disabled")
        await admit()
        if restart:
            if result_file.exists():
                DBOS.logger.info("Reused saved agent result for " + key)
                return json.loads(result_file.read_text())["output"]
            journal = state / ("tools-" + key)
            journal.mkdir(exist_ok=True)
            handlers = {fn.__relay_tool__["name"]: fn for fn in tools}
            if len(handlers) != len(tools):
                raise ValueError("Duplicate tool name")

            async def resolve(file, record):
                fn = handlers.get(record["name"])
                recover = getattr(fn, "__relay_recover__", None)
                if recover is None:
                    raise sdk.ToolReconciliationError("Uncertain tool call requires reconciliation: " + record["name"])
                # A recovery callback queries the service using the original
                # arguments. It must not perform the action again.
                token = context._in_tool.set(True)
                try:
                    output = recover(**record["args"])
                    if inspect.isawaitable(output):
                        output = await output
                    json.dumps(output, allow_nan=False)
                except Exception as exc:
                    raise sdk.ToolReconciliationError("Uncertain tool call requires reconciliation: " + record["name"]) from exc
                finally:
                    context._in_tool.reset(token)
                record.update(status="completed", output=output)
                durable_write(sdk, file, record)
                DBOS.logger.info("Recovered tool result without repeating action: " + record["name"])
                return output

            # Resolve all unfinished actions before admitting a new agent turn.
            for file in sorted(journal.glob("*.json")):
                record = json.loads(file.read_text())
                if record["status"] != "completed":
                    await resolve(file, record)

            def journaled(fn):
                @functools.wraps(fn)
                async def invoke(**arguments):
                    bound = inspect.signature(fn).bind(**arguments)
                    bound.apply_defaults()
                    record = {"name": fn.__relay_tool__["name"], "args": dict(bound.arguments)}
                    digest = hashlib.sha256(json.dumps(record, sort_keys=True, allow_nan=False).encode()).hexdigest()
                    file = journal / (digest + ".json")
                    if file.exists():
                        saved = json.loads(file.read_text())
                        if saved["status"] != "completed":
                            return await resolve(file, saved)
                        DBOS.logger.info("Reused tool result without repeating action: " + record["name"])
                        return saved["output"]
                    record["status"] = "pending"
                    durable_write(sdk, file, record)
                    try:
                        output = fn(**arguments)
                        if inspect.isawaitable(output):
                            output = await output
                        json.dumps(output, allow_nan=False)
                    except Exception as exc:
                        raise sdk.ToolReconciliationError("Uncertain tool call requires reconciliation: " + record["name"]) from exc
                    record.update(status="completed", output=output)
                    durable_write(sdk, file, record)
                    DBOS.logger.info("Saved durable tool result: " + record["name"])
                    return output
                return invoke

            kwargs["tools"] = tuple(journaled(fn) for fn in tools)
            if recovering:
                DBOS.logger.info("Restarting agent with saved tool results: " + (kwargs.get("name") or key))
        durable_write(sdk, marker, {"signature": signature})
        before = len(raw_calls)
        try:
            fn = context.call_agent if kind == "agent" else context.call_mcp
            output = await fn(**kwargs)
            if restart:
                durable_write(sdk, result_file, {"output": output})
            return output
        finally:
            if len(raw_calls) > before:
                record = copy.deepcopy(raw_calls[-1])
                record.update(dbos_workflow_id=run_id, dbos_step_id=step_id,
                              attempt_number=attempt_number)
                sdk._json_write(receipts / (key + ".json"), record)

    async def agent(*, replay_safe=False, recovery=None, **kwargs):
        if recovery not in (None, "restart"):
            raise ValueError("Unknown agent recovery mode")
        if recovery is not None:
            kwargs["recovery"] = recovery
        return await bridge("agent", kwargs, replay_safe)

    def tool(function=None, *, recover=None, **options):
        if recover is not None and not callable(recover):
            raise TypeError("Tool recover must be a callable")
        def decorate(fn):
            fn = sdk.tool(fn, **options)
            fn.__relay_recover__ = recover
            return fn
        return decorate(function) if function is not None else decorate

    async def mcp(*, replay_safe=False, **kwargs):
        return await bridge("mcp", kwargs, replay_safe)

    # A module with only platform capabilities; no workflow/step decorators.
    import types
    import sys
    platform = types.ModuleType("agentworks")
    platform.agent, platform.mcp, platform.tool = agent, mcp, tool
    platform.vault, platform.variables = context.vault, data["variables"]
    platform.run_dir, platform.admit = run_dir, admit
    sys.modules["agentworks"] = platform

    DBOS(config={"name": "relays", "application_version": identity,
                 "executor_id": run_id, "enable_otlp": False,
                 "console_log_level": "WARNING", "system_database_url": url})
    sys.path.insert(0, str(source.parent))
    program = load_module("relay_program", source)
    lifecycle = {"status": "running"}
    events = run_dir / "dbos_events.jsonl"

    class EventLog(logging.Handler):
        def emit(self, record):
            # Authors must not log secrets. Keep model/tool payloads in receipts.
            event = {"timestamp": record.created, "level": record.levelname,
                     "message": record.getMessage(), "workflow_id": run_id,
                     "step_id": DBOS.step_id, "attempt_number": attempt_number}
            with events.open("a", encoding="utf-8") as output:
                output.write(json.dumps(event, ensure_ascii=False) + "\n")

    log_handler = EventLog()
    logging.getLogger("dbos").setLevel(logging.INFO)
    logging.getLogger("dbos").addHandler(log_handler)

    async def snapshot():
        steps = await DBOS.list_workflow_steps_async(run_id)
        saved = [json.loads(file.read_text()) for file in sorted(receipts.glob("*.json"))]
        calls = []
        for step in steps:
            step_id = step["function_id"]
            detail = [record for record in saved if record["dbos_step_id"] == step_id]
            error = step.get("error")
            call = {"id": "step-" + str(step_id), "name": step["function_name"],
                    "status": "failed" if error is not None else "completed",
                    "dbos_step_id": step_id, "dbos_workflow_id": run_id,
                    "checkpoint_reused": step_id in previous,
                    "output": step.get("output"),
                    "tools": [tool for record in detail for tool in record.get("tools", [])],
                    "agent_calls": detail}
            for field in ("provider", "model"):
                if detail and field in detail[0]:
                    call[field] = detail[0][field]
            for field in ("started_at", "completed_at"):
                value = step.get(field + "_epoch_ms")
                if value is not None:
                    call[field] = value / 1000
            if error is not None:
                call["error"] = error_message(error)
            calls.append(call)
        trace = {"version": 1, "durability": "dbos", "execution_model": "native-dbos",
                 "workflow_id": run_id, "attempt_id": attempt.name,
                 "attempt_number": attempt_number, "calls": calls, **lifecycle}
        # JSON is the authored contract. DBOS internal values may need a textual
        # representation (exceptions, sleep receipts, child-workflow handles).
        sdk._json_write(run_dir / "relay_trace.json", json.loads(json.dumps(trace, default=str)))

    async def monitor():
        while True:
            await snapshot()
            await asyncio.sleep(0.5)

    poll = None
    try:
        DBOS.launch()
        await snapshot()
        poll = asyncio.create_task(monitor())
        if await DBOS.get_workflow_status_async(run_id) is None:
            with SetWorkflowID(run_id):
                handle = await DBOS.start_workflow_async(program.run, data["input"])
        else:
            handle = await DBOS.retrieve_workflow_async(run_id)
        result = await handle.get_result()
        encoded = json.dumps(result, ensure_ascii=False, allow_nan=False)
        if len(encoded.encode()) > 128 * 1024:
            raise ValueError("Relay output exceeds 128 KiB")
        sdk._json_write(run_dir / "relay_result.json", result)
        lifecycle["status"] = "completed"
        save_status("completed")
    except BaseException as exc:
        message = error_message(exc)
        lifecycle.update(status="failed", error=message)
        save_status("reconciliation" if "requires reconciliation" in message else "failed", message)
        if message != str(exc):
            raise RuntimeError(message) from exc
        raise
    finally:
        if poll is not None:
            poll.cancel()
            await asyncio.gather(poll, return_exceptions=True)
        try:
            await snapshot()
        finally:
            logging.getLogger("dbos").removeHandler(log_handler)
