# Iteration Run Folder Architecture

This doc describes how `runs/iteration-x` works now.

The important mental model is:
- `iteration-0` is the active mutable execution sandbox
- `iteration-1`, `iteration-2`, and higher are archived older runs
- when groups are enabled, the real run unit is usually `iteration-x/<group-folder>`

Older docs and comments may still sound like every execution directly targets a new `iteration-N`. That is no longer the main runtime model.

## Current Model

For normal workflow execution, the controller always resolves execution into `iteration-0`.

If `runs/iteration-0` already exists:
- it is moved to the next available numbered archive such as `iteration-7`
- a fresh `runs/iteration-0` is created
- the new execution runs in that fresh `iteration-0`

So in practice:
- `iteration-0` = latest working run
- `iteration-N` = preserved history

This is implemented in [controller_run_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_run_manager.go).

## Why It Works This Way

The architecture is optimizing for a stable active workspace:
- builder mode can always point at `iteration-0`
- schedulers can always launch against `iteration-0`
- shell working directories and bridge paths stay predictable
- the most recent run is easy to inspect without guessing the latest number
- historical runs are still preserved by moving older `iteration-0` to `iteration-N`

## Standard Execution

Normal full workflow execution uses this flow:

1. Resolve run folder.
2. If `iteration-0` exists, move it to the next available archive.
3. Create a fresh `runs/iteration-0`.
4. Execute the workflow there.

The main orchestration path does this in [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go) and [controller_run_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_run_manager.go).

The base run folder structure currently creates:
- `runs/iteration-0/`
- `runs/iteration-0/execution/`
- `runs/iteration-0/execution/Downloads/`

Logs and step outputs are then written under that run during execution.

## Group-Scoped Runs

When variables/groups are enabled, the runtime uses nested folders under an iteration:

- `runs/iteration-0/<group-folder>/`
- `runs/iteration-3/<group-folder>/`

Current behavior:
- group folders are always nested under an iteration
- the folder name uses sanitized `display_name` when available
- otherwise it falls back to `group_id`

This logic lives in [controller_batch_execution.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_batch_execution.go).

Examples:
- `iteration-0/production`
- `iteration-0/staging`
- `iteration-4/manish`

## Partial Group Runs

Partial group runs are a special case.

If the user runs only a subset of enabled groups:
- the controller reuses `iteration-0`
- it does **not** back up `iteration-0` first
- this preserves outputs for the other groups already present in the current latest run
- cleanup happens per-group instead of rotating the whole iteration

This behavior is handled in [controller.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go).

This is the main exception to the simple “old `iteration-0` becomes `iteration-N`” rule.

## What The Frontend Sees

The backend run-folder listing does not always expose bare iteration folders.

Current listing behavior:
- if an iteration has group subfolders, the API returns the group paths
- if an iteration has no group subfolders, the API can return the bare iteration folder

So the UI often works with:
- `iteration-8/production`
- `iteration-8/staging`

instead of just:
- `iteration-8`

This behavior comes from [workflow.go](../../agent_go/cmd/server/workflow.go).

## Builder Mode

Workflow builder mode is pinned to `iteration-0`.

That means:
- any incoming builder selection is normalized to `iteration-0` or `iteration-0/<group>`
- the builder should be thought of as operating on the latest mutable run, not on archived iterations

This behavior lives in [interactive_workshop_manager.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go) and is reflected in the canvas logic in [WorkflowCanvas.tsx](../../frontend/src/components/workflow/canvas/WorkflowCanvas.tsx).

## Scheduler Behavior

Schedulers also target `iteration-0`.

The scheduler request path sets:
- `run_mode = use_same_run`
- `selected_run_folder = iteration-0`

Then the controller applies the same backup-and-refresh logic for full runs.

This behavior lives in [scheduler.go](../../agent_go/cmd/server/scheduler.go).

## Evaluation And Report Generation

Evaluation and final report generation have their own internal sandbox behavior.

They do **not** mean “evaluate directly inside the archived target iteration.”

Instead:
- (retired) evaluation used to execute in `evaluation/runs/iteration-0[/group]`; measurement now comes from the producing steps' own stored outputs
- final report generation uses an internal `iteration-0`-based report-generation area and then publishes output back to the requested target run

So `iteration-0` is also the internal scratch space for non-primary execution modes.

See:
- [evaluation_execution.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/evaluation_execution.go)

## Selected Run Folder Semantics

One source of confusion is `selected_run_folder`.

Current reality:
- for observability and UI context, it is a real run-folder selector
- for historical inspection, it can point at archived runs like `iteration-9/production`
- for standard execution, the controller still resolves execution into `iteration-0`

So `selected_run_folder` is not “execute exactly in any arbitrary archived iteration” in the normal workflow path.

## Practical Rule Of Thumb

Use this mental model:

- inspect the latest run in `iteration-0`
- treat `iteration-N` as archived history
- expect grouped workflows to use `iteration-x/<group>`
- expect builder, scheduler, evaluation sandboxes, and report generation to revolve around `iteration-0`

## Related Docs

- [workflow_manifest_architecture.md](workflow_manifest_architecture.md)
- [workflow_monitoring.md](workflow_monitoring.md)
- [workflow_shell_working_directory.md](workflow_shell_working_directory.md)
