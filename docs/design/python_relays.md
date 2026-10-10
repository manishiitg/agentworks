# Python Relays MVP

Tracked by PLAT-611.

## Product contract

New Relays are API-callable native DBOS Python programs. Their manifest sets
`kind: relay`, `relay_runtime: python` and `relay_durability: dbos`.
The entrypoint is `@DBOS.workflow()` / `async def run(INPUT)`, returning JSON.
Native `@DBOS.step()` functions own durable operations. The platform installs,
launches and destroys DBOS; authored code does not do so.

`agentworks` is a thin adapter exposing agent, mcp, tool, vault, variables, run_dir
and async admit. Platform calls execute inside steps of the root workflow. There
are no custom Relay workflow decorators. Existing `run(INPUT, ctx)` programs and
legacy graph comments remain supported without automatic migration.

The Graph derives a possible source flow using Python AST parsing, with no authored
imports/execution. It resolves module-level DBOS functions, branches and loops;
imported and dynamically dispatched helpers may be absent. Run graphs instead use
actual DBOS history, with repeated steps rendered separately. No # @relay comments
or relay.md are needed for native programs.

Execution Logs defaults to DBOS history for durable runs: workflow/step IDs, status,
timing, attempts and reused checkpoints. Agent/provider/tool receipts link by step
ID. DBOS.logger events are recorded in dbos_events.jsonl with invocation, step and
attempt metadata. Agent files remain accessible through a separate view.

Completed native steps are reused after interruption. An in-flight native service
step can replay; service writes require stable idempotency keys and deduplication.
The adapter detects uncertain agent/MCP calls and stops for reconciliation unless
explicitly replay_safe. Before custom external service operations, await admit()
checks live invocation access. Secrets must stay out of persisted arguments,
results and logs. Recovery currently uses a single-host SQLite DBOS database.

Native `agent(..., recovery="restart")` opts into restarting the full agent after
a process interruption while reusing durable Python tool results. This mode
exposes declared Python action tools and read-only tool inventory helpers;
skills, attached MCP servers and generic platform code execution are unavailable.
Native CLI restrictions still depend on the provider. The journal protects the
declared Python tools, not arbitrary actions outside that boundary. This mode
cannot be combined with `replay_safe=True`.
Within one agent call, a tool name and its normalized JSON arguments identify
one operation. Repeated identical requests return the saved result, even within
the same attempt. Include an explicit operation ID in the arguments when the
same tool must perform distinct actions with otherwise identical inputs.

Before calling a tool, the adapter durably records intent; before returning it,
the adapter durably records its result. Completed results are reused across
agent restarts. For a crash between the action and its saved result,
`@tool(recover=lookup_receipt)` may provide a sync or async callback receiving the
original arguments and returning the original JSON result. This callback must
only query a durable service receipt, using a stable business operation ID. It
must raise if the outcome cannot be established. Recovery resolves all pending
tool calls before starting a fresh agent; missing or failed lookups stop for
reconciliation. Exceptions from uncertain tool calls stop the agent rather than
being handed back to the model to work around.

This protects journaled operations with the same arguments. A restarted model
can choose different arguments or new actions, and remote side effects cannot
be made exactly once by a local journal alone. Service-side idempotency or an
atomic transaction with the service remains necessary for those guarantees.
Persisted tool arguments/results must not contain secrets.

```python
from dbos import DBOS
from agentworks import agent

@DBOS.step(name="extract_invoice")
async def extract_invoice(text):
    return await agent(name="extract", system_prompt="Extract invoice fields as JSON.",
                       user_message=text, output_schema={"type": "object"})

@DBOS.workflow(max_recovery_attempts=3)
async def run(INPUT):
    return {"invoice": await extract_invoice(INPUT["text"])}
```

The following Context API descriptions apply to existing programs; the native
adapter exposes the equivalent agent/mcp/tool/vault/variables capabilities.

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

Native DBOS.logger events additionally live in `dbos_events.jsonl`; their workflow
and step IDs correlate with `relay_trace.json`.

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

## DBOS crash recovery and legacy opt-in

New native DBOS Relays enable `relay_durability: "dbos"` by default. Existing
Context-based Python Relays can opt in through the manifest or
**Identity → General → Crash recovery**. The server uses DBOS 3.2.0 through
`relaypython.Config.DBOS`, the real Python SDK and the existing Go agent/MCP
mailbox bridge. Existing Relays retain their current executor until explicitly
enabled. Publish a new version to apply the setting to external API calls.
This integration runs on Linux/macOS with one SQLite database and one supervised
Python process per invocation. It does not introduce a shared database credential
into authored Python, use Conductor, or implement distributed recovery.

Each attempt gets a fresh mailbox under `.relay_ipc/attempt-UUID/`. DBOS state
lives in the invocation's `.relay_dbos/` directory. Re-entering `Run` with the same
run folder and identity recovers pending work, reuses completed operation
results, and restores the returned JSON and trace. A file lock rejects concurrent
executors. Errors remain terminal; a failed run is never silently restarted.

The caller must supply `RunID`, the published `ReleaseHash`, and an `Authorize`
callback that revalidates live invocation access and the **complete** frozen
release checksum before each attempt and platform call. The adapter also binds
input, variables, source bytes, SDK/runner bytes, Python version, DBOS version,
and SQLAlchemy version to the original run. Changed bindings are rejected.
Vault values remain live environment inputs, outside the saved binding. The
existing agent/MCP adapter remains responsible for current connection and tool
permissions. Published helpers must be verified by the admission callback.

For legacy Context programs, `ctx.call_agent` and `ctx.call_mcp` are checkpointed at whole-call boundaries.
`await ctx.step(name, function, arguments={...})` checkpoints a sync/async Python
service operation; its closure can access the live credentials. Only JSON
results and call receipts are checkpointed, not a live Context or tool closures.
Arguments, outputs, receipts, and DBOS errors are stored in run history; authors
must not include secret values in those records.

