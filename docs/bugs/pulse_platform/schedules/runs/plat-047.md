[← platform / scheduler-runs](index.md)

# PLAT-047 — grouped execution artifacts lack an immutable physical run identity

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | schedules |
| Area | runs |
| Summary | run-folder retention and execution identity |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implementation_in_progress` — forward identity/revision binding implemented; immutable partial-run history still pending |
| Last synchronized | `2026-08-28` |

- **Priority:** P1
- **Owner:** run-folder retention and execution identity
- **Source workflow:** Upwork
- **Also reproduced in:** Social Media (`PUL-DB292C50`, where Pulse treated
  normal `iteration-0` use as contamination while inspecting retained
  `iteration-293` evidence).

## Problem

Grouped work can reuse a logical folder such as `iteration-0` across producing
runs. SQLite lifecycle rows and cost records can retain durable execution IDs,
but physical review evidence under the reused folder may be overwritten or
become ambiguous. This is distinct from PLAT-031: that ticket fixed cost-ledger
identity across archive rotation; it did not make every run artifact immutable.

The ambiguity also crosses plan versions. Current run metadata has no
`plan_revision` or `plan_hash`, so a retained `iteration-N` cannot be bound to
the executable plan, step configuration, and validation contracts that
produced it. Pulse can only infer from timestamps and current plan text. That
is how an old routing contract and the current routing contract became one
misleading critical finding in `PUL-DB292C50`.

## Required design

Choose one immutable physical artifact identity per producing execution, keep a
small latest pointer for compatibility, and link findings/reviews to that
identity. Migration and retention limits must be defined before changing folder
layout. No implementation is claimed by the 2026-08-07 reviewer fixes.

The design must also provide two platform-owned records:

1. `Workflow/<workflow>/runs/run_index.json`, atomically maintained by
   `rotatePairedIterationZero`, identifying the active slot, retained folders,
   last completed rotation, and full-run versus partial-group reuse semantics.
2. A content-addressed executable plan revision under
   `planning/revisions/plan-<digest>.json`. Every new `run_metadata.json` binds
   to that revision before execution. The canonical bundle includes the plan,
   step configuration, validation contracts, and every other configuration
   surface that can change step behavior.

Retaining or rotating a run must preserve both its immutable execution identity
and plan-revision reference. Historical runs without proof are
`unknown_legacy`; timestamps must never be used to guess a revision.

Pulse deterministic intake must expose the normalized identity/revision facts.
Reviewers trust those facts and do not infer current/retained status or plan
identity from `iteration-N` names, timestamps, or current plan text.

## Expanded acceptance (2026-08-28)

- A full run rotates the previous active slot and publishes a consistent run
  index; a partial-group run is explicitly represented as reuse of the active
  slot.
- Pulse can resolve any new retained run to its immutable execution identity
  and exact plan revision.
- Repeated runs of an unchanged plan share one revision; a behaviorally
  relevant plan/config/validation mutation creates a different revision.
- Legacy retained runs remain visibly unknown rather than receiving inferred
  revision assignments.
- Re-reviewing `PUL-DB292C50` cannot classify ordinary use of `iteration-0` as
  contamination. It must isolate the actual missing-selection fail-closed
  verification boundary or close the stale claim.

## Relationship to PLAT-197

[PLAT-197](../../goals/plans-contracts/plat-197.md)) starts after this ticket's identity boundary. It owns
who initiated a plan mutation, why, which dependent surfaces were reconciled,
and which later impact evidence is linked. PLAT-047 owns which plan/run actually
produced the evidence PLAT-197 evaluates.

## Implementation status — 2026-08-28

Implemented for new executions:

- full-run rotation and partial-group reuse atomically publish
  `runs/run_index.json` with explicit active/retained lifecycle and policy;
- every producing group receives a new `run-<nanoseconds>` execution identity;
- execution now fails before the group starts if its `run_metadata.json` cannot
  be bound to a content-addressed executable plan revision;
- revisions cover `workflow.json`, the plan, planning step config, evaluation
  plan, and evaluation step config, and are reused when canonical content is
  unchanged; and
- deterministic Pulse runtime intake exposes the run index, lifecycle role,
  execution identity, and plan revision. Legacy evidence is explicitly
  `unknown_legacy` instead of receiving a timestamp-based guess.

Still open before this ticket can close:

- a rerun of the same group during partial-group reuse still replaces that
  group's mutable artifact directory. The new identity prevents provenance
  ambiguity for the current contents, but it does not retain every historical
  partial execution under its own physical path;
- every lower-level artifact/receipt is not yet independently stamped and
  rejected when it contradicts the owning `run_metadata.json`; and
- `PUL-DB292C50` needs a live re-review after deployment to verify that the
  reviewer consumes the deterministic identity facts instead of the stale
  iteration-name inference.


## 2026-09-09 local Pulse audit follow-up

Fresh local evidence still exposes overwritten/mixed partial-run provenance:
partial groups reuse `iteration-0`, while newer output files can coexist with
older failed metadata. Execution IDs and plan revision references already help
identify evidence, but do not make physical partial-attempt artifacts immutable.
This remaining acceptance criterion stays open. Link [PLAT-089](plat-089.md)
for stale attempt-log contamination; do not infer recovery from an outer status
or promote a later artifact into proof for an earlier execution.

Social Media additionally lost retained evidence with retention set to 3.
[PLAT-304](../../sandbox/security/plat-304.md)) fixes the authorized retention setting's write denial;
it does not fix partial-run overwrites or reconstruct already-pruned evidence.


## 2026-09-10 improvement-system dependency

[PLAT-305](../../goals/pulse-governance/plat-305.md)) implements the first review/scheduling/research/lifecycle
release. This ticket's remaining foundation acceptance is still required and is
not closed by adding Architecture or outcome tracking. Historical evidence and
legacy workflow behavior remain unchanged by that release.

## 2026-09-14 scheduled-run identity follow-up

[PLAT-320](plat-320.md) owns the compatible storage migration that reserves
`iteration-0` for Builder/manual execution and gives each saved-schedule
occurrence an immutable `iteration-N-sched` folder before any workflow tool
call. It also owns propagation into direct `execute_step`, evaluation, logs,
retention, APIs/UI, AgentWorks guidance and Pulse intake.

PLAT-047 remains the parent immutable-physical-identity requirement and retains
the unresolved Builder/partial-run evidence criteria. PLAT-320 must not close
PLAT-047 merely by isolating scheduled folders: lower-level evidence identity
and the mutable Builder compatibility path must also remain truthful.
