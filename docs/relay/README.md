# Relays

A Relay is a Python program you call from your product: JSON in, agents and
Python do the work, JSON out. You describe what you want in a Builder chat,
test the draft, then publish versioned releases (`v1`, `v2`, …) that stay
stable while you keep editing.

## How one call flows

<div class="sec-flow">
<div class="sec-box"><b>1. Call</b><span>POST your function, input JSON, and an idempotency key. Empty version means the active release.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box"><b>2. Run pinned</b><span>The run executes that release's frozen snapshot — later publishes can't change it mid-flight.</span></div>
<div class="sec-arrow">→</div>
<div class="sec-box hi"><b>3. Poll result</b><span>GET the run until it completes. The response carries the JSON result plus the version that ran.</span></div>
</div>

Repeat an idempotency key and you get the original run back (`duplicate:
true`), even after newer releases publish. Reuse a key with *different* input
and the API refuses with 409 — keys are promises, not suggestions.

## Drafts vs releases

| | Draft | Release (v1, v2, …) |
|---|---|---|
| Lives in | Your editable workspace | A frozen, checksummed snapshot |
| Changes when | You edit in the Builder | Never — publish mints a new one |
| Runs via | Builder tests | API calls (active, or a pinned older one) |
| On failure | You see it in chat and fix it | Fails visibly with the call and reason |

New Relays use `relay.py` with `async def run(INPUT, ctx)`. Publishing requires
valid Python with that entrypoint and at least one enabled function accepting
a required object `INPUT`. The returned value is the JSON result. Only owners and editors can publish. Users with Relay visibility can invoke permitted API functions.

## What's inside a Relay

<div class="sec-lanes">
<div class="sec-lane"><h4>Agents</h4>Fresh <code>ctx.call_agent</code> calls with your system/user prompts, ordered messages, explicitly selected tools, skills and MCP. Optional output schemas validate JSON.</div>
<div class="sec-lane"><h4>Scripts</h4>Your Python functions implement custom tools, transformations and service calls. Errors stop the run unless your program handles them.</div>
<div class="sec-lane"><h4>Branches</h4>Ordinary Python conditions, loops and chaining. Pass agent results directly as values and return the desired JSON.</div>
</div>

External products invoke Relays through authenticated API function triggers.
Cron/calendar schedules, bot chats and Pulse are not supported. Python and
explicitly selected MCP tools can use authorized Google, Slack or other APIs.
The right pane shows Source and Calls. There is no agent resume or automatic
recovery in this MVP. Selected Vault secrets and provider connections remain
live; published Python source stays frozen. Existing graph Relays retain their
legacy runtime. See [the Python contract](../design/python_relays.md).

## API reference

All calls are authenticated the same way as the rest of the API.

**Start a run** — `POST /api/relays/{id}/runs`

```json
{
  "function": "greet",
  "input": { "name": "Ada" },
  "idempotency_key": "order-8842-attempt-1",
  "version": "v1"
}
```

`version` is optional and defaults to the active release. Returns `202`:

```json
{
  "run_id": "b28e48e9-…",
  "status": "running",
  "version": "v1",
  "duplicate": false,
  "poll_url": "/api/relays/wf_bb882752/runs/b28e48e9-…"
}
```

**Poll a run** — `GET /api/relays/{id}/runs/{run}`

Returns the run status while it works, and the output agent's JSON plus
`version` once complete. Unknown runs and other people's runs both answer
404 — the API never confirms what you may not see.

**List releases** — `GET /api/relays/{id}/releases`

Returns the active version and every published release with its content hash.

## Limits to know

- Snapshots hold UTF-8 text only (50 MiB / 5000 files). Binary assets are
  outside the release contract.
- A crashed run ends honestly as `interrupted` and stays pollable — but it
  never resumes mid-chain. Don't describe Relays as resumable.
- A release whose files change after publishing fails its checksum and stops
  serving until you republish. Runs should only write inside their run folder.

Related: [Security overview](../security/README.md),
[Sharing and slots](../security/sharing.md).

## Platform MCP authoring and execution

Relay tools are admitted by `agent_go/internal/relayproduct/product.yaml` under
`chat.builder.external_tools`. There is no Relay Run-chat mode.

| Tool | Purpose |
| --- | --- |
| `create_relay` | Create an owned draft, an object `INPUT`, a default variable group, and an enabled function trigger |
| `builder_chat` | Edit the graph, exact prompts, message sequences, per-step models, and custom Python tool source through the existing Builder |
| `builder_status`, `builder_reply_input`, `builder_cancel` | Poll, answer, or cancel that Builder operation |
| `update_relay` | Rename the draft or change its designated output agent |
| `test_relay` | Execute the draft with sample JSON input |
| `get_relay_releases`, `publish_relay` | Inspect or freeze immutable versions using the existing publisher |
| `run_relay` | Execute an explicit published version, or the active version when omitted |
| `get_relay_run` | Poll the durable run ID and retrieve status, final JSON, and step outputs |

Deployment must enable `AGENTWORKS_MCP_BUILDER_ENABLED=true` to expose authoring.
An OAuth client must explicitly request `relays:write workflows:read files:read
runs:execute`; Relay authoring is **not** included in default connection scopes.
PATs use the same scopes and workflow bounds. To create new Relays, the grant
must cover all accessible workflows (`all_workflows=true`), and the account must
have create rights and Relay product access. A selected-ID grant edits only the
selected Relays. `relays:write` does not authorize AgentWorks Builder editing.
Existing scoped `builder:chat` grants may edit/publish selected Relays when the
account also has Relay product access. Owner/editor access is checked live.
Read access plus `runs:execute` permits published execution, never draft testing.

Use `get_api_spec` to discover this connection's actual tools and schemas before
calling them. IDs come from `list_workflows` (inspect `manifest.kind=relay`) or
`create_relay`, never filesystem paths. For example, these are **call_tool**
arguments:

```json
{"name":"create_relay","arguments":{"label":"Hello","submission_id":"hello-create-1"}}
```

Creation returns `workflow_id`, an owned manifest, default output agent ID
`answer`, and function `process`. Send `builder_chat` a request to build that
agent with authored system/user prompts and JSON output; use a unique
`submission_id` and poll its `operation_id` with `builder_status`. Source-file
editing uses the shared managed file tools with revision checks and audit.
External Builder retains its existing restrictions on shell, account tools,
and connected-account access; execution uses the existing step sandbox.

```json
{"name":"test_relay","arguments":{"workflow_id":"<returned ID>","function":"process","input":{"name":"Asha"},"idempotency_key":"sample-1"}}
{"name":"publish_relay","arguments":{"workflow_id":"<returned ID>"}}
{"name":"run_relay","arguments":{"workflow_id":"<returned ID>","function":"process","version":"v1","input":{"name":"Asha"},"idempotency_key":"production-1"}}
{"name":"get_relay_run","arguments":{"workflow_id":"<returned ID>","run_id":"<returned run ID>"}}
```

Creation retries with the same grant/submission ID return the original Relay;
conflicting arguments fail. Creation reservations are in private auth state.
Run retries preserve the original input/version after republishing; conflicting
input fails. Draft-test keys are separate from published-run keys. Each response
returns a `run_id`; polling is caller-bound and follows current access grants.
Draft tests have an empty version; published results identify their frozen
version. Large outputs retain the existing artifact-link behavior.
