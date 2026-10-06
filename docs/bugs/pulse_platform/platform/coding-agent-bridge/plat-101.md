[← platform / coding-agent-bridge](index.md)

# PLAT-101 — Claude capacity exhaustion can strand a workflow until an operator intervenes

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P0 — a workflow can stop midway and remain falsely running for |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — suspend/resume shipped; live reverify pending. Stage 1 `multi-llm-provider-go@a9fa11a`, stage 1c `@e8cbc1e`, stage 2 `mcpagent@c88bfc0`, bounded consume `781d52605`, stage 3 `45ba49660`, per-account cache `@2ca6c02`, schedule gate `f64c54619`. Live reproduction captured 2026-08-18 (rtslatency, see below) |
| Last synchronized | `2026-08-19` |

- **Priority:** P0 — a workflow can stop midway and remain falsely running for
  hours after its coding-agent account reaches a usage limit.
- **Owner:** Claude Code adapter error metadata, mcpagent fallback propagation,
  workflow continuation state, durable scheduler wake-up.
- **Observed on:** workflow runs whose Claude pane displayed `You've hit your
  weekly limit · resets 11:30pm (Asia/Calcutta)` while the workflow still had
  unfinished steps.

## Actual defect

Claude Code can exhaust its five-hour or seven-day subscription window during a
workflow rather than before it starts. At that point the coding agent cannot
reason about the failure or ask the platform to recover: the model itself is no
longer available.

The interactive adapter currently recognizes only the exact pane text
`You've hit your limit`. Claude's newer `You've hit your weekly limit` wording
does not contain that exact phrase, so the terminal can stay alive while the
workflow lifecycle waits indefinitely.

Even when quota exhaustion reaches mcpagent correctly, a run with no configured
fallback ends as `all LLMs failed (primary + 0 fallbacks)`. There is no durable
capacity-wait state, reset timestamp, or same-run wake-up. Keeping the tmux pane,
HTTP request, Go goroutine, or workflow agent alive until the reset would not be
safe: it wastes resources, fails across server restarts, and can leave the run
permanently stuck.

## Reliable reset-time source

Text parsing is not the primary contract. Claude Code's status-line JSON already
provides machine-readable subscription windows:

```json
{
  "rate_limits": {
    "five_hour": {
      "used_percentage": 100,
      "resets_at": 1786644000
    },
    "seven_day": {
      "used_percentage": 100,
      "resets_at": 1786721400
    }
  }
}
```

`resets_at` is an absolute Unix timestamp for the exact authenticated Claude
account used by that session. The adapter already reads these fields for
display, but converts them into strings such as `7d 100% → Fri` and discards the
structured timestamp.

Reset-time precedence must be:

1. structured `rate_limits.<window>.resets_at` from Claude's status line;
2. typed provider error metadata if Claude exposes it directly later;
3. visible terminal/result text parsing only as a compatibility fallback;
4. no guessed timestamp — use an unknown-capacity state when none is reliable.

## Proposed repair

### 1. Provider layer: return a typed capacity failure

In `multi-llm-provider-go`:

- preserve each rate-limit window as structured status data (`name`, used
  percentage, absolute reset time);
- recognize daily, weekly, five-hour, and generic usage-limit terminal/result
  variants without depending on one exact sentence;
- return a typed quota error carrying provider, model, exhausted window, and
  `RetryAt`;
- clean up the unusable Claude process after the failure is captured.

Candidate boundaries:

- `llmtypes/types.go`
- `llmerrors/errors.go`
- `pkg/adapters/claudecode/claudecode_interactive_adapter.go`
- `pkg/adapters/claudecode/claudecode_structured_adapter.go`

The provider layer reports the fact and reset time. It does not schedule a
workflow retry.

### 2. Agent layer: try fallbacks, then preserve the typed error

In `mcpagent`:

- skip same-model retries for quota exhaustion;
- try configured same-provider or cross-provider fallbacks immediately;
- preserve `RetryAt` through the final `all LLMs failed` error instead of
  flattening it into text;
- retain the coding-agent session handle needed for an exact continuation.

If a fallback succeeds, the workflow continues normally and no capacity pause
is created.

### 3. Workflow runtime: persist a durable suspension

In `mcp-agent-builder-go`, when no fallback succeeds and a future `RetryAt` is
known, persist the run as `waiting_for_capacity` with:

- provider/model and exhausted window;
- `resume_after` (reset time plus a small safety buffer);
- schedule run ID, workflow run folder, session ID, step ID/path, and phase;
- coding-agent native session handle;
- attempt number and the original typed failure.

The schedule run must no longer say `running`. Pulse, evaluation, backup,
publish, notification, and later sequence messages must not start while the
producing workflow is suspended.

No process, tmux pane, request, or sleeping workflow goroutine remains alive
during the wait.

Candidate boundaries:

- `cmd/server/schedule_runs.go`
- `cmd/server/scheduler.go`
- a focused durable wake-up owner such as `cmd/server/capacity_resume.go`
- `pkg/orchestrator/agents/workflow/step_based_workflow/workflow_continuation_state.go`

### 4. Durable wake-up and exact continuation

The capacity queue must be reconstructed on server startup and wake when its
nearest `resume_after` becomes due. At wake-up it must:

1. acquire the normal workflow/schedule execution lock;
2. atomically claim the suspension so two server loops cannot resume it twice;
3. attach the resumed turn to the original execution lifecycle tree;
4. resume the same native Claude session and same unfinished workflow phase;
5. reuse the existing run folder and outputs rather than start a new workflow;
6. progress through `waiting_for_capacity → resuming → running → completed`;
7. persist a later reset and wait again if capacity is still unavailable.

The next ordinary cron occurrence must not launch a duplicate workflow while an
older run of that schedule is waiting for capacity.

## Side-effect safety

The platform must not blindly replay the entire workflow. That could duplicate
posts, emails, payments, trades, or other external actions.

Automatic recovery is allowed only when the runtime can resume the exact native
session and unfinished phase using the existing run folder and continuation
record. Completed steps and durable outputs remain completed.

If exact continuation cannot be proven, transition to `recovery_required` and
ask the operator to choose a recovery action. Do not silently start over.

## Unknown reset time

When quota exhaustion is certain but no reliable future timestamp exists:

- try configured fallbacks;
- otherwise persist `waiting_for_capacity_unknown`;
- offer **Retry now**, **Use fallback provider**, and **Cancel run**;
- never invent a reset time or keep the run falsely active.

## User experience

Global Monitor and the workflow run should show a clock rather than a spinner:

```text
Waiting for Claude capacity · resumes after 11:35 PM
```

The safety buffer prevents waking exactly on a provider boundary. Controls:

- **Resume now** — recheck capacity and claim the same continuation;
- **Use fallback provider** — continue the unfinished turn using an explicitly
  configured compatible provider;
- **Cancel run** — terminally stop the suspension without running final stages
  that depend on missing workflow output.

## Required tests

### Provider tests

- five-hour and seven-day `resets_at` values survive status parsing as absolute
  timestamps;
- `You've hit your weekly limit` and other supported variants produce typed
  quota failures;
