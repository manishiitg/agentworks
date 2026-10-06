[← goals / pulse-governance](index.md)

# PLAT-271 — `get_pulse_state(view=module)` failed with `SQLITE_BUSY` because every Pulse "read" ran no-op migrations that take SQLite's write lock

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | P1 — while it lasts, the Gate cannot read module state, so the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — build/test verified with fail-before/pass-after tests; live reverify pending on the next confida-login Pulse pass |
| Last synchronized | `2026-09-03` |

- **Priority:** P1 — while it lasts, the Gate cannot read module state, so the
  entire review-and-fix layer of a Pulse pass is skipped (only
  Backup/Publish/Notify ran on the affected pass). The finding rated it
  `high`; it recurred and was re-stamped `external_action_required` on
  2026-09-02.
- **Owner:** `agent_go/cmd/server/report_human_inputs.go`
  (`openReportHumanInputDB` → `ensureReportHumanInputSchema`),
  `agent_go/cmd/server/pulse_worklist.go` (`ensurePulseModuleStateSchema`,
  `migrateMergedModuleRows`, `migratePulseReviewFocusCatalog`), and the
  lifecycle package's `ensurePulseFindingLifecycleSchema` /
  `ensurePulseReviewLogSchema` in
  `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/`.
- **Finding:** confida-login `ce45ba95` / `PUL-7774A6D0`
  (`harness:get_pulse_state:view=module`, filed 2026-08-31 on pulse run
  `schedule-manual--d25999f9_1788163461053375000`). Not in the register
  before this ticket.

## The finding

During the 2026-08-31 Pulse pass, nine consecutive
`get_pulse_state(view=module, pulse_run_id=…)` calls over ~10 minutes all
returned `read persisted Pulse mode: database is locked (5) (SQLITE_BUSY)`
after a ~20 s wait each. In the same window, against the same `db.sqlite`,
`query_workflow_db(SELECT 1)`, `record_pulse_result(command=backup, …)` and
`get_pulse_state(view=backlog)` all succeeded in under 100 ms — including a
`view=backlog` call one tool call before a failing `view=module` retry. The
finding correctly isolated the fault to the `view=module` path and could not
see the lock holder from the workshop shell.

