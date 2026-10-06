[← coding-agents / sessions](index.md)

# PLAT-171 — a chat session's own terminal death cancels unrelated workflow executions it merely launched and is watching

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | coding-agents |
| Area | sessions |
| Summary | P1 — a running, unrelated workflow execution can be silently |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `open` |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — a running, unrelated workflow execution can be silently
  killed by a completely separate failure (the launching chat session's own
  terminal dying), with no causal connection between the two beyond sharing a
  session id used for two different purposes.
- **Owner:** session stop/cancellation lifecycle (`cmd/server/session_lifecycle.go`),
  background-agent registry (`cmd/server/background_agents.go`), tracked
  execution registry (`cmd/server/workflow_execution_tracker.go`).
- **Related:** [PLAT-130](../../schedules/stopping/plat-130.md)) (origin of this cancellation
  machinery — scoped to "does Stop actually stop *this run's own* work,"
  not this ticket's cross-execution cascade), [PLAT-117](plat-117.md)
  (adjacent shape: a registry conflating two different concerns under one
  key).

## Evidence and root cause

A user's own workflow-builder chat session (`f4873013-...`, workflow
`sheet-analysis`) had its Claude Code tmux terminal die unexpectedly.
Server log trail:

- `18:11:27` — `[BG AGENT] Live steer delivery failed ... can't find pane:
  mlp-claude-code-...-af53643a — falling back to queue`
- `18:11:53` — `[TERMINAL RECONCILE] failing main session=f4873013...
  reason="tmux pane disappeared unexpectedly"`, followed by
  `[STOP] session=f4873013... begin runtime cancel`
- `18:11:53` — `[STOP] session=f4873013... background agents: canceled=1
  no_cancel_func=1 already_done=8`, then explicitly:
  `WARNING 1 background agent(s) had no cancel func — marked canceled but
  never told to stop (PLAT-130 shape)`
- `18:11:53` — `[STOP] session=f4873013... tracked execution marked
  canceled: id=msgseq-verification-initial-evidence-gate-...
  kind=workflow_builder_task name="Message sequence item -> Verification
  (Formula USD/INR · Expenses · Salary · Profit) / initial-evidence-gate
  (prevalidation)"` — plus two more unrelated tracked executions in the
  same sweep.

That verification step belonged to a **separate, independently running
workflow execution** — a scheduled/triggered run for group "Rakesh Yadav" —
that shares nothing with the chat session's own dead terminal except a
session id. Its own worker was never told to stop (no cancel func), but its
tracking record was flipped to `canceled`, and its LLM turn was separately
hit with a real context-cancellation mid-flight
(`conversation cancelled after LLM generation: context canceled`), failing
it as genuine collateral damage — the step did not recover.

**Mechanism.** `cancelSessionRuntimeWork` (`cmd/server/session_lifecycle.go:67-120`)
is the single entry point for a dying terminal. After canceling the turn's
own `agentCancelFuncs[sessionID]` and `workflowOrchestratorContexts[queryID]`
(both correctly turn-scoped), it unconditionally calls two session-id-keyed
sweeps:

- `BackgroundAgentRegistry.CancelAll(sessionID)`
  (`cmd/server/background_agents.go:597-628`) walks `r.agents[sessionID]` —
  pure field equality.
- `cancelTrackedExecutionsForSession(sessionID)`
  (`cmd/server/workflow_execution_tracker.go:359-386`) walks every tracked
  execution where `exec.SessionID == sessionID` — same pure field equality.

Neither checks any causal/ownership relationship to the terminal that
actually died — anything registered under that session id is a
cancellation target.

**Why unrelated work ends up under that session id.** `run_full_workflow`
(`pkg/orchestrator/agents/workflow/step_based_workflow/planning_exports.go:1847-1907`)
executes on the chat's own `WorkshopChatSession`, and its message-sequence
items report through `hcpo.workshopExecutionNotifier`, constructed once at
workshop-session setup as
`&workshopExecutionBgNotifier{sessionID: sessionID, ...}`
(`cmd/server/workflow_phase_tools.go:212-218`) — bound to the *chat's own*
session id, not to any identity of the run it is about to launch.
`workshopExecutionBgNotifier.OnExecutionStart`
(`cmd/server/delegation.go:85-159`) then does
`n.api.bgAgentRegistry.Register(n.sessionID, bgAgent)` and
`n.api.trackWorkshopExecutionStart(n.sessionID, ...)`. The same `sessionID`
field simultaneously means "route notifications/UI polling here" and "this
is who owns the right to cancel this" — there is no separate owner-vs-watcher
concept anywhere in this registration path. (The run does get its own
internal identity for folder-guard scoping — `session-group-Rakesh
Yadav-...`, confirmed in the same log — but neither registry keys
cancellation on it.)

