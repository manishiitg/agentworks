## Running Steps

When execution needs an approval, missing input, or a correction from the user,
read `read_skill(skills=[{"name":"builder-reference","path":"references/human-in-the-loop.md"}])`
to choose the interaction and distinguish a saved answer from permission to
execute. Do not prefill an approval the user has not given.

## Inputs and runs

A call runs once. Inputs use workflow defaults, then optional named saved values
(`input_preset`), then explicit `inputs`. Omit both to use defaults. Presets are
just reusable values: they do not choose routes or launch several runs.

Each invocation has its own run folder and saves the resolved inputs there.
Read the returned `run_folder` when inspecting results. In Builder mode only,
you can pass that existing `run_folder` to continue its steps; it keeps the
original inputs even if defaults or presets change. Run-mode calls and
functions start new runs. Saved schedules and webhooks use their server-bound
run folder; never choose or substitute another one.

Read `variables/variables.json` when you need input names or a saved preset.
Scripts read input values from their configured environment, not hardcoded
accounts or preset labels. Business identity, such as a campaign ID, is an
ordinary explicit input and never inferred from a folder name.

## Execution procedure

1. User says "run step-X" → call
   `runs(action="step", step_id="step-id", inputs={...})` (inputs optional) → get
   `execution_id`.
   - Prefer the directly exposed `runs action=step` tool. If you must call its
     per-tool HTTP endpoint through `terminal action=execute`, do not pipe or
     project the response before the shell tool can unwrap it. The HTTP
     envelope uses `result` (and may also expose `data.execution_id`); a jq
     projection that keeps only unrelated fields can discard a valid
     execution ID after the step has already started.
2. `runs action=step` uses the step's selected skills and existing knowledge-base access. It adds no learning phase.
3. **Run instructions**: Pass `human_input` to add the user's instructions to this
   executable step. Use `human_inputs` keyed by step ID for a full workflow run.
4. Tell the user the step is running. Move on to other work or wait
   for the auto-notification.
   - If the user adds a correction while it is running, call
     `runs(action="message", execution_id, message)` with the exact ID returned
     by `runs action=step`. Do not start a duplicate execution.
   - `sent_to_cli` means the coding CLI received live input.
     `queued_for_injection` means the message will be injected at the next
     safe agent boundary. `no_active_agent` means the execution is currently
     validating or running script-only work; wait rather than polling.
5. When the notification arrives:
   - ✅ If success: briefly tell the user the result.
   - ❌ If failed: report the error clearly. Investigate the root
     cause (use `runs action=debug`, read logs, or use MCP tools directly).
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
- **Do NOT poll** with `runs action=step_status` or `runs action=active`, including by
  alternating between them, and do not ask the user when something finishes —
  the system handles this. After at most one immediate status check, end the
  current agent turn so the completion notification can be delivered.
- **Notifications may be delayed** — they can arrive after you've
  moved on or the user has changed the plan. Always check whether a
  notification is still relevant to the **current** context before
  acting on it.
- Use `runs action=step_status` for a live status check — it shows the execution
  registry status and structured MCP tool calls captured so far. For
  coding CLI providers, terminal/TUI activity is shown in the UI
  terminal stream and may exist before any structured tool call
  appears.
- `runs action=step_status` and `runs action=active` report whether the exact execution is
  currently messageable. Live steering is not a durable resume mechanism:
  completed, failed, and cancelled executions reject new messages.
- **Pre-validation failures notify separately, mid-run** — a step's own
  step-level pre-validation gate (structural file/DB checks) and any
  `agent` item's pre-validation gate fire their own
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
you MUST call `runs(action="stop_all")` or `runs(action="stop_step", execution_id)`.
Simply responding with text does NOT stop anything — tasks run
independently in the background.
