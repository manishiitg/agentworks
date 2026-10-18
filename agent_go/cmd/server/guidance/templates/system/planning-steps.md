---
name: "planning-steps"
description: "Workshop plan composition: take-action-by-default discipline, step-type selection, validation_schema requirements, forward-only context flow. Load before adding or editing plan steps."
external: true
external_tools: ["plan", "get_plan", "execute_step", "run_full_workflow"]
external_note: "External mapping: discover current tool schemas before acting. Use plan action=list for native plan and step operations; read get_plan for expected_revision, then pass a stable request_id and listed native arguments. Internal operation names are plan action values, not standalone external tools. Settings uses settings get/update. For skill wiring, use list_workflow_knowledge and read_workflow_knowledge, Crew package actions and Brain skill packages under their existing roles and file/folder grants. Fetch referenced guidance by its listed topic name. Server-only scripts, bridge variables and internal URLs are not external client credentials."
---
## Planning Steps

When a user describes what to automate: take action. Create or update
the plan using workflow-builder best practices and the available
context. Ask the user only for blocking choices that would materially
change behavior, safety, credentials, schedules, external side effects,
or irreversible actions. State reasonable assumptions briefly and
proceed.

Every plan-tool mutation is an atomic graph transaction. Before saving,
the backend verifies that every routing route and explicit
`next_step_id`/branch targets a step that still exists or
`end`. If a tool returns `PLAN_GRAPH_INVALID`, no partial edit was saved:
use the listed source step and field to reroute every dangling reference,
then retry the original add/update/delete. Never delete a referenced step
first and plan to repair the graph afterward.

## Step composition defaults

**Default to one large `agent` per shared-context span.** Modern
agents can own many actions and long tool sessions, so the strongest shape is
one conversation that executes the complete `description` as its first user turn
ALONE, then automatically validates and repairs configured checks. Put binding
policies in the description's Rules section; use `prompt_contract="description-first-v1"` for new
or reviewed migrated agents. `items[]` is optional: zero items is valid. Builder
adds later semantic verification or a real phase only when it improves this task;
never mandate generic execute/verify/repair messages. Improve the runnable task,
run-specific proof/provenance output and validation before adding another workflow step.

Use multiple large sequences when their contexts should not be shared: distinct
credentials/security exposure, independent durable outputs or retries,
clean-room independence, human/routing boundaries, or unrelated context that
would distract or contaminate the next agent. The builder must decide this from
the workflow semantics and be able to state the boundary. Use `scripted` only for
deterministic work implemented as a saved script. Every conversational or
judgment-heavy outcome, including one turn, uses `agent`. Every producing step still needs a
`validation_schema`; context flow stays forward-only via
`context_dependencies` → `context_output`.

For a fixed choice the user already gave the builder, prefer a deterministic
switch instead of asking again — `branch` for a small in-flow decision,
`routing` when the choice forks into a major, self-contained sub-workflow —
and pass `route_selections` when running either.
Declare scripted `predefined_routes` when the agent needs typed Python helpers. Use an explicit Crew step for LLM specialists. Several known actions in one shared context belong in the complete description task; use ordered items only for distinct subsequent phases. Do not add a
checkpoint just to ask the same branch choice again.

## Step types

- **`agent`** (canonical agent step) — a description-first turn plus optional subsequent items in one conversation; optional
  `predefined_routes` expose typed scripted helpers; automatic reflection and LLM children are retired
- **`scripted`** — a scripted deterministic boundary for API/SDK/CLI fetching,
  parsing, normalization, and mechanical persistence; batch related calls
  rather than making micro-steps. Conversational work uses `agent`,
  even for one turn
- **`routing`** — choose next step from a fixed route map; a major,
  self-contained sub-workflow fork
- **`branch`** — same deterministic route-map mechanics as `routing`, for a
  small in-flow next-step decision instead of a major fork
- **`crew`** — invoke a Crew trigger and wait for its response
- **orphan** (`is_orphan: true`) — reusable across agents via
  `shared_with.agent_ids` + `orphan_step_ref`

## See also

For the design playbook (8-step design walkthrough, step-type
trade-offs, validation design, context flow, anti-patterns,
step-types reference, inner steps, reusable orphan-route pattern),
call **`read_skill(skills=[{"name":"builder-reference","path":"references/plan-design.md"}])`** — this is the entry
point for any plan-composition decision. From there:

- **Per-step-type deep dives**: `agent` (conversation, scripted helpers, verification, SQL iteration and script batches), `routing` (major deterministic mode selection), and `branch` (small in-flow decisions).
- **Combining steps**: examples live with their owning contracts in `plan-design`, `agent`, `human-in-the-loop` (review/approval), and `stores` (durable persistence).
