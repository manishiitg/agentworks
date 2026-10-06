[← goals / pulse-governance](index.md)

# PLAT-083 — no-run Pulse Finalizer instructed the agent to record an invalid "dashboard" command

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | goals |
| Area | pulse-governance |
| Summary | P3 — self-recovering agent friction, not data loss or a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — fixed and tested; runtime reverify pending |
| Last synchronized | `2026-08-10` |

- **Priority:** P3 — self-recovering agent friction, not data loss or a
  silent failure; caught live because PLAT-073-A's canonical-failure fix made
  the resulting tool error visible for the first time
- **Owner:** scheduler Pulse Finalizer prompt (`postRunMonitorNoRunSteps`,
  `scheduler.go`)
- **Found on:** live log inspection, `tectonicusadaytrading`, session
  `schedule-manual--cd0655e9_1786385780857980000`, 2026-08-10T23:49

## What happened

A scheduled run that never actually started triggers the no-run Finalizer
turn. Its own prompt instructed: *"(1) mark dashboard skipped because no run
exists; (2) run the configured source-hash-gated backup..."* — telling the
agent to call `record_pulse_result(command="dashboard", result="skipped", ...)`.

`record_pulse_result` rejected it immediately: *`"command \"dashboard\" is not
a valid Pulse final command. Must be one of: backup, publish, notify"`* —
confirmed against `pulse_final_commands.go`: `pulseFinalCommandOrder` and
`validPulseFinalCommands` have only ever contained `backup`, `publish`,
`notify`. Every other reference to "dashboard" in `scheduler.go` is plain
prose describing what's skipped, never a command the tool tracks. This one
instruction was simply wrong from the start.

**Why this was invisible until now**: before PLAT-073-A (mcpagent commit
`8c07adf`), `HandleCustomExecute` never applied the canonical-failure check,
so this exact rejection would have been reported to the agent as a success.
The fix didn't introduce the bug — it just made an existing, always-broken
instruction observable for the first time.

The agent recovered on its own (moved on to backup/publish/notify without the
dashboard receipt), but this pattern repeated for `notify` too: the agent
tried `record_pulse_result(command="notify", result="done", ...)` without
first sending `result="running"`, got rejected by the (correct)
running-before-done state check, and retried the identical failing call twice
more before finally sending `running` first. Three wasted tool calls for a
turn whose own prompt could have prevented the first mistake and whose
contract already requires the sequencing for the second.

## Fix

`postRunMonitorNoRunSteps` (`scheduler.go:2567`) no longer instructs the
agent to record a "dashboard" command. The surrounding paragraph already
states dashboard is "intentionally skipped" by simply not being rendered —
that's sufficient; the numbered instruction now explicitly says dashboard
"has no `record_pulse_result` command and needs no receipt," then proceeds
directly to backup/publish/notify.

Test: `TestNoRunFinalizerSkipsEvidenceStagesAndReportsReason`
(`scheduler_test.go`) updated to assert the new text and added an explicit
regression assertion that the prompt never contains "mark dashboard skipped"
again.

## Not fixed here

The `notify` running-before-done retry pattern (item 2 above) is the state
machine working correctly — three wasted calls, not a wrong result. Left
alone rather than loosening the contract; a prompt-level reminder that
`running` must precede `done` could reduce the retries, but that's a
guidance-tuning improvement, not a defect, and is out of scope for this
ticket.

## Verification

- `go build ./...` clean.
- `go test ./cmd/server/... -run TestNoRunFinalizerSkipsEvidenceStagesAndReportsReason` passes.
- Full baseline (`go test ./cmd/server/... ./pkg/orchestrator/...`) still
  shows exactly 22 pre-existing failures — no new failures.
- **Not yet reverified live** — requires a restart and a scheduled run that
  hits the no-run Finalizer path (a preflight abort, or the scenario that
  produced this session) before closing.

## Acceptance

- A no-run Finalizer turn's prompt never asks the agent to record a
  "dashboard" command.
- `dashboard`, `backup`, `publish`, `notify` are the only strings this prompt
  associates with `record_pulse_result`, and only the latter three are
  actually passed as `command=...`.
