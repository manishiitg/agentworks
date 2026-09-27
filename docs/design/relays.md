# Relays: product idea and implementation plan

**Status:** proposal, 2026-09-27

## Product idea

Relays are reusable, user-authored graphs of agents, scripts, and decisions. A user builds a Relay in the left-side chat, inspects and tests it in the right pane, publishes an immutable version, and calls it from an external website or an existing product through an API trigger. Each call has a defined JSON input and one final, author-defined JSON output.

Relays are a separate product from goal-driven Workflows and continuing Crew conversations. A Relay run follows one path through a saved graph and ends. It has no goal, Pulse, self-improvement cycle, dashboard, or implicit conversational memory between runs. An agent node may use the managed `agent_browser` tool, but browser state belongs to one run and is removed when that run ends.

### Decisions for the first version

| Area | Decision |
| --- | --- |
| Graph | Start, agent, script, decision, and final output nodes. A decision selects one path. Branches may converge, but there are no parallel joins or loops. |
| Agent | Reuse the message-sequence executor. Each agent node has a user-authored system prompt and one or more user-authored message templates. |
| Script | Reuse the scripted step execution primitives for deterministic transformations. |
| User-created tools | An author can define Relay-scoped Python tools with a name, description, JSON argument contract, and JSON result contract, then allow selected agent nodes to call them. Tool definitions and `.py` source are frozen with a published version. |
| Browser | An author may enable managed `agent_browser` for selected agent nodes. A fresh headless browser/profile is owned by one run, may be shared by that run's enabled nodes, and is torn down at the run's terminal state. No browser profile is shared across Relay runs. |
| Decision | Route deterministically on a value from the trigger or an earlier node. Put model judgment in an agent node that produces that value. |
| Inputs | Each trigger supplies JSON mapped into the Relay's named input contract. Messages may reference `{{input.field}}` and `{{steps.node_id.output.field}}`. |
| Output | The HTTP response is always a JSON envelope. Its `output` value is the Relay author's final JSON value; an optional author-defined schema can constrain it. |
| Versions | Chat and direct edits change a draft. Publishing freezes the graph, prompts, scripts, model/tool permissions, and input/output contract. Triggers can pin a version or follow the latest published version. |
| API | A call returns a durable run ID immediately; an optional short wait returns the result when ready. Polling always works. Retry with the same idempotency key returns the same run. |
| Resume | Every accepted run remains durably recorded and reaches a result, a recoverable pause, or an explicit `needs_attention` state. The runtime never silently starts it over. See the resume contract below. |

### Author and caller experience

1. In chat, the author describes the graph, prompts, scripts, branching, trigger inputs, and final JSON shape. Chat changes the draft only.
2. **Plan** shows the graph and a node inspector. The inspector exposes the exact system prompt, ordered user messages, variable references, script, branch rule, model, permitted MCP/user-created tools, and optional browser access. The author can make precise edits there as well as through chat.
3. The author enters sample JSON in Plan and runs the draft. The graph highlights the selected path and shows each node's rendered input and output.
4. The author publishes a version. A trigger is bound to that version or to latest published.
5. A website backend or product calls the trigger. **Execution logs** show the run, its version, the chosen path, every node's input/output and tool calls, and the final JSON. The caller can poll the same run ID after a disconnect.

The right pane reuses the existing AgentWorks structure and names:

| View | Relay content | Existing UI starting point |
| --- | --- | --- |
| **Plan** | Graph, draft testing, node inspector, version selector and Publish | [WorkflowCanvas](../../frontend/src/components/workflow/canvas/WorkflowCanvas.tsx); extract the React Flow shell, graph controls, node/edge visuals, and trace highlighting. |
| **Triggers** | API/webhook endpoints, auth, enabled state, version binding, input mapping, request examples | [WorkflowAPITriggersView](../../frontend/src/components/workflow/WorkflowAPITriggersView.tsx) and [ProductAPITriggersView](../../frontend/src/components/workflow/ProductAPITriggersView.tsx); remove workflow route/group settings. |
| **Execution logs** | Run list and per-node transcript, input/output, selected route, browser actions/artifacts, retry/recovery state | [ExecutionLogsPopup](../../frontend/src/components/workflow/ExecutionLogsPopup.tsx); adapt data loading from workflow run folders to Relay run IDs. |
| **Integrations** | MCP connections, user-created tool definitions, per-node tool permissions, secrets, models, and browser access settings | Extract relevant controls from [WorkflowCapabilitiesPanel](../../frontend/src/components/workflow/WorkflowCapabilitiesPanel.tsx); omit persistent Browser, Slack/WhatsApp, and workflow-only controls. |