- structured `is_error:true` quota results produce the same typed contract;
- reset metadata belongs to the active authenticated session and is not read
  from another account/session;
- pane cleanup happens after the typed error is captured.

### Agent tests

- quota exhaustion skips same-model retries and tries the fallback chain;
- successful fallback does not create a suspension;
- no successful fallback preserves the typed `RetryAt` through the final error.

### Runtime tests

- a mid-step quota failure changes the run from `running` to
  `waiting_for_capacity`;
- the suspension survives a backend restart and wakes once;
- a second scheduler instance cannot claim the same suspension;
- no later sequence/Pulse/finalization stage runs before recovery;
- the due wake-up resumes the same run folder, session, step, and phase;
- completed side-effecting steps are not replayed;
- another cron fire does not duplicate the waiting run;
- a second capacity failure safely updates `resume_after`;
- missing exact continuation proof yields `recovery_required`, not a replay.

## Acceptance

- A real Claude usage-limit event cannot leave a workflow indefinitely
  `running`.
- With a configured fallback, the unfinished turn continues through that
  fallback.
- Without a fallback, the workflow releases all live resources and visibly
  waits until the structured reset timestamp.
- Restarting the server during the wait neither loses nor duplicates the run.
- At reset, only the unfinished work resumes; completed external actions are
  never repeated.
- The run reaches its normal terminal stages only after the recovered workflow
  itself finishes.

## Live reproduction, 2026-08-18 (rtslatency)

The first captured instance of this defect in the wild, found while
diagnosing an unrelated UI report. Recorded here because the ticket's state
was "live reproduction pending", and because it identifies a specific
mechanism the analysis above does not name.

**What the operator saw.** `rtslatency` sat in the global activity monitor
with a live spinner and would not go away across page refreshes. The account's
Claude limit had been reached mid-run.

**What the runtime actually held**, from `/api/sessions/active` more than three
hours after the last real work finished:

```json
{
  "session_id": "schedule-cron--42eca39a_1787009443095002000",
  "created_at":    "2026-08-18T05:16:47+05:30",
  "last_activity": "2026-08-18T08:30:48+05:30",
  "runtime_state": {
    "phase": "running",
    "reason": "foreground turn is active",
    "raw_session_status": "error",
    "foreground_turn": { "busy": false, "can_steer": true, "synthetic": true },
    "background_live": false
  }
}
```

Nothing was executing: the foreground turn was not busy, no background agent
was live, and every real child had finished. A sibling session in the same
response carried `phase: "failed", reason: "provider usage/rate limit reached"`,
confirming the trigger.