A legacy operation or native platform bridge call interrupted before its checkpoint is **uncertain**. Recovery stops
for reconciliation by default. `replay_safe=True` explicitly permits reattempting
that operation and is only appropriate for reads or a service that deduplicates
an idempotency key included in `arguments`. An agent call with this flag requires
every possible tool effect to be safe to repeat; checkpointing an entire agent
does not make its individual LLM turns or tool effects durable.

Authored code must follow DBOS determinism: branches and loops are computed from
input and saved step results; external I/O, time, randomness, and file effects
belong inside steps. Top-level imports must be free of side effects. `replay_safe`
is an author assertion, not proof. The integration does not statically enforce
this contract or automatically migrate existing arbitrary Python programs.

Run the real crash/recovery checks from the repository root:

```sh
python3 -m venv /tmp/relays-dbos-venv
/tmp/relays-dbos-venv/bin/python -m pip install -r agent_go/pkg/relaypython/requirements-dbos.txt
cd agent_go
RELAY_DBOS_PYTHON=/tmp/relays-dbos-venv/bin/python go test ./pkg/relaypython ./pkg/schedulerstate -count=1 -v
```

Without `RELAY_DBOS_PYTHON`, DBOS integration tests explicitly skip and the normal
runner regression still runs. The tests exercise actual process exits after a
checkpoint and during a service effect, two agent calls, Python tool callbacks,
an MCP call, service idempotency, original-result reuse, source/input/release
binding, terminal errors, live authorization revocation, and concurrent executor
rejection. Model and external service boundaries use deterministic fixtures;
these tests do not certify a real LLM provider or external MCP server.

### Browser recovery lab

The standalone browser harness runs the same adapter without starting the main
server or connecting an external account. After installing the interpreter as
above, run from `agent_go`:

```sh
RELAY_DBOS_PYTHON=/tmp/relays-dbos-venv/bin/python go run ./cmd/relay-dbos-demo
```

Open <http://127.0.0.1:18769>. Select **Crash after a checkpoint**, click
**Start new run**, wait for **Process interrupted**, then click **Recover same
run**. The same invocation finishes with two reused checkpoints, one service
attempt and one created order. Expand the trace to inspect actual saved receipts
and Go bridge call counts.

**Crash during a safe action** retries the service with the same idempotency key:
two service attempts, one created order. **Crash during an uncertain action**
stops recovery for reconciliation without repeating the service. To test live
admission, revoke permission after a checkpoint crash; recovery is denied until
permission is restored. **Complete without a crash** shows the ordinary path.

The model, MCP and order-service boundaries are deterministic fixtures. Python
tool callbacks, process exits, SQLite checkpoints and recovery use the real
runtime. This is a local test harness, separate from the deployed Relays UI.
It listens only on loopback, stores test runs in a private temporary folder, and
removes that folder on Ctrl-C. Runs persist across Python crashes while the
harness stays open; restarting the harness starts a fresh lab.

### Actual app supervision and deployment

The scheduler owns the original invocation ID, folder, input, release hash and
execution owner in its durable ledger. A pending Python process exit triggers
recovery; an ordinary application error remains terminal. Backend startup only
re-admits DBOS invocations that its ledger marked interrupted by server restart.
Admission rechecks the live caller, function, account and complete release hash.
A lease watchdog terminates a workspace executor whose backend disappeared;
startup waits 17 seconds before claiming it. Agent receipts use attempt-specific
folders. Retries retain the first one-hour deadline and share a persisted budget
of three process attempts. Stopped, failed and ordinary Python runs are never
reopened by the DBOS recovery path.

Install the pinned requirements in a platform-controlled path readable by the
workspace sandbox and set `RELAY_DBOS_PYTHON` on the backend to that absolute
interpreter path. Both Docker images provide `/opt/relay-dbos/bin/python`.
Native macOS acceptance used `/opt/homebrew/share/agentworks/relays-dbos/bin/python`.
An interpreter inside a host user's home directory is not a supported workspace
sandbox executable. The backend ledger and workspace run directories must both
survive restart. A missing runtime or ledger produces an explicit run error.

Use native @DBOS.step functions for new service operations, with service-side
idempotency. Legacy programs use ctx.step; arbitrary Python is not automatically
replay-safe. In Runs, select the published version
and inspect the attempt count, reused checkpoint badges, tool receipts and final
JSON. `POST /api/relays/{id}/releases` publishes a version with existing write
access checks; the existing run API invokes its enabled function.

This is single-host recovery using per-invocation SQLite and a file lock, not
multi-host failover. A distributed worker architecture and Postgres rollout are
separate work. See [DBOS database configuration](https://docs.dbos.dev/python/tutorials/database-connection).

Opt-in server/sandbox regression (requires the trusted interpreter path):

```sh
cd agent_go
RELAY_DBOS_PYTHON=/opt/relay-dbos/bin/python go test ./cmd/server -run TestDBOS -count=1 -v
```

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

## Legacy graph annotations in Python source

These annotations remain supported for existing run(INPUT, ctx) programs. Native
DBOS programs derive their overview from Python AST and do not need annotations.

PLAT-640 replaces the
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

PLAT-638 fixes shared API
trigger admission and listing for Python Relays: they expose no workflow step or
route inventory and must not load `planning/plan.json`. A function invokes the
Python entrypoint. Workflow step targets and payload routing are rejected,
including disabled trigger definitions; branching belongs in the Python code.
Identity, variable checks, trigger authorization and legacy graph validation
retain their shared paths. Release snapshots exclude the internal manifest lock
created by those normal saves, allowing the real create/publish/run lifecycle.