The reusable pieces are visual and execution primitives. Relay definitions and run state must have their own model; copying workflow `plan.json`, Pulse, iteration folders, or workshop lifecycle into Relays would recreate the complexity this product is meant to avoid.

## Maximum reuse through `product.yaml`

Use the repository's established `product.yaml` spelling (rather than a new `product.yml` loader). Add `agent_go/internal/relayproduct/product.yaml` and load it through `agentprofiles.LoadProductManifest`, as Crew and the other products do. This static manifest owns the **Relay builder chat**: product identity, builder system prompt file, project-scoped conversation, allowed builder tools, runtime policy, and UI surface. It does **not** store an individual Relay's graph or become the system prompt for an agent node. Those user-authored values live in immutable Relay versions.

The Relay profile should use `scope: project`, a keyed chat per Relay, `ui.surface: relays`, and only the shared features its **builder** needs: live chat, MCP selection, secrets, models, and workspace UI. The builder gets a small set of typed product tools to create/update a draft graph, edit a Python tool or script, test a draft, publish a version, and manage Relay triggers. Published version files are writable only through the version publisher, never through generic chat file tools. Do not give the builder a persistent browser, dashboard, Pulse, background work, or Crew's message-only `triggers` feature: that feature delivers a message to a continuing Crew chat, whereas a Relay trigger starts a versioned graph run. Reuse its underlying auth/delivery primitives through a Relay adapter instead. Browser access is a per-node **execution** setting from the Relay definition, not a persistent builder-profile feature.

Choose a transport whose tool allowlist is actually enforced for the builder. The [product.yaml design guide](../core/product_yaml_design_guide.md) records that native/tmux coding-CLI mode can run tools outside `mcpagent`'s allowlist; `structured` mode enforces the narrow product tool policy but may reduce streaming and live steering for some CLI providers. Pin the chosen provider/transport combination with a live tool-discovery test. Relay **execution agents** have their own per-node model, prompt, and tool policy from the published version; they do not inherit the builder profile's prompt or tools.

One shared-platform gap needs an explicit solution: today a registered custom tool is commonly reached through `get_api_spec` and `execute_shell_command` calling its `$MCP_CUSTOM` endpoint. Granting unrestricted shell merely to expose one Relay Python tool would defeat an exact per-node tool allowlist. Extend the shared tool bridge with a server-enforced, tool-call-only path (or an equivalently constrained shell policy) that both existing products and Relays can use. Verify on a real supported provider that an agent can call its allowed Python/MCP tools and cannot invoke an unselected tool or an arbitrary shell command. Until that passes, the product must not advertise exact tool isolation for that provider.

| Concern | Reuse | Relay-specific seam |
| --- | --- | --- |
| Product registration and builder chat | `agentprofiles` manifest loader, product profile registry, `ChatArea` with `inputVariant="product"`, `ProductChatSurface` | `relayproduct/product.yaml`, builder prompt, typed draft/publish/test tools. |
| Left/right layout | Product surface switcher, split rail, workspace toolbar, view headers | A thin `RelaySurface` composing the existing chat and Relay pane. |
| Plan | React Flow shell, canvas controls, node/edge visuals, route trace | Adapter from Relay graph definition and run path; Relay node inspector. |
| Agent node | Message-sequence executor, MCP session, provider continuation, event/logging primitives | Custom system prompt and authored message templates; durable turn cursor; no workflow synthetic turns. |
| Script and decision | Script executor and deterministic route resolution | Relay input/output adapter and one-path graph cursor. |
| Python tool | Existing agent custom-tool registration and tool-call events | Versioned user script loader, isolated JSON stdin/stdout runner, per-node allowlist, effect journal. |
| Browser | Managed `agent_browser` tool, session tracker, and cleanup primitives | Run-ID-owned headless session/profile, per-node enablement, terminal cleanup, and recovery of an interrupted run. |
| Triggers | Existing webhook authentication, encrypted secrets, idempotency, and status-polling primitives | Binding to a Relay version, input mapping, final JSON result endpoint. |
| Execution logs | Shared log rows, tool-call display, step detail, cost display | Query by Relay run ID and show durable checkpoints/recovery state. |
| Resume | `mcpagent` provider-neutral session handles and existing continuation behavior | Relay run ledger, leases/fencing, node/turn/effect checkpoints, startup recovery. |

