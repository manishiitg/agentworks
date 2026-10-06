[← goals / evaluation](index.md)

# PLAT-056 — prevalidation repair-loop recorder files a durable concern for iterations the same attempt already superseded

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | evaluation |
| Summary | prevalidation / `__automatic_final_validation__` repair-loop concern recording |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; current code and regression tests verified locally` |
| Last synchronized | `2026-09-09` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.

- **Priority:** P2
- **Owner:** prevalidation / `__automatic_final_validation__` repair-loop concern recording
- **Source workflow:** Instagram (`Workflow/instagram/db/db.sqlite`) — all 11 of
  its `external_action_required` findings share this one root cause; not 11
  separate defects.
- **Found via:** `scripts/pulse_health.py --section untriaged` (added this
  session to sweep every workflow for `external_action_required` findings not
  yet linked to a register ticket).

## Problem

Every message_sequence step gets a synthetic final `prevalidation` gate
(`__automatic_final_validation__`, `controller_message_sequence.go:462`)
enforcing its top-level `validation_schema`. When that gate fails, each failed
check is recorded (`pulse_finding_events`, `event_type='filed'` /
`'filed:identity_merge:*'`) and rolled into a durable `run_concerns` row. The
repair loop then retries within the **same attempt** — but nothing reconciles
the already-filed concern against the attempt's eventual terminal result. If a
later iteration fixes the failure and the step's terminal `pre_validation.json`
records `overall_pass=true`, the concern from the earlier failing iteration is
never resolved or suppressed — it just sits as an open finding forever.

Pulse itself already diagnosed this correctly for all 11 instagram instances on
2026-08-04 (`run_concerns.resolved_by='pulse'`), e.g. for `route-score`
(fingerprint `cc08ab6cfee867b9`, 19 consolidated field-level failures):

> *"Not a defect in the delivered output. The 19 consolidated field-level gate
> failures were raised by an INTERMEDIATE iteration of the platform's
> `__automatic_final_validation__` repair loop... the loop then converged
> inside the SAME attempt and the step's terminal `pre_validation.json`...
> records `overall_pass=true`, 32/32 checks passed, failed_checks=0... No
> workflow tool can stop the recorder from persisting a pre-repair iteration as
> a durable open concern, and suppressing it in the plan would only silence
> detection."*

Every one of the 11 (`route-critique-topic` through `route-critique-design-plan`,
1 to 54 consolidated checks each) carries the identical disposition. Pulse
correctly recognized it has no workflow-level lever and marked these
`external_action_required` — but no platform ticket existed to receive that
escalation until this sweep found it, so it has sat inert since 2026-08-04.

## Impact

- A workflow with a healthy, self-correcting repair loop (steps that pass on
  iteration 2 or 3 look identical in `run_concerns` to a step that never
  passes) accumulates permanent noise in its findings list, one row per step
  that ever needed a repair iteration, undermining trust in the backlog.
- Real recurring instances of the same underlying signal are counted as
  distinct `fingerprint`s per step, inflating the `external_action_required`
  count workflow-wide without adding real information — Pulse already said as
  much: *"a defect in the delivered output"* is explicitly ruled out.
- The genuinely useful residual signal here — instagram needed 34 repair
  retries across 12 steps in one run — is real operational cost/quality data,
  but it's the wrong shape for `run_concerns` (per-check, per-iteration, no
  attempt-level rollup) and the diagnosis note itself says this belongs to
  `llm_ops_review`/`cost_llm_time`, not workflow correctness review.

## Acceptance boundary

The repair-loop recorder must not leave a durable open concern for a
prevalidation failure that a later iteration inside the **same attempt**
superseded with a passing terminal result. Two viable shapes (implementer's
call, both keep the detection Pulse's note says must not be silenced):

1. Defer filing until the attempt's terminal validation result is known —
   only persist a `run_concerns` row if the attempt's own final iteration
   still fails, or
2. File per-iteration as today, but auto-resolve/suppress the row (not delete
   — keep the event history) once the same attempt's terminal
   `pre_validation.json` shows the check passing, with a distinct resolution
   reason distinguishing "self-healed within attempt" from "resolved by a
   Fixer."

Either way, do **not** silently drop the repair-count signal itself — the
number of retries a step needed to satisfy its own contract is legitimate
operational evidence and should stay queryable (e.g. surfaced to
`llm_ops_review`/`cost_llm_time`), just not as a standing `workflow_review`
concern once the attempt converged.

## Verification

Focused test: a message_sequence step whose first iteration fails
`__automatic_final_validation__` and whose second iteration (same attempt)
passes must not leave a `run_concerns` row in `external_action_required` (or
any open, unresolved) state after the attempt completes. Runtime reverify:
confirm instagram's next producing run does not add a 12th instance of this
same pattern, and that the 11 existing rows can be safely closed once the
recorder stops filing pre-repair iterations as durable concerns (they are
already correctly dispositioned by Pulse — no re-diagnosis needed, only the
platform fix and closure).


## 2026-09-09 local Pulse audit follow-up

This ticket's open status was stale. `SavePreValidationLog` already persists
validation evidence without calling the concern recorder. Existing
`pre_validation_concern_test.go` tests cover failed validation logs and confirm
that no durable concern is filed automatically. No duplicate recorder change
was made; the obsolete source comment was corrected. Historic concern cleanup
and review disposition are separate from whether the recorder exists today.
