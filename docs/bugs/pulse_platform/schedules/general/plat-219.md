[← schedules / general](index.md)

## SQLite backlog reconciled — 2026-09-05

Resolved PUL-5D2B9495 in the corresponding workflow databases after checking
the existing implementation and passing focused regression tests. Full evidence
and the complete remaining inventory: [reconciliation audit](../../../../audits/platform-backlog-reconciliation-2026-09-05.md).
This is internal tracking closure, not a claim of a new deployed end-to-end run.
Previous SQLite records are retained in audit events; unrelated findings remain
open. No business data or historical schedule outcome was rewritten.

# PLAT-219 — scheduled turns now honor a linked full run’s durable failure instead of waiting hours for a missing completion callback

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | schedules |
| Area | general |
| Summary | P0 — a failed workflow remained projected as running for about |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** P0 — a failed workflow remained projected as running for about
  59 minutes and was finally mislabeled `interrupted: server restarted`.
- **Owner:** scheduled conversation-turn lifecycle and full-workflow launch
  tracking.
- **Related:** Sales Outreach `PUL-5D2B9495`. LinkedIn `PUL-3565D07C` is an
  older stale-runtime projection and is not claimed as the same root cause.

## Reproduction

Sales Outreach schedule `633c499d` started the Dubai full run at 10:31:30Z.
The run’s authoritative `runs/iteration-0/dubai-real-estate/run_metadata.json`
recorded `status=failed` and `completed_at=10:44:12Z`. The wrapping schedule
remained running until a server restart at 11:43:12Z, then recorded the restart
rather than the already-durable workflow failure.

`waitForConversationTurnTree` treated any registered running child as proof of
live work and reset its ten-minute inactivity clock repeatedly, up to the
three-hour live-child ceiling. A missed full-run completion callback therefore
overrode the run’s own terminal record.

## Fix

- Full-run launch records now include `execution_type`, iteration, group and
  exact `run_folder` immediately, rather than only adding those fields if the
  completion callback eventually arrives.
- The tracked execution preserves that launch metadata and run folder.
- When the root conversation turn is complete, a linked child still says
  running, and no progress has occurred for 30 seconds, the waiter checks that
  exact run’s `run_metadata.json` every five seconds.
- An explicit durable `failed` status ends the turn with the workflow failure.
  Missing/unreadable/running metadata fails open, and `completed` is not
  force-settled because a successful run may still legitimately need its
  completion notification to resume follow-up work.

## Verification

Focused tests prove launch metadata survives into the tracked execution and a
durable failed descendant is detected while a completed descendant is not
misclassified:

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow -run 'TestRunFullWorkflow|TestWorkshopExecution' -count=1
go test ./cmd/server -run 'TestConversationTurn|TestDurableFailedWorkflowDescendant|TestWorkshopExecutionWithoutExplicitParent' -count=1
```

The next real failed scheduled full run is required to confirm the schedule
closes with the workflow failure without a restart.
