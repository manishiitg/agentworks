[← crew / functions](index.md)

# PLAT-589: Completed Crew ask call remains running when its trigger binding is unavailable

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | crew |
| Area | functions |
| Summary | Excellence Crew aq finished its Code ask call, but the caller kept reporting running: saved call remained queued, polling reported a missing internal trigger, and the supervisor ignored lookup errors. |

## What happened

Read-only investigation on Excellence, 2026-10-06. Both the Code workspace
`linkedin-scraper-de7a2ec2` and Crew `aq-ae5e5fac` are registered to the same
user, `b2bb7148f87b3338355f92e8c59c9c43`. The call correctly used the Crew's
main chat under the same-owner routing rule.

- Call: `fn-42ca174e-8d28-48d0-a16c-9ec558ddb729`, implicit `ask`.
- Trigger: `b2fc312e-481f-4659-b819-b6155894ac66`.
- Run: `b5dc296e-ea71-56ed-bbd8-655eaf6fe2ed`.
- Session: `work:project:ae5e5fac-4534-4405-bf06-ba306f39ac60`.
- `schedule.log` and `Crew/aq-ae5e5fac/schedule-runs.json` agree: started
  `2026-10-06T08:51:55.982887829Z`, succeeded
  `2026-10-06T08:52:31.650257863Z`, duration 35,616 ms, with a nonempty final
  answer saved. Browser timeline records also show the completion painted.
- The persisted function call still has `status: queued`, no result, and its
  original `updated_at`. The user's screenshot shows the caller describing
  the call as `running`, with a background agent still waiting.
- At `2026-10-06T10:56:48+02:00`, `ask_function_update` failed with
  `trigger not found on the attached workflow`.
- At inspection, the Crew's runtime `workflow.json` has no triggers. This is
  not enough to establish which writer removed the binding or exactly when;
  dispatch had successfully resolved it earlier.

The deployed builder revision was `581ccc6606c2caa3f54c4e25964136d5b45df98d`.
The same relevant code remains on main at the time of this report:
`getInternalProductTriggerRun` requires a currently enabled trigger before it
reads the run; `superviseCrewFunctionCall` ignores lookup errors; and
`get_function_call` suppresses them as well. Thus an unavailable binding can
prevent a completed run from settling the function call, while the caller
continues to receive its old status without the lookup failure.

Global Monitor is mounted for Crew/Code and has no per-user disable flag in
the inspected code. It filters out the currently selected session and hides
when no activity remains. No monitor-paint diagnostic was found, so this
investigation does not establish why its button was absent on the user's
screen. A completed Crew run should disappear from it; the screenshot's
background-agent footer can independently remain while the call watcher waits.

## Fix

No implementation or server data changes in this investigation.

## Left

- Identify the writer that removed the internal trigger binding; preserve
  server-managed bindings across ordinary runtime-manifest updates.
- Reconcile a call with its exact saved terminal run while preserving current
  caller authorization and revocation rules. Do not bypass access checks just
  because an old run exists.
- Handle permanent lookup failures explicitly rather than silently returning
  a stale running status; bound retries for transient failures and report them.
- Verify the real Code-to-Crew path: one ask, main-chat answer, result delivered
  to Code, background watcher settled. Cover a missing binding regression.
- If monitor absence persists during a genuinely running background Crew
  turn, capture the header activity response and selected session at that time.
