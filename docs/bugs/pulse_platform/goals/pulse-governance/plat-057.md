[← goals / pulse-governance](index.md)

# PLAT-057 — a harness_issue could be parked in the workflow's own engineering queue

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse finding lifecycle — disposition/status coherence |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-09` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P2
- **Owner:** Pulse finding lifecycle — disposition/status coherence
- **Source workflow:** Upwork (2 findings); found by `scripts/pulse_health.py --section coherence`

## Problem

A finding's `issue_kind` and its `status` are written by two different tools at
two different times, and neither consulted the other:

- **`issue_kind`** — set once at filing, by `record_pulse_finding`
  (`validateTypedPulseReviewFinding`, `pulse_finding_details.go`) or
  `record_run_concern` (`validateStepRunConcern`, `run_concern_tool.go`). Both
  validate the value in isolation and never touch status.
- **`status`** — set later and repeatedly by `record_pulse_result`, mapped from
  a free `disposition` enum through `lifecycleStatusForDisposition`
  (`pulse_finding_lifecycle.go`), a pure lookup table that never read
  `issue_kind`.

So `issue_kind=harness_issue` + `status=queued_for_engineering` was writable,
and is self-contradictory: `harness_issue` means no workflow-level repair can
fix the boundary, while `queued_for_engineering` means a workflow-level
Engineering Review pass will fix it. The finding is therefore re-read as
actionable by every later pass, consumes a reviewer slot rediscovering that it
is not, and is re-deferred — permanently, since nothing in that loop can
resolve it.

## Scope — deliberately narrow

Cross-workflow census at the time of the fix: **48 harness findings, of which 38
(79%) were already correctly `external_action_required`.** Only **2** were
incoherent, both in Upwork:

| Fingerprint | Target |
|---|---|
| `dd9ede3c` | `agent_browser:snapshot` — snapshot results overflow with no pagination recipe |
| `b0a88d49` | `builder-reference:references/post-run-monitor.md` — mandated reference doc not bundled |

The remaining 8 are **legitimate and still allowed**: `resolved` (6 — the
behaviour stopped or was disproven), `acknowledged` (1 — recovered by retry),
`awaiting_verification` (1 — a workflow-side workaround was applied). Only the
`queued_for_engineering` pairing is rejected. This is explicitly *not* a claim
that harness findings must always escalate.

## Implementation (2026-08-09)

- `IssueKindWorkflow` / `IssueKindHarness` promoted to named constants in
  `pulse_finding_details.go`; the two validators previously compared bare string
  literals, which would have become three copies with this check added.
- The coherence check lives in `RecordPulseFindingDispositionsTx`
  (`pulse_finding_lifecycle.go`), not in `validateFindingDisposition` — the
  latter is a pure function with no DB access. It follows the shape of the
  `isPulseAdvisorModule` routing block directly above it, which already loads a
  finding's stored detail in the same transaction and rejects dispositions that
  contradict its `recommended_route`.
- The lookup runs **only** when the disposition is `queued_for_engineering`, so
  the common path costs nothing, and reads the `issue_kind` **column** (one
  indexed PK lookup, no JSON decode).
- `sql.ErrNoRows` is tolerated: plain `CONCERNS:` findings have no
  `pulse_finding_details` row at all, so no `issue_kind` was ever claimed and
  there is nothing to contradict.
- **Rejects rather than coerces.** A correct `external_action_required` also
  requires `external_owner`, `reason_code`, and `reopen_condition` — values only
  the reviewer can supply, which coercion would have to invent. The rejection
  names both exits (escalate with `external_owner="platform"`, or re-file as
  `workflow_issue` if the workflow does own the failure).

## Existing drifted rows — reported, not migrated

The 2 rows are **not** backfilled. A migration cannot invent the
owner/reason/reopen fields a correct escalation requires, and rewriting
historical dispositions without evidence is the kind of silent state change this
register exists to avoid. They are surfaced by
`scripts/pulse_health.py --section coherence` and need re-dispositioning by
Pulse or by hand.

## Regression tests

`pulse_finding_issue_kind_coherence_test.go` — four cases, each pinning a
distinct behaviour:

1. `TestHarnessIssueCannotBeQueuedForEngineering` — rejected, and the message
   names both exits.
2. `TestHarnessIssueStillReachesExternalActionRequired` — the escape hatch the
   rejection points at actually works.
3. `TestWorkflowIssueQueuesForEngineeringUnchanged` — **the important one.**
   `queued_for_engineering` is the most common disposition in the system (75
   events in Upwork alone) and must be untouched.
4. `TestUntypedConcernQueuesForEngineeringUnchanged` — plain `CONCERNS:`
   findings, which carry no details row, still queue normally.

Full-package run after the change reproduced the known 22-failure baseline
exactly (19 `guidance`, 1 `virtual-tools`, 2 `step_based_workflow`, all from the
in-flight Pulse-v2 refactor) with no new failures.

## Acceptance

A finding whose boundary the workflow does not own cannot be parked in the
workflow's own repair queue. Runtime reverify: after the next producing runs,
`scripts/pulse_health.py --section coherence` should report no *new* drift, and
the 2 pre-existing rows should be re-dispositioned rather than re-deferred.
