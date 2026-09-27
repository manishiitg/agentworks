# Relays: product idea and implementation plan

**Status:** proposal, 2026-09-27

## Product idea

Relays are reusable, user-authored graphs of agents, scripts, and decisions. A user builds a Relay in the left-side chat, inspects and tests it in the right pane, publishes an immutable version, and calls it from an external website or an existing product through an API trigger. Each call has a defined JSON input and one final, author-defined JSON output.

Relays are a separate **product experience** from goal-driven Workflows and continuing Crew conversations. The proposed implementation is a Relay kind of workflow, using the existing workflow identity, access, storage, function-call, and execution infrastructure with Relay-only behavior selected explicitly. A Relay run follows one path through a saved graph and ends. It has no goal, Pulse, self-improvement cycle, dashboard, or implicit conversational memory between runs. An agent node may use the managed `agent_browser` tool, but browser state belongs to one run and is removed when that run ends.

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
| Resume | Every accepted run remains durably recorded and reaches a result or an explicit `needs_attention` state. V1 checkpoints completed nodes; turn-level automatic continuation is later work. See the staged resume contract below. |

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

The Relay view is a distinct UI over a workflow-backed object. Its graph, versions, and result contract need Relay-specific fields, but existing workflow identity, access, run history, functions, and storage should remain authoritative. The Relay execution path must switch off goal, Pulse, learning, validation turns, and workshop behavior at explicit mode boundaries rather than copy those lifecycles into Relay code.

## Architecture choice: workflow kind or separate store

| Option | Reuse and cost | Risk |
| --- | --- | --- |
| **Relay kind of workflow (recommended)** | Reuse workflow ownership, ACLs, path guards, secret binding, function calls, delivery IDs, trigger credentials, run discovery, history, CLI and cost accounting. Add a Relay mode, immutable version reference, exact prompt mode, JSON output contract, and run-scoped browser. | Existing workflow controllers may assume goal, Pulse, validation, or mutable `plan.json`; mode boundaries require targeted refactoring and tests. |
| Separate `Relays/` root and runner | Clean domain model and fewer workflow flags inside the Relay runner. | Rebuild or adapt access, proxy/path policy, secrets, trigger/API delivery, active work, costs, backup, CLI, and recovery. This conflicts with the requested maximum reuse. |

Start with the workflow-backed option. A Relay is identified as a Relay in its manifest and UI, while its published graph is a frozen version referenced by a run. Reuse [workflow function triggers](../../agent_go/cmd/server/workflow_function_triggers.go) and the existing call/poll path. Extend their current primitive typed inputs to accept the Relay JSON input contract; do not claim arbitrary JSON object input is already supported. Retain the existing workflow path so workspace tokens, proxy path policy, Folder Guard, encrypted workflow secrets, access lists, and backup/history can apply through their established paths. Any code path that branches on product type must be audited rather than assumed to inherit those rules automatically.

This choice is a v1 implementation direction, not a mandate to run the full goal-driven controller. A spike must prove that Relay mode can call message-sequence, scripted, and route primitives without invoking Pulse, workshop, or hidden workflow prompt behavior. If that boundary cannot be made reliable, return to this comparison with measured extraction cost before creating a new top-level store.

### Reuse rule for implementation

The default is to **extend the existing path**, with a `relay` mode at its boundary. An implementation PR should name the existing entry point it extends and show a compatibility test for ordinary workflows. A new Relay-only store, scheduler, trigger service, ACL, execution engine, graph schema, log system, or parallel React view requires evidence that the existing component cannot support the behavior after a small shared refactor. `product.yaml` selects the builder experience; it must not become a second execution platform.

