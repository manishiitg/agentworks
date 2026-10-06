[← platform / scheduler-runs](index.md)

# PLAT-054 — scheduler idle watchdog kills demonstrably live child work

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | schedules |
| Area | runs |
| Summary | scheduler turn sequencing |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-15` |

- **Priority:** P0
- **Owner:** scheduler turn sequencing
- **Source workflow:** Social Media, Tectonicus, Upwork, LinkedIn, Instagram, RTS Latency, Substack, Build-in-public

## Why this is P0

This defect gates the register itself. Roughly 40 of 52 platform entries are in
`runtime_reverify`, and reverification requires a producing run to survive. **32
scheduled runs across 8 workflows were killed by this watchdog**, so the evidence
those tickets are waiting on keeps being destroyed before it can be recorded.

Counts from `schedule-runs.json` (`idle wait timed out` only, excluding
user interruptions): Social Media 9, Tectonicus 7, Upwork 6, LinkedIn 3,
Instagram 2, RTS Latency 2, Substack 2, Build-in-public 1.

## The defect

`startSessionInternal` blocks until the main agent turn completes — its own doc
comment: *"This blocks until the session completes (first turn only — the event
filter manages lifecycle)."* So when the scheduler begins its idle wait, the
agent turn is already finished. The wait exists solely to avoid overlapping the
next scheduled message onto **child work** the turn launched.
`waitForPulseTurnCompletion` states this explicitly:

> *"`startSessionInternal` has already received the main agent's completion
> before this is called; the only work that can hold the next Pulse message is a
> child execution or background agent the main turn deliberately launched."*

Both wait paths compute a liveness signal, and **neither consults it at the
timeout decision**:

- `waitForPulseTurnCompletion` (`cmd/server/scheduler.go`) defines
  `hasLiveChildWork()` but uses it only on the `settle` branch, to answer *"are
  we done?"*. Its `case <-inactivity:` branch returns an error unconditionally.
- `waitForWorkshopIdleWithInactivityTimeout` has no liveness check at all. It
  infers progress from `workshopLastProgressAt` timestamps, which for
  still-running work can only record the operation's **start** time —
  `CompletedAt` is nil and an in-flight `ToolCallRecord` has `Duration == 0`.

So a tracked execution or background agent that is demonstrably `running`, but
quiet for 10 minutes, is treated as a stall and killed.

## Why it fires in practice

The schedule message calls `run_full_workflow`, which spawns the entire workflow
as tracked child executions running for hours. Browser-driven workflows embed
deliberate pacing waits — Social Media's `pacing.md` specifies 6–12 s before
opening a listing, 10–30 s "reading" pauses, and 20 s slider waits. A step in a
quiet stretch emits no observable event for 10 minutes and the run dies.

Hence the failures cluster on **turn 1** of **browser-heavy** workflows, after
**68–224 minutes** of successful work.

## Damage is asymmetric

The timeout returns straight out of the turn loop, so the **remaining turns never
execute** — including the reporting and mandatory git-backup turns. On Social
Media's 2026-08-04 → 08-06 runs the tweets genuinely posted (verified status
URLs `2085437517713858597`, `2084952637208957398`, `2084738019664511289`); only
the bookkeeping died. Social Media's own schedule message records this as the
*"7th recurrence of this idle-timeout since 2026-07-08."*

Suspected downstream effect: that workflow's `tweets_posted.last_updated` is
stuck at 2026-07-29 while `tweet_performance` has rows through 08-07, and
`daily_metrics` jumps 07-29 → 08-05 with no 08-08 row.

## Acceptance boundary

A scheduled turn whose child work is still `running` must not be terminated by
the inactivity timer. A turn with genuinely no live child work and no observable
progress must still terminate, as today.

1. `waitForPulseTurnCompletion` — on `case <-inactivity:`, consult
   `hasLiveChildWork()` first. Live → reset the timer, log, continue. Not live →
   error as today.
2. `waitForWorkshopIdleWithInactivityTimeout` — same rule, consulting liveness
   directly rather than inferring it from timestamp staleness. The predicate is
   extracted so both paths share one definition
   (`trackedExecutionsForSession` status + `bgAgentRegistry.HasRunningAgents`).
3. A much larger absolute backstop still applies so an immortally-hung child
   cannot block a schedule forever. `idleMaxInactivity()` is the existing seam
   for per-turn-type values.

Explicitly **not** raising `schedulerWorkshopMaxInactivity` from 10 minutes: that
is threshold tuning over a correctness bug, and 10 minutes of genuinely zero
activity with nothing alive remains a reasonable stall definition.

## Implementation — 2026-08-09

`sessionHasLiveChildWork` extracted as one shared predicate
(`trackedExecutionsForSession` status + `bgAgentRegistry.HasRunningAgents`), and
consulted at every expiry decision in both waits:

- `waitForPulseTurnCompletion` — `case <-inactivity:` resets and continues while
  a child is live, instead of erroring unconditionally.
- `waitForWorkshopIdleWithInactivityTimeout` — both expiry branches (normal and
  the tmux-refresh-failure path) consult liveness directly rather than inferring
  it from `workshopLastProgressAt` staleness.

`schedulerWorkshopLiveChildCeiling` (3 h) bounds the suppression so a child that
hangs forever still cannot block its schedule forever. The liveness value is
recorded in the timeout error (`live_child_work=…`) and a log line is emitted
each time a quiet-but-live turn is extended, so a genuine stall stays
distinguishable from a suppressed one.

`schedulerWorkshopMaxInactivity` is unchanged at 10 minutes — deliberately, per
the acceptance boundary above.

## Files changed

- `agent_go/cmd/server/scheduler.go`
- `agent_go/cmd/server/scheduler_idle_live_child_test.go` (new)
- `agent_go/cmd/server/pulse_final_commands.go` — `finalizeAllRunningPulseReviewLogs`
- `agent_go/cmd/server/pulse_review_log_sweep_test.go` (new)

## PLAT-017 overlap

The same interruption strands `pulse_review_log` rows, which the startup sweep
never covered. `finalizeAllRunningPulseReviewLogs` now runs alongside
`finalizeAllUnresolvedPulseFinalCommands` in `Start()`. Evidence and the
still-open `run_metadata` half are recorded in
[PLAT-017](plat-017.md#reproduction-on-the-current-binary--2026-08-09-upwork).

The 2026-08-15 Upwork run proved the sweep can also expose an upstream receipt
gap: Review+Fix and Finalize had already ended, but `pulse_review_log` remained
`running`, so the later restart sweep used its generic interruption verdict.
The scheduler now requires the typed terminal review receipt before advancing;
see [PLAT-017's 2026-08-15 reproduction](plat-017.md#false-interruption-reproduction-and-scheduler-side-repair--2026-08-15-upwork).

## Test command

```
cd agent_go && go test ./cmd/server/ -run 'LiveChild|WaitForWorkshopIdle|PulseReviewLogs'
```

All 4 new idle tests, 2 new sweep tests, and the 7 pre-existing
`WaitForWorkshopIdle*` tests pass. `go build ./...` clean; `go vet` introduces no
new findings (13 pre-existing `unreachable code` warnings in
`scheduler_test.go` are present on baseline too). Full `go test ./...` shows the
same three pre-existing package failures as baseline (`guidance`,
`virtual-tools`, `step_based_workflow`) from an unrelated in-flight Pulse-v2
refactor — `cmd/server` is green.

## Runtime evidence required

After rebuild, a full Social Media or Upwork scheduled run completes all its
turns — including the trailing report and backup — with no `idle wait timed out`
entry in `schedule-runs.json`. The liveness value is logged at each timeout
decision, so a genuine stall remains distinguishable from a suppressed one.
