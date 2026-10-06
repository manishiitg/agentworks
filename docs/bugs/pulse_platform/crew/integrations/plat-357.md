[← crew / integrations](index.md)

# PLAT-357 — Crew functions: typed calls between Crews and workflows

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | crew |
| Area | integrations |
| Summary | adds typed functions a Crew or workflow declares (`functions.json`: input schema, result schema, instructions). |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `one call model (functions + per-caller conversations) on main; not deployed; live RTS check pending` |
| Last synchronized | `2026-09-24` |
| Priority | `P1 product` |
| Category | integrations (runner-up: scheduler-runs) |

## 2026-09-24 — one call model

Crew-to-Crew calling had grown five overlapping ideas: `crew_chat` vs
`isolated` run destinations, fresh-per-call isolated trigger chats, free-form
`call_target`, typed functions and the default `ask`. They are now one model
(user doc: [docs/crew-calls.md](../../../../crew-calls.md)):

- **One way to call:** functions. `ask` covers free-form tasks; typed
  functions add validated inputs and results. `connect_to_target`,
  `call_target`, `get_target_run` and `send_to_target_run` are removed;
  `ask_function_update` carries mid-run follow-ups.
- **One place calls run:** each caller's own continuing conversation with the
  target Crew. An internal trigger (caller binding) always runs there
  (`productWebhookTrigger.ownConversation`), whatever `run_destination` it
  was saved with, and it is keyed by trigger ID only. The per-run
  `triggerID:runID` suffix (`isolatedAutomationID`) is removed, so a caller's
  follow-up calls remember earlier ones. The Crew's main chat is for people.
- **External webhooks and schedules** keep the main chat / own conversation
  choice. A webhook's own conversation now also continues across deliveries
  instead of starting fresh each time.
- **UI:** the Triggers tab is now **Webhooks** (external only), and caller
  bindings are listed under **Functions → Callers** with Disconnect.
  Destination labels read "Main chat" / "Own conversation".
- **MCP/CLI:** each AgentWorks user's `ask_crew` calls continue one
  conversation with that Crew, so an external tool can chat with a Crew.

Migration: none needed. Existing caller bindings saved as `crew_chat` are
routed to their own conversation at dispatch. Old per-run isolated trigger
chats stay in history. Deferred: an optional one-line summary of each
finished call in the Crew's main chat.

## 2026-09-24 — workflow functions are function triggers

Incident: the SDE Crew asked the PR-review gate workflow to review #149 with a
free-text `ask`. A free-text call could not set `GITHUB_OWNER` /
`GITHUB_REPO` / `PR_NUMBER`, so the gate used the saved `PR_NUMBER=147`, found
it already merged, skipped, and reported success.

Fix:
- **Workflows no longer have `ask`.** They offer only function triggers
  (`kind: "function"`): a fixed route and groups plus
  `function: {name, description, inputs, allowed_callers}`.
- **Inputs are declared variables**, typed (`string`, `integer`, `number`,
  `boolean`), required or optional, optionally with an enum. They are set as
  that run's variables (`WorkflowWebhookDelivery.Variables` and `Group`).
- **Checked before anything runs:** a missing required input, an unknown or
  mistyped input, or a bad group is refused
  (`workflowFunctionArgs`, `dispatchWorkflowFunction`).
- **Auth:** no URL and no secret. The server stamps the caller. A Crew or
  workflow chat needs a user with edit access to the workflow; MCP/CLI needs
  `runs:execute` and the token's workflow bound. `allowed_callers` narrows
  this further.
- **Result:** the run's outcome (status, error, steps) returns through the
  normal function call: directly, as an auto-notification, or by polling.
- **Surfaces:**
  - Builder: `manage_workflow_webhook kind=function`;
  - Crews and workflows: `list_functions` / `call_function` (and generated
    tools);
  - MCP: `list_workflow_functions` / `call_workflow_function` /
    `get_workflow_function_call`;
  - CLI: `agentworks functions`;
  - UI: a Functions section on the workflow's Webhooks tab.
- `define_function` / `delete_function` on a workflow now refuse and point to
  its Builder.

Not live-verified yet: needs a real function trigger on a workflow and one
real call.

## Problem

Crews and workflows can already call each other through secretless internal
triggers (`connect_to_target` / `call_target` / `get_target_run` /
`send_to_target_run`, f7130b4f8). A trigger call is a free-form task string
and the answer is whatever prose the target ends with. The caller cannot see
what a target offers, cannot rely on the shape of the answer, cannot get a
quick answer inside its own turn, and cannot ask a long call how it is going
without interrupting it.

## Design

**Functions.** A Crew or workflow declares named functions in
`<target>/functions.json`:

```json
{"version":1,"functions":[{
  "name":"run_login_flow",
  "description":"Run the RTS login flow against a build",
  "input_schema":{"type":"object","required":["build"],"properties":{
     "build":{"type":"string"},"env":{"type":"string","enum":["staging","prod"]}}},
  "result_schema":{"type":"object","required":["passed"],"properties":{
     "passed":{"type":"boolean"},"failed_step":{"type":"integer"},"trace_path":{"type":"string"}}},
  "instructions":"Run tests/login_flow.py with BUILD=<build> ...",
  "created_by":"crew:alpha (Alpha Bot)", "created_at":"...", "updated_at":"..."}]}
```

