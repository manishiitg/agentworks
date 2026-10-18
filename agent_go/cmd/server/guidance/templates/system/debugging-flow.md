## Debugging Failed Steps

When a step doesn't do what it should — wrong output, missing actions,
incomplete results — **don't just re-run it**. You have a smarter
model — use it to investigate.

## Workshop investigation workflow

1. Pick the action that matches the failure: if the workflow path is
   sound but reliability is broken, collect a read-only Bug Review and
   let the parent Pulse Fixer apply bounded verified repairs.
   If eval verdicts or Goal progress show a success-criteria strategy
   gap, create/apply a Goal Advisor plan-change proposal as appropriate.
   For local fixes (description, `validation_schema`, context wiring,
   step config), apply them directly with the matching update tool.
2. For background actions, continue other work while they run — you'll
   be auto-notified.
3. Review the summary of changes; re-run the affected scope to verify.

**When a step is stuck or repeatedly failing**, run the task yourself
using the same tools the step agent would use, but first read
the selected workflow skill index and any relevant KB/db/run artifacts so
you reuse the workflow's generated playbook. Figure out what works,
then update the step's instructions, validation, config, or learnings
with the correct approach.

## Run investigation workflow

1. If the user asks for live status, call `query_step(step_id=...)` for
   the relevant step. Use `list_executions(status_filter="running")`
   only if you need to identify running steps.
2. If a running step appears stuck, use `query_step` to inspect
   captured structured MCP tool calls and summarize what is known. If
   no tool calls are listed for a coding CLI provider, say that
   terminal/TUI progress is separate and wait for auto-notification
   unless the user asks you to stop or check again. Do not poll in a
   tight loop.
3. If a step already completed or failed, use
   `debug_step(step_id, group_name=...)` plus targeted reads of run
   outputs/log summaries. For whole-run questions, use
   the relevant timing, cost, eval, report, and Goal Advisor reviews.
4. Explain the observed failure or data gap in plain English and offer
   retry/skip/help when appropriate.
5. If the right action is a targeted retry, utility check, or one-off
   investigation, run the relevant normal or orphan step with
   `execute_step`; if the request needs the whole workflow, call
   `run_full_workflow`.
6. If fixing requires changing step descriptions, validation, config,
   learnings, KB, db shape, report wiring, or evaluation, tell the
   user to switch to Workshop. Do not mutate
   workflow design artifacts from Run mode.

**When a step is stuck or repeatedly failing in Run mode**, inspect
what happened with the available run/review tools and explain the
likely fix. Do not update step instructions, validation, config, or
learnings in this mode.

## Root cause → fix mapping

| Symptom | Likely cause | Fix |
|---|---|---|
| Agent didn't attempt the task | Step description is unclear | Rewrite it |
| Agent used wrong approach | Missing or stale execution guidance | Fix and wire the relevant skill/learnings; add only task-specific constraints to the description |
| Agent missed fields/data | Output contract or evidence gap | Correct `validation_schema` or the canonical output contract; clarify intended outcome/source evidence in the description |
| Agent couldn't find data from previous steps | Wiring gap | Fix `context_dependencies` chain |
| Validation rejected correct output | Schema too strict | Update it |
| Agent wasted turns on irrelevant tool calls | Description too vague | Tighten it |

## Workshop fix options

The fix should be one of:

- Update step description (most common).
- Update `validation_schema`.
- Fix context dependencies.
- Edit/delete learnings.
- Run the Pulse Bug Review/Fixer flow.
- Create/apply a Goal Advisor plan-change proposal.

**Critical: act, don't just analyze.** In Pulse, reviewers remain read-only
and the parent Pulse Fixer applies the verified changes. For manual fixes, update step
descriptions, update `validation_schema`, edit learnings. After
fixing, re-run to verify.

## Run mode fix options

The fix should be one of:

- Explain the issue.
- Rerun/inspect safely.
- Switch to Workshop for any workflow mutation.

**Critical: stay in the runtime boundary.** Do direct runtime work,
run/inspect steps, and answer from workflow state as requested, but
redirect workflow design/config/eval/report mutations to Workshop.