| Need | Existing path to extend | Small new behavior |
| --- | --- | --- |
| Graph and draft edits | `planning/plan.json`, `step_config.json`, workflow builder plan tools and canvas | Relay mode accepts only message-sequence, scripted, and route steps; exact prompts and final JSON contract. |
| Publish and version | Workflow plan revision/history and backup | Freeze a revision plus referenced code/tool artifacts; pin each call to its hash. |
| Start and poll | Workflow functions, `DeliveryID`, trigger auth, `call_workflow_function`, `get_workflow_function_call` | Accept JSON object inputs and expose the authored JSON result. |
| Execute | Existing message-sequence, scripted, and deterministic route executors | Relay mode omits goal, Pulse, synthetic turns, and default workflow prompt. |
| Run state and resume | Workflow run ID/history and current continuation state | Node checkpoint and `needs_attention` state in the same run record. |
| UI and operations | Product chat/split pane, Plan, Triggers, Execution logs, Integrations, Active Work, costs, CLI, access and secrets | Relay labels, node prompt inspector, version selector, and recovery actions. |
| Python tools and browser | Shared custom-tool bridge/sandbox and managed `agent_browser` | A shared Python tool runner and tool-call-only bridge path; browser ownership scoped to a run. |

The last row is genuine platform work because user-authored Python tools and run-scoped browser ownership are not supplied by the current primitives. Build those as shared capabilities usable by AgentWorks and Crew. Do not create Relay-only copies. This reuse rule is a release gate: a Relay that works by duplicating core subsystems has not met the product requirement.

### Access, security, and shared product surfaces

A Relay inherits the workflow workspace's owner/reader model, read-only account behavior, and product visibility toggle. Integrate co-owner/private behavior as the shared access work lands; do not invent a separate Relay ACL. Review every API, builder tool, trigger, and run-log route with owner, co-owner, reader, and unrelated-user cases; a trigger credential authorizes a scoped invocation, not definition editing or unrestricted run inspection. Apply the existing workspace-token and proxy path policy to the Relay paths, including draft code and immutable version blobs. Never introduce an unguarded top-level `Relays/` prefix.

Execution agents need the same per-session bridge tokens, Folder Guard, and sandbox policy as other coding-CLI sessions. Run Python tools through the existing `workspace/security` isolator with a narrower filesystem and secret grant. Landlock controls filesystem access, not arbitrary network side effects; the open `/tmp` read exposure for bridge configuration and the tmux socket must be closed or explicitly blocked for Relay execution before exposing untrusted user scripts. A script marked pure must have no direct network or external write capability, enforced by the runner rather than by its label.

Relay calls should appear in Active Work and Ctrl+K with a “Called by …” origin, and feed the existing cost ledger, usage hover, run history, backup/export, external MCP selection, and `agentworks` CLI. Add documentation and a QA ticket for the new surface. These are acceptance requirements for the workflow-backed choice, not optional polish after launch.

## Maximum reuse through `product.yaml`

Use the repository's established `product.yaml` spelling (rather than a new `product.yml` loader). Add `agent_go/internal/relayproduct/product.yaml` and load it through `agentprofiles.LoadProductManifest`, as Crew and the other products do. This static manifest owns the **Relay builder chat**: product identity, builder system prompt file, project-scoped conversation, allowed builder tools, runtime policy, and UI surface. It does **not** store an individual Relay's graph or become the system prompt for an agent node. Those user-authored values live in immutable Relay versions.

The Relay profile should use `scope: project`, a keyed chat per Relay, `ui.surface: relays`, and only the shared features its **builder** needs: live chat, MCP selection, secrets, models, and workspace UI. Reuse the workflow builder's typed plan-editing tools with Relay-mode field validation; add only missing publish/test and Python-tool authoring operations. Published revisions are writable only through the version publisher, never through generic chat file tools. Do not give the builder a persistent browser, dashboard, Pulse, or background work. Crew's message-only `triggers` feature does not start a graph run; use the existing workflow function trigger/call path for Relay invocations. Browser access is a per-node **execution** setting from the saved plan, not a persistent builder-profile feature.

Choose a transport whose tool allowlist is actually enforced for the builder. The [product.yaml design guide](../core/product_yaml_design_guide.md) records that native/tmux coding-CLI mode can run tools outside `mcpagent`'s allowlist; `structured` mode enforces the narrow product tool policy but may reduce streaming and live steering for some CLI providers. Pin the chosen provider/transport combination with a live tool-discovery test. Relay **execution agents** have their own per-node model, prompt, and tool policy from the published version; they do not inherit the builder profile's prompt or tools.

