[← platform / scheduler-runs](index.md)

# PLAT-182 — a step's re-read result was selected by max attempt/iteration number, letting a stale dispatch from weeks earlier outrank today's real result

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | schedules |
| Area | runs |
| Summary | P1 — a completion notification can report a stale, unrelated |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — live reverify pending |
| Last synchronized | `2026-08-23` |

- **Priority:** P1 — a completion notification can report a stale, unrelated
  failure from weeks earlier as if it were the current run's result, with no
  indication anything is wrong; whoever reads it has no way to tell without
  independently checking the actual workspace files.
- **Owner:** step result re-read path
  (`pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`,
  `controller_workshop.go`).
- **Related:** [PLAT-176](plat-176.md) (same root shape — evidence filenames
  built from counters local to one dispatch — but a different consumer of
  that shape: PLAT-176 was about overwriting; this is about selecting the
  wrong survivor once files from multiple dispatches coexist).

## Symptom

A background agent labeled "Master Reel Orchestrator" (actually just the
plan step's own `Title`, re-run via the workshop `execute_step` tool) sent
an `[AUTO-NOTIFICATION]` for the "instagram" workflow (2026-08-23) reporting:

```
CONCERNS: Validation history: attempt 1 failed - ... content_package.json
must exist but was not found ...; attempt 2 failed - [same error]; attempt 3
failed - [same error]; unresolved after 3 attempt(s).
```

The referenced file genuinely existed, written today at 00:51. Today's
dispatch had in fact succeeded on its first try.

## Root cause, confirmed by direct code read and file inspection

`loadSingleStepResultFromLogs`
(`controller_execution.go:1018-1037`, before this fix) scans every
`execution-attempt-<N>-iteration-<M>.json` file present in a step's log
folder and keeps whichever has the numerically **highest `(attempt,
iteration)` pair**:

```go
if attempt > latestAttempt || (attempt == latestAttempt && iteration > latestIteration) {
    latestExecutionResult = execResult
    latestAttempt = attempt
    latestIteration = iteration
}
```

`attempt`/`iteration` are counters **local to one dispatch** — both reset to
1/0 every time the step is dispatched again (the exact same shape PLAT-176
already documented for the sibling overwrite bug). Across dispatches they
carry no time ordering whatsoever; nothing archives, clears, or otherwise
retires a prior dispatch's files once a step is re-run weeks later.

Live, for `step-create-reel` under `Workflow/instagram/runs/iteration-0/test-run`:
- `execution-attempt-1-iteration-0.json` — **2026-08-23 00:52:04**, today's
  real, successful dispatch (`execution_result: "STATUS: COMPLETED"`, read
  directly).
- `execution-attempt-2-iteration-0.json` / `execution-attempt-3-iteration-0.json`
  — both **2026-07-17**, a genuinely different, genuinely-failed dispatch
  from over a month earlier. Its `execution_result` field, read directly,
  contains the exact text later reported as today's failure, byte-for-byte:
  `"CONCERNS: Validation history: attempt 1 failed - ... content_package.json
  must exist but was not found ...; attempt 2 failed - ...; unresolved after
  3 attempt(s)."`

Because `3 > 1`, the July file won the comparison even though it is 37 days
older and describes an entirely different, already-resolved outcome.

**Forward path to the notification** (traced and confirmed):
`controller_workshop.go:305-317` deliberately discards the correct in-memory
result from `runExecutionPhase` and re-reads from logs via
`loadSingleStepResultFromLogs` — even when the in-memory execution itself
returned no error — because, per the existing code comment, "runExecutionPhase
writes results to log files." That re-read is what returned the stale July
text. From there: `interactive_workshop_manager.go:3246` →
`OnExecutionComplete(execID, stepDisplayName, result, ...)` →
`delegation.go:283` → `agent.SetResult(result)` →
`background_agents.go:1892`'s `buildAutoNotificationMessage` uses
`snap.Result` verbatim as the notification's `Result:` text.

**Explicitly ruled out**, with citation, so this isn't left as a
plausible-sounding but unverified theory: `RecordRunConcerns`'s durable
cross-run concern aggregation (`run_concerns.go`) is never folded into this
text — its only consumer that formats a history-style line is
`run_concerns.go:571-572` ("seen on N runs"), a different string shape
entirely, used by Pulse worklist/loop-closure code, not this notification
path. `controller_execution.go`'s own live retry loop
(`withValidationFailureConcern`) genuinely produced nothing for today's
dispatch — confirmed by the fact only one `pre_validation_final-gate_*`
file exists for today, proving that loop succeeded on its first live
iteration.

## Fix implemented

`loadSingleStepResultFromLogs` now compares each candidate file's
`completed_at` field (present in every execution-attempt file; wall-clock
time, genuinely comparable across dispatches, unlike the local counters)
and keeps whichever completed most recently. The `(attempt, iteration)`
comparison is retained only as: (a) a tie-breaker when two files share the
identical `completed_at`, and (b) the sole ordering signal for legacy files
that predate `completed_at` being recorded, so no existing evidence becomes
unreadable.

## Review follow-up (same day) — the selection fix alone was not sufficient

A code review of the fix above found the deeper issue it left unaddressed:

> After runExecutionPhase completes, the correct result is discarded and the
> code searches the shared log directory for the globally newest result.
> Another schedule/chat executing the same step can finish between those
> operations, causing the wrong dispatch's response to be returned.

Confirmed real, not theoretical: `controller_workshop.go:305-317`'s re-read
of `loadSingleStepResultFromLogs` has no way to distinguish "the file this
exact call just wrote" from a file a genuinely concurrent, unrelated
dispatch of the same step wrote in between — and nothing serializes those
two dispatches against each other. Traced two independent gaps that both
have to hold for two dispatches to collide, and confirmed both are open:
the session-scoped step registry (`interactive_workshop_manager.go:3089-3098`)
only guards a second `execute_step` within the *same* chat session (a fresh
`WorkshopStepRegistry` per `NewWorkshopChatSession`, and scheduled runs get
their own session ID via `newScheduleSessionID`), and the scheduler's own
`scheduleStateScope` lock (`scheduler.go:108-115`) is workspace-scoped
against *other scheduled runs* only — a chat-triggered `execute_step` never
touches it. Two independent orchestrator instances (a chat session and a
scheduler tick) can dispatch the identical `stepID` concurrently, each
writing its own evidence into the same log folder.

A related, smaller point from the same review: `completed_at` was written
with whole-second `time.RFC3339` precision, so two dispatches finishing in
the same second would still tie and fall back to the attempt/iteration
comparison this ticket's own fix established is invalid across dispatches.

### Fix implemented

`runExecutionPhase` (`controller_execution.go`) now takes an optional
`*LastExecutedStepOutcome` out-parameter, populated in place at both points
it already stores a step's result into `previousExecutionResults` (the
human-input and regular/scripted step branches — routing and todo_task
steps produce no result string this way and leave it unset).
`controller_workshop.go` passes a pointer and, when the outcome was
captured and its step index matches the target step, uses that exact
in-memory string directly — no re-read, no possibility of picking up a
concurrent dispatch's file. The log-folder read remains as a fallback only
for step kinds that never populate an in-memory result this way, and the
pre-existing inner-step-override addressing path is untouched. The other
two `runExecutionPhase` callers (batch/group execution, evaluation) have no
use for a single step's result and pass `nil` — zero behavior change for
them.

