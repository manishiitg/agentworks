[← relays / execution](index.md)

# PLAT-577: Remove variable groups from Relay execution and authoring

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | execution |
| Summary | Remove variable groups from Relay UI, Builder authoring and execution; keep flat configuration and per-call INPUT. |

## What happened

Relays reused Goals' variable-group selection and batch wrapper, including a
synthetic default group. An API product needs one invocation with its own INPUT
and run identity, while reusable values remain useful as flat configuration.

## Fix

- New Relays and function triggers contain no groups. Group fields are rejected
  by Relay trigger/configuration writes and Builder execution tools.
- The graph shows Inputs & configuration, including sample INPUT. Group controls
  and group labels are omitted; values persist as `variables[].value`.
- A Relay invocation skips group batching and iteration-0 rotation. It shares the
  existing execution manager, session lifecycle, step executor, routing, sandbox,
  output collection and durable capacity-resume machinery. Runs own their MCP and
  browser sessions, folders, metadata and webhook progress.
- Node testing/debugging uses a run-folder ID. Cached Builder sessions refresh
  flat values, clear group state and discard stale VAR_* values.
- The product manifest, system prompt, skill and command prompts explain the
  group-free contract. No separate migration ladder is introduced.
- A sole legacy group, or a frozen trigger's explicit legacy binding, is read as
  configuration. Reads leave release bytes/hashes untouched. Explicit draft
  configuration saves flatten values (including undeclared legacy keys) and
  remove old trigger bindings. Ambiguous legacy configurations require explicit
  consolidation; no arbitrary group is chosen.
- Both old nested run artifacts and new group-free artifacts remain readable.

## Verification

- Real isolated workspace HTTP handlers and sandboxed Python: a two-step chain
  failed at step 2, resumed from that step without changing/replaying step 1,
  then a separate INPUT invocation returned an independent greeting. Builder
  execute_step also ran without a group. No synthetic default folder was created.
  Reproduce in agent_go: `RUN_RELAY_GROUPLESS_E2E=1 go test ./pkg/orchestrator/agents/workflow/step_based_workflow -run TestRelayGrouplessChainAndResumeRealWorkspace -count=1`.
- Focused server regressions passed: flat configuration HTTP read/save, immutable
  legacy releases, trigger creation, direct invocation, outputs and capacity resume.
- Frontend TypeScript build passed; seven mounted sidebar/function tests passed.
- Full step-workflow suite has one unrelated stale AGY alpha-gate assertion,
  tracked in [PLAT-578](../../coding-agents/models/plat-578.md).

## Left

Not deployed. No new live model call or external browser run was performed for
this change; the execution check used real saved Python steps. General
process-crash recovery remains the existing deferred feature; this change tests
explicit step resume and preserves the shared capacity-checkpoint path.