Extract shared components and services at these seams, then let both Workflows/Crew and Relays call them. Keep the Relay orchestration layer small: resolve a frozen graph, run one node, commit its output and next edge, and repeat. Do not invoke the full workflow controller merely to reach its message-sequence or scripted executors; that controller also owns workflow folders, run iterations, validation, learning, and Pulse behavior. Where an executor is too coupled to the workflow controller, extract the executor behind a shared interface and leave a compatibility adapter for existing workflows.

## Definition and API contracts

### Definition storage

Proposed canonical layout:

```text
Relays/<relay-id>/draft/definition.json
Relays/<relay-id>/draft/code/<script-node-id>/main.py
Relays/<relay-id>/draft/tools/<tool-id>/main.py
Relays/<relay-id>/versions/v<N>/definition.json
Relays/<relay-id>/versions/v<N>/code/<script-node-id>/main.py
Relays/<relay-id>/versions/v<N>/tools/<tool-id>/main.py
Relays/<relay-id>/runs/<run-id>/artifacts/...
```

Publishing copies a complete draft snapshot into a new, immutable version and records a content hash. A draft test also snapshots its draft revision, so its historical trace does not change after later edits. Trigger bindings and encrypted credentials live separately from versions; a run snapshots the resolved trigger mapping and version at acceptance. Secret **references** may be versioned, but secret values are never written into definitions or logs.

The definition contains stable node IDs, directed edges, entry and final output nodes, input fields, prompt/message templates, script and user-created tool references, decision cases and fallback, model and tool selections, per-node `agent_browser` enablement, optional output schema, and execution limits. Structural checks on save/publish reject dangling edges, unreachable output, cycles, invalid variable references, and unsupported node settings. These checks do not launch a model or run an automatic prevalidation/repair agent.

### User-created tools

An author creates a tool in chat or Integrations and assigns it to one or more agent nodes. A tool has a stable ID, display name, agent-facing description, JSON Schema arguments, optional JSON Schema result, a Python `main.py`, timeout, selected secrets, and an effect policy (`read_only`, `idempotent`, `reconcilable`, or `unknown`). The node's explicit allowlist determines whether the agent can discover and call it. Existing MCP tools remain selectable beside these user-created Python tools.

The tool runner validates the model's JSON arguments, starts the version-pinned Python script in an isolated process, passes one JSON object on standard input, and requires one JSON value on standard output. Standard error is diagnostic output; a nonzero exit, timeout, or invalid JSON is a tool error returned to the agent and recorded in the run. The runner supplies stable run/tool/operation IDs and only the explicitly selected secrets and capabilities; it grants no browser access. This is an on-demand tool call: the agent may call it zero or more times during its message sequence. A scripted graph node instead executes when the graph reaches that node.

This is distinct from the current `enabled_custom_tools` step setting, which selects platform-registered tool categories; it does not itself provide a user-authored tool definition or executor. Register a published Relay tool with the agent's existing runtime tool mechanism, but load its definition and code from the frozen Relay version. Do not grant it implicit access to every MCP server or secret. Record each invocation's validated arguments, result or error, duration, and effect operation ID in Execution logs, with secret redaction.

### Variable resolution

The trigger body becomes `input`. An agent message may refer to that input or a completed earlier node, for example:

```text
Classify ticket {{input.ticket.id}}:
{{input.ticket.body}}

Use the account category also {{steps.lookup_account.output.category}}.
Return JSON with category and reason.
```

Only nodes on the selected path have outputs. A reference to a missing field, skipped node, or incompatible type is a named runtime error, never an empty substitution. The Test view previews the rendered message. Input data belongs in user messages; the authored system prompt is static within a published version. Secrets are exposed to authorized tools, not interpolated into prompts or logs.

### Trigger and result API

Proposed endpoints, using the existing trigger authentication and idempotency patterns where possible:

```text
POST /api/relays/{relay_id}/runs
GET  /api/relays/{relay_id}/runs/{run_id}
POST /api/relays/{relay_id}/runs/{run_id}/resume
POST /api/relays/{relay_id}/runs/{run_id}/effects/{effect_id}/resolve
```

`POST` accepts `{ "input": { ... } }`, a trigger credential, and an optional `Idempotency-Key`. It commits the run and input before returning `202 { "run_id": "...", "status_url": "..." }`. An optional bounded wait may return `200` with the finished result; a timeout still returns the run ID. A terminal result has a stable envelope such as:

```json
{
  "run_id": "run_123",
  "version": "v3",
  "status": "completed",
  "output": { "category": "billing", "priority": "high" }
}
```

