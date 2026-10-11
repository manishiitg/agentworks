---
name: relay-builder
description: Author, test and publish native DBOS Python Relays with platform agents and authorized tools.
---

# Python Relay contract

Read relay.py, workflow.json and variables/variables.json before editing. New Relays
have kind: relay, relay_runtime: python and relay_durability: dbos. Author native
DBOS Python: `from dbos import DBOS`, `@DBOS.workflow()` and `@DBOS.step()`.
The entrypoint is `@DBOS.workflow()` followed by `async def run(INPUT)`.
INPUT is the caller's JSON object. Return JSON (128 KiB limit).
Keep source separate from workflow plans. No custom Relay workflow decorators are needed.

## Graph and execution logs

Users describe behaviour in chat; Code is optional. The Graph is a read-only source
overview derived from Python's AST without importing/executing it. It recognizes
module-level DBOS-decorated functions in relay.py, conditions, loops and returns.
Use readable function names and docstrings. Imported helpers and dynamic dispatch
may not appear in this static overview. Runs and Execution Logs show actual DBOS
step history, including repeated calls, step IDs, timestamps and reused checkpoints.
Agent and tool receipts are linked to those step IDs. DBOS.logger messages appear
in Execution Logs. Never put secrets in step arguments, results, prompts or logs.
Do not create # @relay comments, relay.md or a separate plan for native DBOS source.

## Authoring example

```python
import json
from dbos import DBOS
from agentworks import agent

@DBOS.step(name="extract_invoice")
async def extract_invoice(text):
    """Extract the invoice fields."""
    return await agent(
        name="extract", system_prompt="Extract invoice fields. Return JSON only.",
        user_message=text,
        output_schema={"type": "object", "required": ["amount"],
                       "properties": {"amount": {"type": "number"}}},
    )

@DBOS.step(name="review_invoice")
async def review_invoice(invoice):
    """Review invoices over 1000."""
    return await agent(name="review", system_prompt="Review the invoice. Return JSON.",
                       user_message=json.dumps(invoice))

@DBOS.workflow(max_recovery_attempts=3)
async def run(INPUT):
    invoice = await extract_invoice(INPUT["text"])
    if invoice["amount"] > 1000:
        invoice = await review_invoice(invoice)
    return {"invoice": invoice}
```

The thin `agentworks` module supplies agent, mcp, tool, vault, variables, run_dir
and async admit. It supplies no workflow or step decorators. Use `from agentworks
import tool` for model-callable Python tools. Implement real services; do not leave
placeholder responses. Platform agent/MCP calls must run within a step of the root
run workflow; platform calls in child workflows are currently unsupported.

## Agent calls and tools

- agent is keyword-only. system_prompt is text. Pass user_message or messages
  (nonempty ordered text list), never both. Each call gets a new session. Messages inside
  that call share context; separate calls do not. There is no resume_agent.
- model is provider:model_id or {provider, model_id, connection_id, options}; omitted
  uses the configured Builder model. Use current supported provider/model names.
- tools=[decorated_callable] executes that function in the live Relay Python process
  when the model asks. Closures and async functions work. Return JSON serializable data.
  Provide description/schema to @tool for complex inputs. Tool arguments are checked.
- skills=["installed-name"] attaches explicitly named bundles from this Relay workspace.
- mcp=[{"server":"exact connection","tools":["exact tool"]}] exposes selected tools
  to the agent. Omit tools to select all tools on that explicitly named connection.
  Resolve names/schemas using list_mcp_servers first. Live access/grants are rechecked.
- await mcp(server="exact connection", tool="exact tool", arguments={...})
  calls the shared platform MCP executor directly from Python with the run's authority.
- vault("NAME") returns an admitted SECRET_NAME. Missing/deselected secrets fail.
  Do not print or embed secrets in source, inputs, traces or returned JSON.
- variables["NAME"] is a string from flat variables[].value. INPUT is per-call
  data; Python does not use workflow group batching or template variables.
- output_schema validates the final parsed JSON; invalid/missing JSON raises. There
  are no automatic repair messages. Without it, JSON responses become Python values
  and other responses stay text. Include JSON-only requirements in the authored prompts.
