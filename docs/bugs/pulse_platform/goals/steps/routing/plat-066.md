[← goals / steps / routing](index.md)

# PLAT-066 — `route_selections` is correctly supplied but never seeded; the router silently falls back to the live-action route

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | steps/routing |
| Summary | P1 — silently ran (part of) the wrong, real-world-effecting route on a live account |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` — proven with hard evidence, exact failure point not isolated |
| Last synchronized | `2026-08-10` |

- **Priority:** P1 — silently ran (part of) the wrong, real-world-effecting route on a live account
- **Owner:** step-based workflow orchestrator (`seedRouteSelectionsForRun`, `controller_routing_deterministic.go`) / `controller_execution.go:2943` call site
- **Found on:** social-media, 2026-08-10, session `schedule-cron--4128e261_1786329055349814000` ("Weekly Strategy Discovery (Proposer)")

## What happened

This ticket started as a live "Needs your decision" note the agent typed mid-run (never filed as a real decision — see the UI note below), which read: *"route_selections isn't plumbed through to step-run-mode-router. Until fixed, any non-default route request silently runs the execution pipeline."* Investigating it turned up a precise, reproducible mechanism, not just a description.

1. The schedule's message is explicit: `Call run_full_workflow(group_name="default", route_selections={"step-run-mode-router":"propose_new"})`. Confirmed the agent obeyed it exactly — the server log shows the actual HTTP payload sent: `{"group_name":"default","route_selections":{"step-run-mode-router":"propose_new"},"disable_eval":true}`.
2. Despite that, `runs/iteration-0/default/logs/step-run-mode-router/routing-evaluation.json` for this exact run records:
   ```json
   "route_selection": {"raw_value": "execution", "source_kind": "default_route_id", "source_path": ""},
   "routing_reasoning": "No route_selection.json found; using default_route_id \"execution\".",
   "selected_route_id": "execution"
   ```
   The router genuinely found no seeded route file and fell back to its default — **despite the caller having supplied the selection correctly.**
3. `seedRouteSelectionsForRun` (`controller_routing_deterministic.go:117`) is the function responsible for writing that file from `hcpo.executionOptions.RouteSelections`, and on success it logs `"🔀 Seeded deterministic route selection for %s: %s -> %s"`. That line **never appears anywhere in this session's logs.** Either the function was never called, or it returned before reaching that log line — but the caller-side data (`route_selections` in the tool payload) was present and correctly shaped, so this is not a caller error.
4. Consequence: the router took `execution` → `step-execution-pipeline` → `execute-allocate`, which ran and failed prevalidation (existing bug, `strategy_today.json` missing `per_run_budget`, open since 2026-07-30). The agent apparently noticed the misroute mid-session and separately, manually drove `propose-new-strategies-direct` to fulfil the schedule's actual intent (matching this workflow's own CLAUDE.md debugging instruction: *"don't just re-run it... run the task yourself... act, don't just analyze"*), leaving the wrongly-triggered execution-route job orphaned. `list_executions` later showed it stuck `status: "running"` under a `status: "cancelled"` parent, and the agent's own live attempt to stop it got contradictory answers — `"cannot be cancelled individually"` then `"No running executions found to cancel."` That bookkeeping inconsistency is closely related but is its own separate defect, not re-investigated in depth here.

## Why this matters beyond today

`step-run-mode-router`'s `default_route_id` was `"execution"` — the one route with real, public, live-account side effects (posting/replying/following). Compare `step-0-browser-router` two steps earlier in the same plan, whose default is `"browser_failed"` — i.e., when uncertain, stop. `step-run-mode-router` did the opposite: when its input silently failed to arrive, it defaulted to the most consequential action available, not the safest one. On a schedule that had explicitly promised *"Do not post, reply, like, follow, quote-tweet, or change the profile,"* this is the one case where the seeding gap actually could have caused real, uninstructed public actions if `execute-allocate` had not itself failed prevalidation on an unrelated, pre-existing bug.

**Interim mitigation already shipped (this session, commit `eaa089b`)**: removed `default_route_id` from `step-run-mode-router` in `plan.json`. A future seeding failure now makes the run **fail loudly** instead of silently executing the live-action route. This does not fix the seeding gap — it just converts a silent wrong-route execution into a visible error, which is the safe posture while the real cause is open.

## Investigation so far (root cause NOT isolated)

- Confirmed the caller-side payload was correct (see log line above) — ruling out an agent prompt-following failure.
- Confirmed the router itself saw no file — ruling out a routing-logic bug in how the router *reads* the file; the read path works as documented (`routing.md`: falls back to `default_route_id` when no file exists).
- Confirmed `seedRouteSelectionsForRun`'s own success-path log line never fired — meaning the write path did not complete, but not yet isolated to *why*: not called, called and returned early (`hcpo.executionOptions == nil` or `RouteSelections` empty at that point — inconsistent with the confirmed payload, so this would itself be a bug in how the payload becomes `executionOptions`), errored on `ensureStepExecutionFolderExists`/`WriteWorkspaceFile` (would need its own error log line, not found), or wrote to a **different run/step path** than the one the router later read from (a path-computation mismatch between the two call sites, both of which independently compute `getExecutionFolderPath(..., step.GetID(), stepPath)` from a `steps` list — worth checking whether both call sites see the same steps slice, in the same order, at the times they run).
- Not yet checked: whether `controller_execution.go:2943`'s call to `seedRouteSelectionsForRun` is itself reached for schedule/session-tool-triggered `run_full_workflow` calls the same way it is for the direct HTTP API path, or whether there's a second, divergent code path (mirroring the exact shape of PLAT-065's manual-vs-cron divergence, and the confirmed-dead `Schedule.RouteSelections` field found investigating that ticket — this codebase has more than one place where a route/config value is correctly modeled but not reliably wired through to where it's consumed).

**Diagnostic logging shipped (2026-08-10):** two log points now bracket the gap. `planning_exports.go`, right after `SetExecutionOptions` and before `CreateTodoList`, logs the parsed `route_selections` map whenever it's non-empty — confirming the value that actually reached the orchestrator's execution options at the top of the call. `seedRouteSelectionsForRun` (`controller_routing_deterministic.go:117`) now logs distinctly whether `executionOptions` was `nil` or `RouteSelections` was empty at seed time, instead of silently returning `nil` either way. Traced the parsing at `planning_exports.go:1854-1873` and confirmed it's structurally sound — malformed input returns an explicit tool error, which never fired for the incident run — and `SetExecutionOptions`/`CreateTodoList` are called synchronously on the same orchestrator instance, so the value is not obviously lost at either end I could directly verify. The gap is somewhere inside `CreateTodoList`'s internal call chain between that point and `runExecutionPhase`, which is too large to trace blind without risking a wrong guess in scheduler-critical code. The next occurrence's logs will show which of the two new log lines fired (or didn't), narrowing the search to a specific segment of that chain. Build and the existing 22-failure baseline both verified unaffected.

**Later same-day evidence (not closure):** a LinkedIn Engage run on the current
binary logged `SetExecutionOptions carries route_selections=map[step-workflow-router:route-engage]`, then wrote
`runs/iteration-0/engage/execution/step-workflow-router/route_selection.json`
and logged `Seeded deterministic route selection`. This proves the ordinary
`run_full_workflow` → batch execution path can carry and consume the value on
the current binary. It does not explain why the Hetzner run archived an older
preseed file and then consumed its safe default with no current preseed, so the
ticket remains open pending a like-for-like Hetzner reproduction after restart.

## UI note (unrelated defect, worth its own line)

The "Needs your decision" text that surfaced this had **no backing record** — `report_human_inputs` had zero pending/recent rows for this workspace at the time. The agent typed decision-card-styled prose without calling `create_human_input_request`, so nothing persisted for the UI to show and nothing was trackable/re-openable. Not filing a separate ticket for this alone since it's a single occurrence so far, but noting it because it's the same shape as PLAT-065's Gate→Finalize handoff gap: a described intention with no durable state behind it.

## Acceptance

- **Proven (2026-08-10):** the caller correctly supplied `route_selections`; the router correctly saw nothing; the seeding function's own success log never fired. The gap is real and reproducible in principle, not hypothetical.
- **Interim mitigation (done):** `default_route_id` removed from `step-run-mode-router` (commit `eaa089b`) — converts silent wrong-route execution into a loud failure.
- **Root cause (open):** isolate why `seedRouteSelectionsForRun` did not complete for this call, per the next-step diagnostic above.
- **Related, not yet investigated:** the orphaned `step-execution-pipeline` execution stuck reporting `"running"` under a cancelled parent, and `list_executions`/`stop_step` giving contradictory answers about it. May be a symptom of the same run, or a separate execution-bookkeeping defect — worth a dedicated look once the seeding root cause is found.