One shared-platform gap needs an explicit solution: today a registered custom tool is commonly reached through `get_api_spec` and `execute_shell_command` calling its `$MCP_CUSTOM` endpoint. Granting unrestricted shell merely to expose one Relay Python tool would defeat an exact per-node tool allowlist. Extend the shared tool bridge with a server-enforced, tool-call-only path (or an equivalently constrained shell policy) that both existing products and Relays can use. Verify on a real supported provider that an agent can call its allowed Python/MCP tools and cannot invoke an unselected tool or an arbitrary shell command. Until that passes, the product must not advertise exact tool isolation for that provider.

| Concern | Reuse | Relay-specific seam |
| --- | --- | --- |
| Product registration and builder chat | `agentprofiles` manifest loader, product profile registry, workflow builder tools, `ChatArea` with `inputVariant="product"`, `ProductChatSurface` | `relayproduct/product.yaml`, builder prompt, Relay-mode tool validation and missing publish/test actions. |
| Left/right layout | Product surface switcher, split rail, workspace toolbar, view headers | A thin `RelaySurface` composing the existing chat and Relay pane. |
| Plan | Existing workflow plan representation, React Flow shell, canvas controls, node/edge visuals, route trace | Relay-mode filter and prompt/output inspector over the same plan. |
| Agent node | Message-sequence executor, MCP session, provider continuation, event/logging primitives | Custom system prompt and authored message templates; durable turn cursor; no workflow synthetic turns. |
| Script and decision | Script executor and deterministic route resolution | Relay input/output binding and one selected path. |
| Python tool | Existing agent custom-tool registration, tool-call events, and sandbox isolator | Shared versioned Python tool runner and enforced per-node allowlist. |
| Browser | Managed `agent_browser` tool, session tracker, and cleanup primitives | Run-ID-owned headless session/profile, per-node enablement, terminal cleanup, and recovery of an interrupted run. |
| Triggers | Workflow function trigger/call/poll path, webhook authentication, encrypted secrets, delivery IDs | Extend typed inputs for JSON, bind to a Relay version, and expose its final JSON result. |
| Execution logs | Shared log rows, tool-call display, step detail, cost display | Query by Relay run ID and show durable checkpoints/recovery state. |
| Resume | Workflow run IDs/history and `mcpagent` provider-neutral CLI session handles | V1 durable node checkpoints and explicit uncertain-node state; later turn checkpoints, leases/fencing, effect reconciliation. |

Extend shared components and services at these seams, then let both Workflows/Crew and Relays call them. Keep Relay mode small: resolve the frozen plan revision, run one existing step, commit its output and selected next edge, and repeat. Avoid invoking goal, Pulse, learning, and workshop code merely to reach message-sequence or scripted executors. Where an executor is too coupled to those behaviors, extract it behind a shared interface and leave a compatibility adapter for existing workflows. Consecutive agent nodes should be allowed to share one coding-CLI session when their model, prompt, and tool policy are compatible; a policy or system-prompt change forces a new session. Measure CLI cold-start and MCP-ready cost before choosing a default grouping.

## Definition and API contracts

### Definition storage

Keep the canonical draft in the existing workflow workspace: `workflow.json` identifies the Relay kind and stores product-level input/output/version metadata; `planning/plan.json` holds the graph using existing message-sequence, scripted, and route step types; and `planning/step_config.json` holds compatible step settings. Add the authored system prompt and per-node permissions to those existing step contracts or a small referenced extension. Reuse the workflow's script files and history/backup machinery. User-authored Python tool files are the one new artifact type, stored beneath the guarded workflow workspace and referenced from the plan.

Publishing freezes the plan, step config, referenced scripts and Python tools as one version hash. Extend the existing plan-revision/history path rather than creating a parallel `definition.json` graph and `versions/vN` directory tree. A draft test also pins a revision so its trace does not change after later edits. Trigger bindings and encrypted credentials stay in the existing workflow manifest and secret path; a run snapshots the resolved trigger mapping and version at acceptance. Secret **references** may be versioned, but secret values are never written into definitions or logs.

