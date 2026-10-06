[← goals / pulse-governance](index.md)

## Internal reconciliation — 2026-09-05

PUL-28FC41E1: the exact step-origin/no-owning-module update fixture TestPulseFindingIssueIDUpdateReloadsExistingStepFindingAcrossReviewerModule passes. This is narrower evidence than a claim that every intermittent success-without-persistence report is fixed; PUL-2F70A97F stays open.

Resolved in SQLite for internal tracking with previous concern/detail records
preserved in resolution events. Source/tests verified; deployed replay and
historical business/module-result repair are not claimed. Full mapping:
[remaining-report audit](../../../../audits/platform-open-report-reconciliation-2026-09-05.md).

# PLAT-214 — `record_pulse_finding` no longer reloads through a separate connection; live confirmation of the intermittent failure remains pending

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | goals |
| Area | pulse-governance |
| Summary | P2 — an agent cannot trust the tool's own success/failure |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed pending live confirmation` |
| Last synchronized | `2026-08-29` |

- **Priority:** P2 — an agent cannot trust the tool's own success/failure
  signal for issue-updating calls, per the finding's own impact note: *"This
  risks either duplicate/redundant reattempts... or, worse, an agent
  concluding a real repair/finding was never recorded and skipping
  downstream steps that depended on it."*
- **Owner:** `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/pulse_finding_details.go`
  (`RecordPulseReviewFinding`).
- **Related:** `harness:record_pulse_finding` (`Workflow/ICICI-BANK-PARSING`,
  medium) — the finding this fixes.

## The finding

`record_pulse_finding` calls updating an **existing** `issue_id`
intermittently returned `success:false` with *"recorded Pulse finding could
not be reloaded by its internal lifecycle identity,"* even though the
underlying write had actually succeeded — confirmed durably visible in a
`get_pulse_state(view="backlog")` read moments later. 3 of 7 structurally
identical calls in the same session failed this way; the other 4 succeeded
cleanly with no error.

## Eliminated failure-prone path — confirmed via code read

`RecordPulseReviewFinding` opens one database handle (`db`) at the top of
the function and performs every write on it. Its final step — reloading the
just-written finding to confirm the write landed and return the current
`issue_id`/status — called the *public* `LoadPulseFindingLifecycles`, which
opens its **own, separate** database connection (`openRunConcernsDB`) rather
than reusing `db`. That avoidable cross-handle reload is consistent with the
intermittent symptom, but the observed run does not conclusively prove a
specific SQLite visibility or locking race.

## Fix

Replaced the `LoadPulseFindingLifecycles(ctx, workspacePath, "", -1)` call
(a full, sorted, cross-joined backlog scan on a brand-new connection — far
more than this check actually needs) with a direct, targeted
`SELECT issue_id, status FROM run_concerns WHERE fingerprint=?` on the
**same** `db` handle the write itself used. Reading back on the exact
connection that performed the write removes the separate-handle reload
window by construction — SQLite guarantees a connection sees its own
committed writes — and is simpler and cheaper than the query it replaced,
since this check only ever needed the one row it was about to confirm, not
the whole backlog.

## Explicitly not done

- Did not change `LoadPulseFindingLifecycles` itself, or any of its other
  call sites — it remains correct for its actual purpose (a sorted,
  cross-joined backlog view for reviewers/UI), just not reused here where a
  same-connection point lookup is both more correct and cheaper.
- Did not write a new test attempting to force the former separate-connection
  timing failure directly — it is not something a sequential
  single-goroutine unit test can reliably reproduce. Relied instead on the existing
  `TestPulseFindingIssueIDUpdateReloadsExistingStepFindingAcrossReviewerModule`
  (which already exercises the exact "update an existing issue_id" shape
  end-to-end) continuing to pass as confirmation the replacement query is
  behaviorally correct.

## Verification

- `go build ./...` clean.
- `TestPulseFindingIssueIDUpdateReloadsExistingStepFindingAcrossReviewerModule`,
  `TestPulseFindingIssueIDUpdatesOneRootCauseAndMergePreservesDuplicateHistory`,
  `TestWorkflowObservationBecomesIssueOnlyWhenReviewerPromotesIt`, and the
  two `TestPulseFindingLifecycleCloses*` tests all pass unchanged — the
  exact suite most likely to catch a regression in this reload path.
- Full `pkg/orchestrator/agents/workflow/step_based_workflow` suite passes.
- Full suite: 3 pre-existing failures before and after (`cmd/server/guidance`,
  unrelated content), no regression.
- Not yet reverified live. A real recurrence (or its absence) on
  ICICI-BANK-PARSING's next Pulse pass is the eventual confirmation; until
  then, do not state the former separate-handle path as conclusively proven
  to be the exact cause.
