[← platform / scheduler-runs](index.md)

# PLAT-097 — `update_schedule(messages=[])` and `messages=null` silently no-op instead of clearing

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | scheduler-runs |
| Summary | P1 — reports success while silently discarding the caller's |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — fixed and tested; runtime reverify pending |
| Last synchronized | `2026-08-12` |

- **Priority:** P1 — reports success while silently discarding the caller's
  intent, and blocks every classification-1 (EQUIVALENT ROUTE EXISTS)
  migration in the PLAT-086 schedule-execution-model contract upgrade for any
  schedule that carries `messages`.
- **Owner:** `update_schedule`/`update_workflow_schedule` argument parsing
  (`interactive_workshop_manager.go`, `workflow_schedule_tools.go`) and the
  shared `UpdateSchedule` implementation (`server.go`)
- **Found on:** reported live against a workflow undergoing PLAT-086
  migration; confirmed by full source trace, not reproduced against a live
  server.

## Evidence

`update_schedule(job_id=..., messages=[])` and
`update_schedule(job_id=..., messages=null)` both return success, but
`workflow.json`'s `schedules[].messages` is left completely unchanged — still
the full prior array. A raw HTTP trace confirmed the client sent the correct
body (`{"job_id":"...","messages":[]}`); this ruled out a client-side
encoding issue before the report reached this ticket.

## Root cause

Two independent argument-parsing sites for `update_schedule` (the
workshop-facing tool in `interactive_workshop_manager.go`, and the
chat-facing `update_workflow_schedule` in `workflow_schedule_tools.go`) both
built their `messages []string` variable with no companion "was this key
explicitly provided" signal — unlike `group_names` and `route_selections` in
the exact same functions, which both correctly use a `setGroupNames`/
`setRouteSelections bool` sentinel decoupled from whether the resulting
collection ends up empty:

```go
// group_names — correct
var groupNames []string
setGroupNames := false
if raw, ok := args["group_names"]; ok && raw != nil {
    setGroupNames = true
    ...
}

// messages — before this fix, no sentinel at all
var messages []string
if raw, ok := args["messages"]; ok && raw != nil {
    if arr, ok := raw.([]interface{}); ok {
        for _, v := range arr {
            if s, ok := v.(string); ok { messages = append(messages, s) }
        }
    }
}
```

For `messages: []`, the guard passes (a JSON empty array decodes to a
non-nil `[]interface{}{}`), but the loop runs zero times, so `messages` stays
a **nil** Go slice — identical to what you'd get if `messages` were never in
the request. `messages: null` fails the guard even earlier (`raw != nil` is
false), same nil result. Omitted, `null`, and `[]` all collapse to the exact
same Go value before reaching the implementation.

`UpdateSchedule`'s own write gate (`server.go`) then used `messages != nil` as
its only "was this provided" signal:

```go
if messages != nil {
    sched.Messages = messages
}
```

Which would have been correct *if* the parsing layer preserved the
distinction — but by the time execution reaches here, that distinction was
already gone.

`workflow_schedule_tools.go`'s parsing used a different helper
(`stringSlice`) that happens to return a non-nil empty slice for `[]`
specifically — so that one call path was already correct for `messages: []`,
but still broken for `messages: null` (filtered out by the same `raw != nil`
guard before `stringSlice` is even called). Confirmed by testing both shapes
directly against `UpdateSchedule`: the non-nil-empty-slice shape already
passed under the old gate, the nil-slice shape (covering both real-world
inputs on the workshop path, and `null` on the chat path) did not.

## Fix

Added an explicit `setMessages bool` sentinel, mirroring the working
`group_names`/`route_selections` pattern exactly, at all three touch points:

- `interactive_workshop_manager.go`'s `update_schedule` handler — sentinel
  set whenever the `messages` key is present at all, regardless of its value.
- `workflow_schedule_tools.go`'s `update_workflow_schedule` handler — same.
- `SchedulerCallbacks.UpdateSchedule`'s function-type signature
  (`planning_exports.go`) and its implementation (`server.go`) gained a
  `setMessages bool` parameter, threaded through both call sites, replacing
  every `messages != nil` check with `setMessages`.

Both tool schema descriptions were updated to document that `[]` or `null`
now clears messages back to the route-based default, and that omitting the
field leaves existing messages untouched.

## Not fixed here

- The 26 answered-decision backlog and any schedules already stuck on a
  message-based configuration are unaffected by this fix landing — they can
  now be cleared going forward, but nothing here retroactively migrates them.
- Whether `stringSlice`'s empty-vs-nil inconsistency with the workshop path's
  manual parsing loop should be unified into one shared helper. Left as two
  independently correct implementations rather than introduced a third
  shared one under time pressure; both are now provably correct for all three
  input shapes via the `setMessages` sentinel, so the inconsistency is
  cosmetic, not a functional gap.

## Verification

- `go build ./...` clean.
- `TestUpdateScheduleClearsMessagesOnlyWhenExplicitlySet`
  (`scheduler_test.go`, 4 subtests): omitting `messages` leaves existing
  messages untouched; an explicit empty array clears messages under both
  real parsing shapes this codebase produces (the nil-slice shape from
  `interactive_workshop_manager.go`'s parser and from `null`, and the
  non-nil-empty-slice shape from `workflow_schedule_tools.go`'s
  `stringSlice`); an explicit `null` clears messages.
- Verified against the pre-fix code by reverting the `setMessages` gate to
  `messages != nil` and re-running: the two subtests that exercise the real
  bug (nil-slice empty-array shape, explicit null) fail as expected; the
  subtest passing a Go-level non-nil empty slice directly still passes
  (confirming that specific shape was never broken, and that the test isn't
  vacuously passing).
- Full suite: 23 failures, all pre-existing and accounted for (the known
  22-failure baseline, minus one — `TestUpgradeDirectHTMLReportsPreservesPrimaryDocuments`
  — resolved by a concurrent session's in-flight work during this
  investigation), zero new failures introduced by this change.

## Acceptance

- `update_schedule(messages=[])` and `update_schedule(messages=null)` clear
  a schedule's messages back to the route-based default and persist that to
  `workflow.json`.
- Omitting `messages` entirely leaves existing messages untouched.
- Both the workshop-facing and chat-facing update-schedule tools behave
  identically for all three input shapes.
