[← goals / evaluation](index.md)

# PLAT-243 — A real eval step's own scored output could be misclassified as a validation-schema echo and its score silently discarded

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | goals |
| Area | evaluation |
| Summary | harness_issue, severity high. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity high.
- **Findings:** LinkedIn `PUL-90D1E2C9` — the shared evaluation-report
  aggregator lost a valid `6/10` `eval-strategy-loop` score:
  `context_output.json` correctly held `score=6, max_score=10` with
  passing pre-validation, but `evaluation_report.json` recorded
  `score=0, score_captured=false, "No output_content found for this
  step"`.

## Root cause, confirmed against real, currently-live data

Read the actual live files, not just the finding's evidence text:
`workspace-docs/Workflow/linkedin/evaluation/runs/iteration-0/default/execution/eval-strategy-loop/context_output.json`
genuinely exists, genuinely has `score:6, max_score:10`, and the current
`evaluation_report.json` for that step genuinely still shows
`score_captured:false` — this is a live, current bug, not stale.

`context_output.json`'s own top-level keys include `score`, `max_score`,
`reasoning`, `evidence`, `checks`, `checks_passed`, `checks_failed`,
`summary`, **and `files`** — this eval step's real output legitimately
self-documents which checks it satisfied via a nested `files` array shaped
exactly like a validation schema (`[{"file_name": "context_output.json",
"must_exist": true, "json_checks": [...]}]`).

`isValidationSchemaLikeJSON` (`evaluation_execution.go`) exists to skip a
candidate file that is actually a validation-schema stub, not real step
output — but its check is purely structural: any document with a `files`
array where every entry has `file_name` + `json_checks` is classified as a
schema echo, with no check for whether the document also contains real
scored content. `eval-strategy-loop`'s own real `context_output.json`
trips this heuristic on its nested `files` field, gets classified as a
schema echo, and `extractEvalVerdictFromOutputContent` is never called for
it — silently discarding a real, valid `6/10` score.

## Fix

Added one guard at the top of `isValidationSchemaLikeJSON`: a document
with its own top-level `"score"` key is never treated as a
validation-schema echo, regardless of what else it nests. A genuine
`validation_schema.json` stub never scores itself — only real step output
does — so this is a precise, low-risk discriminator that doesn't touch the
function's original purpose (still correctly rejects a bare schema stub
with no `score` field).

## Verification

New test
`TestIsValidationSchemaLikeJSONAcceptsRealOutputThatEmbedsASchemaShapedFilesField`
reproduces the exact linkedin shape (score + max_score + reasoning +
evidence + checks + a schema-shaped nested `files` array) and confirms it
is no longer misclassified. The pre-existing
`TestIsValidationSchemaLikeJSON` (bare schema stub still detected; plain
result JSON still accepted) continues to pass. `go build ./...` and
`go test ./pkg/orchestrator/agents/workflow/step_based_workflow/... -run
TestIsValidationSchemaLikeJSON` both pass.

Two unrelated test failures were observed in the same package
(`TestWorkshopCLIPromptUsesProjectedWorkspaceToolReference`,
`TestPhaseChatWorkshopSelectsWorkspaceToolGuidanceByTransport`) — traced to
a concurrent, unrelated, in-progress session's uncommitted edit to
`agent_go/pkg/instructions/workspace_special_tools.go` (a `search_web_llm`
feature change stripping the projected workspace-media-tools reference),
confirmed via `git diff` on that file. Not caused by, or related to, this
fix.

## Reverify

No live evaluation run has exercised this fix through the deployed server
yet. Reverify by confirming a future `eval-strategy-loop` evaluation (or
any eval step whose real output nests a schema-shaped `files` field)
correctly captures its score in `evaluation_report.json` instead of
reporting `score_captured=false`.