The Relay-mode plan contains stable step IDs, routes, input fields, prompt/message templates, script and user-tool references, decision cases and fallback, model/tool selections, per-node `agent_browser` enablement, optional output schema, and execution limits. Reuse plan validation for dangling edges, cycles, and step IDs; add only Relay-specific checks for single-path V1, variable references, and unsupported settings. These checks do not launch a model or run an automatic prevalidation/repair agent.

### User-created tools

An author creates a tool in chat or Integrations and assigns it to one or more agent nodes. A tool has a stable ID, display name, agent-facing description, JSON Schema arguments, optional JSON Schema result, a Python `main.py`, timeout, selected secrets, and a declared effect policy (`read_only`, `idempotent`, `reconcilable`, or `unknown`). The node's explicit allowlist determines whether the agent can discover and call it. Existing MCP tools remain selectable beside these user-created Python tools. A user-declared policy is metadata until runtime permissions or an integration contract prove it; V1 conservatively treats user Python tools as `unknown` for crash recovery.

The tool runner validates the model's JSON arguments, starts the version-pinned Python script through the existing sandbox runner in an isolated process, passes one JSON object on standard input, and requires one JSON value on standard output. Standard error is diagnostic output; a nonzero exit, timeout, or invalid JSON is a tool error returned to the agent and recorded in the run. The runner supplies stable run/tool/operation IDs and only the explicitly selected secrets and capabilities; it grants no browser access. This is an on-demand tool call: the agent may call it zero or more times during its message sequence. A scripted graph node instead executes when the graph reaches that node.

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

Optional Relay-facing endpoint aliases over the existing workflow function call/status service; do not add a second scheduler or run registry:

```text
POST /api/relays/{relay_id}/runs
GET  /api/relays/{relay_id}/runs/{run_id}
POST /api/relays/{relay_id}/runs/{run_id}/resolve
```

The `resolve` action records an authorized V1 node outcome and continues the same run. A later per-effect journal can add an effect-specific resolution endpoint. `POST` accepts `{ "input": { ... } }`, a trigger credential, and an optional `Idempotency-Key` mapped to the workflow function delivery ID. Extend the current function input checker beyond its primitive input types to accept the declared JSON contract. The shared service commits the run and input before returning `202 { "run_id": "...", "status_url": "..." }`. An optional bounded wait may return `200` with the finished result; a timeout still returns the run ID. Existing `call_workflow_function` and polling clients should also be able to invoke a published Relay. A terminal result has a stable envelope such as:

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

**Product promise:** Once the API acknowledges a run, that run and its original input/version remain recoverable after an app, worker, or host process restart, assuming its durable storage survives. A recovered run keeps the same run ID. Completed nodes are not repeated. The run either completes or reports an explicit reason that its current node needs attention. Neither the server nor the client has to guess whether a new run was created. This is 100% durable accountability, not 100% automatic replay of every in-flight agent action.

### V1 cut line and later continuation

V1 uses the existing workflow run identity, function-call delivery ID, and durable run history. It checkpoints **at node boundaries**: the frozen version/input, selected route, and each committed node output. On restart, a completed node is never rerun. A node interrupted while running automatically retries only if the runner can enforce that it is side-effect-free. Every other interrupted node becomes `needs_attention` on the same run ID. An authorized operator can inspect its evidence and either confirm its output, confirm no effect occurred and retry, or fail the run. A provider session may be relaunched from its saved CLI handle when available, but V1 does not promise turn-level continuation inside a node.

Later work adds durable turn checkpoints, per-effect reconciliation adapters, multi-worker leases/fencing, and browser-profile recovery. These are not prerequisites for a user to create, test, publish, and call a V1 Relay. Keep the full target design below to guide the upgrade, but label its later-phase mechanisms explicitly. The fault-injection release gate applies to V1's node-boundary promise first, then expands with each continuation capability.

