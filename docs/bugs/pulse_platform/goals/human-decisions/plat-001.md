[← goals / human-decisions](index.md)

## Internal reconciliation — 2026-09-05

PUL-7B849CC6: keyed-input parser, unknown-ID, sibling/map isolation, explicit-input precedence and opening-prompt tests pass on current source.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-001 — `run_full_workflow` drops keyed human input

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | goals |
| Area | human-decisions |
| Summary | workflow orchestration/handoff |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P0
- **Owner:** workflow orchestration/handoff
- **Source finding:** `HARNESS-RUN-FULL-WORKFLOW-HUMAN-INPUT-LOSS`
- **Source database:** `Workflow/upwork/db/db.sqlite`
- **Recorded state:** `external_action_required`, severity `high`
- **Problem:** a human-input override supplied to `run_full_workflow` did not
  appear in the target child step's opening prompt.
- **Impact:** run-specific safety or scope constraints can be silently ignored
  while the workflow continues and reports success.
- **Evidence:** the saved scheduler conversation contains the two-feed
  override; the child `search-find-and-shortlist` session does not; its raw
  artifact shows that `most_recent` was scraped anyway.
- **Implementation (2026-08-03):** `run_full_workflow` now parses
  `human_inputs` strictly, rejects malformed and unknown step IDs before
  launch, copies the map into immutable execution/batch contexts, and derives
  one isolated context per dispatched step. The keyed value reaches regular,
  message-sequence, todo, and human-input steps without leaking to siblings;
  an explicit `execute_step(human_input=...)` remains higher priority.
- **Verification:** focused tests cover both MCP-decoded map shapes, fail-closed
  parsing, unknown IDs, two distinct child values, sibling isolation, map-copy
  isolation, and explicit single-step precedence. A real scheduled full-run
  replay remains required before closing the Upwork finding.
- **Current workaround:** compare each producing child's prompt and raw
  provenance with the requested override.
- **Acceptance:** an E2E passes distinct keyed values to at least two child
  steps and proves each child sees only its intended value; missing or unknown
  keys fail before execution.
