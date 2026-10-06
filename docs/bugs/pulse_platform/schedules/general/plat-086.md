[← schedules / general](index.md)

# PLAT-086 — schedule execution models were indistinguishable and agents chose blindly

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | general |
| Summary | P1 design integrity — both route-backed schedules and direct |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — choice-aware contract migration runs on the next workflow preflight |
| Last synchronized | `2026-08-11` |

- **Priority:** P1 design integrity — both route-backed schedules and direct
  workshop sequences are valid, but the platform did not explain their different
  lifecycle guarantees or record why one was chosen.
- **Observed on:** `Workflow/build-in-public`. Its Daily X Draft schedule contains
  19,955 characters of standalone procedure and the weekly LinkedIn schedule
  contains 11,701. Both use variable group `default` even though the plan has
  explicit route branches.

## The two valid execution models

**Route-backed schedule:** `group_names` and `route_selections` invoke canonical
plan steps. The work receives normal step learnings, validation/retry, repair,
and Pulse attribution. This is the default for durable, reusable workflow
behavior.

**Direct message sequence:** the scheduler sends an ordered workshop
conversation. This is useful for genuinely schedule-specific context or
orchestration, but the direct turns are not canonical steps: their step-level
learnings, validation/retry, repair, and Pulse attribution are weaker or absent.

Neither model is inherently wrong. The bug was making them look equivalent.

## Root cause

`WorkflowSchedule.Messages` was an unrestricted workshop-message queue. Builder
guidance encouraged explicit schedule messages and even copied backup work into
them. The schedule schema had groups but no durable way to pass the existing
`run_full_workflow(route_selections=...)` contract. The natural result was an
entire mini-workflow placed in free text.

This is not merely a token problem. In build-in-public, the existing `x` route
can post while the schedule's pasted procedure is deliberately draft-only.
Pointing the schedule at the closest route without first creating a safe
draft-only route would change external behavior.

## Fix

Workflow contract `1.0.24` added `workflow.json.schedules[].route_selections`,
the canonical map passed to `run_full_workflow`. The corrected choice-aware
contract is `1.0.25`; it adds `direct_messages_reason` for intentional direct sequences. When a route-backed
schedule has no free-text message, the scheduler produces the compact
full-workflow trigger and includes that map.

Builder guidance now asks the agent to choose deliberately:

1. Prefer a route for durable/reusable behavior and preserve exact side effects,
   approvals, outputs, failure behavior, variables, and groups.
2. Keep a direct sequence when the conversation is genuinely schedule-specific,
   and record why its weaker step lifecycle is acceptable.
3. Never convert based only on prompt length or apparent cost.

The `→ 1.0.25` agentic upgrade audits each legacy message and chooses
one of three outcomes: reuse an exactly equivalent route, create the missing
canonical route for durable behavior, or retain the sequence with an explicit
rationale. Ambiguity no longer causes a guessed conversion: the agent keeps the
direct sequence and records the tradeoff. This prevents a blind conversion of a
draft-only schedule into a posting route.

## Pulse ownership

Artifact/Engineering Review flags undocumented direct sequences and proven
duplication/drift, not direct messages merely for existing. Ops Review may
report measured token/time impact, but must not choose a route from runtime cost
evidence alone.

## Verification boundary

Focused source coverage should prove that an empty routed schedule generates a
`run_full_workflow` message with its declared map, a procedure-like direct queue
requires `direct_messages_reason`, and the same queue is accepted once that
rationale is supplied. Live proof is a build-in-public upgrade in which each X
and LinkedIn schedule is classified from behavior: safe draft-only routes are
created where the procedure is durable, while any genuinely schedule-specific
conversation remains direct with an explicit rationale.

## Follow-up 2026-08-12 — the message validator rejected prose

`scheduleMessagesNeedExplicitReason` matched its procedure markers as bare
substrings against the message text. That cannot separate embedded SQL and shell
from ordinary instructions:

- `"update "` matched *"…update the GitHub issue status…"*
- `"git "` matches inside *"digit "*
- `"select "` / `"insert "` match any sentence using those verbs
- `"step 1"` matched *"…the failing step 1 more time…"*

confida-login's "GitHub Issue Reconciliation" schedule could not be saved:

```
tool=update_schedule: direct schedule messages are supported, but this queue
needs direct_messages_reason because messages[1] contains procedure marker "update "
```

Anchoring to the start of a line does not fix it either — an imperative English
sentence opens with its verb.

**Fix.** Match on shape rather than on the verb alone: `SELECT … FROM`,
`INSERT INTO`, `UPDATE … SET`, `DELETE FROM`, `git <subcommand>`, and
line-opening numbered steps. Unambiguous markers (`sqlite3 `, `curl `,
`execute_step(`, `notify_user`, `backup/`) stay as plain substrings. Covered both
directions in `workflow_schedule_contract_markers_test.go`.

This does not change the `nonEmptyCount > 1` rule: a multi-message schedule is a
direct sequence and still needs `direct_messages_reason`. All three confida
schedules do — they now fail with that true reason instead of a spurious marker.

## Follow-up — route selection was accidentally suppressed by a retained message (2026-08-21)

The implementation still treated these two saved fields as alternatives:

- `route_selections`: the canonical choice of workflow path; and
- `messages`: optional schedule-specific follow-up conversation.

`scheduledWorkshopMessages` generated the canonical `run_full_workflow(...,
route_selections=...)` instruction only when `messages` was empty. A
route-backed schedule with even one retained message therefore sent only that
message. Social Media's Daily Execution message began “After the selected work
completes …”, but the selected `execution` route was never explicitly started.
The builder had to guess, and could spend a run on evaluation/backup prose with
no new execution evidence.

### Corrected contract

1. A saved route selection is always emitted as the first canonical schedule
   turn.
2. Retained messages, when justified, run afterwards as follow-ups.
3. Normal-run finalization (backup, report publish, notification) remains
   server-owned; dedicated `pulse_review_only` owns Gate/Review/Fixer. Generic
   copied lifecycle tails are removed from normal schedules.
4. Contract `1.0.29` performs the agentic cleanup while preserving genuine
   time-bound direct procedures (for example Techtonic's close-only pass).

### Immediate repair and proof

- Removed the stale lifecycle-only messages from Social Media's three
  route-backed schedules.
- Removed the redundant manual backup turn from Sales Outreach's two lead-run
  schedules; the server finalizer performs it.
- Added `TestScheduledWorkshopMessagesRunsRouteBeforeCustomFollowUp`, proving a
  route-backed schedule produces the route turn first and retains the follow-up
  second.
- Focused scheduler contract tests pass. Live verification: run a Social Media
  daily execution after server restart and confirm the first tool call is
  `run_full_workflow` with `step-run-mode-router=execution`, with one finalizer
  backup path only.
