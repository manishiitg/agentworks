# Python Relays MVP

Tracked by [PLAT-611](../bugs/pulse_platform/relays/execution/plat-611.md).

## Product contract

A Relay is an API callable Python program. Builder chat authors `relay.py` and
helper files, and maintains structured graph comments in that same source file.
The right pane opens on Graph, with Runs for actual execution records and an
optional Code tab. Users describe behaviour in chat; they do not need to program.
No `relay.md` is created, read or required. Existing copies remain unused.
New Relays set
`kind: relay` and `relay_runtime: python` in the shared `workflow.json` manifest.
`async def run(INPUT, ctx)` accepts one JSON object and returns a JSON value.
Python owns conditions, loops, transformations and agent chaining. Its returned
value is the API result; returning intermediate results is an author choice.
There is no plan translation or workflow step executor on this path.

`ctx.call_agent` reuses the platform's `mcpagent.AgentDefinition` and session
runtime, provider accounts and signed tool bridge. Each call creates a fresh
agent. `messages=[...]` sends sequential user messages in that call's session;
no session handle is exposed for later calls. Authored system instructions are
passed directly, with no workflow goal, Pulse or learned instruction injection.
There is no `resume_agent`, crash recovery, capacity retry or automatic replay.

```python
import json
from relay_sdk import tool

async def run(INPUT, ctx):
    @tool
    async def lookup(customer_id: str):
        """Read one customer from the application's authorized API."""
        # Implement the real service request using ctx.vault("CUSTOMER_KEY").
        return {"id": customer_id}

    customer = await ctx.call_agent(
        system_prompt="Use lookup to find the requested customer. Return JSON.",
        user_message=json.dumps(INPUT),
        tools=[lookup],
        output_schema={"type": "object"},
    )
    return {"customer": customer}
```

The example illustrates the boundary; the Builder must implement real service
requests, rather than leave placeholder tool bodies in a working Relay.

## Capabilities

- Exact system prompt, one user message or an ordered message list.
- Model per call; absent model uses the configured Builder profile.
- Custom sync/async Python functions, including closures, exposed with `@tool`.
  Basic argument annotations infer an input schema; complex inputs require an
  explicit schema. Tool results must serialize to JSON.
- Explicit `skills=[...]` and `mcp=[{"server": "name", "tools": ["tool"]}]`.
  Omitted tools select that server's inventory, not all platform connections.
  No default workflow tools, DB, KB or learnings are attached.
- `await ctx.call_mcp(server=..., tool=..., arguments={...})` uses the same live
  platform authorization and executor as agent MCP tools.
- `ctx.vault("NAME")` reads an admitted secret, resolved live for every run.
  `ctx.variables` is flat string configuration; invocation data is `INPUT`.
- Python can call Google, Gmail, Slack or other APIs with explicitly admitted
  credentials/connections. This does not create chat bots or scheduled runs.
- Optional `output_schema` validates the final message. Invalid/non-JSON output
  raises an error; authors can catch it and explicitly call another agent.

MVP agent/MCP calls are serialized, including `asyncio.gather` calls. Each agent
allows 1–100 user messages and 1–100 turns per message (default 20). A run is
bounded to one hour. Returned JSON is bounded to 128 KiB. Publish checks syntax
and entrypoint without importing or executing authored source.

## Shared infrastructure

Reuse the existing identity/access checks, `product.yaml` prompt/tool/skill
contract, Builder chat, file editor, variables/secrets, MCP management, API
function triggers, idempotent deliveries, durable run states, publishing,
releases, cost observer, running execution monitor and `/api/live` events.
Only the Python SDK/mailbox and core-agent adapter are new execution code.
No new global agent-call HTTP endpoint or RPC credential is introduced.

The Python process runs through the existing workspace shell sandbox. Source is
readable; writes are confined to its invocation folder. Each CLI agent gets its
own isolated session and execution folder. Existing CLI account/sandbox rules
remain responsible for process and credential admission.

Run files retain the existing `runs/iteration-N-hook/` convention:

- `relay_result.json`: returned API JSON.
- `relay_trace.json`: actual calls, resolved models, tool receipts, errors and
  outputs; also included in run polling responses, bounded to 2 MiB.
- `execution/call-N/`: per-agent runtime artifacts and authored file outputs.
- `.relay_ipc/`: private invocation protocol and runner files.

Data passes as Python return values, not user-managed intermediate files.
Internal result/trace/IPC files support the API, inspection and tool callback.
Failures are terminal; infrastructure failures finalize trace status too.

## Releases and compatibility

Existing publishing freezes source, helpers, skills and configuration in a
content-hashed workspace. Draft tests use the draft. External runs use the
active or explicitly selected published version. Editing a draft does not
rewrite previously published source. Secrets, provider accounts and connection
permissions remain live and may revoke a published run.

