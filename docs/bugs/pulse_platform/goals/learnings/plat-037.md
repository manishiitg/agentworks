[← platform / learnings-knowledge](index.md)

# PLAT-037 — learning freshness ledger assigns out-of-band edits to the next step

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | goals |
| Area | learnings |
| Summary | shared learnings freshness-ledger writer |

| Coordination | Value |
|---|---|
| Assigned agent | `Unassigned` |
| Ticket state | `new` |
| Last synchronized | `2026-08-05` |

- **Priority:** P2
- **Owner:** shared learnings freshness-ledger writer
- **Source workflow:** `Workflow/tectonicusadaytrading`
- **Source finding:** `PUL-D00AE7BD`

## Problem

`learnings/_global/_freshness.json` attributes an edit made outside a workflow
step to whichever step next writes its freshness tick. In the August 5 Tectonicus
Pulse pass, the Fixer edited a delivery reference and script directly; the next
workflow run would credit that repair to a step that did not make it.

## Impact

Learning contribution history becomes false. Pulse can misread a platform or
Fixer repair as step-generated learning, then make incorrect retention,
quality, or ownership decisions.

## Required fix

Record the actual writer identity and mutation source at write time. A step may
claim only its own authenticated write; background Fixer/platform/user edits
must be represented as their own source or explicitly out-of-band. Do not infer
authorship from the next freshness tick.

## Acceptance

One step write and one out-of-band write to the same learning package retain
their distinct writers; a later step tick cannot change the out-of-band entry's
owner; and Pulse's learning-health evidence shows the recorded source rather
than a guessed step.