The author controls the JSON value under `output`; the platform owns the run metadata and error envelope. If the final value is not valid JSON or violates the optional output schema, the run fails visibly. There is no silent text wrapping or automatic model repair. Direct API calls supply the input contract as JSON. A provider webhook can instead map its raw payload into the same contract. Trigger credentials are scoped to a Relay and can be rotated or disabled. External websites call from their backend so credentials are not exposed in browser code.

## The "100% resume" contract

**Product promise:** Once the API acknowledges a run, that run and its original input/version remain recoverable after an app, worker, or host process restart, assuming its durable storage survives. A recovered run keeps the same run ID. Completed nodes are not repeated. The run either completes, stays paused for a resolvable dependency, or reports an explicit reason that an external effect needs attention. Neither the server nor the client has to guess whether a new run was created.

This is a guarantee of **durable, safe recovery**, not a claim that arbitrary external actions execute exactly once. An MCP tool or script can complete an outward action just before the process dies and before it reports success. If that service cannot deduplicate or report the action's status, no runner can prove whether replay is safe. The Relay must stop at `needs_attention` rather than silently perform the action again. The API and UI must expose the effect, evidence, and available choices. Automatic continuation is guaranteed only across effects that can be reconciled or repeated safely.

The current [standalone message-sequence executor](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go) does **not** meet this contract: it treats `session.json` as an observation log and abandons an interrupted run. Reusing its execution loop therefore requires new durable turn checkpoints and restart recovery. Existing provider-neutral `AgentSessionHandle` continuation and workflow `continuation_state.json` provide building blocks, not a finished Relay resume implementation. See [Coding Agent Continuation Architecture](../core/coding_agent_continuation_architecture.md).

### Durable run state

Use a transactional run ledger (the existing server database if suitable) as the authority for `relay_runs`, `relay_node_attempts`, `relay_turns`, `relay_effects`, and append-only `relay_events`. Keep large artifacts on disk with atomic write/rename and record their hashes in the ledger. A run records:

- Relay ID, immutable version ID and hash, trigger ID and resolved mapping, original input, and idempotency key.
- Status and current node/turn, selected decision path, attempt numbers, timestamps, cancellation/pause reason, and lease fencing token.
- Each completed node's exact output and each agent turn's rendered user message, conversation checkpoint, and provider-neutral session handle.
- Each external effect's stable operation ID, tool identity, arguments hash, start/result state, and reconciliation evidence. Secret values and credentials are excluded.

The node state machine is `pending -> running -> completed`; exceptions move to `retryable`, `paused`, `needs_attention`, or `failed`. User **Pause** preserves the checkpoint; **Cancel** is a terminal request. The run result is committed once and then remains stable for polling.

### Checkpoint boundaries and recovery

1. **Accept:** In one durable transaction, resolve the published version and trigger mapping, enforce request/idempotency rules, persist input and a queued run, then acknowledge it. A repeat request with the same trigger and idempotency key returns the existing run ID.
2. **Claim:** A worker obtains a time-limited lease with a monotonically increasing fencing token. Every state write checks that token. Another worker can claim an expired lease without both workers committing results.
3. **Before a node/turn:** Persist the planned node, attempt ID, fully rendered input, and intended tool/effect policy before invoking the agent or script.
4. **After a turn:** Persist its conversation, output, and refreshed provider session handle as one logical checkpoint. Native coding-agent continuation is used where supported; API model sessions use the persisted conversation. A partial streamed response is diagnostic evidence, not a committed turn.
5. **After a node:** Atomically commit the node output, selected decision, and next cursor. Never rerun a completed node on recovery.
6. **Restart:** Scan queued/running runs with expired leases. Restore their frozen version and checkpoint. Reattach to a live provider session if possible; otherwise use the saved handle/history. Reconcile any in-flight external effect before continuing or replaying its turn. If continuation cannot be established safely, move to `needs_attention` with a clear reason.
7. **Finish:** Persist final JSON and terminal status before emitting a response or callback. A lost HTTP connection does not lose the result; polling returns the same document.

The execution engine must not infer progress from log text, process IDs, in-memory goroutines, or file timestamps. Those may help diagnostics, but the run ledger owns the cursor. Startup recovery and a periodic lease sweeper use the same recovery path. Resume is idempotent and safe under concurrent requests.

### Run-scoped browser lifecycle

