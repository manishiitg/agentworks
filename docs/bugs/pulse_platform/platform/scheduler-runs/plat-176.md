[← platform / scheduler-runs](index.md)

# PLAT-176 — a re-dispatched step overwrites the previous dispatch's execution evidence in place, so a looping run erases the proof that it looped

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | P1 — silent and self-concealing. |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — build/test verified, live reverify pending on a re-dispatched step |
| Last synchronized | `2026-08-22` |

- **Priority:** P1 — silent and self-concealing. Nothing fails; evidence simply
  disappears. It specifically destroys the record of *repeated* execution, which
  is the exact history needed to diagnose a run that is repeating itself.
- **Owner:** `pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`
  (`saveExecutionConversationLogs`).
- **Related:** [PLAT-168](../learnings-knowledge/plat-168.md)) (established the "real time stays dumb,
  diagnosis happens after the fact with the full transcript" split — this bug
  removes the transcript that split depends on).

## The incident

2026-08-22, workflow `confida-login`, group `confida-staging`, run
`iteration-0`. Investigating why a run had been active ~17.5 hours (a
comparable completed run took 2.5), the step `execute-browser-and-capture-apis`
was found to have been **dispatched 5 times**, `reconfirm-browser-findings` 5
times, and `validate-browser-evidence` 3 times.

The run's own logs showed **one** dispatch each. Every re-dispatch had
overwritten the previous one in place.

This was caught directly, mid-investigation, rather than inferred: the same
file path was read twice, minutes apart, and returned two entirely different
executions.

```
first read : duration_ms 2675000  (44m35s), 21 tool calls
second read: duration_ms  104248  ( 1m44s), 14 tool calls
same path  : logs/reconfirm-browser-findings/execution/
             execution-attempt-1-iteration-1-timing.json
```

The step re-entered and clobbered the file *during* the investigation. A
diagnosis had already been built on the first number — a 44m35s turn reads as
"nearly hit the 45-minute ceiling (PLAT-153)" and led to a wrong conclusion
about the run being deadlocked. The actual history had to be recovered from
`server_debug.log`, which is not run-scoped, not retained with the run, and not
what any reviewer would think to consult.

## Root cause

`saveExecutionConversationLogs` (`controller_execution.go`) names every
evidence file from two counters:

```go
filenameBase := fmt.Sprintf("execution-attempt-%d-iteration-%d", retryAttempt, loopIterationCount)
resultPath   := fmt.Sprintf("%s/%s.json", logDir, filenameBase)
hcpo.WriteWorkspaceFile(saveCtx, resultPath, string(resultJSON))   // plain overwrite
```

Both counters are **local to a single dispatch of a step**:

- `retryAttempt` comes from this function's own
  `for retryAttempt := 1; retryAttempt <= maxRetryAttempts; retryAttempt++`
  loop, which starts at 1 on every dispatch.
- The message_sequence caller (`controller_message_sequence.go`) passes a
  literal `1` for the attempt and a per-entry turn number for the iteration,
  which likewise restarts on re-entry.

Nothing in the filename identifies *which dispatch* produced it. A step that
runs a second time — a route re-entry, an operator re-run, a gate sending work
back — recomputes the identical path, and `WriteWorkspaceFile` overwrites.
Four files are lost per re-dispatch: the result, the conversation, the timing,
and the prompts.

## Why this matters more than "some logs are missing"

PLAT-168 settled the platform's diagnostic split deliberately: real-time
controls stay dumb (bound resources, never judge quality), and root-causing
recurring failure patterns happens **after the fact, with the full
transcript**. That is a sound split, and this bug undercuts it precisely where
it is load-bearing.

A run that repeats a stage is already invisible to every live control — each
individual turn is healthy and well under the 45-minute ceiling (the longest
turn in this 17.5-hour run was 18.5 minutes, zero errors, zero cancellations),
each step reports `COMPLETED`, and `maxRetryAttempts = 3` is never approached
because re-dispatches are not retries. So the loop can only be caught after
the fact — and this bug is what removes the after-the-fact evidence. Both
halves of the split are defeated by the same defect.

