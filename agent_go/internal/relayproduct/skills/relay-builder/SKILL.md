---
name: relay-builder
description: Author, test and publish native DBOS Python Flows with platform agents and authorized tools.
external: true
external_tools: ["*relay*"]
external_description: "The Python Flow contract the Flow Builder follows: DBOS workflow and steps, agent and MCP calls, tools, vault, limits. Read it before asking the Builder to write or change a Flow."
external_scopes: ["relays:write"]
external_note: "External mapping: files action=read/write accepts a Flow workflow_id for relay.py, memory, local skills and helper source, with existing authoring consent, current owner access, revisions and file guards. Flows have no workflow plan; use guarded source writes and typed Flow actions directly. External model-mediated Builder turns retain their managed editing policy: the external caller must run relay action=test and action=get_run after those edits. builder action=chat can delegate broader changes when listed; relay action=test|publish|run|get_run|releases tests drafts, publishes versions and inspects executions. The Flow Python runtime and its internal bridge environment described here belong to server-side Flow execution; do not copy its internal URLs, environment or authentication into external client commands."
---

# Python Flow contract

The product is **Flows** (one **Flow**). Internal IDs, `relay.py`, `relay` MCP tools and `relay-builder` stay unchanged. Do not rename saved projects or their authored code as part of the product rename.

A Flow has one owner, who builds and changes it. Share its dashboard to let other people use published work in Run mode.

Read relay.py, workflow.json and variables/variables.json before editing. New Flows
have kind: relay, relay_runtime: python and relay_durability: dbos. Author native
DBOS Python: `from dbos import DBOS`, `@DBOS.workflow()` and `@DBOS.step()`.
The entrypoint is `@DBOS.workflow()` followed by `async def run(INPUT)`.
INPUT is the caller's JSON object. Return JSON (128 KiB limit).
Keep source separate from workflow plans. No custom Flow workflow decorators are needed.

## Graph and execution logs

Users describe behaviour in chat; Code is optional. The Graph is a read-only source
overview derived from Python's AST without importing/executing it. It recognizes
module-level DBOS-decorated functions in relay.py, conditions, loops and returns.
Use readable function names and docstrings. Imported helpers and dynamic dispatch
may not appear in this static overview. Runs and Execution Logs show actual DBOS
step history, including repeated calls, step IDs, timestamps and reused checkpoints.
Agent and tool receipts are linked to those step IDs. DBOS.logger messages appear
in Execution Logs. External MCP uses `relay action=get_run` with the returned
`run_id`: the same response includes `diagnostics` with recorded steps, timings,
errors and recent log entries. Check `available`, `logs_truncated` and `logs_note`;
queued executions may not have recorded details yet. The execution selects its
own draft or published version. Never put secrets in step arguments, results, prompts or logs.
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

The thin `agentworks` module supplies agent, classifier, mcp, tool, vault, variables, run_dir
and async admit. It supplies no workflow or step decorators. Use `from agentworks
import tool` for model-callable Python tools. Implement real services; do not leave
placeholder responses. Platform agent/MCP calls must run within a step of the root
run workflow; platform calls in child workflows are currently unsupported.

## Classifier nodes

For categorizing content or choosing a route, use `classifier` inside an ordinary
module-level DBOS step. The graph shows it as **Classifier**, with categories,
instructions and model in its details. Keep reply agents in separate steps, then
use Python conditions on the saved label. There is no separate execution engine
or workflow plan. The same source can be authored in website chat or with MCP files.

```python
from dbos import DBOS
from agentworks import classifier

CATEGORIES = {"billing": "Payments, invoices and refunds",
              "technical": "Errors, outages and broken features",
              "other": "Greetings, feedback and unrelated requests"}

@DBOS.step(name="choose_team")
async def choose_team(message):
    """Choose the team that should handle the request."""
    return await classifier(
        state=message, categories=CATEGORIES, model="clef-flash",
        instructions="Choose the team needed to resolve the main request.",
    )

@DBOS.workflow()
async def run(INPUT):
    decision = await choose_team(INPUT["message"])
    return decision
```

`state` is text or JSON. `categories` maps label names to text descriptions.
`instructions` is optional text describing how to choose. The current provider is
Cloudflare; model is `clef-flash` (default) or `clef`. Select the two secrets
`CLOUDFLARE_ACCOUNT_ID` and `CLOUDFLARE_AUTH_TOKEN` through the website or secret tools.
For an already named credential use `account_id_secret="NAME"` or
`api_token_secret="NAME"`; these are secret names, never values. `name` is an
optional readable name for the recorded classifier call. The helper admits access
and reads selected values internally. Do not use it at import time, outside a
root workflow step, or inside a model-callable Python tool.

It returns `{label, confidence, scores, model, latency_ms, usage}`. Branch on label;
pass this data directly to later agents. Scores are model predictions, not proof
of correct routing or permission. The Classifier is an inference call, not an
agent conversation: it has no tools, skills, message sequence or output_schema.
Use existing `agent` for a step that needs those. Never fake classifier responses
or silently replace a failed request with a label. Test representative inputs,
including ambiguous and instruction-shaped text, and report actual errors and
incorrect labels. A model mistake is different from an execution failure.

