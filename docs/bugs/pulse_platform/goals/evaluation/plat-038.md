[← goals / evaluation](index.md)

# PLAT-038 — pre-validation evidence overwrites earlier attempts in a run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | evaluation |
| Summary | shared step-validation artifact writer |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-05` |

- **Priority:** P1
- **Owner:** shared step-validation artifact writer
- **Source workflow:** not identifiable from the retained workspace artifacts
- **Source finding:** `PUL-5AFFB236`
- **Affected step:** `read-credentials`

## Problem

`SavePreValidationLog` always writes the same path:
`runs/<run>/.../logs/<step>/pre_validation.json`. Each validation attempt
replaces the previous result. The code documents this behavior explicitly.

The finding correctly identifies the operational consequence: when an early
attempt fails and a later repair passes, Pulse cannot inspect the original
failed checks from the run artifacts. The durable `run_concerns` row preserves
a deduplicated cross-run summary, but it does not retain the complete failed
check list, schema, and attempt order needed for one-run root-cause analysis.

The finding's comparison is inaccurate: message-sequence steps use the same
`SavePreValidationLog` path and deliberately overwrite it too. This is a
shared validation-recording defect, not a scripted-step-only defect.

## Required fix

Keep the existing `pre_validation.json` as the compact latest-result pointer
for compatibility, and additionally retain every attempt in the run-local log
folder. Prefer deterministic names such as
`pre_validation_attempt_001.json`, `pre_validation_attempt_002.json`, or a
single append-only structured history. Each record must include attempt number,
timestamp, pass/fail, failed checks, schema, step ID/path, run/group, and the
execution mode when known.

Thread the explicit attempt number through regular/scripted and
message-sequence validation callers; do not infer it from timestamps or from
existing files. Retention is bounded by each execution mode's existing retry
limit, so it does not create unbounded artifact growth.

## Acceptance

1. A scripted step that fails validation and then passes retains both complete
   result records for that run, while `pre_validation.json` remains the final
   result.
2. The same is true for a message-sequence repair loop.
3. A later run cannot overwrite any prior run's history.
4. Pulse can link a transient validation finding to the exact failed-attempt
   artifact without parsing terminal text.
5. Existing consumers of `pre_validation.json` remain compatible.

## Evidence and scope

The unavailable historical `read-credentials` failure cannot be reconstructed
truthfully after its first artifact was replaced. The reporter's pasted card
did not identify a workflow, and the current retained workspaces no longer
contain that step, so this ticket deliberately does not invent one. The repair
is forward-only. This ticket is distinct from the lifecycle finding
consolidation: one lifecycle issue per step is correct, but it must retain the
detailed attempt evidence behind that issue.

## Implementation — 2026-08-05

Implemented in the shared writer and all execution-mode callers:

- `PreValidationLogEntry` now records run/group, execution mode, validation
  phase, execution attempt, and validation attempt.
- `SavePreValidationLog` continues to update
  `logs/<step>/pre_validation.json` for existing readers, and first writes an
  immutable sibling named
  `pre_validation_<phase>_execution_N_attempt_M.json`.
- Regular/agentic, saved-script, scripted repair-loop, todo-task, and
  message-sequence callers provide explicit identity. Message-sequence item ID
  is part of the phase, so two gates in one step cannot collide.
- The scripted repair loop now records its initial and every repair validation
  invocation, including transient failures that previously had no persisted
  record at all.

Focused verification passed:

```text
go test ./agent_go/pkg/orchestrator/agents/workflow/step_based_workflow \
  -run 'TestSavePreValidationLog' -count=1
```

`TestSavePreValidationLogRetainsEveryScriptedAndMessageSequenceAttempt` proves
both a failed first attempt and a passing repair remain readable while the
legacy latest-result file points to the pass. A producing workflow run is still
needed to close the historical finding; its erased first attempt cannot be
backfilled honestly.
