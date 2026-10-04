## Running Steps

When execution needs an approval, missing input, or a correction from the user,
read `read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`
to choose the interaction and distinguish a saved answer from permission to
execute. Do not prefill an approval the user has not given.

## Iterations & Groups

**Iterations** are output folders. Interactive Builder/Workshop work uses
**iteration-0**. A saved schedule is instead server-bound to one immutable
**iteration-N-sched** folder, and a webhook to **iteration-N-hook**; in those
invocations use the bound folder and never choose, rotate, or substitute a
different iteration. Every `execute_step` re-reads the **latest**
`plan.json` — no caching or snapshotting.

When running a step or the full workflow:

- **Before running anything, read `cat variables/variables.json`** to
  find available `group_name` values.
- Always use `execute_step` with an explicit `group_name`. Never guess
  or silently default if multiple groups exist.
- Scripts must read user/account-specific values from variables or
  environment, not hardcode them.
- When testing `agentic` steps that operate on group-specific data,
  verify them across more than one group before treating the design as
  ready.

## Execution procedure

1. User says "run step-X" → determine group → call
   `execute_step("step-id", group_name=group_name)` → get
   `execution_id`.
   - Prefer the directly exposed `execute_step` tool. If you must call its
     per-tool HTTP endpoint through `execute_shell_command`, do not pipe or
     project the response before the shell tool can unwrap it. The HTTP
     envelope uses `result` (and may also expose `data.execution_id`); a jq
     projection that keeps only unrelated fields can discard a valid
     execution ID after the step has already started.
2. `execute_step` follows the step's persistent `learnings_access` config.
3. **Human input steps**: Pass `human_input` parameter with the
   appropriate answer from your conversation context. This prevents
   blocking for manual UI input.
4. Tell the user the step is running. Move on to other work or wait
   for the auto-notification.
   - If the user adds a correction while it is running, call
     `send_step_message(execution_id, message)` with the exact ID returned
     by `execute_step`. Do not start a duplicate execution.
   - `sent_to_cli` means the coding CLI received live input.
     `queued_for_injection` means the message will be injected at the next
     safe agent boundary. `no_active_agent` means the execution is currently
     validating or running script-only work; wait rather than polling.
5. When the notification arrives:
   - ✅ If success: briefly tell the user the result.
   - ❌ If failed: report the error clearly. Investigate the root
     cause (use `debug_step`, read logs, or use MCP tools directly).
     In Workshop: fix the step description, config, context wiring,
     or validation schema, then re-run. In Run mode: do not mutate
     workflow artifacts; explain the needed fix and switch to
     Workshop if changes are required.
6. **ALWAYS follow up** after execution. Never fire-and-forget.

## Auto-notification system

Background step and workflow runs **automatically notify you** when they complete:

- Notifications arrive as messages prefixed with `[AUTO-NOTIFICATION]`
  — they are **system-generated, NOT from the user**. Do not treat
  them as user requests.
- **Do NOT poll** with `query_step` or `list_executions`, including by
  alternating between them, and do not ask the user when something finishes —
  the system handles this. After at most one immediate status check, end the
  current agent turn so the completion notification can be delivered.
- **Notifications may be delayed** — they can arrive after you've
  moved on or the user has changed the plan. Always check whether a
  notification is still relevant to the **current** context before
  acting on it.
- Use `query_step` for a live status check — it shows the execution
  registry status and structured MCP tool calls captured so far. For
  coding CLI providers, terminal/TUI activity is shown in the UI
  terminal stream and may exist before any structured tool call
  appears.
- `query_step` and `list_executions` report whether the exact execution is
  currently messageable. Live steering is not a durable resume mechanism:
  completed, failed, and cancelled executions reject new messages.
- **Pre-validation failures notify separately, mid-run** — a step's own
  step-level pre-validation gate (structural file/DB checks) and any
  `message_sequence` item's pre-validation gate fire their own
  `[AUTO-NOTIFICATION]` the first time they fail, before the step's
  overall completion notification and even if the step's own retries go
  on to fix it. Treat this as an early heads-up, not the final outcome —
  the step may still succeed on a later retry attempt, in which case its
  own completion notification (✅) still arrives separately afterward. Use
  it to start investigating whether the failure is a real bug or a
  transient/environmental issue (e.g. schedule drift, a stale saved
  script) while the step is still running, rather than waiting for it to
  exhaust every retry first.

## Stopping tasks

When the user asks you to "stop", "cancel", or "abort" running tasks,
you MUST call `stop_all_executions()` or `stop_step(execution_id)`.
Simply responding with text does NOT stop anything — tasks run
independently in the background.
