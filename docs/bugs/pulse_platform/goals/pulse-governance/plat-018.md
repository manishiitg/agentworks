[← goals / pulse-governance](index.md)

# PLAT-018 — Pulse finalizer cannot record dashboard completion

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | goals |
| Area | pulse-governance |
| Summary | Pulse final-command orchestration and finalizer session grants |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `runtime_reverify` |
| Last synchronized | `2026-08-04` |

> Claim this ticket in this file before implementation. During active work,
> update this fragment rather than the shared index; synchronize the index
> once at handoff, review, or completion.


- **Priority:** P1
- **Owner:** Pulse final-command orchestration and finalizer session grants
- **Source run:** RTS Latency
  `schedule-cron--42eca39a_1785810615371091000`
- **Problem:** the dashboard finalizer generated and read back the dashboard
  projection, then attempted to mark `pulse_final_command_state.dashboard` done
  through `mutate_workflow_db`. The session had no explicit
  `db_access=read-write`, so the canonical DB tool correctly denied the write.
  The stage ended without calling the command-status pathway successfully, and
  the scheduler recorded `dashboard=failed` with reason *"Dashboard stage ended
  without recording its outcome"*.
- **Impact:** a successfully rendered dashboard is reported failed, backup/
  publish/notify initially remain waiting, and the scheduler must start a
  recovery finalizer. The recovery did complete backup and continued publish,
  but it cannot retroactively make the original dashboard stage terminally
  correct.
- **Why this is platform-owned:** final command state is framework-owned Pulse
  bookkeeping. A finalizer should not need general workflow DB mutation rights
  to mark its own command terminal, and the scheduler itself instructed the
  finalizer to use `record_pulse_result(command=...)`.
- **Implementation (2026-08-04):** after the dashboard stage becomes idle, the
  scheduler already validates the new artifact contract and reads it back.
  That deterministic proof now marks only the dashboard command `done` when
  the agent omitted its status call. General workflow DB write access is not
  granted, and later final commands still must record their own outcomes.
- **Verification:** focused state-machine tests prove validated
  `running -> done`, acceptance of an already-done command, preservation of a
  real failure, and no mutation of later waiting commands. Runtime reverify is
  pending.
- **Regression test:**
  `TestReconcilePulseDashboardCommandUsesValidatedArtifactProof`; the complete
  `agent_go/cmd/server` package passes on 2026-08-04.
- **Acceptance:** dashboard, backup, publish, and notify each record `running`
  and exactly one truthful terminal state through the dedicated command API on
  a session with no general workflow DB write grant. A stage that produced its
  artifact but failed bookkeeping is distinguished from one that failed its
  actual work, and recovery resumes only unfinished commands.
