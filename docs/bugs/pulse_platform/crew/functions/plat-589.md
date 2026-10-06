[← crew / functions](index.md)

# PLAT-589: Completed Crew ask call remains running when its trigger binding is unavailable

| Field | Value |
|---|---|
| State | deployed |
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

The writer was identified: at `08:51:56Z`, `ReadWorkflowManifest` treated
`Crew/aq-ae5e5fac` as an AgentWorks workflow and pruned its `triggers` and
`identity`. Its protection recognized only `Chats/Work/projects/...`, not the
new shared Crew root. The manifest changelog records that removal at the exact
start of the call. The same destructive read was reproduced locally.

- Recognize Crew runtime paths through `workspaceref.AnyCrewProject`, keeping
  product-owned manifests read-only under both old and new path formats.
- After three consecutive run-lookup failures (normal polling: about 20 seconds
  from the first failed lookup), fail the call with the actual error and release
  its notification watcher. Successful reads reset the retry count.
- `get_function_call` reports lookup errors instead of returning a stale status.
  Existing binding/caller authorization and revocation checks remain in place.
- A combined regression drives a same-owner private Code ask through its tool,
  durable trigger/run record, Crew runtime read, function supervisor and caller
  notification registry. It verifies the answer and saved completion, and that
  both success and binding revocation settle the background watcher. The model
  answer is supplied by the test; this is not a live model end-to-end check.
- Extended the existing manifest-preservation regression to shared Crew roots.
  Relevant existing function, trigger, authorization and browser tests pass.
  Restoring the old recognizer reproduces the lost trigger and missing answer.

### Existing Excellence call recovered

The owner registry, source and target IDs, successful saved run and destructive
manifest changelog were checked before restoring this call's single missing
binding. No task was reissued and no other binding was changed. Backup:
`/srv/agents/state/repairs/plat-589/aq-workflow-before-20261006T093348Z.json`.
The existing supervisor picked up the saved answer: call status became
`completed`, with a result, at `2026-10-06T09:33:58.045295892Z`.

## Left

- Monitor absence was not independently reproduced. The stale call/watcher
  issue is fixed and the existing call is recovered; if the monitor is absent
  during future live background work, capture the header response and selected
  session then.

## Deployment evidence — 2026-10-06

Excellence release `agents-99dc2842-20261006115535` records builder revision
`99dc284251`, which contains this fix. Public and agent health checks passed;
slot self-test completed with 156 passed, zero failed, 16 skipped.
