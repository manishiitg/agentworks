[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-370 — Blocking questions and function-call lookup do not survive server restart

| Coordination | Value |
|---|---|
| State | Open — code-reviewed durability gap; recovery not implemented |
| Date | 2026-09-28 |
| Priority | P2; prerequisite for restart-safe long-lived interactions |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | execution lifecycle, crew/workflow functions, external MCP/CLI |
| Reviewed revision | `ebc37cf87` on main, including the local changes present during review |

## Current limitation

Blocking feedback requests and waiter channels exist in a process-local
singleton. Restarting the backend loses both the question registry and the
waiting goroutine. Function calls are written to workspace JSON, but external
polling uses only the process-local `crewFunctionCalls` map and returns not
found after restart. The saved call JSON excludes `UserID`, so it is not by
itself a sufficient authenticated recovery record.

This is a durability capability gap rather than a regression against a
documented restart guarantee. Existing function endpoint errors explicitly
say calls are tracked until server restart. A client disconnect is different:
background work may continue while the server process remains alive.

## Code evidence and reusable components

- [human_feedback_store.go](../../../../agent_go/cmd/server/virtual-tools/human_feedback_store.go): request/waiter maps at 33; singleton initialization at 52; cleanup on wait completion at 282.
- [crew_functions.go](../../../../agent_go/cmd/server/crew_functions.go): `UserID` excluded from JSON at 242; map-only lookup at 287; workspace persistence at 374.
- [external_crews.go](../../../../agent_go/cmd/server/external_crews.go): restart limitation in the call-not-found response at 147.
- [report_human_inputs.go](../../../../agent_go/cmd/server/report_human_inputs.go): durable workflow-local SQLite decision and audit-event tables, starting at 220.
- [human_answer_scope.go](../../../../agent_go/cmd/server/human_answer_scope.go): existing human-answer authority rules.

The durable “Needs you” subsystem already persists decisions and attribution,
but it is separate from blocking feedback. Answering a durable record does
not recreate a suspended goroutine or automatically resume its original
step. Reuse it only where its lifecycle and authorization semantics fit.

Evidence is source inspection. A backend restart with a real suspended
workflow was not performed, and no production data loss is claimed.

## Design and acceptance

Define persistent question identity, trusted user/target ownership, expiry,
accepted-answer receipts, and explicit interrupted/recoverable states. Add
an execution checkpoint or continuation job rather than attempting to
restore an in-memory waiter. Do not durably retain OTPs or credentials just
to make a generic answer log convenient; establish field-specific retention.

- [ ] After restart, authorized polling can recover known function calls and
  distinguish completed, failed, interrupted, and genuinely resumable work.
- [ ] Durable questions retain their prompt, target, ownership, lifecycle,
  and expiry; unrecoverable executions are explicitly interrupted rather
  than presenting an answerable question with no consumer.
- [ ] An authorized answer to a recoverable question resumes the intended
  continuation exactly once, including crash/retry boundaries around acceptance.
- [ ] Restoring a call never replays already completed external side effects.
- [ ] Revoked grants and removed target access still deny reads and replies
  after recovery; caller-supplied ownership cannot authorize restoration.
- [ ] Expired/cancelled questions remain closed across restart.
- [ ] Restart tests cover pending input, accepted-but-not-consumed input,
  completed calls, and unrecoverable execution.

No implementation or deployment has started. This is separate from the
registration bug [PLAT-365](plat-365.md), cancellation bug
[PLAT-368](plat-368.md), and basic call-scoped interaction work
[PLAT-369](../integrations/plat-369.md).

## Status (2026-09-28)

Still open. The only related change is in `39b468a8b` (PLAT-366): the Crew
poll's not-found text no longer claims calls vanish on restart. Durable
question storage and function-call recovery are not implemented.