This is a guarantee of **durable, safe recovery**, not a claim that arbitrary external actions execute exactly once. An MCP tool or script can complete an outward action just before the process dies and before it reports success. If that service cannot deduplicate or report the action's status, no runner can prove whether replay is safe. The Relay must stop at `needs_attention` rather than silently perform the action again. The API and UI must expose the effect, evidence, and available choices. Automatic continuation is guaranteed only across effects that can be reconciled or repeated safely.

The current [standalone message-sequence executor](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go) does **not** meet this contract: it treats `session.json` as an observation log and abandons an interrupted run. Reusing its execution loop therefore requires new durable turn checkpoints and restart recovery. Existing provider-neutral `AgentSessionHandle` continuation and workflow `continuation_state.json` provide building blocks, not a finished Relay resume implementation. See [Coding Agent Continuation Architecture](../core/coding_agent_continuation_architecture.md).

### Durable run state

There is no general transactional server database to assume. V1 extends the existing per-workflow file/SQLite run records and atomic-write conventions for Relay checkpoints, after a storage spike verifies crash consistency and concurrent idempotency. Do not add a parallel `relay_runs` service merely to support this product. A later shared transactional ledger may add node attempts, turns, effects, and append-only events if the existing run store cannot support those guarantees. Keep large artifacts in the workflow workspace and record their hashes in durable run metadata. A run records:

- Relay ID, immutable version ID and hash, trigger ID and resolved mapping, original input, and idempotency key.
- V1 status and current node, selected decision path, attempt numbers, timestamps, and cancellation/attention reason; later turn cursor and lease fencing token.
- Each completed node's exact output; later each agent turn's rendered user message, conversation checkpoint, and provider-neutral CLI session handle.
- Any dispatched node's operation ID, tool identity, arguments hash, start/result state, and reconciliation evidence where available. Secret values and credentials are excluded.

The node state machine is `pending -> running -> completed`; exceptions move to `retryable`, `paused`, `needs_attention`, or `failed`. User **Pause** preserves the checkpoint; **Cancel** is a terminal request. The run result is committed once and then remains stable for polling.

### Checkpoint boundaries and recovery (target, with V1 subset)

1. **Accept (V1):** Use the workflow function delivery path to resolve the published version and trigger mapping, enforce request/idempotency rules, persist input and a queued run, then acknowledge it. A repeat request with the same trigger and idempotency key returns the existing run ID.
2. **Claim (later):** For concurrent workers, obtain a time-limited lease with a monotonically increasing fencing token. Every state write checks that token. Another worker can claim an expired lease without both workers committing results.
3. **Before a node (V1):** Persist the planned node, attempt ID, fully rendered input, and effect classification before invoking the agent or script. A direct effect that cannot be mediated makes the whole node `unknown`.
4. **After a turn (later):** Persist the CLI conversation, output, and refreshed provider-neutral session handle as one logical checkpoint. A partial streamed response is diagnostic evidence, not a committed turn. AgentWorks currently runs coding CLIs, so direct API-model session replay is outside this plan.
5. **After a node (V1):** Atomically commit the node output, selected decision, and next cursor. Never rerun a completed node on recovery.
6. **Restart (V1):** Scan accepted/running workflow-backed Relay calls. Restore their frozen version and last committed node. Relaunch a coding CLI from a saved handle only when safe; server startup kills `mlp-*` tmux sessions, so do not assume a live process can be reattached. Otherwise mark the interrupted node `needs_attention`, except for enforceably pure nodes. Prefer structured transport for Relay execution and test provider-specific continuation before enabling it.
7. **Finish (V1):** Persist final JSON and terminal status before emitting a response or callback. A lost HTTP connection does not lose the result; polling returns the same document.

The execution engine must not infer progress from log text, process IDs, in-memory goroutines, or file timestamps. Those may help diagnostics, but the durable workflow run record owns the cursor. Startup recovery uses that record in V1; a later lease sweeper uses the same recovery path when multi-worker execution is introduced. Resume is idempotent and safe under concurrent requests.

### Run-scoped browser lifecycle

