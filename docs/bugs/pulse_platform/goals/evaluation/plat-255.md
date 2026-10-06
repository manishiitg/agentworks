[← goals / evaluation](index.md)

# PLAT-255 — Pre-validation failures were silent to the builder until all retries were exhausted (or invisible if a later retry recovered); rename the misleadingly-named prompt-engineering skill

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | evaluation |
| Summary | P2 — operator-requested improvement, not a regression; wanted |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-30` |

- **Priority:** P2 — operator-requested improvement, not a regression; wanted
  so the builder can catch a genuine bug vs. a transient/schedule issue while
  a step is still retrying, not only after it eventually fails outright.
- **Owner:**
  `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`,
  `controller_todo_task.go`, `controller_message_sequence.go`,
  `cmd/server/guidance/templates/system/running-steps.md`,
  `cmd/server/guidance/guidance.go`,
  `cmd/server/guidance/templates/system/plan-design.md`,
  `cmd/server/guidance/templates/system/prompt-engineering.md` → renamed
  `step-description.md`,
  `pkg/orchestrator/agents/workflow/step_based_workflow/planning_agent.go`.

## What happened

**Pre-validation notification gap.** A step's pre-validation gate (structural
file/DB checks, run after each execution attempt) only ever produced a
`[AUTO-NOTIFICATION]` to the builder chat once — from `OnExecutionComplete`,
fired in a `defer` *after the entire retry loop returns* (up to 3 attempts).
A pre-validation failure on attempt 1 that self-recovers on attempt 2 never
notified at all; a failure that persists through all 3 attempts only
notified once the whole step gave up, with generic "completed — status=failed"
wording that didn't call out "this was a pre-validation failure" as a
distinct category. The operator wanted early visibility — enough to
investigate whether a failure is a real bug or a transient/environmental
issue (schedule drift, a stale saved script) while the step is still
retrying, not only after every attempt is burned.

Separately, `controller_message_sequence.go`'s per-*item* pre-validation gate
inside a `message_sequence` was already deliberately silenced (a prior,
documented decision): announcing it cost a full synthetic LLM turn and
produced a second near-identical "step finished" message per item, deemed
not worth it for a deterministic Go-side check. The operator explicitly
asked to re-enable this too, accepting that cost.

**Misleadingly-named skill.** `cmd/server/guidance/templates/system/
prompt-engineering.md`'s entire content is about writing an optimized step
`description` and `validation_schema` — "prompt engineering" reads as
general LLM-prompting advice, not discoverable when an agent (or a human)
is specifically looking for "how do I write a good step description." The
name predates this ticket; renamed while adding new hints that reference it,
since a rename touching only 2 files (`guidance.go`, `plan-design.md`) was
low-risk to do at the same time rather than leave the confusing name in place.

While wiring a hint to this skill into `add_*_step`/`update_*_step` tool
responses, found the hint itself would have been broken as first written:
`get_workflow_command_guidance(kind="step-description")` — this tool only
resolves against `allKinds` (procedural "guided flows" like `design-plan`/
`ops-review`/`goal-advisor`), a genuinely separate registry from
`referenceKinds` (`step-description`, `file-layout`, `stores`, etc.), which
is never exposed as a callable tool at all — only materialized to disk for
native-CLI `read_skill` reading. Caught before shipping; the correct call is
`read_skill(skills=[{"name":"builder-reference","path":"references/
step-description.md"}])`, matching the pattern `plan-design.md` already used
for the same skill.

## Fix

- `controller_execution.go` (regular/`AI Agent Task` steps) and
  `controller_todo_task.go` (`todo_task` steps): fire an
  `[AUTO-NOTIFICATION]` via `hcpo.workshopExecutionNotifier` on the **first**
  pre-validation failure per step per run — not every retry attempt, to
  avoid spamming a step that recovers on its next attempt — using the same
  start+immediate-complete pattern
  `startMessageSequenceItemNotification`/`completeMessageSequenceItemNotification`
  already established for message_sequence items.
- `controller_message_sequence.go`: removed the `item.Synthetic` early-return
  that silenced the appended final-validation gate's per-item notification;
  author-declared prevalidation items already got notifications, this just
  stops treating the appended gate differently.
- `running-steps.md` (the guidance skill the builder agent itself reads):
  added a section explaining pre-validation failures now notify separately,
  mid-run, and that the step may still succeed on a later retry — so the
  builder agent correctly treats it as an early heads-up, not a final
  outcome, when interpreting the notification.
- Renamed `prompt-engineering` → `step-description` (`guidance.go`'s
  `referenceKinds` map key, the template file itself, and both references in
  `plan-design.md`). `materialize.go` derives the on-disk
  `references/<kind>.md` filename directly from the map key, so no other
  code needed updating.
- Added a "Description & schema quality" bullet to both
  `buildAddedStepArtifactSetupNotice` (every new step, unconditionally) and
  `buildPlanStepDependentArtifactReviewNotice` (only when the edit actually
  touched `description` or `validation_schema`) in `planning_agent.go`,
  pointing at `read_skill(skills=[{"name":"builder-reference","path":
  "references/step-description.md"}])` — the verified-correct call, not the
  initially-wrong `get_workflow_command_guidance` one.

## Explicitly not done

- Did not add per-attempt (as opposed to per-step) pre-validation
  notifications — deliberately once per step per run, to keep this a useful
  early-warning signal rather than retry-attempt spam.
- Did not touch the `run-basic-smoke`-style scripted fast-path's own internal
  pre-validation failure (its own function, separate from the LLM-retry loop
  covered here) — a fast-path failure falls through into the LLM-driven
  retry loop this ticket does cover, so a failure that also fails there still
  gets notified; a fast-path failure that's immediately fixed by the LLM
  fallback does not get its own separate notification.

## Verification

- `go build ./...` clean; `go vet` clean (only pre-existing, unrelated
  issues); full `pkg/orchestrator/agents/workflow/step_based_workflow` and
  `cmd/server/guidance` test suites pass (0 failures), including
  `TestReferenceKindsAllRenderable/step-description` and
  `TestWorkshopPromptMovedSectionsAreReferencedNotInlined`.
- The broken `get_workflow_command_guidance(kind="step-description")` call
  was caught and corrected before being shipped, by tracing the actual
  registered-tool handler code (`RegisterGuidanceTool` in `guidance.go`) and
  confirming it validates against `allKinds`, not `referenceKinds` — not
  assumed from the tool's name.

## Reverify

Trigger a pre-validation failure on a regular or `todo_task` step (e.g. a
deliberately-wrong `validation_schema` for one attempt) and confirm a
distinct `[AUTO-NOTIFICATION]` arrives before the step's own retries are
exhausted, separate from its eventual completion notification. Add or edit a
step's `description`/`validation_schema` via `add_scripted_step`/
`update_message_sequence_step`/etc. and confirm the tool's response includes
the `read_skill(...)` hint, and that calling it actually returns
`step-description.md`'s content (not an "unknown kind" error).

## 2026-09-17 — Notification delivery ownership integration

The [PLAT-106 notification-ownership follow-up](../../platform/frontend-chat/plat-106.md#notification-ownership-follow-up--2026-09-17) clarifies the recipient in this ticket's references to notifying “the builder.” An early pre-validation failure belongs to the session that launched the step. Chat-launched work may notify that chat; a schedule/trigger-launched step must notify its execution session, not an unrelated Builder chat viewing the same workflow.

The operator's decision to enable early failure notices and per-item notices is preserved. This follow-up adds shared registration/delivery ownership checks, not a change to notice timing, retry count, or suppression policy. Add a concurrent schedule + Chat case to live re-verification, and ensure chat-launched early notices still arrive. No new producer-specific live verification is claimed.

Follow-up status: **implemented and tested locally; not deployed; runtime re-verification pending**. PLAT-106 remains the canonical implementation/test record. This note does not close the original ticket or change its assigned agent.
