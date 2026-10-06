[← goals / pulse / findings](index.md)

# PLAT-213 — `harness:record_pulse_result` (ICICI-BANK-PARSING): already fixed by PLAT-206, same code path, different trigger

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | pulse/findings |
| Summary | P3 — no live gap. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `resolved` — correction/closure record only, no new code; already fixed by PLAT-206 |
| Last synchronized | `2026-08-29` |

- **Priority:** P3 — no live gap. Confirms an already-shipped fix covers a
  second, independently-filed instance of the same defect, on a different
  workflow.
- **Owner:** N/A — no code change made or needed by this ticket.
- **Related:** [PLAT-206](plat-206.md) — the fix this confirms already
  covers this finding. `harness:record_pulse_result` (`Workflow/ICICI-BANK-PARSING`,
  high, first seen 2026-08-11) is the finding this closes.

## The finding

A live Pulse pass on `Workflow/ICICI-BANK-PARSING` called
`record_pulse_result(module=workflow_review, result=done, reason=probe)` as
a throwaway/exploratory first call — it succeeded. Minutes later, in the
same conversation, a second call with a complete, correct payload
(`changed_files`, `verification`, 8 real `finding_dispositions`) was
rejected: *"Pulse module 'workflow_review' for run '...' is already
terminal or belongs to another run."* The real review outcome could never
be recorded; only the throwaway `done`/`probe` text stuck as the module's
terminal reason for that run.

## Same mechanism PLAT-206 already fixed, different trigger

PLAT-206 (filed against a confida-login split reviewer/Fixer finding) fixed
`markPulseModuleResultFromAgentWithAuditAndFindings`'s retry guard, which
previously only accepted a second `record_pulse_result` call for an
already-terminal `(module, pulse_run_id)` when its `result` string exactly
matched the first call's. This finding is the identical code path hit by a
different real-world trigger: not a separately-dispatched Fixer, but the
*same session* correcting its own premature call. The shapes are
identical either way — a first call (`result=done`, no real evidence) and a
second, genuinely different call (`result=changed`, real
`changed_files`/`verification`/`finding_dispositions`) — which is exactly
what PLAT-206's relaxed guard (`existing.LastResult == result ||
len(dispositions) > 0`) accepts.

Verified directly rather than assumed: wrote a scratch test reproducing
this finding's exact reported shape (probe `done` call, then a `changed`
call with 8-equivalent real dispositions) against the current code —
passed cleanly. Not committed as a permanent test since PLAT-206's own
`TestRecordPulseResultAcceptsFixerSupplementalDispositionsAfterReviewerTerminal`
already exercises the identical code path and would regress identically if
this behavior ever broke again.

## Explicitly not done

- No code change — PLAT-206, already shipped, covers this.
- Did not add a workflow-specific (ICICI-BANK-PARSING) permanent test —
  the mechanism is workflow-agnostic and PLAT-206's existing test already
  pins it.

## Verification

- Scratch reproduction of this finding's exact reported call sequence
  passed against current code (`go test`, then deleted — not a permanent
  addition, see above).
- `go build ./...` clean; no files changed by this ticket.