The platform Python runtime and installed dependencies are shared deployment
resources; MVP does not create a per-release virtual environment or pin those
binaries. Include dependency declarations in source and install through the
existing authorized sandbox tooling when needed.

Existing JSON/graph Relays remain on their legacy executor and migration rules.
There is no automatic conversion. The Builder product now teaches Python;
creating a new Relay is the supported authoring path. Python Relays have no
workflow contract migration debt. Goals and legacy Relay execution remain
unchanged.

## Verification

- Real Python protocol chain: custom async closure, ordered messages, branch,
  second agent, direct MCP invocation, JSON result and terminal failure.
  The model boundary in this test is deterministic.
- Real workspace handlers: syntax admission without imports, immutable v1/v2
  publication, execution of published starter, result/trace polling and release
  hash verification after runtime writes.
- Real Codex acceptance passed with an actual Python tool and two messages.
  Opt-in real CLI acceptance: `RUN_PYTHON_RELAY_AGENT_E2E=<provider>` drives the
  production adapter and signed HTTP bridge to a Python closure, with two
  messages in one session. Requires a signed-in provider and mcpbridge binary.
- Focused shared Relay/function/webhook/product/livefeed regressions.
- Frontend TypeScript and Graph/Runs/Code/file-editor tests; annotation parser checks
  for quoted examples, malformed/duplicate records and dangling edges.

These checks do not certify every external MCP server, provider or deployment.

MVP custom Python tools must not call `ctx.call_agent` or `ctx.call_mcp` inside
the tool callback; nested calls fail immediately. Put those calls in `run`, or
attach an MCP tool directly to the agent. Tools can call their authorized
external services using ordinary Python clients.

## Graph annotations in Python source

[PLAT-640](../bugs/pulse_platform/relays/frontend-chat/plat-640.md) replaces the
separate Markdown overview proposed in PLAT-637. Graph comments live beside the
implementation and are published as part of the exact frozen `relay.py`.
Execution ignores these display comments; malformed/missing annotations do not
block running or publishing. Python remains the only execution definition.

One standalone comment per record, with a JSON object on the same line:

```python
# @relay node {"id":"extract","type":"agent","label":"Extract invoice","tools":["lookup_customer"]}
# @relay node {"id":"review","type":"agent","label":"Review large invoice"}
# @relay node {"id":"result","type":"output","label":"Return invoice"}
# @relay edge {"from":"extract","to":"review","label":"Amount > 1000"}
# @relay edge {"from":"extract","to":"result","label":"Amount <= 1000"}
# @relay edge {"from":"review","to":"result"}
```

Nodes require a unique `id` (starts with a letter, max 64 letters/digits/underscore/
hyphen), `type` (`input`, `agent`, `script`, `decision`, `output`) and readable
`label`. Optional fields: `description`, `input`, `output`, `system_prompt`,
`user_message`, `messages` (ordered text list), `model`, `tools`/`skills` (text
lists), `mcp`, and `call` (an exact runtime call name when different from the id).
Edges require `from`/`to` existing node ids, with optional `label` for conditions.
Up to 200 nodes and 400 edges; identical edges are rejected. Source comments
inside quoted examples/docstrings do not define graph nodes. An invalid graph
shows correction guidance rather than a partial diagram.

Reuse the workflow React Flow/Dagre stack, route colours, workspace file reads,
file editor, run selectors, live feed and frozen release files. Node selection
shows annotation details. The Builder's product.yaml command, product-owned
prompt and skill own this format and keep it aligned with the saved code.
Annotation fields are author descriptions; they are not evaluated or proof of
the prompts/models used in a run. Never put secret values in comments.

Agent ids (or `call`) match `ctx.call_agent(name=...)`. Runs overlay actual named
agent calls, status, tool receipts and returned outputs; loops can have several
calls under one node. Unmatched calls remain in the run list. Script/decision
nodes and conditional edges have no inferred execution status. Published runs
render their frozen version's annotations; draft run graphs explicitly show the
current draft, which may differ from an older test. No graph renderer walks or
executes the program, and no plan translation or new agent runtime is introduced.

New starters include input/script/output nodes. Existing Python source without
annotations shows an Add graph in chat action; Builder reads the source and adds
comments without changing behaviour. Existing legacy graph Relays are unchanged.

## Shared trigger admission

[PLAT-638](../bugs/pulse_platform/relays/triggers/plat-638.md) fixes shared API
trigger admission and listing for Python Relays: they expose no workflow step or
route inventory and must not load `planning/plan.json`. A function invokes the
Python entrypoint. Workflow step targets and payload routing are rejected,
including disabled trigger definitions; branching belongs in the Python code.
Identity, variable checks, trigger authorization and legacy graph validation
retain their shared paths. Release snapshots exclude the internal manifest lock
created by those normal saves, allowing the real create/publish/run lifecycle.
