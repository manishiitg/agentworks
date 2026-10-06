[← goals / human-decisions](index.md)

# PLAT-077 — human-input answer/dismiss had no concurrent-writer guard; one harness finding split invisibly across two fingerprints

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | goals |
| Area | human-decisions |
| Summary | P2 — data-integrity gap in the human-input lifecycle and the |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — both fixes shipped and tested; runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P2 — data-integrity gap in the human-input lifecycle and the
  pulse finding-identity migration; not currently blocking a workflow, but
  produces misleading loop-closure reports and un-mergeable duplicate findings
- **Owner:** report-human-input lifecycle (`cmd/server/report_human_inputs.go`)
  and pulse finding identity migration (`pulse_finding_lifecycle.go`)
- **Found on:** PLAT-073 Cluster I — `cf457bdd` (upwork), `7602e2ac`
  (social-media), `f2cbf9a1` (rtslatency). `bed0388b` (upwork) was
  investigated as part of the same cluster and found already fixed (commit
  `b541b520d`, predates the finding) — no action needed beyond live reverify.

## Defect 1 — `answerReportHumanInput`/`dismissReportHumanInput` had no status guard in their `UPDATE`

`cf457bdd`: *"report_human_inputs leaves applied, superseded, partially
applied, and still-actionable answers in `answered` state, so Pulse keeps
treating them all as unapplied."* `7602e2ac`: *"loop_closure reported four
`answer_not_applied` findings... two of them were applied over two weeks ago
and carry both `consumed_at` and an `outcome_summary`."*

**Root cause**: a live-observed impossible state — a row with `status='answered'`
that also carries `consumed_at`/`outcome_summary`, which the three functions
that ever write those columns should make structurally unreachable. Found it:
`answerReportHumanInput` (`report_human_inputs.go:505-509`, pre-fix) and
`dismissReportHumanInput` (`:561-565`, pre-fix) issued their `UPDATE` with
`WHERE id=? AND workspace_path=?` — no status condition at all. A single
package-level `sync.Mutex` serializes these functions within one process, but
not against a second writer in another process — and the chat/schedule
concurrency contract this platform already documents (PLAT-040) means two
processes legitimately can write to the same workspace's SQLite db at once.
A late/duplicate answer or dismiss call arriving after the row was already
consumed would silently revert `status` back to `answered` while leaving the
prior `consumed_at`/`outcome_summary` untouched — exactly the state
`7602e2ac` observed through `loop_closure`, which only reads `status` and has
no way to distinguish "genuinely still answered" from "answered-again after
being consumed."

`consumeReportHumanInput` already had a status-scoped `WHERE`
(`status IN ('answered','claimed')`), but never checked `RowsAffected()` — a
losing writer in the same race would report success for a write that changed
nothing.

## Fix

`cmd/server/report_human_inputs.go` — all three transition functions
(`answerReportHumanInput`, `dismissReportHumanInput`, `consumeReportHumanInput`)
now:
1. Add a status guard to the `UPDATE`'s `WHERE` clause matching the states
   each function's own precondition check already allows (`answer`: not
   `consumed`/`dismissed`/`claimed`; `dismiss`: not `consumed`/`claimed`;
   `consume`: unchanged, already correct).
2. Check `RowsAffected()` and return an explicit "consumed/dismissed/claimed
   by another writer" error when it's zero, instead of proceeding to write an
   audit event for a row that was never actually changed.

Tests: `cmd/server/plat073_human_input_concurrent_writer_test.go` — a late
answer after consumption is rejected and the row stays consumed
(`TestConsumedHumanInputRejectsLateAnswer`), same for a late dismiss
(`TestConsumedHumanInputRejectsLateDismiss`), and a second `consume` call is
rejected rather than silently reporting success
(`TestDoubleConsumeRejectsSecondCall`).

## Defect 2 — `migrateDuplicatePulseFindingIdentities` only merges by `finding_id`, missing the harness-specific `target_key` identity

`f2cbf9a1`: *"the backlog returns HARNESS-REFDOC-REVIEW-ARTIFACT-DRIFT under
two internal fingerprints (`dfeaacf06b8317ec` and `b32e117e1f4ac80b`) with the
same finding_id, so closing one leaves the other open... No lifecycle tool
available to a Fixer can merge two fingerprints."*

**Root cause**: verified against the live rtslatency DB — neither fingerprint
actually had a `finding_id` set at all (both empty), so the existing
migration (`migrateDuplicatePulseFindingIdentities`,
`pulse_finding_lifecycle.go:658-700`), which groups duplicates only by
non-empty `finding_id`, could never see them as the same finding. The two
rows' real shared identity was `target_key`, which the migration never
consulted. A merge tool (`merge_pulse_issues`) already exists but wasn't
reachable/used by whatever role encountered the split — this fix addresses
the migration side; the tool-scoping gap is unresolved and out of scope here.

## Fix

`pulse_finding_lifecycle.go` — `migrateDuplicatePulseFindingIdentities` now
also groups rows by `target_key` when `finding_id` is empty, scoped to
`issue_kind = IssueKindHarness` only (so a coincidental `target_key` match
between unrelated non-harness findings is never merged — harness findings are
the only kind where `target_key` is a genuine shared-identity signal). Reuses
the existing `mergePulseIdentityGroup` merge logic unchanged.

Tests added to `pulse_finding_identity_test.go`:
`TestFindingIdentityMigrationMergesHarnessTwinsByTargetKeyWhenFindingIDIsEmpty`
(two harness rows, same `target_key`, both empty `finding_id` → merge into
one `run_concerns` row, `seen_count` summed) and
`TestFindingIdentityMigrationDoesNotMergeNonHarnessRowsByTargetKey` (negative
guard: two `workflow_issue` rows sharing a `target_key` must stay separate).

## Not fixed here

- `merge_pulse_issues` tool-scoping/reachability — the migration fix above
  handles automatic reconciliation on the next schema-ensure pass, but the
  underlying gap ("a Fixer with no accessible merge tool cannot act
  immediately") is unaddressed.
- The `superseded` human-input status idea (an operator files a new decision
  that replaces an old, still-`answered` one) — noted during investigation,
  not requested by either finding, left as a future consideration.

## Verification

- `go build ./...` clean.
- New/updated tests pass:
  `go test ./cmd/server/... -run 'TestConsumedHumanInput|TestDoubleConsume|TestReportHumanInput'`,
  `go test ./pkg/orchestrator/agents/workflow/step_based_workflow/... -run 'TestFindingIdentityMigration'`.
- Full baseline (`go test ./cmd/server/... ./pkg/orchestrator/agents/workflow/step_based_workflow/...`)
  still shows exactly 22 pre-existing failures — no new failures.
- **Not yet reverified live** — requires a restart and a real run exercising
  answer/dismiss/consume under genuine concurrency, and a harness-finding
  split, before `cf457bdd`/`7602e2ac`/`f2cbf9a1` can be closed with
  `pulse_close_stale.py`. `bed0388b` can be closed on reverify alone (no code
  change was needed).

## Acceptance

- A human-input row that has been consumed can never be silently reverted to
  `answered` by a late/duplicate answer or dismiss call; the caller gets an
  explicit error instead.
- A duplicate `consume` call is rejected rather than reporting false success.
- Two harness-finding rows sharing a `target_key` but no `finding_id` merge
  into one on the next schema-ensure pass; non-harness rows sharing a
  `target_key` do not.
