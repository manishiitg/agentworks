[← goals / step-execution](index.md)

# PLAT-286 — No tool could change a step's type, so moving a message_sequence to scripted meant rebuilding it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | step-execution |
| Summary | P2 — a builder-workflow gap, not a runtime failure, but one |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Fable 5.1 |
| Ticket state | `fixed` |
| Last synchronized | `2026-09-05` |

- **Priority:** P2 — a builder-workflow gap, not a runtime failure, but one
  that made the platform's own recommended shape (deterministic work in a
  scripted step, judgment in a sequence around it) expensive to reach for an
  existing step, so agents either rebuilt steps by hand or left them
  conversational.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/change_step_type_tool.go`
  (+ registration in `planning_agent.go`, allowlist in
  `interactive_workshop_manager.go`, `IsPlanModificationTool` in
  `planning_management.go`, `cmd/server/toolset_invariant_test.go`).
- **Related:** PLAT-280 (the drift this tool structurally prevents: plan type
  and declared mode disagreeing), PLAT-282 (same "tool surface has update but
  not the other operation" shape, for eval-plan deletion).

## What was found

The trading workflow's agent, asked to make `place-paper-trades` scripted like
it had just done for three other steps, reported that this one was different:
those were already `regular`-type steps running agentic (a one-field flip);
`place-paper-trades` is a `message_sequence`, and "scripted mode" is a
step_config setting that exists only for the `regular` type. Checked on
`origin/main`: correct. `update_step_config` refuses
`declared_execution_mode="scripted"` on a message_sequence (the plan type and
the mode would disagree and the scripted executor would never run it), and
`update_scripted_step`'s PLAT-280 conversion only fires for a sequence that
was *already* declared scripted — a drift repair, not a converter. So the only
path was `add_scripted_step` + `delete_plan_steps` + rewiring every
`context_dependencies`/`context_output`/route by hand.

`declared_execution_mode` itself turned out to be the leftover of the old
model where type and mode were independent: today it is implied on one type
(`regular` is scripted by definition; without it a regular step is the legacy
agentic shape that runs as a sequence) and forbidden on the other. A type
change *is* the mode change; there was just no tool that did both.

## What shipped

`change_step_type(step_id, target_type: scripted | message_sequence, reason)`:

- Converts in place using the converters the runtime already used for its
  own compatibility paths (`normalizeMessageSequenceStepToRegular`,
  `normalizeRegularStepToMessageSequence`), so id, title, description,
  dependencies, outputs, validation_schema, next_step_id and position are
  kept, nested and orphan steps included.
- To scripted: plan type → `regular`, items dropped (a scripted step's work
  lives in `learnings/<id>/main.py`), step_config declares scripted with the
  reason as `declared_execution_mode_reason`, `use_code_execution_mode`
  synced; the result says whether `main.py` already exists or the step will
  fail until `update_scripted_step(code=...)` writes it. A legacy agentic
  regular step just gets the declaration (its type already matches).
- To message_sequence: plan type → `message_sequence` with one
  execute-and-verify item, declared mode and `lock_code` cleared, and a
  warning if a now-orphaned `main.py` remains.
- Plan and config are written as one operation, plan first; the tool is
  idempotent, so if the second write fails a re-run finishes the mode half —
  the plan type and the declared mode can never end up disagreeing, which is
  exactly PLAT-280's failure. Idempotent no-op when already the target (and
  a sequence carrying a stray scripted declaration gets it cleared).
- Full plan validation before writing; a revertable changelog entry carrying
  the full step JSON before and after (`deleted_steps`/`added_steps`, the
  same revert data `delete_plan_steps` records — snapshots are only hashed
  into `before_ref`/`after_ref`, never stored) plus per-field changes; the
  dependent-artifact review notice; registered in the Workshop allowlist and
  `IsPlanModificationTool`. `update_step_config`'s rejection and
  `update_scripted_step`'s description now point at this tool, and the
  workshop guidance says to use it rather than add + delete.

## Verification

- `change_step_type_tool_test.go`: sequence → scripted (fields kept, items
  dropped, config declared with reason and synced, changelog with distinct
  before/after refs, the type change, and the old/new step JSON), scripted → sequence (one item, chain kept, mode and
  lock cleared, orphaned-script warning), legacy agentic regular → scripted
  (config entry created), no-op writes nothing, orphan steps convert,
  routing/unknown steps rejected, `IsPlanModificationTool`.
- PLAT-280 compat tests and `TestToolSetInvariants` (the guard that catches
  a registered-but-invisible tool) pass with it.
- Not yet done: a live conversion of a real step on a deployment; the first
  candidate is `place-paper-trades` on Dominion, which the user has not yet
  decided to convert (the agent's own recommendation is a split: scripted
  placement, judgment kept in the surrounding sequence).