Secondary cost: cost and timing accounting read these files, so discarded
passes are simply not counted. Four of the five browser-capture dispatches in
this run (including one with 232 tool calls) left no cost or duration record
at all.

## Fix

Before writing, `saveExecutionConversationLogs` calls a new
`archiveSupersededExecutionLogs`, which checks whether the canonical result
file already exists and — only if it does — moves all four evidence files into
a `superseded/` subfolder of the same log directory, stamped with both UTC time
and a UUID (`-YYYYMMDDTHHMMSSZ-<uuid>`). The UUID is required because two
re-dispatches can archive within the same second and the real workspace move
API rejects an existing destination rather than overwriting it.

Two deliberate choices:

- **Canonical names keep holding the newest dispatch.** Every existing reader
  is untouched: `cmd/server/workflow.go`'s `execution-attempt-*` listing,
  `controller_execution.go`'s exact-path lookup, `debug_step`, and
  `controller_progress.go`'s archive sweep all continue to see exactly what
  they see today.
- **Archives go in a subfolder, not a sibling filename.** Readers glob this
  directory by `execution-attempt-` prefix and parse the counters back out
  with `Sscanf`. A stamped sibling (`...-iteration-1-superseded-<stamp>.json`)
  would match that glob, appear as a phantom extra attempt, and misparse. A
  subfolder is invisible to those globs while staying trivially discoverable.

Best-effort by design: this is evidence retention, not correctness. A check or
move failure is logged and execution continues. Losing an archive is bad;
failing a step because an archive could not be written would be worse.

## Deliberately not done

- **Not renaming the canonical scheme to carry a dispatch counter.** That is
  the more principled fix — the filename genuinely lacks a dispatch identity —
  but it changes every path readers already parse, and PLAT-175's neighbouring
  work showed how easily a blanket rename breaks a consumer nobody remembered.
  Archive-on-collision gets the evidence retained today at near-zero blast
  radius. If a dispatch counter is wanted later it can be layered on with the
  readers migrated together.
- **Not addressing why the step was re-dispatched 5 times.** That run's
  duration is now traced to real causes (two content-driven gate rejections
  after a pinned Gemini model stalled and was wedge-killed, one orchestrator
  double-fire, and — dominantly — the host machine sleeping repeatedly
  overnight, with a ~3-hour blackout). That is separate from evidence
  retention and is not a workflow-engine defect.
- **Not adding run-level loop detection.** Confirmed absent (no
  `maxRunDuration`, `runTimeout`, or stage re-entry counter exists anywhere in
  the orchestrator), and worth its own discussion — a stage-re-entry counter is
  a deterministic check, unlike the quality classifier PLAT-168 rejected. Out
  of scope here; this ticket restores the evidence such a discussion would need.

## Verification

- `TestArchiveSupersededExecutionLogsPreservesAPriorDispatch` — new. Stands up
  an `httptest` workspace API that records move requests, seeds all four
  evidence files, and asserts every one is moved into `superseded/` with a
  stamp that a later dispatch cannot collide with. Confirmed failing before the
  fix existed (`go vet`: method undefined) and passing after.
- `TestArchiveSupersededExecutionLogsIsANoopOnFirstDispatch` — the common case
  (a step that runs once) must not pay a move.
- `TestArchiveSupersededExecutionLogsUsesCollisionProofDestinations` — archives
  two dispatches in the same second against a fake workspace API that rejects
  destination collisions, and proves all eight archived files have distinct
  destinations.
- Full `step_based_workflow` package passes, with the one pre-existing,
  unrelated failure already tracked in this register
  (`TestRunInBackgroundPassesBuilderSkillSnapshotToBothAgentKinds`).
- `go build ./...` clean.

Not yet reverified live: the direct signal is a genuinely re-dispatched step
leaving a populated `superseded/` folder alongside its canonical files.

## Acceptance

- [x] A re-dispatched step no longer destroys the previous dispatch's result,
      conversation, timing, or prompts.
- [x] Canonical filenames still hold the newest dispatch, so no existing
      reader changes behavior.
- [x] A first dispatch performs no archive work.
- [ ] Live: a re-dispatched step is observed writing into `superseded/`, and
      the prior dispatch's timing file is still readable afterward.
