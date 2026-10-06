[← goals / pulse / findings](index.md)

# PLAT-206 — a split-dispatch Fixer's supplemental result was rejected as "already terminal," leaving real repairs stuck at queued_for_engineering forever

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | pulse/findings |
| Summary | Upwork `PUL-565F9ED1` and `PUL-E19F5F1B` were resolved for internal tracking after |

## 2026-09-05 reconciliation and correction follow-up

Upwork `PUL-565F9ED1` and `PUL-E19F5F1B` were resolved for internal tracking after
current supplemental-disposition and parent-run receipt tests passed. The first
report also contained the obsolete child-session review-log gate; that gate was
removed in a8aaaa012. Prior SQLite concern/detail rows are retained in audit events.

The original implementation narrative below is historical: the local PLAT-258
follow-up now updates module state and audit together and preserves previous
receipts, rather than leaving the original module result inconsistent with the
later audit. New changes are not claimed deployed; no old workflow repair was
reapplied and no old module result was rewritten by this reconciliation.

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — real applied repairs permanently mislabelled as
  unrepaired work, with a durable, correctness-relevant consequence: "Gate
  reads status to build the active queue, so the next pass sees three
  repaired items as unrepaired work and may re-attempt fixes already
  applied — the exact repeated-repair waste the verify-before-discover rule
  exists to prevent" (the finding's own impact assessment).
- **Owner:** `agent_go/cmd/server/pulse_worklist.go`
  (`markPulseModuleResultFromAgentWithAuditAndFindings`).
- **Related:** `pulse:module_result:split-reviewer-fixer-ordering`
  (confida-login, medium) — the finding this fixes. Distinct from PLAT-198
  (Pulse's broader review/repair/run lifecycle simplification, landed the
  same day this investigation started via a concurrent main-branch rebase)
  — checked whether that already-shipped work covered this specific
  mechanism before touching anything; it did not. **Also checked against
  PLAT-199** (landed the same rebase): PLAT-199 replaced the *automatic
  scheduled* review+repair flow with one retained session per its own
  acceptance criteria — *"the scheduler no longer launches a separate
  Fixer stage or fresh Fixer background agent"* — so the split-dispatch
  scenario this ticket fixes no longer occurs on that primary path. This
  fix is not made moot by that, though: PLAT-199's own doc explicitly
  keeps `/pulse-fixer` available "for repair-only recovery against an
  already reviewed queue," and `scheduler.go`'s "PRE-RUN TARGETED FIXER"
  dispatch (triggered on an approved operator decision) is a second live
  path that also runs a standalone Fixer against a queue a prior review
  pass already terminaled. Both hit exactly the mechanism this ticket
  fixes; neither is a legacy-only, unreachable code path.

## The finding

Pulse's split reviewer/Fixer dispatch contract runs review and repair as
separate background agents. On this run: the reviewer wrote
`record_pulse_result(module=technical_review, result=done)`, terminaling
the module for that `pulse_run_id`. The Fixer then applied three real
repairs to `planning/plan.json` and tried to record them via
`record_pulse_result(module=technical_review, result=changed,
finding_dispositions=[...])` — rejected: *"Pulse module 'technical_review'
for run '...' is already terminal or belongs to another run."* The Fixer's
own completion note flagged the mechanism and fell back to
`record_pulse_finding` (evidence-only, no status mutation) instead. Three
findings (`PUL-6E530150`, `PUL-24D383EA`, `PUL-8DDD5284`) — independently
confirmed repaired and re-read as correct — stayed at
`status=queued_for_engineering` with no `next_check`, invisible as resolved
to Gate's active-queue logic.

## Root cause — confirmed still-live via code read, not assumed fixed

Checked first whether PLAT-198's concurrent "Simplify Pulse review, repair,
and workflow run lifecycle" work (landed the same day via a main-branch
rebase) already covered this — it targets a different layer (making
`changed_unverified` resolve an issue immediately, decoupled from needing a
later verification pass) and does not touch the module-level
`record_pulse_result` retry gate this finding hit.

