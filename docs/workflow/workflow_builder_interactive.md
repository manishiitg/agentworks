# Interactive Workflow Builder

This doc explains how the interactive workflow builder works now.

This is the chat-driven builder experience used from the workflow canvas. Internally it is the `workflow_phase` path with `phase_id = workflow-builder`.

## What It Is

The interactive builder is not the same thing as normal workflow execution.

- normal execution uses `agent_mode = workflow`
- builder chat uses `agent_mode = workflow_phase`
- the builder gets a phase-specific system prompt, a restricted tool set, and builder-specific run-folder behavior

The main code paths are:
- [interactive_workshop_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go)
- [server.go](../../agent_go/cmd/server/server.go)
- [chatSubmitHelpers.ts](../../frontend/src/utils/chatSubmitHelpers.ts)

## Request Shape

From the frontend, builder chat is sent as:

- `agent_mode = workflow_phase`
- `phase_id = workflow-builder`
- `preset_query_id = <workflow preset>`

The server then:
- resolves the workflow manifest
- loads phase LLM settings from the manifest when available
- converts the request onto the standard chat agent path
- overrides the system prompt for the builder phase
- registers builder/workshop tools
- applies a per-turn tool allow list based on workshop mode

This is handled in [server.go](../../agent_go/cmd/server/server.go).

## Core Mental Model

The interactive builder is a persistent chat session that can:

- inspect the current workflow plan
- modify plan structure
- modify step configuration
- manage groups, workflow config, schedules, and related artifacts
- run single steps in the background
- run the full workflow in the background in the appropriate modes
- inspect run results and logs

It is a design-and-operations surface, not just a prompt editor.

## Workshop Modes

The builder has multiple workshop modes.

Current backend modes:
- `builder`
- `optimizer`
- `run`
- `debugger`
- `eval`
- `output`

The most important ones for normal workflow building are:

### `builder`
Use this for designing and testing the workflow.

Allowed behavior:
- add, update, and delete plan steps
- update workflow config, variables, groups, schedules
- run individual steps
- inspect executions
- patch scripts and files

### `optimizer`
Use this after the basic workflow exists and needs hardening against run/eval/metric evidence.

Adds:
- `Pulse Bug Review/Fixer`
- `debug_step`
- `run_full_workflow`
- evaluation helpers
- learning and step-hardening workflows

### `run`
Use this when the workflow is already built and the user mainly wants operational execution, Slack/WhatsApp answers, or result inspection.

Allowed behavior is runtime-focused:
- answer directly from workflow state, KB, learnings, db, and latest run artifacts
- run normal steps
- run orphan utility steps
- run the full workflow
- inspect executions

It is intentionally more operational and less structural.

## Mode Detection

If the frontend does not explicitly force a workshop mode, the backend defaults to Builder.

Current rule:
- default -> `builder`
- `optimizer`, `run`, and reporting mode are explicit frontend/user choices or bot-route settings

This logic lives in [interactive_workshop_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go).

Frontend override still wins when provided.

## Tool Gating

The builder does not expose all tools all the time.

Current behavior:
- the server registers a broad set of workshop-capable tools
- then applies a workshop-mode allow list
- the allow list is recomputed based on the current workshop mode

This is how the product enforces boundaries like:
- builder can modify the plan
- run mode can execute user-facing work, normal steps, orphan utility steps, and full workflows
- run mode can read KB/learnings/db/run artifacts, but cannot freely redesign the workflow
- debugger is read-heavy

The tool allow-list logic is in [interactive_workshop_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go).

## Main Builder Tools

The interactive builder commonly uses:

- `execute_step`
- `query_step`
- `send_step_message`
- `stop_step`
- `stop_all_executions`
- `list_executions`
- `update_step_config`
- plan modification tools such as `add_*`, `update_*`, `delete_*`
- `update_workflow_config`
- `update_variable`, `add_group`, `update_group`, `delete_group`
- schedule tools

Depending on mode, it may also expose:

- `run_saved_main_py`
- `debug_step`
- `run_full_workflow`
- `run_full_report`
- `Pulse Bug Review/Fixer`

## Step Execution From Builder

The builder can execute steps directly from chat.

Key behavior:
- `execute_step(...)` runs in background and returns an execution ID
- `query_step(...)` is used to inspect progress and live tool calls
- `send_step_message(execution_id, message)` steers the exact active child-agent
  turn without starting a second run. Coding CLIs receive live input; other
  agents queue it for the next safe turn boundary. It cannot resume completed
  work and may return `no_active_agent` during validation or script-only phases.

In builder mode:
- `iteration` is effectively pinned to `iteration-0`
- any incoming builder run-folder selection is normalized to `iteration-0` or `iteration-0/<group>`
- every execution re-reads the latest plan and config state

This means builder testing always operates against the current mutable run, not an archived iteration.

## Full Workflow Runs From Builder

In `optimizer` and `run` modes, the workshop agent can execute the entire workflow via `run_full_workflow(...)`.

That is still a background workflow execution, not a special toy mode.

Important behavior:
- it runs the real workflow controller path
- it can require `human_inputs` if the workflow has `human_input` steps
- it uses the current workflow plan and config from the workspace

## Run Folder Behavior

Interactive builder mode is tightly coupled to `iteration-0`.

Current rule:
- builder mode normalizes run-folder selection to `iteration-0`
- if a group path is selected, it becomes `iteration-0/<group>`

This is why the builder always feels attached to the latest mutable run.

For the broader run-folder model, see [iteration_run_folder_architecture.md](iteration_run_folder_architecture.md).

## Session Behavior

The builder uses a persistent per-session workshop chat session.

Current behavior:
- the server reuses the workshop session for the same chat session when possible
- refreshed manifest settings can be pushed back into the session
- background step executions are tracked so the frontend can keep polling and showing status
- stop-state checks are wired to avoid orphaned background processes

This is one reason builder chat behaves more like an operator console than a plain LLM chat.

## Files And Persistence

The interactive builder writes and reads several kinds of state:

### Plan and config state
- `planning/plan.json`
- `planning/step_config.json`
- `workflow.json`
- variables/group data

### Builder chat artifacts
- `builder/conversation/YYYY-MM-DD/session-{sessionId}-conversation.json`

### Builder phase costs
- `costs/phase/token_usage.json`
- `costs/phase/daily/YYYY-MM-DD.json`

### Execution artifacts from tested steps
- `runs/iteration-0/...`

So the builder has both:
- phase-level chat state and costs
- execution-level run artifacts when it tests or runs workflow steps

## Relationship To Other Modes

The same underlying workshop infrastructure also supports adjacent specialized phases:

- `eval`
- `output`
- `debugger`

But the primary workflow-builder experience is still `workflow-builder` plus the builder/optimizer/run workshop modes.

## Practical Summary

Use this mental model:

- interactive builder = `workflow_phase` chat for workflow design and operation
- it is sessionful, tool-driven, and workspace-backed
- it auto-detects or accepts a workshop mode
- tool access changes by mode
- single-step testing happens through workshop execution tools
- full runs are available in the more operational modes
- builder run-folder context is centered on `iteration-0`

## Related Docs

- [iteration_run_folder_architecture.md](iteration_run_folder_architecture.md)
- [cost_and_log_measurement.md](cost_and_log_measurement.md)
- [workflow_shell_working_directory.md](workflow_shell_working_directory.md)
- [workflow_manifest_architecture.md](workflow_manifest_architecture.md)