**The mechanism that kept it `running`.** Five `synthetic-turn:steer-message-*`
child executions were still `running`, started 00:00:09, 00:00:22 (×2),
00:29:31 (×2) UTC and never settled. Each one had been spawned by a child
*failing*, within milliseconds:

| failed child completed_at | stuck synthetic turn started_at | gap |
|---|---|---|
| 00:00:09.478 (`bg-pulse-engineering+ops-backlog`) | 00:00:09.483 | 5 ms |
| 00:00:22.548 (`msgseq-daily-latency-report`) | 00:00:22.581 | 33 ms |
| 00:00:22.561 (`exec-daily-latency-collect-dev`) | 00:00:22.688 | 127 ms |
| 00:29:31.416 (`msgseq-security-sweep-reflection`) | 00:29:31.451 | 35 ms |
| 00:29:31.430 (`exec-daily-security-sweep`) | 00:29:31.558 | 128 ms |

The correlation is total and runs both ways in the same session: **5 of 5**
children that failed produced a permanently-stuck notification turn, while
**4 of 4** children that completed produced notification turns that settled
normally in 14–17 s.

**Why those turns never settle.** `executeSyntheticTurn`
(`cmd/server/background_agents.go`) always settles its tracked execution from a
`defer`, so a turn stuck `running` means the goroutine never reached that
defer. It is parked in the stream-consume loop:

```go
textChan, err := llmAgent.StreamWithEvents(agentCtx, syntheticMsg)
...
for range textChan {          // no deadline
    ...
}
```

`agentCtx` is `context.WithCancel(context.Background())` — no timeout and no
deadline. When the provider is rate-limited and its stream never closes the
channel, this loop blocks forever, the deferred `completeTrackedExecution`
never runs, and the session keeps reporting a live foreground turn
indefinitely. That is the concrete path by which "falsely running for hours"
happens, and it is downstream of every layer the repair plan above addresses:
even a correct typed quota error does not release a turn already parked on a
channel that will never close.

Implication for the repair: alongside the typed error, durable suspension, and
wake-up, the auto-notification consume loop needs a bound (deadline or
cancellation tied to the provider failure) so a stream that never terminates
cannot hold a session open on its own.

**Not the same bug as PLAT-130.** That ticket covers a cancel path that marks
executions canceled without stopping the work. This is the inverse: no cancel
was requested at all, and the work is already finished — only the notification
turn is stuck.

## Implementation status

Staged so each increment is independently shippable and testable. Current
state was verified in-repo before starting, not assumed from this document.

### Stage 1 — provider: structured reset time and typed quota error — **DONE**

`multi-llm-provider-go@545fb18`, wiring completed in `a9fa11a`.

What was actually found versus what this ticket predicted:

| the ticket said | verified state |
|---|---|
| adapter matches only one exact sentence | confirmed — **two** sites (`detectTmuxFatalStatus`, `isClaudeFatalProgressLine`), and `"You've hit your weekly limit"` genuinely does not contain `"You've hit your limit"` |
| `resets_at` read for display and discarded | confirmed — `claudeStatusExtras` formats it to `"7d 100% →Fri"` and drops the instant |
| need a typed quota failure carrying `RetryAt` | `KindQuotaExhausted` already existed; `RetryAt` did not exist anywhere in any of the three repos |

Shipped:

- `IsClaudeUsageLimitText` matches the stable shape of the wording (verb +
  possessive + optional qualifier + `limit`) instead of one sentence, anchored
  on hit/reached/exceeded so an agent discussing rate limits in its own output
  is not misread as a limit wall. Both detection sites now use it.
- `llmtypes.RateLimitWindow` + `StatusLine.SetRateLimitWindows` /
  `RateLimitWindows()` carry each window with its reset instant intact, beside
  (not instead of) the display extras. `EarliestReset` returns the soonest
  future reset among *exhausted* windows — the instant a caller may retry.
- `llmerrors.Error` gained `RetryAt` (absolute) and `Window`, with
  `RetryAtOrZero` / `QuotaWindow` / `IsQuotaExhausted`. `RetryAt` is separate
  from the existing `RetryAfter` on purpose: a duration is only meaningful
  relative to when it was computed, so it cannot survive being persisted and
  reloaded, which is precisely what a suspension has to do.

Throughout, *exhausted with an unknown reset* stays distinguishable from *not
exhausted* rather than collapsing to one state, so stage 3 can implement this
ticket's `waiting_for_capacity_unknown` branch instead of inventing a
timestamp.

14 tests including the status-line JSON quoted above, a JSON round trip (a
`StatusLine` crosses a process boundary between the adapter that fills it and
the runtime that reads it), and explicit false-positive coverage for the
matcher. Full repo suite green.

