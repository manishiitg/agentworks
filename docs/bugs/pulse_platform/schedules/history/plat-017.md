[← schedules / history](index.md)

# PLAT-017 — scheduler success leaves durable workflow metadata running

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | history |
| Summary | scheduler/workflow terminal-state persistence and reconciliation |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented locally; focused verification passed; live acceptance pending` |
| Last synchronized | `2026-09-17` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** scheduler/workflow terminal-state persistence and reconciliation
- **Source findings:** `HARNESS-PULSE-RUN-STATUS-MISMATCH` and
  `HARNESS-SCHEDULER-CHILD-STATUS`
- **Source database:** `Workflow/social-media/db/db.sqlite`
- **Recorded state:** `external_action_required`; the two IDs describe one
  terminal-state boundary and must not become two repair projects
- **Problem:** the discovery children completed and the scheduler reported
  success, while `runs/iteration-0/default/run_metadata.json` remained
  `status=running`.
- **Distinction from PLAT-004:** PLAT-004 prevented success while required work
  was still running. PLAT-017 concerns stale durable workflow metadata after
  genuine completion.
- **Impact:** Pulse and later consumers cannot choose one authoritative run
  status; the same completed run can appear successful in schedule history and
  active/incomplete in workflow evidence.
- **Current state:** canonical workflow-outcome implementation is complete
  locally. Focused verification passes; live scheduled-run acceptance and
  deployment remain.
- **Acceptance:** after one completed, failed, canceled, and interrupted
  scheduled fixture, scheduler history and `run_metadata` agree on terminal
  state, completion time, and owning execution identity. A partial write is
  retried or surfaced as failure rather than leaving contradictory success.

## Reproduction on the current binary — 2026-08-09 (Upwork)

The blocking reproduction is supplied. A second durable projection has the same
defect: `pulse_review_log` rows are written **only** by an agent
(`recordPulseReviewOnDB`), so a pass that dies between "review started" and
"review recorded" strands its row at `status='running'` permanently.

Three stranded `workflow_review` rows were found in
`Workflow/upwork/db/db.sqlite`, from three different runs on a single day:

| `review_run_id` | recorded_at |
|---|---|
| `schedule-manual--78ba88d0_1786169154458106000` | 2026-08-08T07:55:21Z |
| `workshop-background-task-1786193263563776000` | 2026-08-08T12:59:19Z |
| `schedule-cron--78ba88d0_1786203048970002000` | 2026-08-08T17:35:52Z |

Historic distribution in that database: `completed` 42, `failed` 10, `running`
3, empty 1 — so this is a steady leak, not a one-off. The 17:35 row also
carried an empty `verdict` alongside 5 verifications, which is the same surface
PLAT-046 hardened at the tool boundary.

**Root cause of the leak, distinct from the metadata half:** `Start()` only ever
reconciled `pulse_final_command_state` (via
`finalizeAllUnresolvedPulseFinalCommands`). Nothing swept the reviewer
projection, so an interruption that was correctly reconciled on one table was
silently left inconsistent on the other.

### Partial fix shipped under PLAT-054

`finalizeAllRunningPulseReviewLogs` now runs in the same startup sweep. It is
deliberately narrow: only rows already `running` are touched, only
status/verdict are rewritten, and `finding_count` / `verification_count` the
dead pass genuinely recorded are preserved. Tests:
`TestFinalizeAllRunningPulseReviewLogsClosesStrandedRows` and
`...ToleratesMissingTable`. The three Upwork rows above were reconciled directly
because that server is intentionally stopped pending a rebuild.

The original `run_metadata.json` half is implemented locally as described in
the 2026-09-17 update below. Live scheduled-run acceptance remains open.

## Canonical workflow outcome implementation — 2026-09-17

The implementation now makes the existing `run_metadata.json` record the
single workflow-execution outcome instead of allowing its consumers to infer a
second outcome:

- the persisted vocabulary is `running`, `completed`, `failed`, or `canceled`;
- non-fatal persistence problems remain `status=completed` and are recorded in
  `warnings` plus the existing structured `persistence_errors`, rather than the
  pseudo-status `completed_with_persistence_error`;
- Pulse normalizes legacy `success`, `error`, `stopped`, spelling variants of
  canceled, and `completed_with_persistence_error` before assessing a run;
- listing a run no longer changes an existing `status=running` record merely
  because `steps_done.json` contains every step index. Step progress is detail,
  not authority. The inference path remains only for pre-metadata legacy run
  folders; and
- chat/session completion and the scheduler's whole-job phase remain separate
  lifecycle projections. They consume the workflow result but do not redefine
  it. The durable scheduler ledger continues to own post-run Pulse/finalization.

Focused tests cover status normalization, the legacy successful-with-warning
case in Pulse intake, warning de-duplication, and the requirement that the
canonical workflow result is finalized before evaluation begins.

Remaining acceptance is operational: run one successful, failed, canceled and
restart-interrupted scheduled fixture, then verify the workflow record,
scheduler ledger/history, Pulse intake and UI converge without a later reader
rewriting the workflow outcome.

## False interruption reproduction and scheduler-side repair — 2026-08-15 (Upwork)

The startup sweep exposed a second failure mode that its original acceptance
did not cover. Upwork's run
`schedule-cron--78ba88d0_1786764627822589000` completed Review+Fix at 10:23,
completed Finalize at 10:27, and delivered its email. The reviewer had written
finding and module-result state, but its current-run `pulse_review_log` row was
still `running`: it never completed the typed `complete_pulse_review` receipt.
At the next backend start (11:35), the PLAT-054 sweep correctly found that row
stale but misleadingly labelled the already-finished review "Pulse interrupted
because the server restarted."

The root cause was upstream of the sweep. `validatePulseDueModuleResults`
checked only `pulse_module_state.last_result`. It did not require the second
durable completion projection, so the scheduler logged `review-fix done` and
advanced to Finalize while the typed reviewer receipt remained open.

The scheduler completion contract now requires, for every due module:

- a terminal current-run module result; and
- a current-run `pulse_review_log` row whose status is `completed` or `failed`
  and whose verdict is non-empty.

A missing or `running` receipt activates the existing parent reconciliation
turn. If that turn still does not produce the receipt, Review+Fix is failed and
the finalizer receives explicit partial-failure context; it can no longer look
fully successful and then be rewritten as interrupted on a later restart.
Focused regression coverage reproduces both a completely missing receipt and
the exact Upwork state: terminal module result plus `pulse_review_log=running`.

This closes the `pulse_review_log` completion-boundary half. The independent
`run_metadata.json` half was subsequently implemented in the 2026-09-17 update
above; live acceptance remains.

## Fresh terminal-reconciliation reproduction — 2026-08-18 (Tectonic USA Day Trading)

Manual signals session
`schedule-manual--9db4dc39_1787065226955186000` completed its workflow/agent
work at approximately 20:39 IST. The workspace backend on port 18744 was
unavailable when the scheduler attempted the final listing/status write, so
that projection could not be persisted. When the server restarted at 21:07,
startup reconciliation rewrote the run as:

```text
status=error
error="interrupted: server restarted"
completed_at=null
```

This is current-binary evidence for the still-open core of PLAT-017. A restart
was treated as evidence that the run was interrupted even though provider and
workflow evidence already showed terminal completion.

### RCA

Terminal truth is split across in-memory scheduler state, provider completion,
workflow artifacts, and `schedule-runs.json`. The final projection write can
fail independently. Startup reconciliation currently gives process restart
greater authority than already-durable terminal evidence and therefore
manufactures an interruption instead of reconciling the projections.

### Fix reasoning

Persist one durable terminal event keyed by `run_id` and `turn_id` before
best-effort projections. On restart, reconcile in this order:

1. canonical terminal event;
2. bound provider completion evidence;
3. workflow run metadata/artifact receipts;
4. only then, absence of all terminal evidence permits `interrupted`.

Projection writes must be retryable/idempotent. A temporarily unavailable
workspace backend should leave `terminal_projection_pending`, not overwrite a
proved completion with `interrupted`.

Extend acceptance with a fixture that completes the provider/workflow turn,
fails the final workspace projection, restarts the server, and proves every
projection converges on the original terminal result and completion time.

> **2026-09-10:** [PLAT-306](../../goals/pulse/reviews/plat-306.md)) removes mandatory checkpoint files.
> Runtime still tracks incomplete runs, using source run identity and saved SQLite
> notes; legacy checkpoint paths remain optional historical references.

## Interrupted review recovery — 2026-09-04 (RTS Latency)

RTS Latency exposed a related but distinct receipt-loss path. Its Strategic
Review checkpoint recorded turn 1 of 4, but the child never persisted a final
module result. A later Gate therefore saw no strategic backlog and repeatedly
selected Technical Review instead.

The scheduler now owns this lifecycle boundary:

- it creates a `running` review-recovery attempt before dispatching a due
  Technical or Strategic child;
- a missing terminal result, timeout, or server restart changes that attempt
  to durable `incomplete` state with its exact checkpoint path and reason;
- the next eligible Gate forces that module due and exposes the checkpoint to
  the executor; a terminal `record_pulse_result` atomically clears recovery.

Plan Drift is intentionally exclusive: when deterministic drift is due it is
the only review mode for that pass. Interrupted Technical/Strategic recovery
is retained and resumes on the next non-drift pass, rather than competing with
Plan Drift or disappearing behind a cooldown.

Regression coverage: `pulse_review_recovery_test.go` proves forced recovery,
terminal clearing, restart reconciliation, and Plan Drift deferral.