**The "no cancel func" half is separately, deliberately correct — and a red
herring for this ticket.** The message-sequence item's
`WorkshopExecutionStart{...}` literal
(`pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go:547-553`)
never sets `Cancel`, matching the incident's `no_cancel_func=1`. PLAT-130's
2026-08-18 correction (`plat-130.md:138-143`) explicitly judged this
intentional: that registration is a *notification*, "an item that runs
inline in the caller's goroutine — there is no separate goroutine to
cancel." That reasoning is sound *for a step that is actually part of the
same run as the session watching it*. It does not address — and PLAT-130's
scope never claimed to address — a *different, unrelated* run's item being
marked canceled at all, by a session that has no business canceling it in
the first place. The bug here is one level up from where PLAT-130 looked:
not "was the cancel signal delivered," but "should this session have been
allowed to cancel this execution at all."

## Decision

Not resolved in this pass — this needs a design decision, not a mechanical
fix, because the same registration path is shared by genuinely different
cases that need opposite behavior:

- A todo-task sub-agent or inline tool call spawned *during the chat
  session's own current turn* has no independent existence — if that turn's
  session dies, canceling it is correct (there's nothing else keeping it
  alive or meaningful once the parent turn is gone).
- A `run_full_workflow`/group run *launched* by a chat turn but running as
  its own independent execution (own run folder, own group scope, own
  lifecycle, continues after the launching turn ends) should **not** die
  because the chat session that launched it later crashes for an unrelated
  reason — the chat session is a *watcher* of that run, not its life
  support.

Both currently register through the identical `BackgroundAgentRegistry` /
tracked-execution path keyed on one `sessionID` field, so today's registries
cannot tell them apart. A fix needs either: (a) a second, distinct
"owning execution id" recorded alongside the existing notification
`sessionID`, with `CancelAll`/`cancelTrackedExecutionsForSession` canceling
on owner match rather than notification-route match; or (b) an explicit
"detached" marker set at registration time for launches like
`run_full_workflow` that are known to outlive their launching turn, checked
before including an entry in either cancel sweep. Recommend (a) as more
general — it doesn't require every future launch site to remember to opt
into detachment — but the existing `ParentExecutionID` field already on
`BackgroundAgent` (`cmd/server/background_agents.go:54`) and the
group-scope identity already computed for folder-guard purposes
(`session-group-<name>-...`) both look like plausible starting points worth
evaluating against real call sites before committing to a shape.

## Non-goals

- Not investigating why the tmux pane itself died — traced the log window
  around it (17:53–18:12) and found nothing: no OOM, no other tmux deaths,
  no resource-limit or reaper activity nearby (`TMUX_REAPER` fired only
  twice all day, five hours apart). The pane was already gone by 18:05:53
  (first failed tag attempt), 5.5 minutes before discovery; nothing in the
  log explains the death itself, and this ticket does not speculate beyond
  that evidence. If it recurs, worth revisiting with OS-level signals this
  log doesn't carry.
- Not re-litigating PLAT-130's own scope (same-run stop efficacy) — that
  ticket is `implemented` and live-reverified; this is a materially
  different failure shape it never claimed to cover.

## Acceptance tests (once a fix is designed)

1. A chat session's own terminal dying does not change the status of a
   `run_full_workflow`-launched group execution's tracked steps that were
   running at the time, and does not touch that execution's own worker.
2. A todo-task sub-agent or inline tool call genuinely scoped to the dying
   session's own current turn is still canceled exactly as today —
   verifying the fix narrows scope without weakening PLAT-130's own
   guarantee.
3. The chat session still receives (or, if now structurally unable to,
   visibly logs why not) whatever notification it was watching for once the
   independent run actually completes on its own — the notification/watch
   relationship must survive even though the cancellation relationship no
   longer does.

## Verification

Not yet started — filed for design + implementation as a follow-up. This
pass is investigation and root-cause documentation only, verified by direct
code citation (file:line) and the live log trail above, not by a build or
test run.
