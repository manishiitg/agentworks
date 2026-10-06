# PLAT-535: a workflow function call reports `queued` for its whole run

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | integrations |
| Area | mcp |
| Summary | fixed on main, not deployed: a workflow run's states (`workflow_running` ...) were never read as running, so the call stayed `queued`; live check on RTS after a deploy. |

**State:** fixed on main (2026-10-05); not deployed; not yet checked with a live `review_pr` call on RTS. P3.

**Found:** 2026-10-05, RTS, `call_workflow_function` on `rts-pr-reviweer` / `review_pr` (PR 180): `get_workflow_function_call` returned `status: queued` for about eight minutes while `list_executions` showed the run executing (gate done, review step running). It changed to `completed` only at the end.

**Cause (verified in code, 2026-10-05):** the call supervisor (`superviseCrewFunctionCall`, `crew_functions.go`) set `running` only when the target run's status was the word `running` (a Crew run). A workflow run reports the scheduler's own states (`starting`, `waiting_for_capacity`, `workflow_running`, `pulse_gate`, `pulse_modules`, `pulse_finalizing`, `workflow_finished`), so the call never left `queued` until it finished. The progress tool the ticket suspected is not involved.
**Fix:** `triggerTargetRunIsRunning` (`trigger_link_tools.go`): for a workflow target `workflow_running`, `pulse_*` and `workflow_finished` count as running; `starting` and `waiting_for_capacity` stay `queued`. Crew calls keep `running`. Test `TestWorkflowFunctionCallStatusAndResultSize`.

**Left:** check with a real `review_pr` call after the next RTS deploy (needs the owner's go): `get_workflow_function_call` must show `running` shortly after the start, then `completed`. Use a scratch PR or a dry run: a real call posts a GitHub review.

## Register notes

[PLAT-535](plat-535.md), P3, fixed on main, not deployed: a workflow run's states (`workflow_running` ...) were never read as running, so the call stayed `queued`; live check on RTS after a deploy.
