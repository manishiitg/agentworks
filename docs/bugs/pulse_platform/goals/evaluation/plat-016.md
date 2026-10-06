[← goals / evaluation](index.md)

# PLAT-016 — evaluation report serialization drops a real zero score

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | evaluation result/report serializer |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** evaluation result/report serializer
- **Source finding:** `HARNESS-EVAL-REPORT-ZERO-SCORE`
- **Source database:** `Workflow/social-media/db/db.sqlite`
- **Recorded state:** `external_action_required`
- **Problem:** a real numeric score of `0` is treated like an absent value and
  omitted from `evaluation_report.json`.
- **Impact:** the worst valid evaluation result can disappear, making reports,
  trend calculations, and Pulse evidence falsely incomplete or healthier than
  the producing evaluator actually reported.
- **Implementation:** workflow and server report projections serialize Score
  without `omitempty`; `score_captured` distinguishes a real zero from a
  missing score. Score/MaxScore use `float64`, so a valid 7.5 is no longer
  truncated to 7. SQLite persists the captured bit, and the evaluation UI
  renders only captured values as score badges. The server keeps this bit
  nullable so a historical report where it did not exist is not rewritten as
  explicit false and hidden by the UI.
- **Verification (2026-08-04):** focused Go tests cover missing, skipped, real
  zero, and fractional extraction plus JSON/SQLite/API projection. Frontend
  tests cover new and legacy reports and prove `0/10` and `7.5/10` display while
  missing/skipped scores remain hidden. TypeScript compilation passes.
- **Regression tests:** `TestExtractEvalVerdictPreservesFractionalScore`,
  `TestEvaluationStepScoreSerializesGenuineZeroScore`,
  `TestEvaluationReportAPIProjectionPreservesZeroAndFractionalScores`, and the
  frontend `evaluationReport.test.ts` suite.
- **Current state:** implementation fixed; runtime reverify remains.
- **Acceptance:** table-driven serialization covers `0`, positive, negative,
  fractional, `null`, unavailable, and missing values. Numeric zero survives
  unchanged through persisted result, report JSON, API projection, and UI.