`agent_go/logs/schedule.log` shows the platform side of the same window: the
scheduler's once-a-minute `pendingFastPulseRequest` poll logged `[PULSE]
failed to read fast Pulse request for Workflow/confida-login: database is
locked (5) (SQLITE_BUSY)` at 13:35:31, 13:37:33, 13:38:36, 13:41:31 and
13:42:31 IST (one more on 2026-08-24), bracketed exactly by the Gate turns
(`[PULSE] starting pulse` 13:34:57 → `step "gate" done` 13:38:56 → retry →
`step "gate" done` 13:42:44).

## Root cause, confirmed in code and reproduced

The workflow database is in WAL mode (`db.sqlite` header bytes 18/19 = 2/2;
every workflow's is). In WAL mode readers never block and are never blocked;
only writers serialize on the single write lock. So a *read* can only get
`SQLITE_BUSY` if it is not actually a read.

`view=module` is not a read:

1. Every open in `pulse_worklist.go` goes through `openPulseModuleStateDB` →
   `openReportHumanInputDB`, which runs `ensureReportHumanInputSchema` on
   **every open**. That function ends with three unconditional
   `UPDATE report_human_inputs …` statements (legacy attribution,
   strategy_auditor/goal_advisor → strategic_review, prompt-contract
   apply_contract backfill).
2. `getPulseRunMode` — the call the finding's error text comes from — then
   runs `ensurePulseModuleStateSchema`, which runs `migrateMergedModuleRows`
   twice and `migratePulseReviewFocusCatalog`, each a `BEGIN … INSERT..SELECT
   / DELETE / UPDATE … COMMIT`, on every call.
3. `readPulseModuleView` opens and closes the database five times per call
   (its own comment says so), so one Gate call was roughly eight write
   transactions.
4. The scheduler's `pendingFastPulseRequest` poll uses the same opener, so
   it was a writer once a minute too.

An `UPDATE`, `DELETE` or `INSERT..SELECT` that matches **zero rows still
takes the write lock**. Verified directly against SQLite in WAL mode with a
second connection holding `BEGIN IMMEDIATE`: `CREATE TABLE/INDEX IF NOT
EXISTS` (existing), `DROP … IF EXISTS` (missing), `PRAGMA table_info` and
plain `SELECT` all succeed; `UPDATE … WHERE <no match>`, `DELETE … WHERE <no
match>`, `INSERT … SELECT … WHERE <no match>` and `INSERT OR IGNORE … SELECT
<no rows>` all return `database is locked`. So on an already-migrated
database every one of these "idempotent" migrations is a no-op that still
queues behind whatever real writer holds the lock, waits `busy_timeout`
(5 s in `sqliteopen.DSN`), and fails.

That is the whole isolation the finding observed: `view=backlog` and
`query_workflow_db` are genuine reads; `view=module` and the fast-Pulse poll
were writers. The migrations were added 2026-08-18 (`migrateMergedStrategicReviewRows`),
2026-08-21 (`migrateMergedTechnicalReviewRows`) and 2026-08-23
(`migratePulseReviewFocusCatalog`); the first `SQLITE_BUSY` in the log is
2026-08-24. The same pattern also exists on the lifecycle side
(`ensurePulseFindingLifecycleSchema`'s three legacy-module UPDATEs,
`migrateRunConcernIssueIDs`, `migrateMergedPulseAliasesClosed`,
`migrateAppliedPulseFixesClosed`) and in `ensurePulseReviewLogSchema`, which
every `LoadPulseFindingLifecycles` / `LoadModuleReviewHistory` read runs — it
just happened not to collide during the recorded window.

Which real writer held the lock for the duration of the two Gate turns is not
recorded (the finding could not see it either). It does not matter for the
fix: a write lock held for longer than 5 s by a legitimate writer is normal
(a repair, a `mutate_workflow_db`, a step writing through `$DB_PATH`), and a
read path must simply not need that lock.

Checked and ruled out: direct `sqlite3`/Python access from agent sessions
(already hard-blocked for Builder, Pulse, workshop children and
message-sequence steps by `configureWorkflowDBSession`, PLAT-175; only saved
scripted steps use `$DB_PATH`, by design), rollback-journal mode (all
workflow DBs are WAL), a pool self-deadlock (every `BeginTx` in these files
uses `tx.*` exclusively), and the modernc `_pragma=journal_mode(WAL)` on
connect (a no-op on an already-WAL file).

## Fix

Every idempotent migration statement reached from a read path is now probed
with a `SELECT` first and only executed when it would change something:

- `agent_go/cmd/server/sqlite_write_guard.go` — `sqliteRowsExist` /
  `sqliteExecIfRows` (`SELECT EXISTS(<probe>)`).
- `report_human_inputs.go` — the three `UPDATE report_human_inputs` backfills.
- `pulse_worklist.go` — `migrateMergedModuleRows` probes the four tables for
  any legacy-module row before `BeginTx`; `migratePulseReviewFocusCatalog`
  probes focus state/history for any legacy focus key (including inside
  `deferred_focuses_json`) before `BeginTx`, and skips the per-row
  `deferred_focuses_json` rewrite when the canonical form is unchanged.
- `step_based_workflow/pulse_write_guard.go` — `pulseRowsExist` /
  `pulseExecIfRows` over `pulseFindingLifecycleDB`.
- `pulse_finding_lifecycle.go` — the three legacy-module UPDATEs in
  `ensurePulseFindingLifecycleSchema`, `migrateRunConcernIssueIDs`,
  `migrateMergedPulseAliasesClosed`, and the three statement groups in
  `migrateAppliedPulseFixesClosed` (applied-fix close, attempt status,
  unfixed-wait reopen), each probed by the same predicate it writes with.
- `pulse_review_log.go` — the two legacy-module UPDATEs.

Migration semantics are unchanged: when legacy rows exist the original
statements run exactly as before (the existing
`TestPulseModuleSchemaMigratesLegacyAdvisorsToNewestStrategicState` /
`…MergesLegacyEngineeringAndOpsIntoTechnicalReview` and the lifecycle
migration tests still pass unmodified). `CREATE … IF NOT EXISTS`, the
`PRAGMA table_info`-guarded `ALTER TABLE`s, and the row-driven migrations
that already only write for rows they found were left alone — they never
take the lock when there is nothing to do.

## Verification

- New `TestPulseStateReadsDoNotTakeWriteLock` (`cmd/server`) and
  `TestPulseLifecycleReadsDoNotTakeWriteLock` (`step_based_workflow`): create
  the workflow db, hold `BEGIN IMMEDIATE` on a second connection, then call
  `getPulseRunMode`, `getLatestPulseRunMode`, `pendingFastPulseRequest`,
  `openPulseModuleStateDB(create=false)`, `LoadPulseFindingLifecycles`,
  `LoadExternallyOwnedRunConcerns` and `LoadModuleReviewHistory` under a 2 s
  deadline. **Fail-before:** both hung for the full 5 s `busy_timeout` and
  failed (`context deadline exceeded (after 5.06s)`) — the exact production
  shape. **Pass-after:** both complete in ~1 s including test setup.
- Full `step_based_workflow` suite passes; `cmd/server` suite's only
  failures are the five model-catalog pins from the same-day Gemini 3.8 /
  Fable 5.1 bumps, fixed in the follow-up commit; `golangci-lint` reports 0
  issues on both packages.

## Live reverify

The finding's own `next_check` is the right one: on the next confida-login
Pulse pass, `get_pulse_state(view=module)` must succeed first thing. When it
does, dispose `ce45ba95` as resolved. The workflow's Pulse DB was not touched
by this ticket (workflow folders are the workflow's own domain).

## Not changed (follow-ups, if wanted)

- `readPulseModuleView` still opens the database five times per call; with
  the read paths off the write lock this is now only a latency cost.
- `busy_timeout` stays at 5 s and readers still do not retry on
  `SQLITE_BUSY`; a genuine writer that needs the lock for longer than that
  (a large repair) can still fail a concurrent *write* from Pulse, which is
  the correct signal and unrelated to this ticket.
- The `cf878b55` (env dump persisted into a step's `session.json`) and
  `34d49698` (`soul/` not writable by the Fixer even after operator
  authorisation) confida-login findings are also unregistered and still
  need tickets; neither is a Pulse read-path problem.
