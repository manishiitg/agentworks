[← goals / evaluation](index.md)

# PLAT-282 — The evaluation plan had a tool to update a step but no tool to delete one

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | evaluation |
| Summary | P2 — not live-breaking (no run failed because of it), but a |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Sonnet 5 |
| Ticket state | `fixed` |
| Last synchronized | `2026-09-04` |

- **Priority:** P2 — not live-breaking (no run failed because of it), but a
  confirmed, repeatable audit-trail gap: every deletion until now has left
  no record for Pulse Artifact Review to see or judge.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_plan_tool.go`,
  `planning_agent.go` (tool registration), `interactive_workshop_manager.go`
  (`planMod` Workshop-mode allowlist), `agent_go/cmd/server/toolset_invariant_test.go`.
- **Related:** `update_evaluation_plan`'s own doc comment and AR-20260729-2 —
  the same failure mode (a direct file write to `evaluation_plan.json`
  leaving nothing in `planning/changelog`) this ticket closes for deletion,
  already fixed once for the update case.

## What was found

While working a trading-workflow agent removed an evaluation step and
flagged, unprompted, that it had edited `evaluation_plan.json` directly
because there was no delete tool for eval steps — an honest self-report of
exactly the gap `update_evaluation_plan` exists to prevent.

Checked the actual tool surface for both plan types. The regular plan
(`planning/plan.json`) has full CRUD: `add_scripted_step`,
`add_message_sequence_step`, `add_human_input_step`, `add_routing_step`,
`add_branch_step`, `add_orchestrator_step` and their matching `update_*`
tools, plus `delete_plan_steps` for removal. `evaluation/evaluation_plan.json`
had exactly one tool: `update_evaluation_plan`. No add, no delete.

This is not a coincidental omission. `update_evaluation_plan`'s own doc
comment explains it already exists *because* the eval plan previously had no
tool at all, so every historical edit bypassed `planning/changelog` and
Artifact Review couldn't detect drift (AR-20260729-2). The agent's direct
edit today repeated that same failure for deletion, because nothing had
covered that operation.

## What shipped

`delete_evaluation_step`, mirroring `delete_plan_steps`'s shape
(`step_ids`, required `reason`, atomic all-or-nothing across a batch, the
full deleted-step JSON captured in the changelog entry for a manual
revert) but simpler: eval steps carry no `next_step_id`/route graph to each
other — `applies_to_routes` references a routing step in the *main* plan,
never another eval step — so unlike `delete_plan_steps` this needs no
downstream-reference revalidation after filtering, only that every
requested id exists.

Implemented against the decoded JSON map, not the `EvaluationStep` struct,
for the same reason `update_evaluation_plan` is: the struct doesn't model
every field a real plan carries (`max_score`, `context_dependencies`), so a
struct round-trip would silently drop them from any step it touched. The
shared read/decode/steps-key-resolution logic (including support for the
legacy `eval_steps` key name) was factored out of `UpdateEvaluationPlanStep`
into `loadEvaluationPlanDocument` so both tools stay consistent.

Registered alongside `update_evaluation_plan` in two places, not one: the
tool registrar itself, and separately the Workshop-mode allowlist
(`planMod` in `interactive_workshop_manager.go`) that actually exposes
registered tools to the agent. Missing the second registration would have
shipped a tool that exists but is invisible in practice —
`TestToolSetInvariants` (`cmd/server`) exists specifically to catch that
class of gap and caught it here on the first test run before this was
pushed.

## Verification

- 6 new tests (`evaluation_plan_tool_test.go`): single delete with full
  changelog capture (including a field the typed struct doesn't model, to
  prove the same struct-blind-spot protection `update_evaluation_plan` has),
  batch delete with a duplicate id, atomic rejection with **no partial
  deletion** when any one id in a batch is missing, `reason`/`step_ids`
  validation, and the legacy `eval_steps` key.
- Full `step_based_workflow` and `cmd/server` suites pass, including
  `TestToolSetInvariants`.
- Built and tested from a clean `origin/main` worktree with only this
  change applied (the primary checkout had unrelated concurrent WIP from
  another session) — one pre-existing, unrelated failure
  (`TestStrategyAuditorGuidanceRequiresLongitudinalEvidenceAndReadOnlyHandoff`,
  `cmd/server/guidance`, Pulse strategy-auditor content) reproduces
  identically against that same clean tip, confirming it predates this work
  and is not a regression from it.
- Not yet done: live reverify — no workflow has called
  `delete_evaluation_step` in production yet; the next time an agent
  removes an eval step, confirm the changelog entry actually appears and
  Artifact Review picks it up.