Selected agent nodes receive the managed `agent_browser` tool in headless mode. Give each run a unique browser session and temporary profile, never the persistent browser or user Chrome/CDP profile used by Crew and Workflows. Browser-enabled nodes in the same live run may share that session; other runs cannot. Close the session and remove its profile on completion, failure, cancellation, or retention cleanup. Startup recovery and the idle reaper also clean abandoned sessions, using run ownership rather than a shared workflow identity.

Retain the temporary profile only while the run is active or paused so a server restart can reopen it. Checkpoint the run's browser ownership, current URL/tab information, and action results alongside node/turn state. Reopening a profile may restore cookies and storage, but it does not prove that arbitrary in-page JavaScript state survived. On recovery, re-observe the page before continuing; restart the interrupted browser turn from its last durable checkpoint when safe. Navigation and snapshots can usually be repeated. A click, form submission, purchase, post, or similar action is an external effect: journal it before dispatch and reconcile an uncertain outcome before retrying. If the site cannot confirm the action, use `needs_attention` on the same run ID. No browser session or login state carries into a later Relay run.

### External effects and scripts

For a tool call that may change external state, including a user-created tool, write an effect record **before** dispatch. Pass its stable operation ID as an idempotency key when the tool/provider supports one. On restart, query the provider or tool's operation status when possible. Commit the observed result before advancing the turn. Unknown tools default to `needs_attention` if interrupted after dispatch. A Python tool is `read_only` only when that is enforced by its runtime permissions; it is `idempotent` or `reconcilable` only when its external operation contract supports that claim. An author-selected label alone never makes replay safe.

Resolving `needs_attention` is part of resume, not a new run. Execution logs show the effect's recorded request and any reconciliation evidence. An authorized user can record that the effect succeeded (including its observed result), confirm it did not occur and retry it, or fail the run. The resolution and actor are audited, then the same run ID continues from its checkpoint. The API exposes the same action for an external operator. The runtime never treats lack of evidence as proof that an effect did not occur.

Script nodes receive the same run/node/attempt identifiers. Pure scripts may rerun. Scripts that write to external systems must use an idempotent API or a Relay effect helper that journals and reconciles the action. An arbitrary shell command or third-party MCP tool with unobservable side effects cannot be promised automatic replay; Plan should show its resume-safety status before publication. This policy is separate from model prevalidation.

## Implementation plan

### 1. Register the Relay product and extract shared seams

- Add `relayproduct/product.yaml`, its builder prompt, manifest loader/validator, project-scoped profile registration, and typed builder tools using the shared `agentprofiles` pattern. Pin excluded features and allowed tools with manifest tests.
- Add a thin Relay product surface using the shared product chat and split-pane primitives. Extract graph, trigger, log, and integration view pieces only where both existing products and Relays can consume them.
- Establish separate builder and run permissions: builder tools mutate drafts or publish through typed APIs; runtime agent nodes see only their published per-node tools and cannot edit definitions.
- Acceptance: a fresh Relay builder chat resolves the declared prompt and tool set; denied tools are absent in a live provider test; no Relay runtime agent receives the builder prompt or authoring tools.

### 2. Define the Relay contract and storage

- Add a Relay definition schema, graph structural validator, immutable version publisher, draft revision snapshots, and input/output schema handling.
- Add a transactional run ledger with idempotency uniqueness, node/turn/effect records, artifact hashes, leases, and fencing tokens.
- Define the persisted state machine and migration/retention policy before wiring API traffic. Preserve terminal status and final JSON longer than optional bulky artifacts.
- Acceptance: concurrent publication cannot mutate an existing version; the same trigger/idempotency key cannot produce two runs; a run is queryable immediately after its `202` response.

### 3. Adapt the agent, script, and decision runtimes

- Add a custom-prompt mode to the message-sequence agent path. In Relay mode, send the authored system prompt in the supported provider's system role or equivalent and the authored messages in order. Bypass the workflow execution-only prompt template and opening user-message envelope. Show the effective prompt in logs; do not claim full prompt control for a provider that cannot supply it.
- Disable message-sequence synthetic validation, learning/KB closing turns, workflow goal context, and automatic fallback message for Relay nodes. Keep its useful session, MCP/tool, logging, stop, and cost primitives.
- Run scripts through the existing scripted executor with explicit input/output paths and a Relay-scoped execution directory. Run decisions through a deterministic value switch. Reject cycles and unsupported step types in v1.
- Add a versioned Python-tool loader and isolated process runner that registers only node-allowed tools with the agent runtime. Validate JSON stdin/stdout against the declared contracts, scope secrets and MCP access, and apply time and resource limits. Do not treat platform `enabled_custom_tools` as a user-tool authoring feature.
- Use a shared, server-enforced custom-tool invocation path so exposing a Python tool does not also expose an unrestricted shell. Exercise the actual provider transport, not only the registration list.
- Adapt the managed `agent_browser` runtime for run-ID-owned headless sessions: register only on browser-enabled agent nodes, retain a temporary profile only for an active/paused run, and tear it down at terminal state. Do not route Relays through a workflow's shared browser or a user's CDP profile.
- Acceptance: each agent receives exactly the authored messages plus explicitly selected inputs and tools; no workflow-only turns appear; a graph follows one and only one path and returns the declared final JSON. An unauthorized node cannot discover or invoke another node's user-created tool.

