# PLAT-053 — Background workshop agents lost the parent tool surface

| Field | Value |
|---|---|
| State | open |
| Priority | P0 |
| Product | coding-agents |
| Area | bridge |
| Summary | workshop background-agent construction |

- **State:** `runtime_reverify`
- **Assigned:** Codex
- **Owner:** workshop background-agent construction
- **Severity:** P0 — a reviewer could diagnose a workflow defect but could not
  persist its finding, create a decision, or repair the workflow.

## Evidence

The 2026-08-08 Upwork Pulse run launched Strategy and Engineering+Fix through
`run_in_background`. Both children received the ordinary workflow-step default
tool list rather than the parent workshop list. Their logs showed unregistered
`get_pulse_state`, `record_pulse_finding`, `record_pulse_result`,
`record_pulse_verification`, `update_message_sequence_step`,
`update_step_config`, `update_schedule`, and `update_evaluation_plan` calls.
Folder Guard then denied direct-file fallbacks.

## Root cause

`runBackgroundTaskAgentSequence` called `prepareCustomTools(nil)`. For a plan
step, `nil` deliberately means the restricted default workspace/human/DB set;
it does **not** mean the complete workshop surface. The native workshop tools
were also registered only after the parent agent had been constructed, while
the child was constructed from the reduced workspace bundle.

## Fix

Background workshop children now:

1. receive the parent controller's entire `WorkspaceTools` and executor bundle;
2. collect the parent workshop's native plan/schedule/execution tools as
   immutable direct definitions before MCP-agent construction; and
3. register `get_workflow_command_guidance` in that child definition, because
   slash-command wrappers require the child to load the exact guidance that
   dispatched it; and
4. preserve those preconfigured direct definitions when the common agent
   factory adds the workspace-tool definitions.

There is intentionally no second category allow-list. The per-agent Folder
Guard and the child task instruction govern safe scope, exactly as they do for
the parent workshop agent.

## Acceptance

- The focused registration test proves a child definition includes plan,
  step-config, schedule, evaluation, and background-execution tools.
- A new Pulse child must successfully call the typed Pulse persistence tools
  and a safe workflow mutation tool without falling back to shell/SQLite.

## Verification

```text
go test ./pkg/orchestrator/agents/workflow/step_based_workflow \
  -run 'TestBackgroundTaskGetsWorkshopMutationToolDefinitions|TestWorkshopMode|TestPrepareCustomTools' -count=1
go test ./cmd/server -run 'Test.*Pulse|Test.*Toolset' -count=1
```

Both passed on 2026-08-08. Runtime verification requires a server restart and
one new background Pulse review; the currently running server predates this
code change.
