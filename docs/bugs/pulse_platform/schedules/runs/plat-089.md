[← platform / scheduler-runs](index.md)

# PLAT-089 — grouped runs leave previous-run attempt logs in the active evidence folder

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | schedules |
| Area | runs |
| Summary | grouped-run artifact cleanup and execution-evidence identity |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-11` |

- **Priority:** P0 evidence integrity
- **Owner:** grouped-run artifact cleanup and execution-evidence identity
- **Source workflow:** Instagram, scheduled run
  `schedule-manual--bae435e5_1786432476119732000`
- **Also reproduced in:** RTS Latency grouped runs

## Confirmed evidence

The active Instagram folder
`runs/iteration-0/test-run/logs/step-create-reel/execution/` contains attempt
logs from three different execution generations:

```text
2026-06-30  execution-attempt-1-iteration-{1,2}.json
2026-07-17  execution-attempt-{2,3}-iteration-0.json
2026-08-11  execution-attempt-1-iteration-0.json
```

The current 11 August run had one successful attempt: its current
`pre_validation.json` reports `overall_pass:true`, `execution_attempt:1`, and
`validation_phase:"final-gate"`. A reviewer nevertheless described attempts
1, 2, and 3 as belonging to this run because the June/July files still occupy
the same active evidence namespace.

A read-only tree scan found the same mixed-generation pattern in ten active
step-log directories across Instagram and RTS Latency. RTS Latency examples
contain July and 11 August attempt files side by side under
`runs/iteration-0/dev/logs/.../execution/`.

## Root cause

Partial/group runs deliberately reuse `iteration-0`. Their live artifacts are
written below a group segment such as:

```text
runs/iteration-0/test-run/logs/...
runs/iteration-0/dev/logs/...
```

The cleanup helpers construct their paths from only `selectedRunFolder`:

```text
runs/<selectedRunFolder>/execution
runs/<selectedRunFolder>/logs
```

They omit the active group path. Cleanup therefore targets
`runs/iteration-0/logs/...`, while execution writes to
`runs/iteration-0/<group>/logs/...`. The new run overwrites the attempt-1
filename it happens to produce, but obsolete attempt-2/3 and higher-iteration
files survive indefinitely.

## Impact

- Pulse and reviewers can confidently report failures that occurred weeks
  earlier as failures of the current run.
- A current passing run can be classified as retried, failed, or unstable.
- Fix verification and recurrence counts become contaminated by unrelated
  execution generations.
- Workflow prompts that inspect the active folder can skip work or choose a
  recovery path based on stale artifacts.

This is more severe than untidy retention: the active evidence boundary no
longer identifies one run.

## Required fix

Use the canonical concrete run root, including the group segment, for every
cleanup/archive operation. On a fresh group/step invocation, archive or clear
the complete active attempt-log namespace before attempt 1 is written.

Add a second integrity boundary: stamp every execution and validation record
with the producing run/execution ID, and make Pulse/evidence readers reject
records whose ID does not match the run being reviewed. Cleanup prevents the
normal failure; identity filtering prevents a missed cleanup from silently
corrupting conclusions.

Historical logs may be retained under an explicit archive path. They must not
remain discoverable as current-run evidence.

## Acceptance

- Seed a grouped step with old attempts 1–3, then start a new invocation that
  produces only attempt 1. The active evidence view returns only the new
  attempt; old files are available only under an archive path.
- Cleanup operates on `runs/iteration-0/<group>/...`, not its parent.
- Every attempt, timing, conversation, pre-validation, and final-validation
  record carries the same current execution ID.
- Pulse cannot cite a mismatched historical attempt as evidence for the
  current run, even when such a file is deliberately placed in the tree.
- Cover both a grouped todo-task step (Instagram `test-run`) and a grouped
  regular/message-sequence step (RTS Latency `dev`).


## 2026-09-09 local Pulse audit follow-up

Local sales/workflow evidence shows newer outputs under older failed run
metadata. Preserve the existing open scope: attempt-specific immutable logs and
provenance are needed before Pulse can reliably attribute a retry's outcome.
See [PLAT-047](plat-047.md). Advisory detector wording in PLAT-163 reduces false
severity, but does not repair contaminated history or establish recovery.


## 2026-09-10 improvement-system dependency

[PLAT-305](../../goals/pulse-governance/plat-305.md)) implements the first review/scheduling/research/lifecycle
release. This ticket's remaining foundation acceptance is still required and is
not closed by adding Architecture or outcome tracking. Historical evidence and
legacy workflow behavior remain unchanged by that release.
