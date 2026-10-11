## Planning Steps

When a user describes what to automate: take action. Create or update
the plan using workflow-builder best practices and the available
context. Ask the user only for blocking choices that would materially
change behavior, safety, credentials, schedules, external side effects,
or irreversible actions. State reasonable assumptions briefly and
proceed.

Every plan-tool mutation is an atomic graph transaction. Before saving,
the backend verifies that every routing route and explicit
`next_step_id`/human-input branch targets a step that still exists or
`end`. If a tool returns `PLAN_GRAPH_INVALID`, no partial edit was saved:
use the listed source step and field to reroute every dangling reference,
then retry the original add/update/delete. Never delete a referenced step
first and plan to repair the graph afterward.

## Step composition defaults

**Default to one large `agent` per shared-context span.** Modern
agents can own many actions and long tool sessions, so the strongest shape is
one conversation that completes the coherent span and then proves it in
follow-up items: `[do the whole span] → [re-open evidence and prove every
criterion] → [repair gaps and double-check]`. Put the stable objective,
boundaries, and definition of done in the system-level `description`; put those
execution and verification instructions in `items[]` as user turns. Improve the charter,
run-specific proof/provenance output, top-level `validation_schema`, and
verify/repair turns before adding another step. A request for more checking is
not itself a step boundary.

Use multiple large sequences when their contexts should not be shared: distinct
credentials/security exposure, independent durable outputs or retries,
clean-room independence, human/routing boundaries, or unrelated context that
would distract or contaminate the next agent. The builder must decide this from
the workflow semantics and be able to state the boundary. Use `regular` only for
deterministic work implemented as a saved script. Every conversational or
judgment-heavy outcome, including one turn, uses `agent`. Every producing step still needs a
`validation_schema`; context flow stays forward-only via
`context_dependencies` → `context_output`.

For a fixed choice the user already gave the builder, prefer a deterministic
switch instead of asking again — `branch` for a small in-flow decision,
`routing` when the choice forks into a major, self-contained sub-workflow —
and pass `route_selections` when running either.
Add `predefined_routes` to a `agent` only when the agent must choose
specialist work adaptively from runtime evidence; several known actions in one
compatibility shapes for this routed agent. Do not add a
`human_input` step just to ask the same branch choice again.

## Step types

- **`agent`** (canonical agent step) — a system-level description
  charter plus ordered user-message items in one conversation; optional
  `predefined_routes` expose bounded specialists the agent chooses adaptively
- **`regular`** — a scripted deterministic boundary for API/SDK/CLI fetching,
  parsing, normalization, and mechanical persistence; batch related calls
  rather than making micro-steps. Conversational work uses `agent`,
  even for one turn
- **`agent`** — legacy compatibility records for a
  `agent` with `predefined_routes`; do not author new plans in this shape
- **`routing`** — choose next step from a fixed route map; a major,
  self-contained sub-workflow fork
- **`branch`** — same deterministic route-map mechanics as `routing`, for a
  small in-flow next-step decision instead of a major fork
- **`human_input`** — pause for operator input
- **orphan** (`is_orphan: true`) — reusable across orchestrators via
  `shared_with.orchestrator_ids` + `orphan_step_ref`

## See also

For the design playbook (8-step design walkthrough, step-type
trade-offs, validation design, context flow, anti-patterns,
step-types reference, inner steps, reusable orphan-route pattern),
call **`read_skill(skills=[{"name":"builder-reference","path":"references/plan-design.md"}])`** — this is the entry
point for any plan-composition decision. From there:

- **Per-step-type deep dives**: `orchestrator` (legacy compatibility + route
  migration), `human-input` (input types + routing
  pairing + unattended schedules), `agent` (full pattern
  catalog: Stateful Specialist, Test/Fix Loop, Maker+Reviewer, Panel,
	  Clean-Room Retry, HITL Re-entry, Scripted Conversation), `routing`
	  (deterministic route_selection.json contract, anti-patterns), `branch`
	  (same mechanics as `routing`, for a small in-flow decision).
- **Combining steps**: examples live with their owning contracts in `plan-design`,
  `agent` (script batches, verification, SQL iteration), `orchestrator`
  (adaptive investigations), `human-in-the-loop` (review/approval), and `stores`
  (durable persistence).