`markPulseModuleResultFromAgentWithAuditAndFindings`'s retry branch (reached
whenever the module is already terminal for the run) only accepted a second
call when its `result` string **exactly matched** the already-recorded one
(`existing.LastResult == result`). The split contract legitimately produces
*different* result values for the same run — reviewer `done`, Fixer
`changed` — so the Fixer's call always fell through to the terminal
rejection, every time, by design of the existing guard rather than by
accident.

## Fix

Relaxed the retry condition from `existing.LastResult == result` to
`existing.LastResult == result || len(dispositions) > 0`. A same-run,
same-result call (a completion-turn replay) behaves exactly as before. A
same-run call with a genuinely different result but real
`finding_dispositions` now succeeds too — this is the split
reviewer/Fixer contract working as designed, not a conflict to reject.
`result=changed` already *requires* non-empty `finding_dispositions` at the
schema-validation layer above this function, so a `result=changed` Fixer
call always qualifies for the relaxed path; a call with a different result
and zero dispositions (carrying no new evidence) still falls through to the
original rejection, unchanged.

The retry path writes `finding_dispositions` (the load-bearing,
Gate-visible record) via the existing `RecordPulseFindingDispositionsTx`
call, and writes an audit entry via `recordPulseModuleAudit` — but does
**not** re-write the module's own `last_result`/`last_result_reason`
columns, which stay the reviewer's original terminal verdict rather than
being overwritten by the Fixer's later, different result.

## A secondary property surfaced by testing, not introduced by this fix

`pulse_module_audit` is keyed unique on `(workspace_path, module,
pulse_run_id)` — it upserts, it is not an append log. Before this fix, a
second write into this retry path never happened for a mismatched result
(it was rejected first), so this was latent and unexercised for the split
scenario. Now that the Fixer's write does reach `recordPulseModuleAudit`,
its `result`/`reason`/`evidence` fields upsert over the reviewer's own
audit snapshot for that one row — the reviewer's original audit detail is
not separately preserved at the module-audit-table level. The **load-bearing
record for Gate's decisions is the FINDING's own lifecycle**
(`RecordPulseFindingDispositionsTx`'s target), which is genuinely
append-only and correctly preserves both the reviewer's original finding
and the Fixer's resolving attempt as distinct history — verified directly
in the new test. Changing the module-audit table's uniqueness to also
preserve every individual write is a separate, smaller-priority
improvement, deliberately not made here.

## Explicitly not done

- Did not change `pulse_module_audit`'s schema/uniqueness to preserve every
  individual write (see above) — the finding's own stated impact is about
  Gate's active queue reading finding status, which this fix resolves
  directly; the module-audit collapse is a lesser, separate concern.
- Did not touch PLAT-198's broader lifecycle work — confirmed it targets a
  different, non-overlapping layer before making any change here.

## Verification

- `go build ./...` clean.
- New `TestRecordPulseResultAcceptsFixerSupplementalDispositionsAfterReviewerTerminal`
  reproduces the exact confida-login shape (reviewer `done`, then a
  separately-dispatched Fixer `changed` call with real
  `finding_dispositions` for the same run) and proves: the Fixer's call now
  succeeds, the module's own terminal result stays the reviewer's `done`
  (not overwritten), and the finding's lifecycle correctly resolves with
  both the reviewer's finding and the Fixer's verified attempt preserved.
- New `TestRecordPulseResultStillRejectsMismatchedResultWithNoDispositions`
  proves the relaxed guard did not become a general-purpose way to
  overwrite an already-terminal module — a mismatched result with zero
  dispositions is still rejected exactly as before.
- Full pre-existing `record_pulse_result`/`mark_pulse_module_result` test
  suite passes unchanged.
- Full suite: 3 pre-existing failures before and after this change
  (`cmd/server/guidance`, unrelated content), no regression.