Schemas are a JSON Schema subset: `object` (properties, required),
`array` (items), `string`, `number`, `integer`, `boolean`, `enum`. A
function is managed like a trigger (`define_function`, `delete_function`,
`list_functions`); any Crew may define functions on any Crew (Crews are
shared read-write server-wide); workflows need owner/editor access.
Creation records the creator.

**Calling.** `call_function(target, function, args)` validates `args`
against the input schema, then dispatches through the target's standard
internal trigger binding (the same durable run and turn queue as
`call_target`: idle → now, busy → after the current turn). A Crew target
receives the function's instructions, the validated arguments, a `call_id`
and a result contract: it must end by calling
`return_function_result(call_id, result)`, which is validated against the
result schema (an invalid result is rejected with the validation error so
the target can fix it; after two rejections the call fails). A Crew run
that ends without a result gets one retry turn, then the call fails with
its final text as the reason. A workflow target runs its plan with the
arguments in the payload; its result is the run's outcome (step outputs),
not schema-validated in this slice.

- **Fast path:** if the call completes within 120 s, `call_function`
  returns the result directly as its tool result.
- **Slow path:** it returns `{status:"running", call_id}` and, unless
  `notify=false`, the caller's chat is resumed with an `[AUTO-NOTIFICATION]`
  carrying the result, failure or timeout (the same background-execution
  pipeline as `trigger_and_auto_notify` / `call_target`).

**Default `ask`.** Every Crew and workflow implicitly offers
`ask(message: string) -> {answer: string}` over its standard inbound trigger,
so a Crew with no declared functions is still callable (listed by
`list_functions`, callable via `call_function`, generated as `<crew>__ask`).
Its result is the target's final free-text reply; `return_function_result`
is optional. A declared `ask` replaces it. Targets are told to suggest a
typed function when the same ask keeps arriving.

**Generated tools.** For Crews and workflows tagged (`#crew:` /
`#workflow:`) or attached to the calling Crew, each function is also
registered as its own tool, e.g. `rts_flow_tester__run_login_flow(build,
env)`, with its input schema as parameters. Any other function stays
reachable through `list_functions` + `call_function`.

**Progress and updates mid-run.**

- `report_function_progress(call_id, message, percent?)` — the target
  reports milestones (it is told to in the call instructions); the latest
  10 entries are kept on the call record.
- `get_function_call(call_id)` — status, result/error, latest progress,
  and a short tail of the target's recent activity (last assistant text,
  current tool) read from its session events, without interrupting it.
  `get_target_run` (plain trigger calls) returns the same activity tail.
- `ask_function_update(call_id, question)` — Crew targets only: delivers a
  question into the target's running turn through the existing
  `send_to_target_run` live-input path; the target answers with
  `report_function_progress`, which the caller sees on its next poll.

**Guards.** No self-calls. A call chain is propagated through in-flight
calls: a target already in the chain is refused (cycle), depth is capped at
4, and one chain may make at most 20 calls. `return_function_result` and
`report_function_progress` are accepted only from the call's own target.

**UI.** The Crew's Automation panel has a **Functions** tab next to
Schedules and Triggers (`CrewFunctionsView`): each function with its
inputs, returns, creator and last update (the built-in `ask` marked "Built
in"), and **Recent calls** (caller, status, latest progress; expandable to
the progress log and the result or error). The owner can remove a
function; "Edit in chat" sends a guided request to the Crew chat. Backed by
`GET /api/crew-functions` and `DELETE /api/crew-functions/{name}`
(owner-scoped like `/api/product-webhooks`).

## Not in this slice

- Call records live in memory (with a JSON copy under
  `<target>/functions/calls/`); the auto-notification watch does not
  survive a server restart (same as `call_target`), `get_function_call`
  falls back to the saved copy.
- Workflow results are not schema-validated, and workflows have no
  Functions tab yet (the tab and endpoint are Crew-only).
- "Recent calls" shows calls made since the server started (in-memory).

## Register notes

[PLAT-357](plat-357.md) adds typed functions a
Crew or workflow declares (`functions.json`: input schema, result schema,
instructions). Other Crews and workflows call them with `call_function` or a
generated per-function tool; arguments and results are validated, a call
that finishes within 120 s returns its result directly, longer calls come
back as an `[AUTO-NOTIFICATION]`, and callers can poll progress or ask a
Crew target for an update mid-run. Built on the existing internal-trigger
bindings. Since 2026-09-24 functions are the only way to call a Crew (the
`*_target` tools are removed): every caller, whether a Crew, a workflow or
MCP/CLI, gets one continuing conversation with each Crew it calls, never its
main chat. The Triggers tab is now Webhooks (external only), and callers are
listed under Functions → Callers. Not yet deployed.