`completed_at` was also raised to nanosecond precision
(`formatRFC3339UTC`, the single shared helper every writer of that field
uses) to shrink the residual tie window for the read paths that still
compare timestamps — `loadExecutionResultsFromLogs`' cross-step context
reads, and this fix's own fallback paths. The real fix is the exact binding
above; this only tightens what remains for the cases it doesn't cover.

### Not implemented

- No dedicated new test for the exact-binding fix itself:
  `runExecutionPhase` has no existing test harness to extend (it invokes
  real step execution internally, including LLM agent calls), and building
  one from scratch was out of scope for this pass. Verified instead via a
  full build and the existing regression suite (only pre-existing,
  independently confirmed-unrelated failures) — a materially weaker
  verification than the fail-before/pass-after tests the rest of this
  ticket has. Live reverify is the real proof this needs.
- Nothing archives or clears a step's prior-dispatch `execution-attempt-*`
  files once a step is re-run — this fix changes which survivor gets
  *selected*, not whether stale ones keep accumulating on disk indefinitely.
  A future pass could extend PLAT-176's `superseded/` archival to this read
  path's source files too, but that's a larger change than fixing the
  selection bug itself and wasn't attempted here.

## Acceptance tests

Covered by `controller_execution_stale_result_test.go`, both proven
fail-before/pass-after against the actual prior comparison logic:

1. `TestLoadSingleStepResultFromLogsPrefersRecentCompletionOverHigherAttemptNumber`
   — three fixture files reproducing exactly this incident's shape (today's
   attempt-1 success alongside a July attempt-2/attempt-3 failure); asserts
   the result returned is today's, not July's. Confirmed failing against the
   prior max-`(attempt, iteration)` comparison, passing against the fix.
2. `TestLoadSingleStepResultFromLogsFallsBackToAttemptOrderingWithoutTimestamps`
   — legacy fixture files with no `completed_at` field; asserts the
   attempt/iteration fallback ordering still applies unchanged.

The exact-dispatch-binding follow-up fix has no dedicated test — see "Not
implemented" above.

## Verification

- `go build ./...` clean.
- `go test ./pkg/orchestrator/agents/workflow/step_based_workflow/...`: both
  new tests pass; the two pre-existing failures
  (`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`,
  `TestWorkshopPromptShellExamplesUseAbsolutePaths`) confirmed present on a
  clean `origin/main` checkout before this change, unrelated to this fix.
- `go test ./...` (full repo, after the review follow-up): same
  pre-existing failure set as every other check this session
  (Pulse-module-naming drift in `cmd/server`, plus the two above) —
  independently confirmed unrelated, no new regressions.
- Live reverify pending for both the original selection fix and the
  exact-dispatch-binding follow-up: no confirmation yet against a real
  re-dispatched step whose log folder already holds files from an earlier,
  unrelated dispatch, or against two genuinely concurrent dispatches of the
  same step.
