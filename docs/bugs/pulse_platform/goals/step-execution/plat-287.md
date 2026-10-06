[← goals / step-execution](index.md)

# PLAT-287 — Retire `declared_execution_mode`: a step's plan type alone decides its execution model

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | step-execution |
| Summary | P3 — no live failure; a model-cleanup the user asked for |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Fable 5.1 |
| Ticket state | `implemented` (half 1: contract v1.0.38, plan types explicit; half 2: contract v1.0.39, runtime reads the plan type only, field stripped) |
| Last synchronized | `2026-09-05` |

- **Priority:** P3 — no live failure; a model-cleanup the user asked for
  ("can we just remove this from code base") once PLAT-286 made the field
  redundant.
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/declared_execution_mode_migration.go`
  (v1.0.38 tool), `declared_execution_mode_strip.go` (v1.0.39 tool),
  `isScriptedStep` in `interactive_workshop_manager.go` (the one runtime
  predicate), `agent_go/cmd/server/workflow_manifest.go` +
  `workflow_version_upgrades.go` (both rungs).
- **Related:** PLAT-286 (`change_step_type`, which made the type change *be*
  the mode change), PLAT-280 (the drift a stray declaration caused),
  v1.0.35 (`migrate_orchestrator_step_type`, the pattern copied).

## What was found

`declared_execution_mode` is a leftover of the model in which a step's plan
type and its execution mode were independent. Today it is implied on one
type and forbidden on the other: a `regular` step is scripted by definition
(`add_scripted_step` sets the declaration automatically), a `regular` step
*without* it is the legacy agentic shape that the runtime silently runs as a
`message_sequence` through a compatibility shim
(`normalizeRegularStepToMessageSequence`, chosen by
`shouldNormalizeRegularStepToMessageSequence` in
`controller_message_sequence.go`), and `update_step_config` rejects the
declaration on a `message_sequence` outright. Deleting the field outright
would flip every legacy agentic regular step on disk to scripted with no
`main.py`, so the removal has to be staged.

**Review finding that reshaped the staging (2026-09-05, before anything
ran):** the first cut of half 1 also stripped the field from
`step_config.json`. That was wrong: the *then-current* runtime still read it
to tell a scripted regular step from a legacy agentic one, so stripping it
would have turned every real scripted step into a message_sequence whose
`main.py` never runs — and a re-run would have converted every remaining
regular step. Half 1 became convert-only; the strip moved to half 2, in the
same release as the runtime rule that makes the field redundant.

## What shipped

### Half 1 — contract v1.0.38 (plan types explicit)

- **`migrate_declared_execution_mode`**, a trusted, idempotent,
  behavior-preserving tool that touches only `planning/plan.json`: every
  legacy agentic `regular` step becomes the `message_sequence` it already
  ran as (same id, description, dependencies, validation, `next_step_id`,
  position; nested and orphan steps included); any `message_sequence` still
  declared scripted (PLAT-280 drift) becomes `regular`. It deliberately
  keeps the *pre-1.0.38* reading of the config — a regular step is legacy
  agentic unless its config declares scripted — because that is what the
  workflows it runs on were written under. Refuses, changing nothing, when a
  declared-scripted step has no `learnings/<id>/main.py`.
- **Contract v1.0.38** (`workflowContractDeclaredExecutionModeRetiredVersion`,
  rank 37), rolled out per workflow on each Builder's next preflight.

### Half 2 — contract v1.0.39 (runtime reads the plan type; field stripped)

- **One runtime predicate**, `isScriptedStep(step, cfg)`: a `regular` step
  is scripted, a `message_sequence` is conversational, an `EvaluationStep`
  is scripted when its new `execution_mode` field (`evaluation_plan.json`,
  editable through `update_evaluation_plan`, validated to
  `scripted`/`agentic`) says so. `isScriptedExecutionModeConfig` and
  `syncDeclaredExecutionModeConfig` are gone; every former caller
  (`controller_agent_factory.go`, `controller_execution.go`,
  `controller_workshop.go`, `controller_orchestrator.go`,
  `workflow_continuation_recovery.go`, the Workshop's `execute_step`,
  step-config listings and `review_step_code`) takes the plan step. The
  orchestrator step type is never scripted. The rich-context
  `execution_mode` label is derived the same way.
- **Transitional shim (the only remaining reader of the old key):** a
  `regular` step whose *unstripped* `step_config.json` still says
  `declared_execution_mode: "agentic"` keeps running as a message_sequence
  (`isLegacyAgenticRegularStep`), and an evaluation step whose unstripped
  config declares scripted keeps running its script, until the v1.0.39
  migration removes the key. The two struct fields survive as
  `LegacyDeclaredExecutionMode` / `LegacyDeclaredExecutionModeReason`, still
  bound to the JSON keys, for exactly two reasons: the shim, and so that a
  `step_config.json` write made between the two migrations does not
  silently drop the key and flip such a step to scripted. **Nothing sets
  them.** Delete both fields and the shim once no live workflow predates
  1.0.39.
- **Every writer stopped:** `add_scripted_step` /
  `upsertNewScriptedRegularStepConfig` only turns code execution on;
  `change_step_type` rewrites the plan and, to scripted, sets
  `use_code_execution_mode` and clears any legacy key (a legacy agentic
  regular → scripted is now just that clear), to message_sequence clears
  `lock_code`; `migrate_message_sequence_code_items` no longer stamps the
  mode; `update_scripted_step` no longer silently downgrades a
  declared-scripted sequence (it refuses and points at `change_step_type`);
  `update_message_sequence_step` upgrades a regular step only when it is
  legacy agentic; `update_step_config` rejects `declared_execution_mode` /
  `_reason` with a pointer to `change_step_type` (workflow steps) or
  `update_evaluation_plan(updates={"execution_mode": …})` (evaluation
  steps), and no longer lists them as clearable; `validateDeclaredExecutionModeChange`
  deleted; Video Studio's generated plan no longer writes the key.
- **`strip_declared_execution_mode`** (v1.0.39 tool, idempotent): refuses,
  changing nothing, while any `regular` step still carries
  `declared_execution_mode: "agentic"` (i.e. 1.0.38 did not complete);
  otherwise marks every evaluation step that was declared scripted with
  `execution_mode: "scripted"` in `evaluation_plan.json` *first*, then
  removes both keys from `planning/step_config.json` and
  `evaluation/step_config.json` (other fields untouched), recording every
  removed mode and reason in the `planning/changelog` entry. Registered in
  the Workshop allowlist, `IsPlanModificationTool`, the toolset invariant.
- **Contract v1.0.39** (`workflowContractDeclaredExecutionModeStrippedVersion`,
  rank 38, `WorkflowContractCurrentVersion` bumped); prompt
  `upgradeDeclaredExecutionModeStripped`.
- **Frontend** derives the mode from the plan type
  (`effectiveExecutionMode` / `runsAsMessageSequence` in
  `utils/stepConfigMatching.ts`; nodes, canvas and LearningsView use it;
  `EvaluationStep.execution_mode` typed). **Guidance/docs** rewritten:
  `step-config.md`, `evaluation-plan.md`, `message-sequence.md`,
  `optimize-playbook.md`, `workflow-tools.md`, `stores.md`,
  `pulse-fixer-practices.md`, `ops-review.md`, `improve-evaluation.md`,
  `agent_go/docs/learn_code_flow.md`, `instructions.go`, the
  `evaluation-plan` guidance description, the `create_workflow` /
  `update_scripted_step` / `add_scripted_step` tool descriptions.

### Known window (documented, not guarded)

Between deploying half 2 and a workflow reaching 1.0.38, a `regular` step
that has **no** `declared_execution_mode` key at all (pre-1.0.38 that meant
legacy agentic; post-1.0.39 it means scripted) runs through the scripted
executor. That is not a hard failure — with no saved `main.py` the scripted
path is the code-execution agent that authors and saves one — but it is a
behavior change for such a step on a *manual* run before the Builder's
preflight has migrated the workflow (scheduled runs are gated). Every
workflow whose regular steps were created by `add_scripted_step` /
`create_plan` carries the key and is unaffected. The original plan's "plan
validation flags a regular step with no `main.py`" guard was **not** added:
it would break the learn-on-first-run path that a fresh scripted step
legitimately uses.

## Verification

- Half 1: `declared_execution_mode_migration_test.go` (mixed plan; PLAT-280
  drift repair with the declaration preserved; refusal that changes
  nothing; clean-workflow no-op; `IsPlanModificationTool`).
- Half 2: `declared_execution_mode_strip_test.go` (keys removed, other
  fields kept, mode+reason in the changelog, idempotent; refusal naming the
  legacy step and the v1.0.38 tool with `step_config.json` untouched;
  declared-scripted evaluation step marked on the plan and both evaluation
  entries stripped; `IsPlanModificationTool`); `change_step_type_tool_test.go`
  rewritten for "type decides" (a plain regular step → scripted is a no-op;
  a legacy agentic regular step → scripted clears the key and turns code
  execution on; to message_sequence clears `lock_code`);
  `regular_message_sequence_compat_test.go` (update_scripted_step never
  converts a sequence; a regular step with no key is scripted; the legacy
  shim still upgrades through update_message_sequence_step);
  `controller_agent_factory_test.go` (new `isScripted` argument);
  `pkg/orchestrator/types` e2e fixtures (the scripted fast path with no
  declaration; the control is now a legacy-agentic step the shim holds off
  the scripted path; the id-mismatch case inverted — the regular step runs
  scripted, the phantom id writes nothing); contract-ladder tests +1 rung,
  `TestToolSetInvariants`, "Pending migrations (16)";
  `utils/__tests__/stepConfigMatching.test.ts` (7). `go build ./...`,
  `go test` for `step_based_workflow`, `cmd/server`, `types`,
  `videoproduct` green; `npx tsc -b` clean.
- Not yet done: running both upgrades on a real workflow (Dominion's
  `tectonicusadaytrading` first, after the pending `dominion-agent`
  restart), then the final cleanup — delete the two `Legacy…` fields and
  `isLegacyAgenticRegularStep` once every live workflow is on 1.0.39.
