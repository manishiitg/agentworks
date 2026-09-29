[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-370 — Blocking questions and function-call lookup do not survive server restart

| Coordination | Value |
|---|---|
| State | Open — function-call lookup is restart-safe; blocking questions are not |
| Date | 2026-09-28; updated 2026-09-29 |
| Priority | P2; prerequisite for restart-safe long-lived interactions |
| Owner | human-decisions; implementation unassigned |
| Related subsystems | execution lifecycle, crew/workflow functions, external MCP/CLI |
| Reviewed revision | `03e6844cf` merge commit on main ([PR #246](https://github.com/manishiitg/agentworks/pull/246)) |

## Current limitation

Blocking feedback requests and waiter channels still exist in a process-local
singleton. Restarting the backend loses both the question registry and the
waiting goroutine; an open question cannot be answered or resumed afterward.

[PR #246](https://github.com/manishiitg/agentworks/pull/246) added a private
`_system/function_calls/<call_id>.json` index with the user and saved record
path. Polling can now load a completed call after restart. An open call is
loaded as **failed/interrupted**, with its saved progress retained, rather
than disappearing or pretending it can still accept an answer. Submission IDs
are saved separately to make uncertain retries inspect the original call.

This remains a question-continuation gap, not a regression against a promised
restart guarantee. A client disconnect is different: background work may
continue while the server process remains alive.

## Code evidence and reusable components

- [human_feedback_store.go](../../../../agent_go/cmd/server/virtual-tools/human_feedback_store.go): request/waiter maps at 33; singleton initialization at 52; cleanup on wait completion at 282.
- [crew_functions.go](../../../../agent_go/cmd/server/crew_functions.go): `lookupCrewFunctionCall`, the `_system` index, and `loadSavedCrewFunctionCall` recover a saved call or mark an open one interrupted.
- [crew_function_timeout_test.go](../../../../agent_go/cmd/server/crew_function_timeout_test.go): `TestCrewFunctionCallSurvivesRestartAsInterrupted` covers lookup, retained progress and safe unknown IDs.
- [report_human_inputs.go](../../../../agent_go/cmd/server/report_human_inputs.go): durable workflow-local SQLite decision and audit-event tables, starting at 220.
- [human_answer_scope.go](../../../../agent_go/cmd/server/human_answer_scope.go): existing human-answer authority rules.

The durable “Needs you” subsystem already persists decisions and attribution,
but it is separate from blocking feedback. Answering a durable record does
not recreate a suspended goroutine or automatically resume its original
step. Reuse it only where its lifecycle and authorization semantics fit.

The restart test simulates loss of the in-memory call map. A backend restart
with a real suspended workflow was not performed, and no production data loss
is claimed.

## Design and acceptance

Define persistent question identity, trusted user/target ownership, expiry,
accepted-answer receipts, and explicit interrupted/recoverable states. Add
an execution checkpoint or continuation job rather than attempting to
restore an in-memory waiter. Do not durably retain OTPs or credentials just
to make a generic answer log convenient; establish field-specific retention.

- [x] After restart, authorized polling can recover saved function calls and
  report a formerly open call as interrupted, keeping its progress.
- [ ] Define and support genuinely resumable calls, distinct from interrupted
  calls, where the execution has a durable continuation.
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

The saved-call portion is implemented on main; deployment is not verified
here. Durable question storage and continuation remain open. This is separate
from [PLAT-365](plat-365.md), [PLAT-368](plat-368.md), and the call-scoped
polling/reply route in [PLAT-369](../integrations/plat-369.md).