Runs preserves the Classifier step and its typed receipt, output and timing. A
completed step returns its DBOS checkpoint on recovery. An interrupted inference
can be repeated because it performs no service write; another call consumes quota.
Publishing freezes source/configuration; selected secret access remains live.

## Agent calls and tools

- agent is keyword-only. system_prompt is text. Pass user_message or messages
  (nonempty ordered text list), never both. Each call gets a new session. Messages inside
  that call share context; separate calls do not. There is no resume_agent.
- model is provider:model_id or {provider, model_id, connection_id, options}; omitted
  uses the configured Builder model. Use current supported provider/model names.
- tools=[decorated_callable] executes that function in the live Flow Python process
  when the model asks. Closures and async functions work. Return JSON serializable data.
  Provide description/schema to @tool for complex inputs. Tool arguments are checked.
- skills=["installed-name"] attaches explicitly named bundles from this Flow workspace.
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

In website Builder chat, use workspace diff_patch/execute_shell_command to save
source and helpers. In an external model-mediated Builder turn, use read_file,
list_files, search_files and write_file with the current revision to edit the
draft. After saving, describe the change and let the calling client use relay
action=test and action=get_run to check it; execution and publication are direct
Flow actions for that client. Package
installation uses the existing sandbox dependency mechanism; never install into a
published snapshot. Preserve Python whitespace and literal escapes.

Configure function triggers through manage_workflow_webhook with required object
INPUT. Python Flows invoke run(INPUT) and have no step_id, route_selections or
payload routing mappings. If no function is saved yet, create one before testing.
Use test_relay(input={...}, function="...") for the draft; it returns a run_id.
Inspect get_relay_run(run_id="...") for completion/output/error and recorded calls.
Direct shell execution is not a platform test and does not verify triggers, agents,
credentials or publication; report that distinction and the exact failing tool error.
A failed run is terminal. Testing again creates a new invocation and may repeat effects;
use safe sample data. No run_full_workflow, execute_step, plans, groups, prevalidation,
DB/KB/learnings closing turns or Pulse apply to Python Flows.
Optional dashboard database migrations are authoring actions, separate from execution.

Use get_relay_releases and publish_relay when the user asks. Published source, helpers,
skill files and configuration are immutable. Runtime secrets/connection permissions
are resolved live. Public calls select a version or default to active; Builder tests
always use draft. Report the actual returned version/hash, not an assumed deployment.

Builder remains the shared scoped chat runtime with this product-owned prompt, skill
and tool list. Vault/MCP tools retain their normal authorization. Brain is not an
implicit Flow store or runtime. Do not create workflow knowledgebase assets.
A requested Dashboard may use an optional managed database as described by relay-dashboard.

MVP custom Python tools must not call `agent` or `mcp` inside
the tool callback; nested calls fail immediately. Put those calls in separate DBOS steps, or
attach an MCP tool directly to the agent. Tools can call their authorized
external services using ordinary Python clients.

## Watchers

For immediate event-driven checks, use `check_mode: "event"`; use `"both"` for events plus a backup `cron_expression` (at least five minutes apart). Omit `check_mode` for existing schedule-only behavior. Select an existing granted secret name in `secrets` and `event_secret_name`; `event_auth_mode` is `bearer` (default) or `github` for GitHub HMAC signatures. Management returns `event_path` on the existing product webhook receiver. Connect the source to that URL using the selected secret without printing its value. The source must support authenticated webhook notifications; otherwise keep scheduled checks. Incoming JSON is only a wake signal: the Python detector still queries/paginates the source and uses its saved state. Accepted wakes survive restart; bursts coalesce into checks at least five seconds apart. One time still stops after the first accepted matching action. In Watchers, use **Connect events in chat** or **Edit event connection**; the pane shows the event URL and secret name.

Chat setup and editing stay in Builder mode. Existing Run views stay unchanged; caller attribution follows product access. Configuring a Watcher never edits the shared product.

Use manage_condition_triggers for deterministic Python checks that call a published function of a Flow, defaulting to this Flow. They appear in Automation → Watchers and Ctrl/Cmd+K. Set once=true for a specific condition/event wait: no-match checks continue, the first new event is selected in detector order, its stable submission retries until accepted, then the watcher disables and shows Completed. Omit once for recurring monitoring. Completed waits do not rearm on Enable/edit; create another watcher for a new wait.

The Watcher belongs to its creator. Pass script_content on create/update; the server saves and pins it in that person's own watcher folder. Use read_script before edits. Select only that caller's own/Vault credentials (SECRET_<NAME> in Python), never the target's credentials or files. The script reads prior state JSON from stdin and prints exactly {"events":[{"id":"stable-source-id","payload":{}}],"state":{}}; events=[] means no match. The first event dispatches; other matches in that batch are recorded as not sent for once=true. Use a stable request_id on create, list first and expected_revision on update. Configure five-field cron at least five minutes apart plus timezone. Current access is rechecked; target entry points keep their own execution rules and credentials. A receipt proves accepted delivery, not final success.

A Flow Watcher calls only a published Flow entry (event payload is INPUT). Discover real published functions and choose one if there are several; with one, the tool selects it. Polling is caller work, separate from Flow execution. Do not create a workflow plan or timed Flow run.