Selected agent nodes receive the managed `agent_browser` tool in headless mode. Give each run a unique browser session and temporary profile, never the persistent browser or user Chrome/CDP profile used by Crew and Workflows. Browser-enabled nodes in the same live run may share that session; other runs cannot. Close the session and remove its profile on completion, failure, cancellation, or retention cleanup. Startup recovery and the idle reaper also clean abandoned sessions, using run ownership rather than a shared workflow identity.

In V1, retain the temporary profile only while the process is live; after a restart, an interrupted browser node becomes `needs_attention` on the same run. A later browser-recovery phase may retain an active/paused run's profile, checkpoint browser ownership and URL/tab information, then reopen and re-observe the page. Reopening a profile may restore cookies and storage, but it does not prove that arbitrary in-page JavaScript state survived. A click, form submission, purchase, post, or similar action is an external effect: record the node dispatch before acting and reconcile an uncertain outcome before retrying. If the site cannot confirm the action, keep `needs_attention`. No browser session or login state carries into a later Relay run.

### External effects and scripts

V1 records node dispatch before any call that may change external state and treats an interrupted node as unknown. The later per-effect phase writes an effect record **before** each tool dispatch, passes a stable operation ID as an idempotency key when the provider supports one, and queries operation status on restart. Unknown tools remain `needs_attention` if interrupted after dispatch. A Python tool is `read_only` only when that is enforced by its runtime permissions; it is `idempotent` or `reconcilable` only when its external operation contract supports that claim. An author-selected label alone never makes replay safe.

Resolving `needs_attention` is part of resume, not a new run. Execution logs show the effect's recorded request and any reconciliation evidence. An authorized user can record that the effect succeeded (including its observed result), confirm it did not occur and retry it, or fail the run. The resolution and actor are audited, then the same run ID continues from its checkpoint. The API exposes the same action for an external operator. The runtime never treats lack of evidence as proof that an effect did not occur.

Script nodes receive the same run/node/attempt identifiers. In V1, journal the **entire user-authored script invocation as an unknown effect before dispatch** and never automatically replay it after an interruption. A script may be classified as pure and auto-retried only when a runner enforces no network, no external writes, no privileged bridge or tmux socket access, and only an isolated scratch directory; the existing filesystem isolator alone does not prove network purity. A later effect helper can enable idempotent/reconcilable external calls, but merely asking a script to use it is not enforcement. An arbitrary shell command or third-party MCP tool with unobservable side effects also pauses at `needs_attention` after an uncertain dispatch. Plan must show this resume-safety status before publication. This policy is separate from model prevalidation.

## Implementation plan

The estimates below are rough **engineer effort**, not calendar dates. The architecture spike can change them. V1 includes the authoring experience, an externally callable run, Python tools, optional `agent_browser`, and the node-boundary resume contract. Turn-level continuation and automatic reconciliation are later work.

### 0. Prove the reuse boundary (3–5 engineer days)

- Trace one workflow function from `workflow_function_triggers.go` through accepted run, status polling, CLI launch, run history, and access checks. Confirm how a JSON object input, frozen version reference, and JSON result can be added without a parallel service.
- Prototype a `relay` workflow kind and a no-goal execution mode that reaches message-sequence, scripted, and route steps without invoking Pulse, workshop, validation, or learning turns. Test structured transport and exact system-prompt delivery on each supported coding CLI. Measure cold-start and MCP-ready cost; decide when adjacent agent nodes can share a session.
- Verify the existing per-workflow file/SQLite storage can atomically accept a call and checkpoint a completed node. If it cannot, specify a shared run-store extension before implementation; do not silently assume a general server database.
- Gate: record code entry points and a working test. Revisit the architecture table if the workflow-backed path proves more costly or less isolated than a shared executor extraction.

### 1. Build a callable vertical slice (2–3 engineer weeks)

