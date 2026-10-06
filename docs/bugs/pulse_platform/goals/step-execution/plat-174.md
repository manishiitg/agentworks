[← goals / step-execution](index.md)

# PLAT-174 — step_config.json was read exactly once per run, so an `update_step_config` change made after the run started never reached a step that hadn't begun yet

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — any mid-run `update_step_config` call is silently a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — build/test verified, live reverify pending on a fresh run |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — any mid-run `update_step_config` call is silently a
  no-op for the current run, for every field, on every step, with no error
  or warning anywhere in the path that would tell the caller it didn't
  apply.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/controller.go`
  (`PrepareExistingPlan`'s one-time `ReadStepConfigs`+`populateRuntimeFields`
  sweep), `controller_execution.go` (`executeSingleStep`).

## The incident

2026-08-21, workflow `confida-login`, group `confida-staging`, run
`workflow-full-mt2vmamr0k`. The user pinned `execute-browser-and-capture-apis`
to `pi-cli`/`google/gemini-3.7-flash` via `update_step_config`, as a
time-boxed cost trial (PUL-22A2D8C6 showed this step was 64% of the cycle's
cost). The change:

- Landed on disk correctly — `step_config.json`'s changelog records it at
  `11:54:21Z`, with a review note explicitly reasoning that the timing was
  safe: *"Applied while the run ... was on step 3, before this step
  started."*
- Never took effect. Every execution log for that step in this run —
  `12:10:53Z`, `13:03:58Z`, `13:05:39Z`, all well after the change — recorded
  `model: claude-code/claude-sonnet-5`, not the pinned model.

## Root cause

`PrepareExistingPlan` (`controller.go:741-751`) reads `step_config.json`
**exactly once**, near the top of run preparation, and copies every step's
config into the in-memory plan for the run's entire lifetime:

```go
stepConfigs, err := hcpo.ReadStepConfigs(ctx)
...
for _, step := range existingPlan.Steps {
    if err := populateRuntimeFields(step, stepConfigs); err != nil { ... }
}
```

Nothing re-reads the file after that point. This run's first step
(`survey-app-and-refresh-knowledge`) started at `11:39:04Z` — 15 minutes
*before* the config change. So the snapshot every step in this run carried
was already stale by the time the file was correctly updated, and stayed
stale for the rest of the run regardless of how early before its own turn a
later step's config changed.

The review note's reasoning checked the wrong boundary. "Before this step
started" only matters if per-step config is read per-step; the actual
boundary was "before the *run* started," and by the time any step but the
first one executes, that boundary has already passed. This is a general
platform gap, not specific to `execution_llm` — every field `populateRuntimeFields`
sets (`execution_tier`, `learnings_access`, `knowledgebase_access`,
`enabled_skills`, `use_code_execution_mode`, all of it) is subject to the
same staleness for the rest of an in-flight run.

## Fix

`executeSingleStep` (`controller_execution.go`) now calls a new
`refreshStepAgentConfigsBeforeExecution` immediately before a step executes,
re-reading `step_config.json` and re-running `populateRuntimeFields` for that
one step. A change made anywhere in the run, any time before a step's own
dispatch, now reaches it — closing exactly the gap `execute-browser-and-capture-apis`
hit, and the same class of gap for every other `AgentConfigs` field.

A failed re-read (transient I/O, a file mid-write) does not abort dispatch or
blank the step's config — it logs a warning and keeps whatever
`populateRuntimeFields` already set at run start, the same fail-open
behavior `controller.go` already uses for the original read.

## Deliberately out of scope

- **Message-sequence route re-entry.** `execute-browser-and-capture-apis`
  runs as a message_sequence *route*: `getMessageSequenceRuntime` creates its
  execution agent once and caches it in-memory
  (`hcpo.msgSeqRoutes[routeKey]`), reused across re-entries within a run
  unless the caller explicitly requests `Restart`. This fix refreshes config
  before a step's **dispatch** (`executeSingleStep`), which covers a step's
  first entry into a run correctly — the case that actually failed here. It
  does **not** retroactively change an already-created agent's model
  mid-conversation on a *later* re-entry into the same route within the same
  run; that agent is frozen at creation by the same deliberate design already
  documented at `controller_message_sequence.go:1044-1052` for its folder-guard
  snapshot. Swapping a live agent's LLM mid-conversation is a materially
  different problem (does the conversation history/tool state carry over
  correctly to a new provider?) and wasn't needed to fix the reported case —
  the pin was made well before the step's *first* entry into this run, which
  is exactly what this fix addresses.
- **Warning the caller synchronously.** `update_step_config` could
  reasonably tell the caller "note: a run is currently in progress; this
  change applies starting from that run's steps that haven't started yet"
  vs. silently succeeding either way. Not implemented — this fix makes the
  common case (change lands before a step's own first dispatch) actually
  work, which removes most of the need for the warning. Worth reconsidering
  if per-run-in-progress mid-conversation route staleness (the case above)
  turns out to matter enough to need its own fix.

## Verification

- `TestRefreshStepAgentConfigsBeforeExecutionPicksUpAMidRunConfigChange` —
  new. Builds a real `StepBasedWorkflowOrchestrator` against an
  `httptest.Server` standing in for the workspace API, populates a step from
  an initial `step_config.json`, rewrites the served config to add an
  `execution_llm` pin (simulating a mid-run `update_step_config` call), then
  asserts the refresh picks it up. Failed correctly before the fix existed
  (`refreshStepAgentConfigsBeforeExecution` was undefined — confirmed via
  `go vet`); passes after.
- `TestRefreshStepAgentConfigsBeforeExecutionFallsBackSilentlyWhenConfigIsUnreadable` —
  corrupts the served config mid-test and asserts the step keeps its
  run-start config rather than losing it to a transient read failure.
- `TestRefreshStepAgentConfigsBeforeExecutionNoopsOnEmptyStepID` — a step
  with no ID (sub-agent-under-construction, orphan utility step) must not
  panic or error.
- `go build ./...` clean.
- Full `step_based_workflow` and `cmd/server`/`cmd/server/guidance` suites
  run: the only failures present are pre-existing and unrelated, confirmed
  by re-running identically with this change reverted
  (`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds` plus a
  cluster of `cmd/server` Pulse/reviewer tests already failing before this
  change).

Not yet reverified live: the direct signal is a future `update_step_config`
call made mid-run, on a step that hasn't started yet, actually reaching that
step's execution.

## Acceptance

- [x] A step's runtime `AgentConfigs` is refreshed from `step_config.json`
      immediately before that step executes, not only once at run start.
- [x] A failed refresh degrades to the run-start snapshot rather than
      aborting the step or discarding its existing config.
- [ ] Live: an `update_step_config` change made mid-run, before a step's
      first dispatch in that run, is confirmed to reach that step's actual
      execution.