### Stage 2 — agent: fallbacks, then preserve the typed error — **DONE**

`mcpagent@c88bfc0`.

Most of what this stage asked for already existed and simply never fired:
`isQuotaExhaustedError` checks `llmerrors.KindOf` before its string fallbacks,
so once stage 1b returned a typed error, a Claude weekly limit already skipped
same-model retries and moved to the fallback chain. Prior to that the same wall
classified as transient throttling and was retried against a window that
reopens in hours.

What was genuinely missing was *time*. `quotaExhaustedModels` was a
`map[string]bool`, which can only express "never try this again":

- a five-hour window that reopened mid-run left that model benched for the
  agent's entire lifetime, because nothing recorded that it would return;
- the terminal `all models are quota-exhausted` error was a plain string, so no
  caller could learn when capacity comes back — the single fact a workflow
  needs to suspend rather than lose a partially-executed run.

The map now stores the reset instant, taken from the typed provider error
rather than re-parsed from text at this layer. A model whose window has
reopened is removed and retried. When every model is out, the returned error is
a typed `KindQuotaExhausted` carrying the soonest reset among them — which is
what stage 3 will schedule its resume on.

A zero reset keeps its meaning end to end: exhausted, no reliable time. Such a
model is skipped like any other, contributes no resume time, and is never
retried on a guessed schedule. `model_not_found` shares this map and is stored
with a zero reset deliberately — a missing model is a config error, not a
window that reopens.

### Stage 3 — runtime: durable suspension and wake-up — not started

Both inputs this stage needs now exist and are correct: `IsQuotaExhausted` to
decide, and `RetryAtOrZero` to schedule. Neither was usable before stages 1-2 —
every limit looked like transient throttling and carried no time.

The largest stage, and the one with real correctness hazards: durable
`waiting_for_capacity` state, releasing every live resource, atomic claim so
two server loops cannot resume once each, restart survival, and preventing the
next cron occurrence from duplicating a waiting run. Detailed in the repair
plan above.

Note the additional requirement from the live reproduction: bounding the
auto-notification consume loop. That defect is independent of the typed error —
a turn already parked on a channel that never closes is not released by
classifying the failure correctly.

### Stage 4 — UI — not started

Clock instead of spinner, plus Resume now / Use fallback provider / Cancel run.

This refers to the durable **workflow-run** capacity controls. Direct product
chat now separately normalizes quota failures into the shared product failure
card, stops the working indicator, shows a retry action, and preserves an exact
`retry_at` when the event carries one. That frontend improvement does not mark
this workflow stage complete: Global Monitor still needs the clock and the
three run-level controls above.

## What shipped (2026-08-18)

A run that hits a capacity wall is now **suspended, not failed**.

- **Reset times are read, never reconstructed.** The Claude statusline sidecar
  already carried exact per-window `resets_at`; the failure path was parsing
  `resets 10am` out of pane text instead. `llmtypes.MostConstrainedReset` exists
  alongside `EarliestReset` because the sidecar reads 99.x rather than a clean
  100 at the instant of the wall, so `EarliestReset` returns nothing there and
  silently demotes an exact instant to a guess.
- **The zombie is closed.** `executeSyntheticTurn`'s consume loop had one exit —
  the producer closing the channel — and a pane parked on a limit never closes
  it. Two bounds now: context cancellation and an idle deadline. A stall is
  recorded as a failure with its reason rather than as `context canceled`, so it
  stops being filed as a user stop.
- **`waiting_for_capacity` is its own run status.** `capacity_wait.json` records
  which step, of how many, which window, and when it reopens. Durable because a
  wait outlives the process, and the restart sweep would otherwise convert the
  pause into a false `interrupted: server restarted`.
- **The schedule stops firing while a run is suspended**, which is what ends the
  run storm, and the tick loop resumes the run in place at its reset instant —
  same run row, same run folder, at the step that could not run.
- **A schedule-level gate** refuses to start a run into a window already at
  100%, using a per-account cache of the quota reading the provider already
  sends on every turn.

Two refusals are deliberate: a wait with no stated reset never wakes on its own,
and a wall with no record on disk is reported as the failure it effectively is.

## Known gaps

1. **No per-item checkpoint in `message_sequence`.** Resuming a suspended step
   replays from item 1. Since `message_sequence` is the default step type, a
   step that posted to Slack at item 2 and died at item 5 will re-post on
   resume. This is the most consequential remaining gap.
2. **No per-step preflight.** The orchestrator has no account identity and no
   StatusLine handle in scope, so only the schedule-level gate exists. Deliberate
   — a per-step gate cannot predict whether a run FITS in the remaining quota
   without a cost estimate, and agentic steps vary too much between runs for any
   such estimate to be safe.
3. **Live reverify outstanding.** All of the above is code and unit coverage;
   no capacity wall has been observed end-to-end since the change.