- Register `relayproduct/product.yaml`, builder prompt, and thin left-chat/right-pane surface. Store a Relay kind under an existing workflow workspace and reuse its owner, readers, tokens, path guards, encrypted secrets, and history. Extend `planning/plan.json` and step config only for missing Relay fields; publish a frozen plan revision and draft-test snapshot through the existing history path.
- Extend workflow functions to accept the declared JSON input contract and bind a call to an immutable published version. Reuse delivery ID, trigger auth, call ID, immediate accept, and polling. Produce one stable JSON output envelope. Add a Relay-facing HTTP alias only if the existing function endpoint is unsuitable for external callers.
- Add Relay mode to message-sequence execution: authored system prompt and ordered user messages, variables from trigger and prior nodes, no workflow template or synthetic turns. Reuse scripted steps, deterministic routes, and existing plan validation. Reject loops and parallel branches in V1.
- Gate: author and publish a two-agent branching Relay in chat, invoke it from a website backend and through `call_workflow_function`, poll the same call ID, and retrieve author-defined JSON. Editing the draft cannot change the running version.

### 2. Add tools, browser, and enforceable permissions (2–3 engineer weeks)

- Add versioned Python tool definitions and a JSON stdin/stdout runner through the existing sandbox isolator. Pass only selected secrets and capabilities. Every user Python tool and script is an unknown-effect node in V1 unless a separate runner proves it has no network or external writes. Journal dispatch before execution; an interrupted invocation requires attention, never automatic replay.
- Extend the shared agent tool bridge to enforce per-node allowlists without granting arbitrary shell access. Verify discovery and denial on the actual coding-CLI transport. Register selected MCP tools and the optional managed `agent_browser` only for authorized nodes.
- Give each run a temporary headless browser/profile, close it at terminal state, and mark uncertain interrupted browser actions `needs_attention`. Automatic browser-profile restoration is later work.
- Gate: an unselected tool cannot be discovered or called; Python tools cannot read bridge config or tmux socket material in `/tmp`; crash after a user script's external call never automatically repeats it; browser state cannot leak to another run.

### 3. Ship node-level recovery and shared product coverage (2–3 engineer weeks)

- Checkpoint accepted input/version, current node, chosen route, committed outputs, and terminal JSON in the workflow run store. Add startup recovery and an audited `needs_attention` resolution that continues the original run. Retry only enforceably pure nodes. Keep the same call ID and idempotency key across reconnects and retries.
- Audit owner/co-owner/reader/private and read-only-account access, product toggles, workspace-token/proxy path policy, per-session bridge tokens, Folder Guard, Landlock, secret binding, and the `/tmp` exposure before external triggers are enabled.
- Surface Relay runs in Active Work, Ctrl+K, origin labels, cost ledger, usage hover, run history, backup/export, external MCP selection, and the `agentworks` CLI. Add docs and a QA ticket.
- Gate: no acknowledged run disappears on restart; completed nodes do not rerun; uncertain nodes state exactly what requires attention. A second caller with the same delivery ID joins the original run.

### 4. Complete the right pane and V1 release gate (1–2 engineer weeks, overlaps earlier stages)

- Reuse Plan, Triggers, Execution logs, and Integrations UI against the same workflow data services. Show exact authored prompts, rendered inputs, selected path, JSON output, version, Python tool policy, browser setting, and recovery state. Add Relay-only controls behind the product mode rather than maintaining parallel React state or duplicate views.
- Fault-inject at acceptance, node dispatch, script/tool dispatch, node commit, route selection, final JSON commit, and HTTP response. Test one side-effecting MCP integration and every supported CLI transport. Test key rotation, concurrent duplicate calls, draft edit during a published run, browser cleanup, and access roles.
- Gate: the V1 node-boundary resume promise and security checks pass. Clearly label an interrupted in-flight node `needs_attention`; never imply that its agent turn or external effect was automatically resumed.

### 5. Later continuation work (separate estimate)

- Add turn-level CLI session checkpoints and typed stale/non-continuable handle outcomes. Relaunch from saved handles after startup rather than relying on `mlp-*` tmux sessions to survive.
- Add per-effect operation IDs and reconciliation adapters, then multi-worker leases/fencing if execution becomes distributed. Add browser-profile recovery only after reliable action journaling and re-observation are proven.
- Expand fault injection to turn dispatch, effect success before acknowledgment, lease races, and browser-profile reopening. An unknown outward effect remains `needs_attention` regardless of phase.
