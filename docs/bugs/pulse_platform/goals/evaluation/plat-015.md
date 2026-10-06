[← goals / evaluation](index.md)

# PLAT-015 — evaluation harness mishandles skipped-result sentinels

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | evaluation execution/result collection |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** evaluation execution/result collection
- **Source finding:** `HARNESS-EVAL-RESULTS-SKIPPED-SENTINEL`
- **Source database:** `Workflow/social-media/db/db.sqlite`
- **Recorded state:** `external_action_required`
- **Problem:** the evaluation harness's skipped-result sentinel behavior does
  not preserve the workflow evaluation contract. A workflow evaluation-plan
  edit cannot repair how the shared harness recognizes and serializes a
  skipped result.
- **Impact:** a legitimately unavailable or skipped evaluation can be
  misclassified, dropped, or made indistinguishable from a malformed result.
- **Implementation:** current evaluation filtering emits one explicit
  `EvaluationStepScore` with `skipped=true`, an applicability reason, and
  evidence for each route-gated non-applicable step. The report retains those
  rows alongside active step scores.
- **Verification (2026-08-04):** focused tests now exercise the real route-log
  read, prove a non-applicable step becomes an explicit JSON sentinel with
  `skipped=true`, and prove SQLite retains `skipped=1` with
  `score_captured=0`. Social Media still needs a producing evaluation run to
  close the source finding.
- **Regression tests:** `TestFilterEvaluationPlanEmitsExplicitSkippedSentinel`
  and `TestPersistEvalResultsKeepsSkippedSentinelDistinct`.
- **Acceptance:** explicit completed, skipped, unavailable, and failed fixtures
  retain distinct terminal states through execution, persistence, report
  projection, and Pulse review. A skipped sentinel cannot become success or
  disappear.