### 4. Add durable turn and node continuation

- Extend or extract message-sequence execution so its turn queue is driven by the Relay ledger rather than only in-memory state and `session.json`. Persist a turn cursor and provider-neutral `AgentSessionHandle` after every completed turn.
- Restore the current node from the frozen version and checkpoint after restart. Reuse `mcpagent` continuation for providers that support it, and persisted history for API models. Surface typed `non_continuable` and `stale_handle` outcomes instead of guessing.
- Add worker leases, fencing, startup scan, and periodic recovery. Mark uncertain in-flight work `needs_attention` until reconciled.
- Acceptance: kill the server before/after every turn and node commit; recovery retains the run ID and version, does not rerun committed nodes, and either completes or names the exact unresolved effect.

### 5. Make effects safe to resume

- Introduce a shared effect journal around Relay MCP calls, user-created tools, browser actions, and side-effecting scripts; classify tools as read-only, idempotent/reconcilable, or unknown. Add an audited `resolve-effect` action that continues the same run after an uncertain effect is reconciled.
- Propagate stable operation IDs to supported integrations and add status reconciliation adapters. Gate interrupted unknown effects at `needs_attention` with UI and API details for manual reconciliation.
- Acceptance: crash after an external action but before its response cannot silently issue a duplicate action. An idempotent action completes automatically with one externally observed effect.

### 6. Build triggers and reuse the right pane

- Add Relay trigger management and run/result endpoints using existing webhook bearer/auth, secret rotation, status polling, and idempotency patterns where they fit. A trigger binds to a published version or latest-published pointer and maps payload fields into the Relay input contract.
- Reuse the workspace shell and named Plan, Triggers, Execution logs, and Integrations views. Extract shared visual controls from workflow components instead of making Relay state masquerade as workflow state.
- Add draft test execution to Plan; show resolved prompts, selected path, final JSON, version, run recovery state, and Resume/Pause/Cancel actions in Execution logs.
- Acceptance: a website can send an input, lose its connection, retry with the same key, and retrieve the same final JSON; the UI can explain and safely resume a paused run.

### 7. Prove the resume contract before release

Use fault injection and process-kill integration tests at acceptance, lease claim, prompt rendering, agent turn dispatch, tool dispatch, tool success before acknowledgment, browser action dispatch, node output commit, branch selection, final output commit, and HTTP response. Test two workers racing after a lease expires, trigger retries, key rotation, app restart, provider session loss, browser profile reopening/cleanup, and a draft being edited while a published run executes.

Required assertions:

- Every acknowledged run remains discoverable with the same input, version, run ID, and status after restart.
- A completed node is never rerun; a decision keeps its committed route.
- A recoverable in-flight agent resumes from the last durable turn; an unavailable provider handle becomes an explicit recoverable or `needs_attention` state.
- An idempotent external effect occurs once despite retry and restart. An unknown external effect is never replayed automatically.
- A user-created tool is available only to its assigned agent nodes, receives only its selected secrets/capabilities, and produces a durable invocation record that survives restart.
- A Python tool's invalid arguments, nonzero exit, timeout, and invalid JSON result are reported as distinct failures; a crash after its external effect follows the same journal and reconciliation rules as an MCP call.
- A `needs_attention` effect can be resolved with evidence and the original run resumes from the correct checkpoint; a duplicate resolution cannot advance it twice.
- Final output is stable JSON, and polling/repeated requests return the same result.
- No Relay run invokes Pulse, workflow validation/learning turns, a persistent/shared browser, or a hidden workflow system prompt. Only browser-enabled nodes can invoke `agent_browser`; an interrupted browser submission is reconciled or paused before replay.

Do not ship the external trigger API with a best-effort resume label. The release gate is the fault-injection suite plus a live end-to-end restart test for each supported agent provider class and at least one side-effecting MCP integration.