- max_turns defaults to 20, range 1-100. Calls are serialized in MVP. Python can loop
  and branch. Use DBOS steps for external I/O as described below.

## Native DBOS recovery

DBOS owns workflow execution, durable steps, replay and native retry policies. Keep
imports and workflow orchestration deterministic and free of external effects.
Branch on INPUT, configuration and saved step results. External I/O, clock/random
reads and file writes belong inside @DBOS.step functions. Use DBOS.sleep_async for
durable waiting. Do not instantiate, launch or destroy DBOS in authored source;
the platform owns process startup and binds the original invocation identity.

Completed steps return their saved results on recovery. An interrupted ordinary
DBOS step can execute again: service writes must use a stable idempotency key and
service-side deduplication. Never claim exactly-once external effects solely from
checkpointing. Before custom external service operations, `await admit()` checks
live invocation access. Read selected credentials with vault inside the step; do
not persist them in DBOS arguments/results. Agent/MCP admission is automatic.

The platform bridge records intent for agent/MCP calls. Uncertain bridge calls
stop for reconciliation by default. Set replay_safe=True only after verifying the
call is read-only or every possible tool/service effect deduplicates a stable key.
This rule also applies to DBOS automatic step retries. Native service steps retain
DBOS semantics; they do not acquire the bridge's uncertain-action protection.

Each invocation has at most three Python process attempts and keeps its original
one-hour deadline. Source/helpers/configuration and runtime identity must match
for recovery; editing a draft during its test fails recovery closed. Published
versions are immutable. Credentials and grants are admitted live on each attempt.
Runtime installation is platform configuration (RELAY_DBOS_PYTHON).
SQLite recovery is currently single-host; distributed execution is not provided.

## Existing programs

Preserve existing `async def run(INPUT, ctx)` programs unless migration is requested.
Their relay_sdk Context and optional # @relay graph comments remain supported.
Legacy DBOS-enabled programs checkpoint ctx.call_agent, ctx.call_mcp and ctx.step;
unsafe uncertain operations stop. Do not silently enable recovery on legacy source.
For an explicit migration, move calls into native @DBOS.step functions, import
platform helpers from agentworks, change run to one argument and enable DBOS.

## Build, test and publish

Use workspace diff_patch/execute_shell_command to save source and helpers. Package
installation uses the existing sandbox dependency mechanism; never install into a
published snapshot. Preserve Python whitespace and literal escapes.

Configure function triggers through manage_workflow_webhook with required object
INPUT. Python Relays invoke run(INPUT) and have no step_id, route_selections or
payload routing mappings. If no function is saved yet, create one before testing.
Use test_relay(input={...}, function="...") for the draft; it returns a run_id.
Inspect get_relay_run(run_id="...") for completion/output/error and recorded calls.
Direct shell execution is not a platform test and does not verify triggers, agents,
credentials or publication; report that distinction and the exact failing tool error.
A failed run is terminal. Testing again creates a new invocation and may repeat effects;
use safe sample data. No run_full_workflow, execute_step, plans, groups, prevalidation,
DB/KB/learnings closing turns or Pulse apply to Python Relays.
Optional dashboard database migrations are authoring actions, separate from execution.

Use get_relay_releases and publish_relay when the user asks. Published source, helpers,
skill files and configuration are immutable. Runtime secrets/connection permissions
are resolved live. Public calls select a version or default to active; Builder tests
always use draft. Report the actual returned version/hash, not an assumed deployment.

Builder remains the shared scoped chat runtime with this product-owned prompt, skill
and tool list. Vault/MCP tools retain their normal authorization. Brain is not an
implicit Relay store or runtime. Do not create workflow knowledgebase assets.
A requested Dashboard may use an optional managed database as described by relay-dashboard.

MVP custom Python tools must not call `agent` or `mcp` inside
the tool callback; nested calls fail immediately. Put those calls in separate DBOS steps, or
attach an MCP tool directly to the agent. Tools can call their authorized
external services using ordinary Python clients.
